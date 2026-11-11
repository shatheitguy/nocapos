import { api } from './client';

/** What the server's own monitor shows: the full desktop, NoCapOS full screen, or the console. */
export type DesktopMode = 'full' | 'kiosk' | 'off';

/** NoCap Desktop on the server's own monitor. */
export interface DesktopState {
  available: boolean;
  reason?: string;
  /** Some mode other than off is on. */
  enabled: boolean;
  active: boolean;
  /** A monitor is plugged into the server. */
  display: boolean;
  mode: DesktopMode;
  /** The modes this server has (always ending in "off" when any). */
  modes: DesktopMode[] | null;
  /** Signed in on the full desktop right now. */
  session_user?: string;
  autologin: boolean;
  autologin_user?: string;
  autologin_supported: boolean;
  /** The screen can be opened in a NoCap Desktop window. */
  remote: boolean;
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
  setDesktopMode: (mode: DesktopMode) => api<HostAccess>('/api/v1/system/desktop', { method: 'PUT', body: { mode } }),
  setAutologin: (enabled: boolean) => api<HostAccess>('/api/v1/system/desktop/autologin', { method: 'PUT', body: { enabled } }),
  /** A single-use ticket for the server's own screen (the NoCap Desktop window). */
  view: (width: number, height: number, dpi: number) =>
    api<{ ticket: string; protocol: 'rdp' | 'vnc' }>('/api/v1/system/desktop/view', { method: 'POST', body: { width, height, dpi } }),
};
