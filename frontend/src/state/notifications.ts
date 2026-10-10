import { create } from 'zustand';
import { api } from '../api/client';
import { subscribe } from '../api/socket';
import { openApp } from '../apps/meta';
import { playCue } from '../lib/uiSound';
import { usePrefs } from './prefs';

// System notifications (admins): NoCapOS and app updates, app crashes, storage
// health and failed backups. New ones arrive on the `notifications` topic and
// show as banners (macOS or Windows style); all of them stay in the
// Notification Center until cleared. In-app feedback ("Saved") stays a toast.

export type NoticeLevel = 'info' | 'success' | 'warning' | 'error';

export interface Notice {
  id: number;
  key: string;
  kind: 'update' | 'app_update' | 'app_error' | 'storage' | 'backup' | 'test';
  level: NoticeLevel;
  title: string;
  body?: string;
  /** NoCapOS app id, or "store:<id>" for an App Store app. */
  icon?: string;
  action?: { app: string; props?: Record<string, string> };
  created_at: string;
  read_at?: string;
}

/** The "Notify me about" switches (kept on the server). */
export interface NotifySettings {
  update: boolean;
  app_updates: boolean;
  app_errors: boolean;
  storage: boolean;
  backups: boolean;
}

interface Event {
  type: 'new' | 'sync';
  item?: Notice;
  unread: number;
}

export const notifyApi = {
  list: (limit = 100) => api<{ items: Notice[]; unread: number }>(`/api/v1/notifications?limit=${limit}`),
  read: (body: { ids?: number[]; all?: boolean }) => api<{ unread: number }>('/api/v1/notifications/read', { method: 'POST', body }),
  remove: (id: number) => api<void>(`/api/v1/notifications/${id}`, { method: 'DELETE' }),
  clear: () => api<void>('/api/v1/notifications', { method: 'DELETE' }),
  test: () => api<void>('/api/v1/notifications/test', { method: 'POST' }),
  settings: () => api<NotifySettings>('/api/v1/notifications/settings'),
  saveSettings: (s: NotifySettings) => api<NotifySettings>('/api/v1/notifications/settings', { method: 'PUT', body: s }),
};

/** How long a banner stays up (hovering pauses it). */
export const BANNER_MS = 6000;
const MAX_BANNERS = 3;

interface Store {
  items: Notice[];
  unread: number;
  /** Banners on screen, oldest first. */
  banners: Notice[];
  centerOpen: boolean;
  load: () => Promise<void>;
  dismissBanner: (id: number) => void;
  setCenter: (open: boolean) => void;
  remove: (id: number) => void;
  removeMany: (ids: number[]) => void;
  clear: () => void;
  _reset: () => void;
}

export const useNotifications = create<Store>((set, get) => ({
  items: [],
  unread: 0,
  banners: [],
  centerOpen: false,
  load: async () => {
    const r = await notifyApi.list();
    if (r.ok) set({ items: r.data.items, unread: r.data.unread });
  },
  dismissBanner: (id) => set({ banners: get().banners.filter((b) => b.id !== id) }),
  setCenter: (open) => {
    set({ centerOpen: open, ...(open ? { banners: [] } : {}) });
    if (!open) return;
    // Opening the center marks what it shows as read.
    const ids = get().items.filter((n) => !n.read_at).map((n) => n.id);
    if (!ids.length) return;
    const now = new Date().toISOString();
    set({ items: get().items.map((n) => (n.read_at ? n : { ...n, read_at: now })), unread: Math.max(0, get().unread - ids.length) });
    void notifyApi.read({ ids }).then((r) => r.ok && set({ unread: r.data.unread }));
  },
  remove: (id) => get().removeMany([id]),
  removeMany: (ids) => {
    const gone = new Set(ids);
    set({ items: get().items.filter((n) => !gone.has(n.id)), banners: get().banners.filter((b) => !gone.has(b.id)) });
    for (const id of ids) void notifyApi.remove(id);
  },
  clear: () => {
    set({ items: [], banners: [], unread: 0 });
    void notifyApi.clear();
  },
  _reset: () => set({ items: [], unread: 0, banners: [], centerOpen: false }),
}));

/** The notification look for this device: macOS on Macs, iPhones and iPads, Windows elsewhere. */
export const notifyLook = (): 'mac' | 'windows' => (isApple() ? 'mac' : 'windows');

export function isApple(): boolean {
  const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
  const platform = nav.userAgentData?.platform || nav.platform || '';
  return /mac|iphone|ipad|ipod/i.test(platform) || /Macintosh|iPhone|iPad|iPod/.test(nav.userAgent);
}

export const useNotifyStyle = notifyLook;

function arrived(n: Notice) {
  const s = useNotifications.getState();
  if (s.items.some((x) => x.id === n.id)) return;
  useNotifications.setState({ items: [n, ...s.items].slice(0, 200) });
  const focus = usePrefs.getState().focusMode;
  // Focus: no banners or sounds, except errors (as with toasts). They still land in the center.
  if (focus && n.level !== 'error') return;
  playCue(n.level === 'error' ? 'error' : n.level === 'success' ? 'success' : 'info');
  if (s.centerOpen) return; // already on screen in the center
  useNotifications.setState({ banners: [...useNotifications.getState().banners, n].slice(-MAX_BANNERS) });
}

let syncTimer: number | undefined;

/** Load the list and follow new notifications; returns a stop function. */
export function watchNotifications(): () => void {
  const st = useNotifications.getState();
  void st.load();
  const off = subscribe('notifications', (d) => {
    const e = d as Event;
    if (e.type === 'new' && e.item) {
      arrived(e.item);
      useNotifications.setState({ unread: e.unread });
    } else if (e.type === 'sync') {
      useNotifications.setState({ unread: e.unread });
      // Read or deleted elsewhere (or a reconnect): refresh the list shortly.
      window.clearTimeout(syncTimer);
      syncTimer = window.setTimeout(() => void useNotifications.getState().load(), 400);
    }
  });
  return () => {
    off();
    window.clearTimeout(syncTimer);
    useNotifications.getState()._reset();
  };
}

/** Do what a notification is about (open its app) and mark it read. */
export function openNotice(n: Notice) {
  const s = useNotifications.getState();
  s.dismissBanner(n.id);
  if (s.centerOpen) s.setCenter(false);
  if (n.action?.app) openApp(n.action.app, n.action.props ? { props: n.action.props } : {});
  if (!n.read_at) {
    useNotifications.setState({
      items: s.items.map((x) => (x.id === n.id ? { ...x, read_at: new Date().toISOString() } : x)),
      unread: Math.max(0, s.unread - 1),
    });
    void notifyApi.read({ ids: [n.id] });
  }
}

/** "now", "5m ago", "2h ago", "Yesterday", then a date (macOS style). */
export function relTime(iso: string, now = Date.now()): string {
  const t = new Date(iso).getTime();
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return 'now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400 && new Date(t).getDate() === new Date(now).getDate()) return `${Math.floor(s / 3600)}h ago`;
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  if (new Date(t).toDateString() === y.toDateString()) return 'Yesterday';
  return new Date(t).toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}

export const isToday = (iso: string, now = new Date()) => new Date(iso).toDateString() === now.toDateString();
