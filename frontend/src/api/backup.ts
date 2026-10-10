import { api, getAccessToken } from './client';

export type RepoKind = 'local' | 'sftp' | 's3';

export interface RepoConfig {
  path?: string;
  host?: string;
  port?: number;
  user?: string;
  endpoint?: string;
  bucket?: string;
  prefix?: string;
  key_id?: string;
}

export interface BackupRepo {
  id: number;
  name: string;
  kind: RepoKind;
  config: RepoConfig;
  location: string;
  created_at: string;
}

export interface BackupSource {
  root?: string;
  path?: string;
  special?: 'nocapos';
}

export interface BackupSchedule {
  every: 'hour' | 'day' | 'week' | 'manual';
  at?: string;
  weekday?: number;
}

export interface BackupKeep {
  hourly?: number;
  daily?: number;
  weekly?: number;
  monthly?: number;
  yearly?: number;
}

export interface BackupProgress {
  percent: number;
  files_done: number;
  files_total: number;
  bytes_done: number;
  bytes_total: number;
}

export interface BackupJob {
  id: string;
  kind: 'backup' | 'restore';
  plan_id: number;
  phase: string;
  progress: BackupProgress;
  done: boolean;
  error?: string;
  result?: string;
  started: string;
  ended?: string;
}

export interface BackupPlan {
  id: number;
  repo_id: number;
  name: string;
  sources: BackupSource[];
  schedule: BackupSchedule;
  keep: BackupKeep;
  enabled: boolean;
  last_run: string;
  last_status: '' | 'ok' | 'failed' | 'canceled';
  last_error?: string;
  last_snapshot?: string;
  last_bytes: number;
  next_run: string;
  job?: BackupJob;
}

export interface BackupStatus {
  installed: boolean;
  version?: string;
  error?: string;
  can_install: boolean;
  ssh_public_key?: string;
}

export interface Snapshot {
  id: string;
  short_id: string;
  time: string;
  paths: string[];
}

export interface SnapNode {
  name: string;
  type: 'file' | 'dir' | 'symlink';
  path: string;
  size: number;
  mtime: string;
}

export interface NewRepo {
  name: string;
  kind: RepoKind;
  config: RepoConfig;
  secret?: string;
  recovery_key?: string;
}

const enc = encodeURIComponent;
const planPath = (id: number) => `/api/v1/backup/plans/${id}`;

export const backupApi = {
  overview: () => api<{ status: BackupStatus; repos: BackupRepo[]; plans: BackupPlan[] }>('/api/v1/backup'),
  install: () => api<{ status: BackupStatus }>('/api/v1/backup/install', { method: 'POST' }),
  addRepo: (r: NewRepo) => api<{ repo: BackupRepo; recovery_key: string }>('/api/v1/backup/repos', { method: 'POST', body: r }),
  deleteRepo: (id: number) => api<void>(`/api/v1/backup/repos/${id}`, { method: 'DELETE' }),
  recoveryKey: (id: number) => api<{ recovery_key: string }>(`/api/v1/backup/repos/${id}/key`, { method: 'POST' }),
  savePlan: (p: Partial<BackupPlan>) =>
    p.id
      ? api<{ id: number }>(planPath(p.id), { method: 'PUT', body: p })
      : api<{ id: number }>('/api/v1/backup/plans', { method: 'POST', body: p }),
  deletePlan: (id: number) => api<void>(planPath(id), { method: 'DELETE' }),
  run: (id: number) => api<BackupJob>(`${planPath(id)}/run`, { method: 'POST' }),
  snapshots: (id: number) => api<{ snapshots: Snapshot[] }>(`${planPath(id)}/snapshots`),
  browse: (id: number, snap: string, path: string) =>
    api<{ entries: SnapNode[] }>(`${planPath(id)}/snapshots/${enc(snap)}/ls?path=${enc(path)}`),
  restore: (planId: number, snapshot: string, paths: string[], mode: 'beside' | 'replace') =>
    api<BackupJob>('/api/v1/backup/restore', { method: 'POST', body: { plan_id: planId, snapshot, paths, mode } }),
  job: (id: string) => api<BackupJob>(`/api/v1/backup/jobs/${enc(id)}`),
  cancel: (id: string) => api<void>(`/api/v1/backup/jobs/${enc(id)}/cancel`, { method: 'POST' }),
};

/** Download a file (or a folder as .tar) from a backup; returns an error message or null. */
export async function downloadFromBackup(planId: number, snap: string, node: SnapNode): Promise<string | null> {
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const dir = node.type === 'dir' ? '&dir=1' : '';
  const r = await fetch(`${planPath(planId)}/snapshots/${enc(snap)}/dump?path=${enc(node.path)}${dir}`, { headers, credentials: 'same-origin' });
  if (!r.ok) {
    try {
      return (await r.json())?.error?.message ?? `Download failed (HTTP ${r.status})`;
    } catch {
      return `Download failed (HTTP ${r.status})`;
    }
  }
  const url = URL.createObjectURL(await r.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = node.type === 'dir' ? `${node.name}.tar` : node.name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
  return null;
}
