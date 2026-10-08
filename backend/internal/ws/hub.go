// Package ws multiplexes real-time topics over a single WebSocket per client.
//
// Protocol (JSON text frames):
//
//	client → server  {"id":"1","op":"subscribe","topic":"system.metrics"}
//	                 {"id":"2","op":"unsubscribe","topic":"system.metrics"}
//	                 {"id":"3","op":"ping"}
//	server → client  {"type":"ack","id":"1"}
//	                 {"type":"error","id":"1","error":"..."}
//	                 {"type":"event","topic":"system.metrics","data":{...}}
//	                 {"type":"end","topic":"container.logs/abc","error":"..."}
//	                 {"type":"pong","id":"3"}
//
// A topic's producer (Source) starts when its first subscriber arrives and is
// cancelled when the last one leaves, so idle topics cost nothing.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alfaos/alfad/internal/store"
)

const (
	sendBuffer         = 256
	maxTopicsPerClient = 32
	readLimit          = 4096
	writeTimeout       = 10 * time.Second
	pingInterval       = 30 * time.Second
)

// Source produces events for one topic until ctx is cancelled or it returns.
// emit is safe for concurrent use.
type Source func(ctx context.Context, emit func(any)) error

type Subscription struct {
	Source Source
	// Shared feeds are fanned out to every subscriber. Unshared feeds run once
	// per client, e.g. logs, where each viewer needs its own backlog.
	Shared bool
}

// Resolver maps a topic to a Subscription, enforcing authorization for u.
type Resolver func(topic string, u *store.User) (Subscription, error)

type Message struct {
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	Topic string `json:"topic,omitempty"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type clientMsg struct {
	ID    string `json:"id"`
	Op    string `json:"op"`
	Topic string `json:"topic"`
}

type Hub struct {
	ctx     context.Context
	resolve Resolver
	log     *slog.Logger

	mu     sync.Mutex
	feeds  map[string]*feed
	nextID uint64
}

type feed struct {
	key    string
	topic  string
	subs   map[*client]struct{}
	cancel context.CancelFunc
}

type client struct {
	id     uint64
	user   *store.User
	conn   *websocket.Conn
	send   chan []byte
	topics map[string]string // topic → feed key; guarded by Hub.mu
	kicked chan struct{}
	once   sync.Once
}

// NewHub creates a hub whose feeds and connections end when ctx is cancelled.
func NewHub(ctx context.Context, resolve Resolver, log *slog.Logger) *Hub {
	return &Hub{ctx: ctx, resolve: resolve, log: log, feeds: make(map[string]*feed)}
}

// Serve runs the connection until either side closes it. It blocks.
func (h *Hub) Serve(conn *websocket.Conn, u *store.User) {
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()

	h.mu.Lock()
	h.nextID++
	c := &client{
		id:     h.nextID,
		user:   u,
		conn:   conn,
		send:   make(chan []byte, sendBuffer),
		topics: make(map[string]string),
		kicked: make(chan struct{}),
	}
	h.mu.Unlock()

	conn.SetReadLimit(readLimit)
	writerDone := make(chan struct{})
	go c.writeLoop(ctx, cancel, writerDone)
	h.readLoop(ctx, c)

	cancel()
	<-writerDone
	h.drop(c)
	conn.CloseNow()
}

func (h *Hub) readLoop(ctx context.Context, c *client) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var m clientMsg
		if err := json.Unmarshal(data, &m); err != nil {
			c.reply(Message{Type: "error", Error: "malformed message"})
			continue
		}
		switch m.Op {
		case "subscribe":
			start, err := h.subscribe(c, m.Topic)
			if err != nil {
				c.reply(Message{Type: "error", ID: m.ID, Topic: m.Topic, Error: err.Error()})
				continue
			}
			c.reply(Message{Type: "ack", ID: m.ID, Topic: m.Topic})
			if start != nil {
				start() // after the ack so the client sees ack before the first event
			}
		case "unsubscribe":
			h.mu.Lock()
			h.unsubscribeLocked(c, m.Topic)
			h.mu.Unlock()
			c.reply(Message{Type: "ack", ID: m.ID, Topic: m.Topic})
		case "ping":
			c.reply(Message{Type: "pong", ID: m.ID})
		default:
			c.reply(Message{Type: "error", ID: m.ID, Error: "unknown op"})
		}
	}
}

func (h *Hub) subscribe(c *client, topic string) (start func(), err error) {
	if topic == "" || len(topic) > 200 {
		return nil, errors.New("invalid topic")
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := c.topics[topic]; ok {
		return nil, nil
	}
	if len(c.topics) >= maxTopicsPerClient {
		return nil, fmt.Errorf("at most %d subscriptions per connection", maxTopicsPerClient)
	}
	sub, err := h.resolve(topic, c.user)
	if err != nil {
		return nil, err
	}
	key := topic
	if !sub.Shared {
		key = fmt.Sprintf("%s#%d", topic, c.id)
	}
	c.topics[topic] = key

	if f, ok := h.feeds[key]; ok {
		f.subs[c] = struct{}{}
		return nil, nil
	}
	ctx, cancel := context.WithCancel(h.ctx)
	f := &feed{key: key, topic: topic, subs: map[*client]struct{}{c: {}}, cancel: cancel}
	h.feeds[key] = f
	return func() { go h.runFeed(ctx, f, sub.Source) }, nil
}

func (h *Hub) unsubscribeLocked(c *client, topic string) {
	key, ok := c.topics[topic]
	if !ok {
		return
	}
	delete(c.topics, topic)
	if f := h.feeds[key]; f != nil {
		delete(f.subs, c)
		if len(f.subs) == 0 {
			f.cancel()
			delete(h.feeds, key)
		}
	}
}

func (h *Hub) drop(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic := range c.topics {
		h.unsubscribeLocked(c, topic)
	}
}

func (h *Hub) runFeed(ctx context.Context, f *feed, src Source) {
	err := src(ctx, func(v any) { h.broadcast(f, v) })

	h.mu.Lock()
	if h.feeds[f.key] == f {
		delete(h.feeds, f.key)
	}
	subs := make([]*client, 0, len(f.subs))
	for c := range f.subs {
		if c.topics[f.topic] == f.key {
			delete(c.topics, f.topic)
		}
		subs = append(subs, c)
	}
	h.mu.Unlock()
	f.cancel()

	if ctx.Err() != nil { // stopped because nobody listens, or shutdown
		return
	}
	end := Message{Type: "end", Topic: f.topic}
	if err != nil {
		end.Error = err.Error()
		h.log.Debug("ws feed ended", "topic", f.topic, "err", err)
	}
	for _, c := range subs {
		c.reply(end)
	}
}

func (h *Hub) broadcast(f *feed, v any) {
	b, err := json.Marshal(Message{Type: "event", Topic: f.topic, Data: v})
	if err != nil {
		h.log.Error("ws marshal", "topic", f.topic, "err", err)
		return
	}
	h.mu.Lock()
	subs := make([]*client, 0, len(f.subs))
	for c := range f.subs {
		subs = append(subs, c)
	}
	h.mu.Unlock()
	for _, c := range subs {
		c.enqueue(b)
	}
}

func (c *client) reply(m Message) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	c.enqueue(b)
}

// enqueue never blocks producers: a client that cannot keep up is
// disconnected (it will reconnect and resubscribe) instead of stalling feeds
// for everyone else.
func (c *client) enqueue(b []byte) {
	select {
	case <-c.kicked:
		return
	default:
	}
	select {
	case c.send <- b:
	default:
		c.once.Do(func() { close(c.kicked) })
	}
}

func (c *client) writeLoop(ctx context.Context, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	defer cancel()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.kicked:
			c.conn.Close(websocket.StatusPolicyViolation, "slow consumer")
			return
		case b := <-c.send:
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
