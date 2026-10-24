import { api, getAccessToken, refresh, type ApiResult } from './client';

export interface PkgStatus {
  supported: boolean;
  reason?: string;
  manager?: 'apt' | 'dnf' | 'pacman' | 'zypper';
  can_hold: boolean;
  /** ".deb" or ".rpm" (none: local package files aren't supported). */
  file_types?: string;
}

export interface PkgFeature {
  id: string;
  title: string;
  installed: boolean;
  can_install: boolean;
}

export interface Pkg {
  name: string;
  version: string;
  arch?: string;
  size: number;
  description: string;
  held: boolean;
}

export interface PkgUpdate {
  name: string;
  current?: string;
  available: string;
  arch?: string;
  held: boolean;
}

export interface PkgJob {
  id: string;
  action: string;
  target?: string;
  started_at: string;
  ended_at?: string;
  running: boolean;
  ok: boolean;
  output: string;
  error?: string;
}

export type PkgAction = 'refresh' | 'upgrade-all' | 'upgrade' | 'install' | 'remove' | 'hold' | 'unhold' | 'autoremove' | 'clean';

export const pkgApi = {
  status: () => api<{ status: PkgStatus; features: PkgFeature[]; jobs: PkgJob[] }>('/api/v1/packages/status'),
  list: (q: string, offset: number, limit: number, held = false) =>
    api<{ packages: Pkg[]; total: number; installed: number; size: number }>(
      `/api/v1/packages?q=${encodeURIComponent(q)}&offset=${offset}&limit=${limit}${held ? '&held=1' : ''}`,
    ),
  updates: () => api<{ updates: PkgUpdate[] }>('/api/v1/packages/updates'),
  act: (action: PkgAction, name?: string, confirm?: string) => api<{ job: PkgJob }>('/api/v1/packages/actions', { method: 'POST', body: { action, name, confirm } }),
  installFeature: (id: string) => api<{ job: PkgJob }>(`/api/v1/packages/features/${encodeURIComponent(id)}/install`, { method: 'POST' }),
  job: (id: string) => api<PkgJob>(`/api/v1/packages/jobs/${encodeURIComponent(id)}`),
  upload: async (file: File, onProgress: (pct: number) => void): Promise<ApiResult<{ job: PkgJob }>> => {
    let r = await sendFile(file, onProgress);
    if (r.status === 401 && (await refresh())) r = await sendFile(file, onProgress);
    return r;
  },
};

function sendFile(file: File, onProgress: (pct: number) => void): Promise<ApiResult<{ job: PkgJob }>> {
  return new Promise((resolve) => {
    const form = new FormData();
    form.append('file', file, file.name);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/v1/packages/upload');
    const token = getAccessToken();
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100));
    xhr.onload = () => {
      let data: unknown = null;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* empty */
      }
      const ok = xhr.status >= 200 && xhr.status < 300;
      resolve({
        ok,
        status: xhr.status,
        data: data as { job: PkgJob },
        error: ok ? undefined : ((data as { error?: { message?: string } })?.error?.message ?? `HTTP ${xhr.status}`),
      });
    };
    xhr.onerror = () => resolve({ ok: false, status: 0, data: null as unknown as { job: PkgJob }, error: 'Upload interrupted' });
    xhr.send(form);
  });
}
