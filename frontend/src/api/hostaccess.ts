import { api } from './client';

/** NoCap Desktop: NoCapOS full screen on the server's own monitor. */
export interface DesktopState {
  available: boolean;
  reason?: string;
  enabled: boolean;
  active: boolean;
  /** A monitor is plugged into the server. */
  display: boolean;
}

export interface HostAccess {
  native: boolean;
  /** This browser runs on the server itself. */
  client_local: boolean;
  remote: { supported: boolean; enabled: boolean; reason?: string };
  desktop: DesktopState;
}

export const hostAccessApi = {
  get: () => api<HostAccess>('/api/v1/system/host-access'),
  setRemote: (enabled: boolean, confirm = false) =>
    api<HostAccess>('/api/v1/system/remote-access', { method: 'PUT', body: { enabled, confirm } }),
  setDesktop: (enabled: boolean) => api<HostAccess>('/api/v1/system/desktop', { method: 'PUT', body: { enabled } }),
};
