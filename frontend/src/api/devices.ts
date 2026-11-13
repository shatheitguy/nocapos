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

export type DeviceCap = 'notifications' | 'clipboard' | 'files';

export interface LinkedDevice {
  id: string;
  name: string;
  platform: 'android' | 'ios' | 'other';
  created_at: string;
  last_seen_at: string | null;
  app_version: string;
  battery: number | null;
  /** Called this server in the last few minutes. */
  online: boolean;
  /** As the device reports it, e.g. "Google Pixel 8" ("" until it does). */
  model: string;
  os_version: string;
  last_ip: string;
  /** NoCap Link features it has on. */
  capabilities: DeviceCap[];
  /** Administrators: whose device it is. */
  username?: string;
  user_id?: string;
}

export interface UserDeviceLimit {
  id: string;
  username: string;
  role: string;
  used: number;
  limit: number;
  /** null = the server-wide default. */
  override: number | null;
}

export interface DeviceLimits {
  used: number;
  /** 0 = no limit. */
  limit: number;
  default: number;
  max: number;
  /** Administrators only. */
  users?: UserDeviceLimit[];
  total?: number;
}

export const devicesApi = {
  info: () => api<PairInfo>('/api/v1/pair/info'),
  /** 409 when the account already has as many devices as it may. */
  pairCode: () => api<PairCode>('/api/v1/devices/pair-code', { method: 'POST' }),
  list: () => api<LinkedDevice[]>('/api/v1/devices'),
  unlink: (id: string) => api<void>(`/api/v1/devices/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  rename: (id: string, name: string) => api<LinkedDevice>(`/api/v1/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: { name } }),
  limits: () => api<DeviceLimits>('/api/v1/devices/limits'),
  setDefaultLimit: (n: number) => api<void>('/api/v1/devices/limits', { method: 'PUT', body: { default: n } }),
  setUserLimit: (userId: string, limit: number | null) =>
    api<void>(`/api/v1/devices/limits/${encodeURIComponent(userId)}`, { method: 'PUT', body: { limit } }),
};
