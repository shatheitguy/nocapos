import { api, getAccessToken } from './client';

// Linux apps: desktop apps from Flathub, installed on the server itself.

export interface NativeApp {
  id: string;
  name: string;
  summary: string;
  developer?: string;
  /** Flathub's icon URL (the UI loads icons through alfad instead). */
  icon?: string;
  installed: boolean;
  version?: string;
}

export interface NativeDetails extends NativeApp {
  description: string;
  homepage?: string;
  license?: string;
}

export interface NativeJob {
  id: string;
  app: string;
  name: string;
  action: 'install' | 'uninstall';
  phase: string;
  percent: number;
  done: boolean;
  ok: boolean;
  error?: string;
}

export interface NativeStatus {
  supported: boolean;
  reason?: string;
  /** The server's own screen is on: apps open there. */
  screen: boolean;
}

export interface NativeStream {
  id: string;
  key: string;
  title: string;
  phase: 'idle' | 'starting' | 'ready' | 'error' | 'installing';
  message: string;
  ready: boolean;
}

export interface Launched {
  where: 'screen' | 'stream';
  stream?: NativeStream;
}

const enc = encodeURIComponent;

export const nativeApi = {
  installed: () => api<{ status: NativeStatus; apps: NativeApp[]; jobs?: NativeJob[] }>('/api/v1/native-apps/installed'),
  popular: () => api<{ apps: NativeApp[]; jobs: NativeJob[] }>('/api/v1/native-apps/popular'),
  search: (q: string) => api<{ apps: NativeApp[] }>(`/api/v1/native-apps/search?q=${enc(q)}`),
  details: (id: string) => api<NativeDetails>(`/api/v1/native-apps/details/${enc(id)}`),
  jobs: () => api<{ jobs: NativeJob[] }>('/api/v1/native-apps/jobs'),
  install: (id: string, name: string) => api<NativeJob>(`/api/v1/native-apps/${enc(id)}/install`, { method: 'POST', body: { name } }),
  uninstall: (id: string, name: string) => api<NativeJob>(`/api/v1/native-apps/${enc(id)}/uninstall`, { method: 'POST', body: { name } }),
  launch: (id: string) => api<Launched>(`/api/v1/native-apps/${enc(id)}/launch`, { method: 'POST' }),
  stream: (sid: string) => api<NativeStream>(`/api/v1/native-apps/streams/${enc(sid)}`),
  stopStream: (sid: string) => api<void>(`/api/v1/native-apps/streams/${enc(sid)}`, { method: 'DELETE' }),
};

/** The noVNC page for a stream, proxied same-origin by alfad. */
export const streamURL = (sid: string) =>
  `/apps/native/${sid}/vnc.html?autoconnect=1&reconnect=1&resize=scale&show_dot=1&path=apps/native/${sid}/websockify`;

/** Fetches an app's icon (with this session's token) as a blob: URL, or null. */
export async function fetchNativeIcon(id: string): Promise<string | null> {
  const token = getAccessToken();
  try {
    const res = await fetch(`/api/v1/native-apps/icons/${enc(id)}`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      credentials: 'same-origin',
    });
    if (!res.ok) return null;
    return URL.createObjectURL(await res.blob());
  } catch {
    return null;
  }
}
