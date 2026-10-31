import { api } from './client';

export interface PairInfo {
  nocap_id: string;
  name: string;
  version: string;
}

export interface PairCode {
  code: string;
  expires_at: string;
  nocap_id: string;
  pair_url: string;
}

export interface LinkedDevice {
  id: string;
  name: string;
  platform: 'android' | 'ios' | 'other';
  created_at: string;
  last_seen_at: string | null;
  app_version: string;
  battery: number | null;
  /** Administrators: whose device it is. */
  username?: string;
}

export const devicesApi = {
  info: () => api<PairInfo>('/api/v1/pair/info'),
  pairCode: () => api<PairCode>('/api/v1/devices/pair-code', { method: 'POST' }),
  list: () => api<LinkedDevice[]>('/api/v1/devices'),
  unlink: (id: string) => api<void>(`/api/v1/devices/${encodeURIComponent(id)}`, { method: 'DELETE' }),
};
