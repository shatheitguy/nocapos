// Live system state shared by widgets and apps: one metrics subscription for
// the whole desktop, with a short history for charts.
import { create } from 'zustand';
import type { Snapshot, SystemInfo } from '../api/types';

export const HISTORY = 90; // samples (~3 min at the default 2 s interval)

export interface Sample {
  t: number;
  cpu: number;
  mem: number;
  rx: number;
  tx: number;
  cores: number[]; // per-core CPU %, empty when the host doesn't report it
}

interface SystemStore {
  info: SystemInfo | null;
  latest: Snapshot | null;
  history: Sample[];
  online: boolean;
  setInfo: (i: SystemInfo) => void;
  push: (s: Snapshot) => void;
  setOnline: (v: boolean) => void;
}

export const useSystem = create<SystemStore>((set) => ({
  info: null,
  latest: null,
  history: [],
  online: false,
  setInfo: (info) => set({ info }),
  push: (s) =>
    set((st) => {
      const net = s.network ?? [];
      const sample: Sample = {
        t: new Date(s.time).getTime(),
        cpu: s.cpu.percent,
        mem: s.memory.percent,
        rx: net.reduce((a, n) => a + n.rx_rate, 0),
        tx: net.reduce((a, n) => a + n.tx_rate, 0),
        cores: s.cpu.per_core ?? [],
      };
      const last = st.history[st.history.length - 1];
      if (last && last.t === sample.t) return { latest: s };
      return { latest: s, history: [...st.history, sample].slice(-HISTORY) };
    }),
  setOnline: (online) => set({ online }),
}));
