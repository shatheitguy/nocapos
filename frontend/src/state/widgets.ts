// Free-floating, movable & resizable desktop widgets with a per-user saved
// layout (localStorage, like the window layout).
import { create } from 'zustand';
import { workArea } from './prefs';

export type WidgetType =
  | 'clock'
  | 'analog'
  | 'calendar'
  | 'notes'
  | 'system'
  | 'storage'
  | 'network'
  | 'uptime'
  | 'containers'
  | 'accelerators'
  | 'cores'
  | 'memrings'
  | 'netwave'
  | 'gauges'
  | 'containerGrid'
  | 'scripts'
  | 'transfers';

export interface WidgetDef {
  type: WidgetType;
  name: string;
  icon: string;
  min: { w: number; h: number };
  max: { w: number; h: number };
  default: { w: number; h: number };
  /** Needs admin rights (acts on containers or the host). */
  adminOnly?: boolean;
}

export const WIDGETS: WidgetDef[] = [
  { type: 'clock', name: 'Clock & greeting', icon: 'auto', min: { w: 240, h: 110 }, max: { w: 560, h: 260 }, default: { w: 320, h: 150 } },
  { type: 'analog', name: 'Analog clock', icon: 'auto', min: { w: 140, h: 140 }, max: { w: 320, h: 320 }, default: { w: 180, h: 180 } },
  { type: 'calendar', name: 'Calendar', icon: 'calendar', min: { w: 240, h: 230 }, max: { w: 420, h: 360 }, default: { w: 280, h: 250 } },
  { type: 'notes', name: 'Notes', icon: 'pencil', min: { w: 220, h: 150 }, max: { w: 480, h: 400 }, default: { w: 280, h: 220 } },
  { type: 'system', name: 'System monitor', icon: 'monitor', min: { w: 260, h: 180 }, max: { w: 520, h: 420 }, default: { w: 320, h: 250 } },
  { type: 'storage', name: 'Storage', icon: 'disk', min: { w: 240, h: 130 }, max: { w: 520, h: 320 }, default: { w: 320, h: 170 } },
  { type: 'network', name: 'Network', icon: 'network', min: { w: 240, h: 120 }, max: { w: 520, h: 260 }, default: { w: 320, h: 150 } },
  { type: 'uptime', name: 'Uptime & load', icon: 'power', min: { w: 200, h: 120 }, max: { w: 420, h: 240 }, default: { w: 260, h: 150 } },
  { type: 'containers', name: 'Containers', icon: 'containers', min: { w: 200, h: 120 }, max: { w: 420, h: 220 }, default: { w: 240, h: 150 } },
  { type: 'cores', name: 'CPU core heatmap', icon: 'cpu', min: { w: 260, h: 170 }, max: { w: 640, h: 420 }, default: { w: 340, h: 210 } },
  { type: 'memrings', name: 'Memory & swap rings', icon: 'memory', min: { w: 260, h: 170 }, max: { w: 480, h: 300 }, default: { w: 320, h: 190 } },
  { type: 'netwave', name: 'Network waveform', icon: 'network', min: { w: 260, h: 160 }, max: { w: 640, h: 360 }, default: { w: 340, h: 190 } },
  { type: 'gauges', name: 'Storage gauges', icon: 'disk', min: { w: 220, h: 150 }, max: { w: 640, h: 420 }, default: { w: 320, h: 180 } },
  { type: 'containerGrid', name: 'Container cards', icon: 'containers', min: { w: 280, h: 170 }, max: { w: 860, h: 640 }, default: { w: 420, h: 280 }, adminOnly: true },
  { type: 'scripts', name: 'Quick scripts', icon: 'fileCode', min: { w: 240, h: 130 }, max: { w: 640, h: 560 }, default: { w: 320, h: 230 }, adminOnly: true },
  { type: 'transfers', name: 'Transfers', icon: 'swap', min: { w: 260, h: 140 }, max: { w: 560, h: 560 }, default: { w: 320, h: 220 }, adminOnly: true },
  { type: 'accelerators', name: 'AI accelerators', icon: 'gpu', min: { w: 240, h: 120 }, max: { w: 520, h: 320 }, default: { w: 320, h: 180 } },
];

export const widgetDef = (t: WidgetType) => WIDGETS.find((w) => w.type === t)!;

export interface WidgetInstance {
  type: WidgetType;
  x: number;
  y: number;
  w: number;
  h: number;
}

interface WidgetStore {
  widgets: WidgetInstance[];
  editing: boolean;
  setEditing: (v: boolean) => void;
  move: (type: WidgetType, x: number, y: number) => void;
  resize: (type: WidgetType, w: number, h: number) => void;
  add: (type: WidgetType) => void;
  remove: (type: WidgetType) => void;
  restore: (userId: string) => void;
  resetLayout: () => void;
  _reset: () => void;
}

const GUTTER = 20;

let key: string | null = null;

function defaultLayout(): WidgetInstance[] {
  // A tidy column down the left, matching the old sidebar.
  let y = GUTTER;
  const out: WidgetInstance[] = [];
  for (const t of ['clock', 'system', 'network'] as WidgetType[]) {
    const d = widgetDef(t).default;
    out.push({ type: t, x: GUTTER, y, w: d.w, h: d.h });
    y += d.h + 14;
  }
  return out;
}

function persist(widgets: WidgetInstance[]) {
  if (!key) return;
  try {
    localStorage.setItem(key, JSON.stringify(widgets));
  } catch {
    /* ignore */
  }
}

function clampPos(x: number, y: number, w: number, h: number) {
  // Fall back to a large bound when the window hasn't been measured yet
  // (e.g. during initial mount), so positions aren't collapsed to 0.
  const area = window.innerWidth ? workArea() : { w: 100000, h: 100000 };
  const vw = area.w;
  const vh = area.h;
  return {
    x: Math.min(Math.max(x, 0), Math.max(0, vw - w)),
    y: Math.min(Math.max(y, 0), Math.max(0, vh - h)),
  };
}

export const useWidgets = create<WidgetStore>((set, get) => {
  const update = (fn: (ws: WidgetInstance[]) => WidgetInstance[]) => {
    const widgets = fn(get().widgets);
    set({ widgets });
    persist(widgets);
  };
  return {
    widgets: [],
    editing: false,
    setEditing: (editing) => set({ editing }),

    move: (type, x, y) =>
      update((ws) => ws.map((w) => (w.type === type ? { ...w, ...clampPos(x, y, w.w, w.h) } : w))),

    resize: (type, w, h) =>
      update((ws) =>
        ws.map((it) => {
          if (it.type !== type) return it;
          const d = widgetDef(type);
          const nw = Math.min(Math.max(w, d.min.w), d.max.w);
          const nh = Math.min(Math.max(h, d.min.h), d.max.h);
          return { ...it, w: nw, h: nh, ...clampPos(it.x, it.y, nw, nh) };
        }),
      ),

    add: (type) =>
      update((ws) => {
        if (ws.some((w) => w.type === type)) return ws;
        const d = widgetDef(type).default;
        // Place near the top-left, offset by how many already exist.
        const n = ws.length;
        const pos = clampPos(GUTTER + n * 24, GUTTER + n * 24, d.w, d.h);
        return [...ws, { type, ...pos, w: d.w, h: d.h }];
      }),

    remove: (type) => update((ws) => ws.filter((w) => w.type !== type)),

    restore: (userId) => {
      key = `alfa.widgets.${userId}`;
      let saved: WidgetInstance[] | null = null;
      try {
        const raw = localStorage.getItem(key);
        saved = raw ? (JSON.parse(raw) as WidgetInstance[]) : null;
      } catch {
        saved = null;
      }
      const widgets = Array.isArray(saved)
        ? saved
            .filter((w) => WIDGETS.some((d) => d.type === w.type))
            .map((w) => ({ ...w, ...clampPos(w.x, w.y, w.w, w.h) }))
        : defaultLayout();
      set({ widgets, editing: false });
    },

    resetLayout: () => {
      const widgets = defaultLayout();
      set({ widgets });
      persist(widgets);
    },

    _reset: () => {
      key = null;
      set({ widgets: [], editing: false });
    },
  };
});
