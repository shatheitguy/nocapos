// One multiplexed WebSocket for the whole desktop. Components subscribe to
// topics; the socket reconnects with backoff and re-subscribes automatically.
import { api } from './client';

type Handler = (data: unknown) => void;
type EndHandler = (error?: string) => void;
type StatusListener = (online: boolean) => void;

interface Topic {
  handlers: Set<Handler>;
  endHandlers: Set<EndHandler>;
}

const topics = new Map<string, Topic>();
const statusListeners = new Set<StatusListener>();
let ws: WebSocket | null = null;
let wanted = false;
let retry = 0;
let msgId = 0;
let online = false;
let reconnectTimer: number | undefined;

function setOnline(v: boolean) {
  online = v;
  for (const fn of statusListeners) fn(v);
}

export function onSocketStatus(fn: StatusListener): () => void {
  statusListeners.add(fn);
  fn(online);
  return () => statusListeners.delete(fn);
}

function send(op: 'subscribe' | 'unsubscribe', topic: string) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ id: String(++msgId), op, topic }));
}

export async function connect(): Promise<void> {
  wanted = true;
  if (ws) return;
  const t = await api<{ ticket: string }>('/api/v1/ws/ticket', { method: 'POST' });
  if (!wanted || ws) return;
  if (!t.ok) {
    scheduleReconnect();
    return;
  }
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const sock = new WebSocket(`${proto}://${location.host}/ws?ticket=${encodeURIComponent(t.data.ticket)}`);
  ws = sock;
  sock.onopen = () => {
    retry = 0;
    setOnline(true);
    for (const name of topics.keys()) send('subscribe', name);
  };
  sock.onmessage = (ev) => {
    const m = JSON.parse(ev.data as string) as { type: string; topic?: string; data?: unknown; error?: string };
    if (!m.topic) return;
    const t = topics.get(m.topic);
    if (!t) return;
    if (m.type === 'event') for (const h of t.handlers) h(m.data);
    else if (m.type === 'end' || m.type === 'error') for (const h of t.endHandlers) h(m.error);
  };
  sock.onclose = () => {
    if (ws === sock) ws = null;
    setOnline(false);
    if (wanted) scheduleReconnect();
  };
}

function scheduleReconnect() {
  window.clearTimeout(reconnectTimer);
  const delay = Math.min(30_000, 1000 * 2 ** retry++);
  reconnectTimer = window.setTimeout(() => {
    if (wanted && !ws) void connect();
  }, delay);
}

export function disconnect() {
  wanted = false;
  window.clearTimeout(reconnectTimer);
  ws?.close();
  ws = null;
  topics.clear();
  setOnline(false);
}

/** Subscribe to a topic. Returns an unsubscribe function. */
export function subscribe(topic: string, onData: Handler, onEnd?: EndHandler): () => void {
  let t = topics.get(topic);
  if (!t) {
    t = { handlers: new Set(), endHandlers: new Set() };
    topics.set(topic, t);
    send('subscribe', topic);
  }
  t.handlers.add(onData);
  if (onEnd) t.endHandlers.add(onEnd);
  return () => {
    const cur = topics.get(topic);
    if (!cur) return;
    cur.handlers.delete(onData);
    if (onEnd) cur.endHandlers.delete(onEnd);
    if (cur.handlers.size === 0) {
      topics.delete(topic);
      send('unsubscribe', topic);
    }
  };
}

/** Re-send a subscribe for a topic whose feed ended (e.g. Docker came back). */
export function resubscribe(topic: string) {
  if (topics.has(topic)) send('subscribe', topic);
}
