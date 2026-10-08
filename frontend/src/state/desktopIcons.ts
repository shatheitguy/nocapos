import { create } from 'zustand';
import { usePrefs, workArea, type IconSize } from './prefs';

// App shortcuts on the desktop, placed freely and saved per user.

export interface DesktopIcon {
  appId: string;
  x: number;
  y: number;
}

const CELLS: Record<IconSize, { w: number; h: number; icon: number }> = {
  small: { w: 76, h: 80, icon: 40 },
  medium: { w: 92, h: 96, icon: 52 },
  large: { w: 112, h: 120, icon: 68 },
};

/** The grid cell (and icon image size) for the current icon-size setting. */
export const iconCell = () => CELLS[usePrefs.getState().iconSize] ?? CELLS.medium;

const MARGIN = 16;

const keyFor = (userId: string) => `alfa.desktop.${userId}`;

function load(userId: string): DesktopIcon[] {
  try {
    const v = JSON.parse(localStorage.getItem(keyFor(userId)) ?? '[]');
    return Array.isArray(v) ? v.filter((i) => i && typeof i.appId === 'string') : [];
  } catch {
    return [];
  }
}

/** Keep an icon fully inside the desktop area. */
export function clampIcon(x: number, y: number): { x: number; y: number } {
  const { w: vw, h: vh } = workArea();
  const c = iconCell();
  return {
    x: Math.round(Math.min(Math.max(x, 4), vw - c.w - 4)),
    y: Math.round(Math.min(Math.max(y, 4), vh - c.h - 4)),
  };
}

/** The i-th grid slot, filling columns from the top-right (like macOS). */
function slot(i: number): { x: number; y: number } {
  const { w: vw, h: vh } = workArea();
  const c = iconCell();
  const perCol = Math.max(1, Math.floor((vh - MARGIN) / c.h));
  const col = Math.floor(i / perCol);
  const row = i % perCol;
  return clampIcon(vw - MARGIN - c.w * (col + 1), MARGIN + row * c.h);
}

const overlaps = (a: { x: number; y: number }, b: { x: number; y: number }) => {
  const c = iconCell();
  return Math.abs(a.x - b.x) < c.w - 8 && Math.abs(a.y - b.y) < c.h - 8;
};

/** The free grid slot closest to a point. */
function nearestFreeSlot(p: { x: number; y: number }, others: DesktopIcon[]) {
  let best: { x: number; y: number } | null = null;
  let bestD = Infinity;
  for (let i = 0; i < 400; i++) {
    const s = slot(i);
    if (others.some((o) => overlaps(o, s))) continue;
    const d = (s.x - p.x) ** 2 + (s.y - p.y) ** 2;
    if (d < bestD) {
      best = s;
      bestD = d;
    }
  }
  return best ?? slot(0);
}

interface DesktopIconsStore {
  userId: string | null;
  icons: DesktopIcon[];
  restore: (userId: string) => void;
  _reset: () => void;
  has: (appId: string) => boolean;
  /** Add an icon (or move it) to x,y; without a position it takes the next free slot. */
  place: (appId: string, pos?: { x: number; y: number }) => void;
  remove: (appId: string) => void;
  /** Line every icon up on the grid. */
  cleanUp: () => void;
}

export const useDesktopIcons = create<DesktopIconsStore>((set, get) => {
  const commit = (icons: DesktopIcon[]) => {
    set({ icons });
    const { userId } = get();
    if (!userId) return;
    try {
      localStorage.setItem(keyFor(userId), JSON.stringify(icons));
    } catch {
      /* ignore */
    }
  };

  return {
    userId: null,
    icons: [],
    restore: (userId) => set({ userId, icons: load(userId).map((i) => ({ ...i, ...clampIcon(i.x, i.y) })) }),
    _reset: () => set({ userId: null, icons: [] }),
    has: (appId) => get().icons.some((i) => i.appId === appId),
    place: (appId, pos) => {
      const others = get().icons.filter((i) => i.appId !== appId);
      let at: { x: number; y: number };
      if (!pos) at = nearestFreeSlot(slot(0), others);
      else if (usePrefs.getState().iconSnap) at = nearestFreeSlot(clampIcon(pos.x, pos.y), others);
      else at = clampIcon(pos.x, pos.y);
      const existing = get().icons.some((i) => i.appId === appId);
      commit(existing ? get().icons.map((i) => (i.appId === appId ? { appId, ...at } : i)) : [...others, { appId, ...at }]);
    },
    remove: (appId) => commit(get().icons.filter((i) => i.appId !== appId)),
    cleanUp: () => {
      // Keep the current visual order: top-right first, then down each column.
      const c = iconCell();
      const col = (i: DesktopIcon) => Math.round(i.x / c.w);
      const sorted = [...get().icons].sort((a, b) => col(b) - col(a) || a.y - b.y);
      commit(sorted.map((i, n) => ({ appId: i.appId, ...slot(n) })));
    },
  };
});
