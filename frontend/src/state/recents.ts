import { create } from 'zustand';

const KEY = 'alfa.recents';
const MAX = 4;

function load(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]');
    return Array.isArray(v) ? v.slice(0, MAX) : [];
  } catch {
    return [];
  }
}

interface RecentsStore {
  recents: string[]; // app ids, most recent first
  push: (appId: string) => void;
}

export const useRecents = create<RecentsStore>((set, get) => ({
  recents: load(),
  push: (appId) => {
    const next = [appId, ...get().recents.filter((id) => id !== appId)].slice(0, MAX);
    set({ recents: next });
    try {
      localStorage.setItem(KEY, JSON.stringify(next));
    } catch {
      /* ignore */
    }
  },
}));
