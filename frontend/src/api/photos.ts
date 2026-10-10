import { api } from './client';

export interface PhotoKey {
  root: string;
  path: string;
}

export interface Photo extends PhotoKey {
  name: string;
  size: number;
  mod_time: string;
  taken: string;
  width?: number;
  height?: number;
  video?: boolean;
  /** Seconds, for videos whose length is known. */
  duration?: number;
  favorite?: boolean;
  /** Set on items in Recently deleted. */
  trash?: { id: number; orig_path: string; deleted_at: string };
}

export interface Album {
  id: number;
  name: string;
  created_at: string;
  items: PhotoKey[];
  cover?: PhotoKey;
}

export interface Camera {
  make?: string;
  model?: string;
  lens?: string;
  exposure?: number;
  f_number?: number;
  iso?: number;
  focal?: number;
  focal_35?: number;
}

export interface TrashItem {
  id: number;
  root: string;
  path: string;
  orig_path: string;
  size: number;
  taken: string;
  width?: number;
  height?: number;
  duration_ms?: number;
  deleted_at: string;
}

const qs = (o: Record<string, string>) => new URLSearchParams(o).toString();
const items = (list: PhotoKey[]) => list.map(({ root, path }) => ({ root, path }));

export const photosApi = {
  list: () => api<{ items: Photo[]; scanning: boolean; upload: PhotoKey }>('/api/v1/photos'),
  tickets: () =>
    api<{ tickets: Record<string, string>; trash?: Record<string, string>; expires_at: string }>('/api/v1/photos/tickets', { method: 'POST' }),
  info: (p: PhotoKey) => api<{ camera?: Camera }>(`/api/v1/photos/info?${qs({ root: p.root, path: p.path })}`),
  favorite: (list: PhotoKey[], on: boolean) => api<void>('/api/v1/photos/favorite', { method: 'POST', body: { items: items(list), on } }),
  remove: (list: PhotoKey[]) => api<void>('/api/v1/photos/delete', { method: 'POST', body: { items: items(list) } }),
  albums: () => api<{ albums: Album[] }>('/api/v1/photos/albums'),
  createAlbum: (name: string, list: PhotoKey[] = []) =>
    api<{ id: number }>('/api/v1/photos/albums', { method: 'POST', body: { name, items: items(list) } }),
  renameAlbum: (id: number, name: string) => api<void>(`/api/v1/photos/albums/${id}`, { method: 'PATCH', body: { name } }),
  deleteAlbum: (id: number) => api<void>(`/api/v1/photos/albums/${id}`, { method: 'DELETE' }),
  albumItems: (id: number, list: PhotoKey[], on: boolean) =>
    api<void>(`/api/v1/photos/albums/${id}/items`, { method: 'POST', body: { items: items(list), on } }),
  setCover: (id: number, p: PhotoKey | null) =>
    api<void>(`/api/v1/photos/albums/${id}/cover`, { method: 'PUT', body: p ? { root: p.root, path: p.path } : {} }),
  trash: () => api<{ items: TrashItem[] }>('/api/v1/photos/trash'),
  restore: (ids: number[]) => api<{ items: PhotoKey[] }>('/api/v1/photos/trash/restore', { method: 'POST', body: { ids } }),
  purge: (ids: number[]) => api<void>('/api/v1/photos/trash/purge', { method: 'POST', body: { ids } }),
};

/** A Recently deleted entry as a library item (its path is in the recycle bin). */
export const fromTrash = (t: TrashItem): Photo => ({
  root: t.root,
  path: t.path,
  name: t.orig_path.slice(t.orig_path.lastIndexOf('/') + 1),
  size: t.size,
  mod_time: t.deleted_at,
  taken: t.taken,
  width: t.width,
  height: t.height,
  video: /\.(mp4|mov|m4v|webm)$/i.test(t.orig_path),
  duration: t.duration_ms ? t.duration_ms / 1000 : undefined,
  trash: { id: t.id, orig_path: t.orig_path, deleted_at: t.deleted_at },
});

export const photoKey = (p: PhotoKey) => `${p.root}\u0000${p.path}`;

/** A thumbnail URL ("s" for the grid, "l" for pictures the browser can't show). */
export const thumbUrl = (p: Photo, ticket: string, size: 's' | 'l' = 's') =>
  `/api/v1/photos/thumb?${qs({ root: p.root, path: p.path, size, t: ticket, v: `${p.size}-${p.mod_time}` })}`;

/** The original file (shown inline when the browser supports it). */
export const originalUrl = (p: PhotoKey, ticket: string, download = false) =>
  `/api/v1/files/raw?${qs({ root: p.root, path: p.path, t: ticket, ...(download ? { download: '1' } : {}) })}`;

/** Pictures browsers can show as-is; others (HEIC) are shown from a large thumbnail. */
export const browserCanShow = (p: Photo) => p.video || !/\.(heic|heif)$/i.test(p.name);
