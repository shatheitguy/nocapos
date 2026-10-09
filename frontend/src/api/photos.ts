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
  favorite?: boolean;
}

export interface Album {
  id: number;
  name: string;
  created_at: string;
  items: PhotoKey[];
}

const items = (list: PhotoKey[]) => list.map(({ root, path }) => ({ root, path }));

export const photosApi = {
  list: () => api<{ items: Photo[]; scanning: boolean; upload: PhotoKey }>('/api/v1/photos'),
  tickets: () => api<{ tickets: Record<string, string>; expires_at: string }>('/api/v1/photos/tickets', { method: 'POST' }),
  favorite: (list: PhotoKey[], on: boolean) => api<void>('/api/v1/photos/favorite', { method: 'POST', body: { items: items(list), on } }),
  remove: (list: PhotoKey[]) => api<void>('/api/v1/photos/delete', { method: 'POST', body: { items: items(list) } }),
  albums: () => api<{ albums: Album[] }>('/api/v1/photos/albums'),
  createAlbum: (name: string, list: PhotoKey[] = []) =>
    api<{ id: number }>('/api/v1/photos/albums', { method: 'POST', body: { name, items: items(list) } }),
  renameAlbum: (id: number, name: string) => api<void>(`/api/v1/photos/albums/${id}`, { method: 'PATCH', body: { name } }),
  deleteAlbum: (id: number) => api<void>(`/api/v1/photos/albums/${id}`, { method: 'DELETE' }),
  albumItems: (id: number, list: PhotoKey[], on: boolean) =>
    api<void>(`/api/v1/photos/albums/${id}/items`, { method: 'POST', body: { items: items(list), on } }),
};

export const photoKey = (p: PhotoKey) => `${p.root}\u0000${p.path}`;

const qs = (o: Record<string, string>) => new URLSearchParams(o).toString();

/** A thumbnail URL ("s" for the grid, "l" for pictures the browser can't show). */
export const thumbUrl = (p: Photo, ticket: string, size: 's' | 'l' = 's') =>
  `/api/v1/photos/thumb?${qs({ root: p.root, path: p.path, size, t: ticket, v: `${p.size}-${p.mod_time}` })}`;

/** The original file (shown inline when the browser supports it). */
export const originalUrl = (p: PhotoKey, ticket: string, download = false) =>
  `/api/v1/files/raw?${qs({ root: p.root, path: p.path, t: ticket, ...(download ? { download: '1' } : {}) })}`;

/** Pictures browsers can show as-is; others (HEIC) are shown from a large thumbnail. */
export const browserCanShow = (p: Photo) => p.video || !/\.(heic|heif)$/i.test(p.name);
