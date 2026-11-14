import { create } from 'zustand';
import { api, type ApiResult } from '../api/client';
import { onSocketStatus, subscribe } from '../api/socket';

// NoCap Link sync, NoCap OS's side: notifications and status from the
// person's phones, a shared clipboard, and "Open on NoCap" carries. Live over
// the "link.phone" topic; polled as a fallback (every 15 s while the socket is
// down, once a minute while it's up).

export type CallState = 'incoming' | 'ongoing' | 'missed';

export interface PhoneNote {
  device_id: string;
  device_name: string;
  key: string;
  app: string;
  package: string;
  title: string;
  text: string;
  /** Base64 PNG, or "". */
  icon: string;
  /** When the phone showed it (ms since the epoch). */
  posted: number;
  kind?: '' | 'message' | 'call';
  call_state?: CallState;
  number?: string;
  sender?: string;
  can_reply?: boolean;
  can_decline?: boolean;
}

export interface Clip {
  id: number;
  /** "text", or "image" (a PNG or JPEG picture; text is then empty). Older servers leave it out. */
  kind?: 'text' | 'image';
  text: string;
  /** A picture's type and size in bytes. */
  mime?: string;
  size?: number;
  from: string;
  source: 'phone' | 'web' | 'desktop';
  /** The phone it came from (source "phone") or is for ("web"; "" = all). */
  device_id: string;
  at: string;
}

export interface ClipSettings {
  /** Keep a short history; off keeps only the last item. */
  history: boolean;
  /** NoCap Desktop's own clipboard (this machine's screen) joins in. */
  desktop: boolean;
}

/** Whether a clip came from another of the person's devices (a phone or NoCap Desktop). */
export const fromElsewhere = (c: Clip) => c.source === 'phone' || c.source === 'desktop';

/** How the phone reaches this server: on the same network or over the internet. */
export type Route = 'local' | 'remote' | '';
export type PhoneNetwork = 'wifi' | 'cellular' | 'ethernet' | 'other' | 'none' | '';

export interface PhoneState {
  /** Percent, -1 = unknown. */
  battery: number;
  charging: boolean;
  network: PhoneNetwork;
  /** 0-4 bars, -1 = unknown. */
  signal: number;
  route: Route;
  /** "Find my phone" is ringing right now. */
  ringing: boolean;
  at: string;
}

export interface Phone {
  id: string;
  name: string;
  platform: string;
  online: boolean;
  route: Route;
  last_seen?: string;
  state?: PhoneState;
}

/** A phone did "Open on NoCap": a web address, or one of this person's files. */
export interface Carry {
  id: string | number;
  device_id: string;
  device_name: string;
  url?: string;
  title: string;
  root?: string;
  path?: string;
  at: string;
}

export type PhoneAction =
  | { kind: 'reply'; key: string; text: string }
  | { kind: 'decline'; key: string }
  | { kind: 'ring' }
  | { kind: 'stop_ring' }
  | { kind: 'open'; url: string }
  | { kind: 'file'; root: string; path: string; open?: boolean };

export interface LinkAddresses {
  nocap_id: string;
  lan: string[];
  remote: string;
  /** Administrators only: what was set by hand. */
  saved_lan: string[];
  saved_remote: string;
}

type LinkEvent = { type: 'changed' } | { type: 'open'; carry: Carry } | { type: 'clip'; clip: Clip };

const enc = encodeURIComponent;

export const linkApi = {
  notes: () => api<{ items: PhoneNote[] }>('/api/v1/phone-notifications'),
  dismiss: (n: PhoneNote) => api<{ ok: boolean }>(`/api/v1/phone-notifications/${enc(n.device_id)}/${enc(n.key)}`, { method: 'DELETE' }),
  clips: () => api<{ items: Clip[] }>('/api/v1/clipboard'),
  send: (text: string, device_id?: string) =>
    api<{ ok: boolean; id: number }>('/api/v1/clipboard', { method: 'POST', body: device_id ? { text, device_id } : { text } }),
  clearClips: () => api<{ ok: boolean }>('/api/v1/clipboard', { method: 'DELETE' }),
  clipSettings: () => api<ClipSettings>('/api/v1/clipboard/settings'),
  saveClipSettings: (c: Partial<ClipSettings>) => api<ClipSettings>('/api/v1/clipboard/settings', { method: 'PUT', body: c }),
  phones: () => api<{ phones: Phone[]; carries: Carry[] }>('/api/v1/phones'),
  action: (deviceId: string, a: PhoneAction) =>
    api<{ ok: boolean; id: string | number }>(`/api/v1/phones/${enc(deviceId)}/actions`, { method: 'POST', body: a }),
  addresses: () => api<LinkAddresses>('/api/v1/link/addresses'),
  saveAddresses: (remote: string, lan: string[]) =>
    api<LinkAddresses>('/api/v1/link/addresses', { method: 'PUT', body: { remote, lan } }),
};

/** The API's error code ("gone", "not_found", …), when there is one. */
export const errorCode = (r: ApiResult<unknown>): string => (r.data as { error?: { code?: string } } | null)?.error?.code ?? '';

export const POLL_MS = 15_000;
/** While the socket is up, the poll only catches up on anything missed. */
export const SOCKET_POLL_MS = 60_000;
export const CLIP_BANNER_MS = 12_000;
export const CARRY_BANNER_MS = 20_000;

interface LinkStore {
  notes: PhoneNote[];
  clips: Clip[];
  /** This person's own linked phones, with their live status. */
  phones: Phone[];
  /** Recent "Open on NoCap" carries, newest first. */
  carries: Carry[];
  /** A clip that just arrived from a phone (shown as a banner). */
  clipBanner: Clip | null;
  /** A carry that just arrived (shown as a banner with an Open button). */
  carryBanner: Carry | null;
  /** Phone notifications posted after this were added since the center was last opened. */
  seenAt: number;
  load: () => Promise<void>;
  loadPhones: () => Promise<void>;
  dismiss: (n: PhoneNote) => void;
  /** Forget a notification here only (the phone already dropped it). */
  drop: (n: PhoneNote) => void;
  markSeen: () => void;
  _reset: () => void;
}

let lastClip = 0;
let loaded = false;
let lastLoad = 0;

const sameNote = (a: PhoneNote, b: PhoneNote) => a.device_id === b.device_id && a.key === b.key;

export const useLink = create<LinkStore>((set, get) => ({
  notes: [],
  clips: [],
  phones: [],
  carries: [],
  clipBanner: null,
  carryBanner: null,
  seenAt: Date.now(),
  load: async () => {
    lastLoad = Date.now();
    const [n, c] = await Promise.all([linkApi.notes(), linkApi.clips(), get().loadPhones()]);
    if (n.ok) set({ notes: n.data.items ?? [] });
    if (!c.ok) return;
    const items = c.data.items ?? [];
    set({ clips: items });
    const fresh = items.filter((x) => x.id > lastClip && fromElsewhere(x));
    if (items.length) lastClip = Math.max(lastClip, ...items.map((x) => x.id));
    // Only clips that arrive while we're watching pop up, not the backlog.
    if (loaded && fresh.length) {
      set({ clipBanner: fresh[0] });
      clipHandler?.(fresh[0]);
    }
    loaded = true;
  },
  loadPhones: async () => {
    const r = await linkApi.phones();
    if (r.ok) set({ phones: r.data.phones ?? [], carries: r.data.carries ?? [] });
  },
  dismiss: (n) => {
    get().drop(n);
    void linkApi.dismiss(n);
  },
  drop: (n) => set({ notes: get().notes.filter((x) => !sameNote(x, n)) }),
  markSeen: () => set({ seenAt: Date.now() }),
  _reset: () => set({ notes: [], clips: [], phones: [], carries: [], clipBanner: null, carryBanner: null, seenAt: Date.now() }),
}));

/** Phone notifications that came in since the center was last opened. */
export const useLinkUnseen = () => useLink((s) => s.notes.filter((n) => n.posted > s.seenAt).length);

let clipHandler: ((c: Clip) => void) | null = null;

/** Called with every clip that arrives live from another device (see clipSync.ts). */
export function onClipArrived(fn: ((c: Clip) => void) | null) {
  clipHandler = fn;
}

function clipArrived(c: Clip) {
  const s = useLink.getState();
  if (s.clips.some((x) => x.id === c.id)) return;
  const fresh = c.id > lastClip && fromElsewhere(c);
  lastClip = Math.max(lastClip, c.id);
  useLink.setState({ clips: [c, ...s.clips].slice(0, 20), ...(fresh ? { clipBanner: c } : {}) });
  if (fresh) clipHandler?.(c);
}

function carryArrived(h: Carry) {
  const s = useLink.getState();
  useLink.setState({ carries: [h, ...s.carries.filter((x) => x.id !== h.id)].slice(0, 20), carryBanner: h });
}

/** Follow the person's phones; returns a stop function. */
export function watchLink(_username: string): () => void {
  lastClip = 0;
  loaded = false;
  void useLink.getState().load();

  let reloadTimer: number | undefined;
  const reload = () => {
    window.clearTimeout(reloadTimer);
    reloadTimer = window.setTimeout(() => void useLink.getState().load(), 300);
  };
  const off = subscribe('link.phone', (d) => {
    const e = d as LinkEvent;
    if (e.type === 'open' && e.carry) carryArrived(e.carry);
    else if (e.type === 'clip' && e.clip) clipArrived(e.clip);
    else reload();
  });
  // A reconnect may have missed events: catch up.
  let live: boolean | null = null;
  const offStatus = onSocketStatus((on) => {
    if (on && live === false) reload();
    live = on;
  });
  const timer = window.setInterval(() => {
    if (document.visibilityState !== 'visible') return;
    if (!live || Date.now() - lastLoad >= SOCKET_POLL_MS) void useLink.getState().load();
  }, POLL_MS);
  return () => {
    off();
    offStatus();
    window.clearInterval(timer);
    window.clearTimeout(reloadTimer);
    useLink.getState()._reset();
  };
}

/** Copy text to this computer's clipboard; false when the browser said no. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** This computer's clipboard text, or null when the browser won't share it. */
export async function readClipboard(): Promise<string | null> {
  try {
    const t = await navigator.clipboard.readText();
    return t || null;
  } catch {
    return null;
  }
}
