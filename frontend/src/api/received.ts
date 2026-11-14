import { api } from './client';

// Received files: what linked devices send this server lands in the
// person's own Received folder ("Drops"), one subfolder per device.

export interface ReceivedInfo {
  /** False when the account has no home folder here. */
  available: boolean;
  root?: string;
  /** The folder's name in the home. */
  folder: string;
  /** "/Drops" */
  path?: string;
  /** The name it gets when none was picked (in the person's voice). */
  default: string;
  /** Files that arrived since the folder was last opened. */
  unread: number;
  /** Each of the person's devices' subfolder, by device id. */
  devices: Record<string, string>;
}

/** Files that just arrived from a device (a live event). */
export interface ReceivedEvent {
  device_id: string;
  device_name: string;
  root: string;
  folder: string;
  paths: string[];
  at: string;
}

export const receivedApi = {
  info: () => api<ReceivedInfo>('/api/v1/received'),
  seen: () => api<{ ok: boolean }>('/api/v1/received/seen', { method: 'POST' }),
  setFolder: (folder: string) => api<ReceivedInfo>('/api/v1/received', { method: 'PUT', body: { folder } }),
  /** Where a file going to one of your devices is uploaded first. */
  outbox: (deviceId: string) => api<{ root: string; path: string }>(`/api/v1/devices/${encodeURIComponent(deviceId)}/outbox`, { method: 'POST' }),
};
