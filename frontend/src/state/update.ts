import { create } from 'zustand';
import { api } from '../api/client';

// The in-app update: Settings starts it, the full-screen overlay
// (shell/UpdateOverlay) follows it through NoCapOS restarting itself.

export type UpdateStage = 'download' | 'verify' | 'packages' | 'install' | 'restart' | 'done' | 'failed';

export interface UpdateResult {
  ok: boolean;
  from: string;
  to: string;
  exit_code: number;
  finished_at: string;
  rolled_back: boolean;
  unchanged: boolean;
  error?: string;
}

export interface UpdateRunStatus {
  running: boolean;
  supported: boolean;
  version: string;
  target?: string;
  stage?: UpdateStage;
  percent: number;
  rolling_back?: boolean;
  started?: string;
  log?: string;
  result: UpdateResult | null;
}

/** Where the overlay is at. */
export type UpdatePhase = 'idle' | 'installing' | 'restarting' | 'success' | 'failed' | 'signin';

interface UpdateStore {
  phase: UpdatePhase;
  from: string;
  target: string;
  status: UpdateRunStatus | null;
  percent: number;
  set: (p: Partial<Omit<UpdateStore, 'set'>>) => void;
}

export const useUpdate = create<UpdateStore>((set) => ({
  phase: 'idle',
  from: '',
  target: '',
  status: null,
  percent: 0,
  set: (p) => set(p),
}));

// Survives a reload while the server is down (the page can't ask it then).
const KEY = 'nocap.update';

export function rememberUpdate(from: string, target: string) {
  try {
    sessionStorage.setItem(KEY, JSON.stringify({ from, target }));
  } catch {
    /* private mode: the overlay just won't come back on a reload */
  }
}

export function forgetUpdate() {
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

function remembered(): { from: string; target: string } | null {
  try {
    const v = sessionStorage.getItem(KEY);
    return v ? (JSON.parse(v) as { from: string; target: string }) : null;
  } catch {
    return null;
  }
}

/** Show the overlay for an update that just started. */
export function openUpdate(from: string, target: string) {
  rememberUpdate(from, target);
  useUpdate.getState().set({ phase: 'installing', from, target, percent: 0, status: null });
}

export function closeUpdate() {
  forgetUpdate();
  useUpdate.getState().set({ phase: 'idle', status: null, percent: 0 });
}

/** On page load: an update was in flight when this tab reloaded. */
export function resumeRememberedUpdate() {
  const r = remembered();
  if (r && useUpdate.getState().phase === 'idle') {
    useUpdate.getState().set({ phase: 'restarting', from: r.from, target: r.target, percent: 0, status: null });
  }
}

/** For admins once signed in: bring the overlay back if an update is running. */
export async function resumeRunningUpdate() {
  if (useUpdate.getState().phase !== 'idle') return;
  const r = await api<UpdateRunStatus>('/api/v1/system/update/run');
  if (!r.ok || !r.data.running) return;
  const from = r.data.result?.from ?? r.data.version;
  rememberUpdate(from, r.data.target ?? '');
  useUpdate.getState().set({ phase: 'installing', from, target: r.data.target ?? '', status: r.data, percent: r.data.percent });
}

/** "0.7.2" / "v0.7.2" → "v0.7.2" */
export const vtag = (v?: string) => (v ? (v.startsWith('v') ? v : `v${v}`) : '');
