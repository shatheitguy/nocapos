import { api } from './client';

export type CloudKind = 'drive' | 'dropbox' | 'onedrive' | 's3' | 'webdav' | 'sftp';

export interface CloudStatus {
  installed: boolean;
  version?: string;
  error?: string;
  can_install: boolean;
}

export interface CloudAccount {
  id: number;
  name: string;
  kind: CloudKind;
  detail: string;
  created_at: string;
}

export interface CloudSchedule {
  every: 'manual' | 'day' | 'week';
  at?: string;
  weekday?: number;
}

export interface CloudProgress {
  bytes: number;
  total_bytes: number;
  files: number;
  total_files: number;
  errors: number;
  speed: number;
}

export interface CloudJob {
  id: string;
  import_id: number;
  progress: CloudProgress;
  done: boolean;
  error?: string;
}

export interface CloudImport {
  id: number;
  account_id: number;
  name: string;
  source: string;
  dest_root: string;
  dest_path: string;
  schedule: CloudSchedule;
  last_run: string;
  last_status: '' | 'ok' | 'failed' | 'canceled';
  last_error?: string;
  last_bytes: number;
  last_files: number;
  next_run: string;
  job?: CloudJob;
}

export interface NewCloudAccount {
  name: string;
  kind: 's3' | 'webdav' | 'sftp';
  endpoint?: string;
  key_id?: string;
  secret?: string;
  url?: string;
  vendor?: string;
  host?: string;
  port?: number;
  user?: string;
  password?: string;
}

export const cloudApi = {
  overview: () => api<{ status: CloudStatus; accounts: CloudAccount[]; imports: CloudImport[] }>('/api/v1/cloud'),
  install: () => api<{ status: CloudStatus }>('/api/v1/cloud/install', { method: 'POST' }),
  addAccount: (a: NewCloudAccount) => api<CloudAccount>('/api/v1/cloud/accounts', { method: 'POST', body: a }),
  signInStart: (kind: CloudKind) => api<{ session: string; url: string }>('/api/v1/cloud/signin/start', { method: 'POST', body: { kind } }),
  signInFinish: (session: string, address: string, name: string) =>
    api<CloudAccount>('/api/v1/cloud/signin/finish', { method: 'POST', body: { session, address, name } }),
  removeAccount: (id: number) => api<void>(`/api/v1/cloud/accounts/${id}`, { method: 'DELETE' }),
  folders: (id: number, path: string) => api<{ folders: { name: string; path: string }[] }>(`/api/v1/cloud/accounts/${id}/folders?path=${encodeURIComponent(path)}`),
  saveImport: (i: Partial<CloudImport>) =>
    i.id ? api<{ id: number }>(`/api/v1/cloud/imports/${i.id}`, { method: 'PUT', body: i }) : api<{ id: number }>('/api/v1/cloud/imports', { method: 'POST', body: i }),
  removeImport: (id: number) => api<void>(`/api/v1/cloud/imports/${id}`, { method: 'DELETE' }),
  run: (id: number) => api<CloudJob>(`/api/v1/cloud/imports/${id}/run`, { method: 'POST' }),
  cancel: (job: string) => api<void>(`/api/v1/cloud/jobs/${encodeURIComponent(job)}/cancel`, { method: 'POST' }),
};
