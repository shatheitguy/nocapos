import { api } from './client';

export interface BraveStatus {
  phase: 'idle' | 'installing' | 'starting' | 'ready' | 'error';
  message: string;
  ready: boolean;
}

// noVNC client served through alfad's proxy. `path` keeps the VNC WebSocket
// under /apps/brave/ (noVNC resolves it from the site root).
export const BRAVE_URL = '/apps/brave/vnc.html?autoconnect=1&reconnect=1&resize=scale&show_dot=1&path=apps/brave/websockify';

/** Start (or attach to) native Brave and grant this browser the proxy cookie. */
export const startBrave = () => api<BraveStatus>('/api/v1/apps/brave/start', { method: 'POST' });
export const braveStatus = () => api<BraveStatus>('/api/v1/apps/brave/status');
