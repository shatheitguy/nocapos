import { create } from 'zustand';
import { api } from '../api/client';
import { devicesApi, type LinkedDevice } from '../api/devices';

// NoCap Link sync, NoCap OS's side: notifications from the person's phones
// and a shared clipboard. Kept in memory on the server for a day; polled here
// every 15 s while signed in.

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
}

export interface Clip {
  id: number;
  text: string;
  from: string;
  source: 'phone' | 'web';
  /** The phone it came from (source "phone") or is for ("web"; "" = all). */
  device_id: string;
  at: string;
}

export const linkApi = {
  notes: () => api<{ items: PhoneNote[] }>('/api/v1/phone-notifications'),
  dismiss: (n: PhoneNote) =>
    api<{ ok: boolean }>(`/api/v1/phone-notifications/${encodeURIComponent(n.device_id)}/${encodeURIComponent(n.key)}`, { method: 'DELETE' }),
  clips: () => api<{ items: Clip[] }>('/api/v1/clipboard'),
  send: (text: string, device_id?: string) =>
    api<{ ok: boolean; id: number }>('/api/v1/clipboard', { method: 'POST', body: device_id ? { text, device_id } : { text } }),
};

export const POLL_MS = 15_000;
export const CLIP_BANNER_MS = 12_000;

interface LinkStore {
  notes: PhoneNote[];
  clips: Clip[];
  /** This person's own linked phones. */
  phones: LinkedDevice[];
  /** A clip that just arrived from a phone (shown as a banner). */
  clipBanner: Clip | null;
  /** Phone notifications posted after this were added since the center was last opened. */
  seenAt: number;
  load: () => Promise<void>;
  dismiss: (n: PhoneNote) => void;
  markSeen: () => void;
  _reset: () => void;
}

let lastClip = 0;
let loaded = false;

export const useLink = create<LinkStore>((set, get) => ({
  notes: [],
  clips: [],
  phones: [],
  clipBanner: null,
  seenAt: Date.now(),
  load: async () => {
    const [n, c] = await Promise.all([linkApi.notes(), linkApi.clips()]);
    if (n.ok) set({ notes: n.data.items });
    if (!c.ok) return;
    set({ clips: c.data.items });
    const fresh = c.data.items.filter((x) => x.id > lastClip && x.source === 'phone');
    if (c.data.items.length) lastClip = Math.max(lastClip, ...c.data.items.map((x) => x.id));
    // Only clips that arrive while we're watching pop up, not the backlog.
    if (loaded && fresh.length) set({ clipBanner: fresh[0] });
    loaded = true;
  },
  dismiss: (n) => {
    set({ notes: get().notes.filter((x) => !(x.device_id === n.device_id && x.key === n.key)) });
    void linkApi.dismiss(n);
  },
  markSeen: () => set({ seenAt: Date.now() }),
  _reset: () => set({ notes: [], clips: [], phones: [], clipBanner: null, seenAt: Date.now() }),
}));

/** Phone notifications that came in since the center was last opened. */
export const useLinkUnseen = () => useLink((s) => s.notes.filter((n) => n.posted > s.seenAt).length);

/** Follow phone notifications and the clipboard for username; returns a stop function. */
export function watchLink(username: string): () => void {
  lastClip = 0;
  loaded = false;
  const st = useLink.getState();
  void st.load();
  void devicesApi.list().then((r) => {
    if (r.ok) useLink.setState({ phones: r.data.filter((d) => !d.username || d.username === username) });
  });
  const timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') void useLink.getState().load();
  }, POLL_MS);
  return () => {
    window.clearInterval(timer);
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
