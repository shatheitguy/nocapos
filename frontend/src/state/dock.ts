import { create } from 'zustand';
import { PINNED } from '../apps/meta';

// Per-user dock pins. Falls back to the default PINNED set the first time.
const keyFor = (userId: string) => `alfa.dock.${userId}`;

function load(userId: string): string[] {
  try {
    const raw = localStorage.getItem(keyFor(userId));
    if (raw === null) return [...PINNED];
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [...PINNED];
  } catch {
    return [...PINNED];
  }
}

interface DockStore {
  userId: string | null;
  pins: string[]; // app ids, left-to-right
  restore: (userId: string) => void;
  _reset: () => void;
  has: (id: string) => boolean;
  add: (id: string) => void;
  remove: (id: string) => void;
  /** Move `id` so it sits where `targetId` is (reorder). */
  moveTo: (id: string, targetId: string) => void;
  /** Pin `id` (or move it if already pinned) just before `beforeId`, or at the end. */
  place: (id: string, beforeId?: string | null) => void;
  resetDefaults: () => void;
}

export const useDock = create<DockStore>((set, get) => {
  const persist = (pins: string[]) => {
    const { userId } = get();
    if (!userId) return;
    try {
      localStorage.setItem(keyFor(userId), JSON.stringify(pins));
    } catch {
      /* ignore */
    }
  };

  return {
    userId: null,
    pins: [...PINNED],
    restore: (userId) => set({ userId, pins: load(userId) }),
    _reset: () => set({ userId: null, pins: [...PINNED] }),
    has: (id) => get().pins.includes(id),
    add: (id) => {
      if (get().pins.includes(id)) return;
      const pins = [...get().pins, id];
      persist(pins);
      set({ pins });
    },
    remove: (id) => {
      const pins = get().pins.filter((p) => p !== id);
      persist(pins);
      set({ pins });
    },
    moveTo: (id, targetId) => {
      if (id === targetId) return;
      const rest = get().pins.filter((p) => p !== id);
      const at = rest.indexOf(targetId);
      if (at < 0 || !get().pins.includes(id)) return;
      rest.splice(at, 0, id); // drop onto target's slot, pushing target aside
      persist(rest);
      set({ pins: rest });
    },
    place: (id, beforeId) => {
      const rest = get().pins.filter((p) => p !== id);
      const at = beforeId ? rest.indexOf(beforeId) : -1;
      rest.splice(at < 0 ? rest.length : at, 0, id);
      persist(rest);
      set({ pins: rest });
    },
    resetDefaults: () => {
      persist([...PINNED]);
      set({ pins: [...PINNED] });
    },
  };
});
