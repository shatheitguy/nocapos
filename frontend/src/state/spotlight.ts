import { create } from 'zustand';

/** Universal search (Spotlight) open/closed, shared by the shortcut and the menu bar. */
export const useSpotlight = create<{ open: boolean; set: (open: boolean) => void; toggle: () => void }>((set) => ({
  open: false,
  set: (open) => set({ open }),
  toggle: () => set((s) => ({ open: !s.open })),
}));
