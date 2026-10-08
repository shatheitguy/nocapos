// Window manager state: stacking, focus, minimize/maximize, snapping, and a
// per-user layout that survives reloads and sign-out/sign-in.
import { create } from 'zustand';
import { workArea } from './prefs';

export type Snap = 'left' | 'right' | null;

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface WinState extends Rect {
  id: string;
  appId: string;
  title: string;
  props?: Record<string, string>;
  z: number;
  minimized: boolean;
  maximized: boolean;
  snap: Snap;
}

export interface OpenRequest {
  appId: string;
  title: string;
  props?: Record<string, string>;
  /** Windows with the same key are reused instead of duplicated. */
  key?: string;
  size: { w: number; h: number };
}

interface WM {
  windows: WinState[];
  focused: string | null;
  topZ: number;
  open: (req: OpenRequest) => void;
  close: (id: string) => void;
  focus: (id: string) => void;
  minimize: (id: string) => void;
  toggleMaximize: (id: string) => void;
  snapTo: (id: string, snap: Snap) => void;
  move: (id: string, x: number, y: number) => void;
  resize: (id: string, r: Rect) => void;
  restoreLayout: (userId: string) => void;
  reset: () => void;
}

let layoutKey: string | null = null;

function persist(windows: WinState[]) {
  if (!layoutKey) return;
  try {
    localStorage.setItem(layoutKey, JSON.stringify(windows));
  } catch {
    /* ignore */
  }
}

function clampRect(r: Rect): Rect {
  const { w: vw, h: vh } = workArea();
  const w = Math.min(r.w, vw);
  const h = Math.min(r.h, vh);
  // Keep at least the title bar reachable.
  const x = Math.min(Math.max(r.x, -w + 120), vw - 120);
  const y = Math.min(Math.max(r.y, 0), vh - 40);
  return { x, y, w, h };
}

export const useWM = create<WM>((set, get) => {
  const update = (fn: (ws: WinState[]) => WinState[], focused?: string | null) => {
    const windows = fn(get().windows);
    set(focused === undefined ? { windows } : { windows, focused });
    persist(windows);
  };

  return {
    windows: [],
    focused: null,
    topZ: 1,

    open: (req) => {
      const id = req.key ?? `${req.appId}:${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
      const existing = get().windows.find((w) => w.id === id);
      if (existing) {
        // Reusing a window with new props (e.g. Settings → a specific page).
        if (req.props) update((ws) => ws.map((w) => (w.id === id ? { ...w, props: req.props } : w)));
        get().focus(id);
        return;
      }
      const n = get().windows.length;
      const { w: vw, h: vh } = workArea();
      const w = Math.min(req.size.w, vw - 32);
      const h = Math.min(req.size.h, vh - 32);
      const z = get().topZ + 1;
      const win: WinState = {
        id,
        appId: req.appId,
        title: req.title,
        props: req.props,
        x: Math.max(8, (vw - w) / 2 + (n % 6) * 28 - 70),
        y: Math.max(8, (vh - h) / 2 + (n % 6) * 24 - 50),
        w,
        h,
        z,
        minimized: false,
        maximized: vw < 640, // tablet/phone: windows go full screen
        snap: null,
      };
      set({ topZ: z });
      update((ws) => [...ws, win], id);
    },

    close: (id) =>
      update(
        (ws) => ws.filter((w) => w.id !== id),
        get().focused === id ? topVisible(get().windows.filter((w) => w.id !== id)) : undefined,
      ),

    focus: (id) => {
      const z = get().topZ + 1;
      set({ topZ: z });
      update((ws) => ws.map((w) => (w.id === id ? { ...w, z, minimized: false } : w)), id);
    },

    minimize: (id) => {
      const rest = get().windows.filter((w) => w.id !== id && !w.minimized);
      update((ws) => ws.map((w) => (w.id === id ? { ...w, minimized: true } : w)), topVisible(rest));
    },

    toggleMaximize: (id) =>
      update((ws) => ws.map((w) => (w.id === id ? { ...w, maximized: !w.maximized, snap: null } : w))),

    snapTo: (id, snap) => update((ws) => ws.map((w) => (w.id === id ? { ...w, snap, maximized: false } : w))),

    move: (id, x, y) =>
      update((ws) => ws.map((w) => (w.id === id ? { ...w, ...clampRect({ ...w, x, y }), maximized: false, snap: null } : w))),

    resize: (id, r) => update((ws) => ws.map((w) => (w.id === id ? { ...w, ...clampRect(r), snap: null } : w))),

    restoreLayout: (userId) => {
      layoutKey = `alfa.layout.${userId}`;
      let saved: WinState[] = [];
      try {
        saved = JSON.parse(localStorage.getItem(layoutKey) ?? '[]') as WinState[];
      } catch {
        saved = [];
      }
      const windows = Array.isArray(saved) ? saved.map((w) => ({ ...w, ...clampRect(w) })) : [];
      const topZ = windows.reduce((m, w) => Math.max(m, w.z), 1);
      set({ windows, topZ, focused: topVisible(windows) });
    },

    reset: () => {
      layoutKey = null;
      set({ windows: [], focused: null, topZ: 1 });
    },
  };
});

function topVisible(ws: WinState[]): string | null {
  const vis = ws.filter((w) => !w.minimized);
  if (!vis.length) return null;
  return vis.reduce((a, b) => (b.z > a.z ? b : a)).id;
}

/** Effective on-screen rect, accounting for maximize/snap. */
export function effectiveRect(w: WinState): Rect {
  // Coordinates are relative to the window layer, which is inset by the dock.
  const { w: vw, h: vh } = workArea();
  if (w.maximized) return { x: 0, y: 0, w: vw, h: vh };
  if (w.snap === 'left') return { x: 0, y: 0, w: Math.floor(vw / 2), h: vh };
  if (w.snap === 'right') return { x: Math.ceil(vw / 2), y: 0, w: Math.floor(vw / 2), h: vh };
  return w;
}
