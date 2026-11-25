// Live system state shared by widgets and apps: one metrics subscription for
// the whole desktop, with a short history for charts.
import { useEffect } from 'react';
import { create } from 'zustand';
import { api } from '../api/client';
import { subscribe } from '../api/socket';
import type { Snapshot, SystemInfo } from '../api/types';
import { useDocumentVisible } from '../lib/hooks';

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

// ---- the live metrics feed ----
// alfad sends a snapshot every couple of seconds. Only pages that show one
// (widgets, Monitor, Settings) take it, and only while they're visible: an
// app window or a desktop without such parts never receives or renders it.

let feedUsers = 0;
let stopFeed: (() => void) | null = null;

function startFeed() {
  const sys = useSystem.getState();
  stopFeed = subscribe('system.metrics', (d) => sys.push(d as Snapshot));
  const last = sys.latest ? new Date(sys.latest.time).getTime() : 0;
  if (Date.now() - last > 5000) void api<Snapshot>('/api/v1/system/metrics').then((r) => r.ok && sys.push(r.data));
}

/** Keeps the live metrics coming while the calling component is shown. */
export function useMetricsFeed(on = true) {
  const visible = useDocumentVisible();
  useEffect(() => {
    if (!on || !visible) return;
    if (feedUsers++ === 0) startFeed();
    return () => {
      if (--feedUsers === 0 && stopFeed) {
        stopFeed();
        stopFeed = null;
      }
    };
  }, [on, visible]);
}

/** useSystem for the live parts (latest, history): also keeps the feed on. */
export function useLive<T>(select: (s: SystemStore) => T): T {
  useMetricsFeed();
  return useSystem(select);
}
