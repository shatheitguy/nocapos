import { api } from './client';

// The App data folder: one folder per app (<root>/<app>/<folder>), moving
// apps' Docker volumes into it, moving it, and cleaning up after moves.

export interface AppDataState {
  root: string;
  default: string;
  /** A copy left by moving the folder, waiting to be removed or kept. */
  old_root?: string;
}

/** One app's folder in App data. */
export interface AppFolder {
  name: string;
  path: string;
  /** Bytes; -1 while still being added up. */
  size: number;
  title: string;
  icon?: string;
  logo?: string;
  logo_mono?: boolean;
  tile?: [string, string];
}

export interface VolMove {
  volume: string;
  target: string;
  dir: string;
}

/** An app that still keeps data in Docker volumes. */
export interface AppDataCandidate {
  app: string;
  kind: 'app' | 'stack' | 'container';
  containers: string[];
  volumes: VolMove[];
  blocked?: string;
}

export interface AppDataJob {
  id: string;
  kind: 'move' | 'organize' | 'root' | 'cleanup';
  app?: string;
  phase: string;
  percent: number;
  done: boolean;
  error?: string;
  results?: { app: string; ok: boolean; error?: string }[];
}

export interface AppDataOverview {
  available: boolean;
  docker?: boolean;
  state?: AppDataState;
  folders?: AppFolder[];
  candidates?: AppDataCandidate[];
  candidates_error?: string;
  job?: AppDataJob | null;
  share?: boolean;
}

export interface OldVolume {
  volume: string;
  app: string;
  dir: string;
  moved_at: string;
  size: number;
  in_use: boolean;
}

export const appDataApi = {
  overview: () => api<AppDataOverview>('/api/v1/appdata'),
  move: (app: string) => api<AppDataJob>('/api/v1/appdata/move', { method: 'POST', body: { app } }),
  organizeAll: () => api<AppDataJob>('/api/v1/appdata/move', { method: 'POST', body: { all: true } }),
  job: () => api<{ job: AppDataJob | null }>('/api/v1/appdata/job'),
  setRoot: (path: string) => api<AppDataJob>('/api/v1/appdata/root', { method: 'POST', body: { path } }),
  oldRoot: (action: 'remove' | 'keep') => api<AppDataState>('/api/v1/appdata/root/old', { method: 'POST', body: { action } }),
  oldVolumes: () => api<{ volumes: OldVolume[] }>('/api/v1/appdata/old-volumes'),
  removeOldVolumes: (volumes: string[]) => api<{ removed: string[]; error?: string }>('/api/v1/appdata/old-volumes/remove', { method: 'POST', body: { volumes } }),
  share: (enabled: boolean) => api<{ enabled: boolean; warning?: string }>('/api/v1/appdata/share', { method: 'PUT', body: { enabled } }),
  suggest: (owner: string, name: string) =>
    api<{ path: string; root: string }>(`/api/v1/appdata/suggest?owner=${encodeURIComponent(owner)}&name=${encodeURIComponent(name)}`),
};

/** The Files location of the whole App data folder. */
export const APPDATA_ROOT = 'appdata';

export { safeName, sizeText, suggestPath } from '../lib/appdataPaths';
