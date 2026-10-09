import { api } from './client';

export interface NetSupport {
  native: boolean;
  can_install: boolean;
  smb: boolean;
  nfs: boolean;
  samba: boolean;
}

export interface NetDrive {
  id: number;
  name: string;
  kind: 'smb' | 'nfs';
  host: string;
  share: string;
  username?: string;
  auto: boolean;
  mounted: boolean;
  error?: string;
  root_id: string;
  address: string;
  created_at: string;
}

export interface NetShare {
  id: number;
  name: string;
  root: string;
  path: string;
  read_only: boolean;
  missing?: boolean;
}

export interface Sharing {
  user: string;
  password_set: boolean;
  smb: boolean;
  webdav: boolean;
  shares: NetShare[];
}

export interface NewDrive {
  name: string;
  kind: 'smb' | 'nfs';
  host: string;
  share: string;
  username?: string;
  password?: string;
  auto: boolean;
}

export const netApi = {
  overview: () => api<{ support: NetSupport; drives: NetDrive[]; sharing: Sharing }>('/api/v1/netdrives'),
  install: (tool: 'smb' | 'nfs' | 'samba') => api<{ support: NetSupport }>('/api/v1/netdrives/install', { method: 'POST', body: { tool } }),
  add: (d: NewDrive) => api<NetDrive>('/api/v1/netdrives', { method: 'POST', body: d }),
  connect: (id: number, on: boolean) => api<NetDrive>(`/api/v1/netdrives/${id}/${on ? 'connect' : 'disconnect'}`, { method: 'POST' }),
  setAuto: (id: number, auto: boolean) => api<void>(`/api/v1/netdrives/${id}`, { method: 'PATCH', body: { auto } }),
  remove: (id: number) => api<void>(`/api/v1/netdrives/${id}`, { method: 'DELETE' }),
  exports: (host: string) => api<{ exports: string[] }>(`/api/v1/netdrives/exports?host=${encodeURIComponent(host)}`),
  setPassword: (password: string) => api<void>('/api/v1/sharing/password', { method: 'PUT', body: { password } }),
  setProtocols: (p: { smb?: boolean; webdav?: boolean }) => api<void>('/api/v1/sharing', { method: 'PUT', body: p }),
  addShare: (s: { name: string; root: string; path: string; read_only: boolean }) => api<NetShare>('/api/v1/sharing/shares', { method: 'POST', body: s }),
  setShareReadOnly: (id: number, read_only: boolean) => api<void>(`/api/v1/sharing/shares/${id}`, { method: 'PATCH', body: { read_only } }),
  removeShare: (id: number) => api<void>(`/api/v1/sharing/shares/${id}`, { method: 'DELETE' }),
};
