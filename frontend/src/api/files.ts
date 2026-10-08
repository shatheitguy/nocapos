import { api, getAccessToken, refresh, type ApiResult } from './client';

export interface FileRoot {
  id: string;
  name: string;
  total: number;
  free: number;
}

export interface FileEntry {
  name: string;
  dir: boolean;
  size: number;
  mod_time: string;
  symlink?: boolean;
  /** ls-style permissions, e.g. "drwxr-xr-x". */
  mode: string;
  perm: number;
  owner?: string;
  group?: string;
}

export const RECYCLE = '/.recycle';

export const joinPath = (dir: string, name: string) => `${dir === '/' ? '' : dir}/${name}`;
export const parentPath = (p: string) => (p.lastIndexOf('/') <= 0 ? '/' : p.slice(0, p.lastIndexOf('/')));
export const baseName = (p: string) => p.slice(p.lastIndexOf('/') + 1);
export const inRecycle = (p: string) => p === RECYCLE || p.startsWith(RECYCLE + '/');

const q = (params: Record<string, string>) => new URLSearchParams(params).toString();

export type Conflict = 'rename' | 'replace' | 'skip';

export interface TransferJob {
  id: string;
  kind: 'copy' | 'move';
  root: string;
  sources: string[];
  dest: string;
  status: 'running' | 'done' | 'failed' | 'canceled';
  progress: { bytes_done: number; bytes_total: number; items_done: number; items_total: number; current: string };
  result?: { paths: string[]; skipped: string[] };
  error?: string;
}

export const fileApi = {
  roots: () => api<FileRoot[]>('/api/v1/files/roots'),
  list: (root: string, path: string) =>
    api<{ path: string; entries: FileEntry[]; unix_perms: boolean }>(`/api/v1/files/list?${q({ root, path })}`),
  mkdir: (root: string, path: string, name: string) =>
    api<{ path: string }>('/api/v1/files/mkdir', { method: 'POST', body: { root, path, name } }),
  rename: (root: string, path: string, name: string) =>
    api<{ path: string }>('/api/v1/files/rename', { method: 'POST', body: { root, path, name } }),
  remove: (root: string, paths: string[], permanent = false) =>
    api('/api/v1/files/delete', { method: 'POST', body: { root, paths, permanent } }),
  transfer: (root: string, paths: string[], dest: string, move: boolean, conflict: Conflict = 'rename') =>
    api<{ paths: string[]; skipped: string[] }>('/api/v1/files/transfer', { method: 'POST', body: { root, paths, dest, move, conflict } }),
  transferJob: (root: string, paths: string[], dest: string, move: boolean, conflict: Conflict) =>
    api<TransferJob>('/api/v1/files/transfer', { method: 'POST', body: { root, paths, dest, move, conflict, background: true } }),
  job: (id: string) => api<TransferJob>(`/api/v1/files/jobs/${encodeURIComponent(id)}`),
  cancelJob: (id: string) => api(`/api/v1/files/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  conflicts: (root: string, paths: string[], dest: string) => api<{ names: string[] }>('/api/v1/files/conflicts', { method: 'POST', body: { root, paths, dest } }),
  readText: (root: string, path: string) =>
    api<{ content: string; size: number; mod_time: string }>(`/api/v1/files/text?${q({ root, path })}`),
  writeText: (root: string, path: string, content: string, mod_time?: string) =>
    api<{ size: number; mod_time: string }>('/api/v1/files/text', { method: 'PUT', body: { root, path, content, mod_time } }),
};

// ---------- scoped links for <img>/<video>/downloads ----------

const ticketCache = new Map<string, { ticket: string; exp: number }>();

/** A ticket valid for `path` and everything below it (cached until near expiry). */
export async function fileTicket(root: string, path: string): Promise<string | null> {
  const key = `${root}:${path}`;
  const hit = ticketCache.get(key);
  if (hit && hit.exp - Date.now() > 60_000) return hit.ticket;
  const r = await api<{ ticket: string; expires_at: string }>('/api/v1/files/ticket', {
    method: 'POST',
    body: { root, path },
  });
  if (!r.ok) return null;
  ticketCache.set(key, { ticket: r.data.ticket, exp: new Date(r.data.expires_at).getTime() });
  return r.data.ticket;
}

export const rawUrl = (root: string, path: string, ticket: string, download = false) =>
  `/api/v1/files/raw?${q({ root, path, t: ticket, ...(download ? { download: '1' } : {}) })}`;

export async function downloadFile(root: string, path: string): Promise<boolean> {
  const t = await fileTicket(root, path);
  if (!t) return false;
  const a = document.createElement('a');
  a.href = rawUrl(root, path, t, true);
  a.download = baseName(path);
  document.body.append(a);
  a.click();
  a.remove();
  return true;
}

// ---------- uploads ----------

export interface UploadProgress {
  loaded: number;
  total: number;
}

function sendUpload(root: string, dir: string, files: File[], onProgress: (p: UploadProgress) => void, opts: UploadOptions): Promise<ApiResult<{ paths: string[]; skipped?: string[] }>> {
  return new Promise((resolve) => {
    const form = new FormData();
    for (const f of files) form.append('file', f, f.name);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `/api/v1/files/upload?${q({ root, path: dir, conflict: opts.conflict ?? 'rename' })}`);
    if (opts.signal) {
      if (opts.signal.aborted) {
        resolve({ ok: false, status: -1, data: { paths: [] }, error: 'Canceled' });
        return;
      }
      opts.signal.addEventListener('abort', () => xhr.abort(), { once: true });
    }
    xhr.onabort = () => resolve({ ok: false, status: -1, data: { paths: [] }, error: 'Canceled' });
    const token = getAccessToken();
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress({ loaded: e.loaded, total: e.total });
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
        data: data as { paths: string[] },
        error: ok ? undefined : ((data as { error?: { message?: string } })?.error?.message ?? `HTTP ${xhr.status}`),
      });
    };
    xhr.onerror = () => resolve({ ok: false, status: 0, data: { paths: [] }, error: 'Upload interrupted' });
    xhr.send(form);
  });
}

export interface UploadOptions {
  conflict?: Conflict;
  signal?: AbortSignal;
}

export async function upload(root: string, dir: string, files: File[], onProgress: (p: UploadProgress) => void, opts: UploadOptions = {}) {
  let r = await sendUpload(root, dir, files, onProgress, opts);
  if (r.status === 401 && (await refresh())) r = await sendUpload(root, dir, files, onProgress, opts);
  return r;
}

// ---------- file kinds ----------

export type FileKind = 'folder' | 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'code' | 'archive' | 'file';

const EXT: Record<string, FileKind> = {};
const add = (kind: FileKind, exts: string) => exts.split(' ').forEach((e) => (EXT[e] = kind));
add('image', 'png jpg jpeg gif webp avif bmp ico');
add('video', 'mp4 m4v webm ogv mov mkv avi');
add('audio', 'mp3 m4a aac wav flac ogg opus');
add('pdf', 'pdf');
add('text', 'txt md log csv ini conf cfg env rtf');
add('code', 'json yaml yml toml xml html htm css js ts tsx jsx go py rs java c h cpp sh ps1 bat sql dockerfile svg');
add('archive', 'zip tar gz tgz bz2 xz 7z rar zst');

export function fileKind(name: string, dir = false): FileKind {
  if (dir) return 'folder';
  const lower = name.toLowerCase();
  if (lower === 'dockerfile' || lower === 'makefile' || lower.startsWith('.')) return 'code';
  const ext = lower.includes('.') ? lower.slice(lower.lastIndexOf('.') + 1) : '';
  return EXT[ext] ?? 'file';
}

/** Kinds the viewer can show (video/audio playback depends on browser codecs). */
export const PREVIEWABLE: FileKind[] = ['image', 'video', 'audio', 'pdf', 'text', 'code'];
