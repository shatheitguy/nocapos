// Package notify keeps the admins' system notifications (NoCapOS and app
// updates, app crashes, storage health, failed backups): it stores them once
// per event, applies the per-category settings and streams new ones live.
package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"alfaos/alfad/internal/store"
)

// Kinds, one per "Notify me about" switch (test is always delivered).
const (
	KindUpdate     = "update"
	KindAppUpdate  = "app_update"
	KindAppError   = "app_error"
	KindStorage    = "storage"
	KindBackup     = "backup"
	KindTest       = "test"
	keepNewest     = 500
	settingsKey    = "notify.settings"
	defaultListMax = 100
)

// Action says what clicking a notification opens: a NoCapOS app with props.
type Action struct {
	App   string         `json:"app"`
	Props map[string]any `json:"props,omitempty"`
}

type Notification struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Level string `json:"level"` // info | success | warning | error
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Icon names the app whose icon is shown: a NoCapOS app id, or
	// "store:<id>" for an App Store app.
	Icon      string     `json:"icon,omitempty"`
	Action    *Action    `json:"action,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// Settings are the "Notify me about" switches.
type Settings struct {
	Update     bool `json:"update"`
	AppUpdates bool `json:"app_updates"`
	AppErrors  bool `json:"app_errors"`
	Storage    bool `json:"storage"`
	Backups    bool `json:"backups"`
}

func DefaultSettings() Settings { return Settings{true, true, true, true, true} }

func (s Settings) allows(kind string) bool {
	switch kind {
	case KindUpdate:
		return s.Update
	case KindAppUpdate:
		return s.AppUpdates
	case KindAppError:
		return s.AppErrors
	case KindStorage:
		return s.Storage
	case KindBackup:
		return s.Backups
	}
	return true
}

// Event is what the notifications topic sends: a new item, or just the
// unread count after something was read or deleted ("sync").
type Event struct {
	Type   string        `json:"type"` // new | sync
	Item   *Notification `json:"item,omitempty"`
	Unread int           `json:"unread"`
}

// DB is the part of the store the service uses.
type DB interface {
	AddNotification(ctx context.Context, n *store.Notification, keep int) (bool, error)
	Notifications(ctx context.Context, limit int) ([]store.Notification, int, error)
	UnreadNotifications(ctx context.Context) (int, error)
	MarkNotificationsRead(ctx context.Context, ids []int64, all bool) error
	DeleteNotification(ctx context.Context, id int64) error
	ClearNotifications(ctx context.Context) error
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

type Service struct {
	db  DB
	log *slog.Logger

	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func New(db DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log, subs: map[chan Event]struct{}{}}
}

// stored is the action_json column: the action plus the icon.
type stored struct {
	Icon   string  `json:"icon,omitempty"`
	Action *Action `json:"action,omitempty"`
}

// Add stores n unless its key was seen before or its kind is switched off,
// and tells live subscribers. added is false when it was skipped.
func (s *Service) Add(ctx context.Context, n Notification) (added bool, err error) {
	if set, err := s.Settings(ctx); err == nil && !set.allows(n.Kind) {
		return false, nil
	}
	if n.Level == "" {
		n.Level = "info"
	}
	raw := ""
	if n.Icon != "" || n.Action != nil {
		b, _ := json.Marshal(stored{Icon: n.Icon, Action: n.Action})
		raw = string(b)
	}
	row := &store.Notification{Key: n.Key, Kind: n.Kind, Level: n.Level, Title: n.Title, Body: n.Body, Action: raw, CreatedAt: n.CreatedAt}
	ok, err := s.db.AddNotification(ctx, row, keepNewest)
	if err != nil || !ok {
		return false, err
	}
	item := fromRow(*row)
	unread, _ := s.db.UnreadNotifications(ctx)
	s.publish(Event{Type: "new", Item: &item, Unread: unread})
	return true, nil
}

func fromRow(r store.Notification) Notification {
	n := Notification{ID: r.ID, Key: r.Key, Kind: r.Kind, Level: r.Level, Title: r.Title, Body: r.Body, CreatedAt: r.CreatedAt.UTC()}
	if r.Action != "" {
		var st stored
		if json.Unmarshal([]byte(r.Action), &st) == nil {
			n.Icon, n.Action = st.Icon, st.Action
		}
	}
	if !r.ReadAt.IsZero() {
		t := r.ReadAt.UTC()
		n.ReadAt = &t
	}
	return n
}

// List returns the newest notifications (limit ≤ 0 = 100, at most 500) and the unread count.
func (s *Service) List(ctx context.Context, limit int) ([]Notification, int, error) {
	if limit <= 0 {
		limit = defaultListMax
	}
	limit = min(limit, keepNewest)
	rows, unread, err := s.db.Notifications(ctx, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Notification, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, unread, nil
}

func (s *Service) MarkRead(ctx context.Context, ids []int64, all bool) error {
	if err := s.db.MarkNotificationsRead(ctx, ids, all); err != nil {
		return err
	}
	s.sync(ctx)
	return nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.db.DeleteNotification(ctx, id); err != nil {
		return err
	}
	s.sync(ctx)
	return nil
}

func (s *Service) Clear(ctx context.Context) error {
	if err := s.db.ClearNotifications(ctx); err != nil {
		return err
	}
	s.sync(ctx)
	return nil
}

func (s *Service) sync(ctx context.Context) {
	unread, err := s.db.UnreadNotifications(ctx)
	if err == nil {
		s.publish(Event{Type: "sync", Unread: unread})
	}
}

// Unread is the number of unread notifications.
func (s *Service) Unread(ctx context.Context) int {
	n, _ := s.db.UnreadNotifications(ctx)
	return n
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	set := DefaultSettings()
	raw, err := s.db.Setting(ctx, settingsKey)
	if err != nil || raw == "" {
		return set, err
	}
	err = json.Unmarshal([]byte(raw), &set)
	return set, err
}

func (s *Service) SetSettings(ctx context.Context, set Settings) error {
	b, _ := json.Marshal(set)
	return s.db.SetSetting(ctx, settingsKey, string(b))
}

// Subscribe streams events until cancel is called.
func (s *Service) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *Service) publish(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default: // a stuck reader catches up from the REST list
		}
	}
}
