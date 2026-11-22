// Which pictures a photo viewer window steps through when Photos opens one:
// a view of the library (all, favorites, videos, recent, an album, recently
// deleted, with a search) or a fixed list (a memory). The window reads the
// library itself and picks the same pictures, so only this small description
// travels with it. Pure (no DOM, no stores): tested in scripts/photoSet.test.ts.

export interface SetKey {
  root: string;
  path: string;
}

export interface SetPhoto extends SetKey {
  mod_time: string;
  favorite?: boolean;
  video?: boolean;
  trash?: unknown;
}

export interface SetAlbum {
  id: number;
  items: SetKey[];
}

export type SetView = 'library' | 'favorites' | 'videos' | 'recent' | 'album' | 'trash';

export type PhotoSet = { view: SetView; album?: number; q?: string } | { keys: SetKey[] };

/** How far back "Recent" reaches. */
export const RECENT_DAYS = 30;

/** The most pictures a fixed list carries (the window's props have a size limit). */
export const MAX_KEYS = 60;

const keyOf = (p: SetKey) => `${p.root}\u0000${p.path}`;

/**
 * The pictures of a set, in the order Photos shows them. `trash` is only
 * read for the "trash" view.
 */
export function pickSet<P extends SetPhoto>(set: PhotoSet, items: P[], albums: SetAlbum[], trash: P[] = [], now = Date.now()): P[] {
  if ('keys' in set) {
    const at = new Map(items.map((p) => [keyOf(p), p]));
    return set.keys.map((k) => at.get(keyOf(k))).filter((p): p is P => !!p);
  }
  let list: P[];
  switch (set.view) {
    case 'favorites':
      list = items.filter((p) => p.favorite);
      break;
    case 'videos':
      list = items.filter((p) => p.video);
      break;
    case 'recent': {
      const since = now - RECENT_DAYS * 86_400_000;
      list = items.filter((p) => new Date(p.mod_time).getTime() >= since).sort((a, b) => (a.mod_time < b.mod_time ? 1 : -1));
      break;
    }
    case 'album': {
      const keys = new Set((albums.find((a) => a.id === set.album)?.items ?? []).map(keyOf));
      list = items.filter((p) => keys.has(keyOf(p)));
      break;
    }
    case 'trash':
      list = trash;
      break;
    default:
      list = items;
  }
  const q = (set.q ?? '').trim().toLowerCase();
  return q ? list.filter((p) => p.path.toLowerCase().includes(q)) : list;
}

/**
 * A fixed list as a set: long lists keep the pictures around the one opened,
 * so the props stay small. Returns the set and where the opened one is in it.
 */
export function keysSet(list: SetKey[], index: number): { set: PhotoSet; index: number } {
  const from = Math.max(0, Math.min(index - Math.floor(MAX_KEYS / 2), list.length - MAX_KEYS));
  const keys = list.slice(from, from + MAX_KEYS).map(({ root, path }) => ({ root, path }));
  return { set: { keys }, index: index - from };
}

/** The set as a window prop. */
export const encodeSet = (set: PhotoSet) => JSON.stringify(set);

/** A window prop back to a set (null when it isn't one). */
export function decodeSet(raw: string | undefined): PhotoSet | null {
  if (!raw) return null;
  try {
    const v = JSON.parse(raw) as unknown;
    if (!v || typeof v !== 'object') return null;
    const o = v as Record<string, unknown>;
    if (Array.isArray(o.keys)) {
      const keys = o.keys.filter((k): k is SetKey => !!k && typeof k === 'object' && typeof (k as SetKey).root === 'string' && typeof (k as SetKey).path === 'string');
      return { keys: keys.map(({ root, path }) => ({ root, path })) };
    }
    const views: SetView[] = ['library', 'favorites', 'videos', 'recent', 'album', 'trash'];
    if (typeof o.view !== 'string' || !views.includes(o.view as SetView)) return null;
    const set: { view: SetView; album?: number; q?: string } = { view: o.view as SetView };
    if (typeof o.album === 'number') set.album = o.album;
    if (typeof o.q === 'string' && o.q) set.q = o.q;
    return set;
  } catch {
    return null;
  }
}
