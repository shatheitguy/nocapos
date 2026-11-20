// The Files sidebar's order and hidden places (pure; tested in
// scripts/sidebarOrder.test.ts). Kept per person in their prefs, so it's the
// same in every browser and on NoCap Desktop.
//
// Places are keyed "home", "recents", "drops", "appdata" or "root:<id>";
// favorites "<root>:<path>".

export interface SidebarLayout {
  /** The places, in the person's order (missing ones go in their usual spot). */
  order: string[];
  /** Places hidden from the sidebar. */
  hidden: string[];
  /** Favorites, in the person's order (new ones go at the end). */
  favs: string[];
}

export const EMPTY_LAYOUT: SidebarLayout = { order: [], hidden: [], favs: [] };

const strings = (v: unknown): string[] => (Array.isArray(v) ? [...new Set(v.filter((x): x is string => typeof x === 'string' && x.length > 0 && x.length < 600))].slice(0, 300) : []);

/** A layout read from saved prefs (anything unexpected is dropped). */
export function cleanLayout(raw: unknown): SidebarLayout {
  if (!raw || typeof raw !== 'object') return { ...EMPTY_LAYOUT };
  const r = raw as Record<string, unknown>;
  return { order: strings(r.order), hidden: strings(r.hidden), favs: strings(r.favs) };
}

/**
 * The places in the person's order. Places they never moved keep their usual
 * spot relative to their neighbours: each goes right after the place that
 * comes before it in the usual order.
 */
export function arrangePlaces(saved: string[], present: string[]): string[] {
  const here = new Set(present);
  const out = saved.filter((k, i) => here.has(k) && saved.indexOf(k) === i);
  for (let i = 0; i < present.length; i++) {
    const k = present[i];
    if (out.includes(k)) continue;
    let at = 0;
    for (let j = i - 1; j >= 0; j--) {
      const p = out.indexOf(present[j]);
      if (p >= 0) {
        at = p + 1;
        break;
      }
    }
    out.splice(at, 0, k);
  }
  return out;
}

/** Favorites in the person's order; ones pinned since go at the end. */
export function arrangeFavs(saved: string[], present: string[]): string[] {
  const here = new Set(present);
  const out = saved.filter((k, i) => here.has(k) && saved.indexOf(k) === i);
  for (const k of present) if (!out.includes(k)) out.push(k);
  return out;
}

/** The places shown: everything not hidden. */
export const shownPlaces = (order: string[], hidden: string[]) => order.filter((k) => !hidden.includes(k));

/** Move key to index (in the list without it). */
export function moveTo(list: string[], key: string, index: number): string[] {
  const rest = list.filter((k) => k !== key);
  if (!list.includes(key)) return list;
  const at = Math.max(0, Math.min(rest.length, index));
  return [...rest.slice(0, at), key, ...rest.slice(at)];
}

/** Move key one step up (-1) or down (+1) among the shown keys; hidden ones keep their place. */
export function step(list: string[], key: string, delta: -1 | 1, hidden: string[] = []): string[] {
  const shown = list.filter((k) => !hidden.includes(k));
  const i = shown.indexOf(key);
  const j = i + delta;
  if (i < 0 || j < 0 || j >= shown.length) return list;
  const other = shown[j];
  return list.map((k) => (k === key ? other : k === other ? key : k));
}

/** Hide or show a place again. */
export function setHidden(hidden: string[], key: string, hide: boolean): string[] {
  const rest = hidden.filter((k) => k !== key);
  return hide ? [...rest, key] : rest;
}

/**
 * Where a dragged item lands: the index among the other items, from the
 * pointer's y and the other items' vertical centres (in order).
 */
export function dropIndex(y: number, centres: number[]): number {
  let i = 0;
  while (i < centres.length && y > centres[i]) i++;
  return i;
}
