// Pure helpers for the shell surfaces: dock list, roster folders and search,
// paging, notification grouping. No React, no DOM, so `npm test` runs them.
import type { AppInfo, NotifyItem, WindowInfo } from './protocol';

// ---------- apps and windows ----------

/** A comparable form of an app or window id: "Org.Example.Files.desktop" → "org.example.files". */
export function appKey(id: string): string {
  return id.trim().toLowerCase().replace(/\.desktop$/, '');
}

/** The last dotted part of an id: "org.example.files" → "files". */
const tail = (key: string) => key.slice(key.lastIndexOf('.') + 1);

/** Which app (from apps.list) a window belongs to, or null. */
export function appForWindow(appId: string, apps: readonly AppInfo[]): AppInfo | null {
  const k = appKey(appId);
  if (!k) return null;
  let loose: AppInfo | null = null;
  for (const a of apps) {
    const ak = appKey(a.id);
    if (ak === k) return a;
    if (!loose && (tail(ak) === k || tail(ak) === tail(k))) loose = a;
  }
  return loose;
}

export interface DockEntry {
  /** The app id, or "window:<appId>" for a window of an app the list doesn't know. */
  id: string;
  app: AppInfo | null;
  /** A name for windows without an app. */
  title: string;
  pinned: boolean;
  windows: WindowInfo[];
  active: boolean;
}

/**
 * The dock: pinned apps in their order (only ones that exist, once apps are
 * known), then apps with open windows that aren't pinned, in window order.
 */
export function dockEntries(pinned: readonly string[], windows: readonly WindowInfo[], apps: readonly AppInfo[]): DockEntry[] {
  const byId = new Map(apps.map((a) => [a.id, a]));
  const out: DockEntry[] = [];
  const index = new Map<string, DockEntry>();
  for (const id of pinned) {
    if (index.has(id)) continue;
    const app = byId.get(id) ?? null;
    if (apps.length && !app) continue;
    const e: DockEntry = { id, app, title: app?.name ?? id, pinned: true, windows: [], active: false };
    out.push(e);
    index.set(id, e);
  }
  for (const w of windows) {
    const app = appForWindow(w.appId, apps);
    const id = app ? app.id : `window:${appKey(w.appId) || String(w.id)}`;
    let e = index.get(id);
    if (!e) {
      e = { id, app, title: app?.name ?? (w.title || w.appId), pinned: false, windows: [], active: false };
      out.push(e);
      index.set(id, e);
    }
    e.windows.push(w);
    if (w.active && !w.minimized) e.active = true;
  }
  return out;
}

/** What a click on a dock icon does. */
export type DockClick = { kind: 'launch'; id: string } | { kind: 'focus'; window: WindowInfo } | { kind: 'minimize'; window: WindowInfo };

export function dockClick(e: DockEntry): DockClick | null {
  if (!e.windows.length) return e.app ? { kind: 'launch', id: e.app.id } : null;
  const active = e.windows.find((w) => w.active && !w.minimized);
  if (active) return { kind: 'minimize', window: active };
  // Bring back the first window that is showing, else the first one.
  return { kind: 'focus', window: e.windows.find((w) => !w.minimized) ?? e.windows[0] };
}

/** The list with the item at `from` moved to index `to`. */
export function reorder<T>(list: readonly T[], from: number, to: number): T[] {
  const out = list.slice();
  if (from < 0 || from >= out.length) return out;
  const [it] = out.splice(from, 1);
  out.splice(Math.max(0, Math.min(to, out.length)), 0, it);
  return out;
}

/** Pin or unpin an app, keeping the order (max 24 pins, the protocol's limit). */
export function togglePin(pinned: readonly string[], id: string): string[] {
  return pinned.includes(id) ? pinned.filter((p) => p !== id) : [...pinned, id].slice(0, 24);
}

// ---------- roster: folders, search, pages ----------

export type FolderId = 'games' | 'internet' | 'media' | 'office' | 'system' | 'utilities';

/** Desktop-entry categories behind each automatic folder, in priority order. */
export const FOLDER_CATEGORIES: [FolderId, string[]][] = [
  ['games', ['game']],
  ['internet', ['network', 'webbrowser', 'email', 'chat', 'instantmessaging', 'filetransfer', 'p2p']],
  ['media', ['audiovideo', 'audio', 'video', 'graphics', 'photography', 'player', 'recorder', 'music', 'tv']],
  ['office', ['office', 'wordprocessor', 'spreadsheet', 'presentation', 'calendar', 'contactmanagement', 'finance']],
  ['system', ['system', 'settings', 'monitor', 'packagemanager', 'hardwaresettings', 'terminalemulator']],
  ['utilities', ['utility', 'accessories', 'texteditor', 'archiving', 'compression', 'calculator', 'filemanager']],
];

export function folderOf(app: AppInfo): FolderId | null {
  const cats = new Set(app.categories.map((c) => c.toLowerCase()));
  for (const [id, list] of FOLDER_CATEGORIES) if (list.some((c) => cats.has(c))) return id;
  return null;
}

export type RosterEntry = { kind: 'folder'; id: FolderId; apps: AppInfo[] } | { kind: 'app'; app: AppInfo };

const byName = (a: AppInfo, b: AppInfo) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });

/** The roster's home grid: a folder per category with at least two apps (in folder order), then the other apps A–Z. */
export function buildRoster(apps: readonly AppInfo[]): RosterEntry[] {
  const groups = new Map<FolderId, AppInfo[]>();
  const loose: AppInfo[] = [];
  for (const a of apps) {
    const f = folderOf(a);
    if (!f) loose.push(a);
    else groups.set(f, [...(groups.get(f) ?? []), a]);
  }
  const folders: RosterEntry[] = [];
  for (const [id] of FOLDER_CATEGORIES) {
    const list = groups.get(id);
    if (!list) continue;
    if (list.length >= 2) folders.push({ kind: 'folder', id, apps: list.sort(byName) });
    else loose.push(...list);
  }
  return [...folders, ...loose.sort(byName).map((app): RosterEntry => ({ kind: 'app', app }))];
}

/** How well one word matches an app: lower is better, -1 = no match. */
function wordScore(app: AppInfo, w: string): number {
  const name = app.name.toLowerCase();
  if (name.startsWith(w)) return 0;
  if (name.split(/[\s\-_.]+/).some((p) => p.startsWith(w))) return 1;
  if (name.includes(w)) return 2;
  if (app.keywords.some((k) => k.toLowerCase().startsWith(w))) return 3;
  if (app.keywords.some((k) => k.toLowerCase().includes(w))) return 4;
  if (app.comment.toLowerCase().includes(w)) return 5;
  if (appKey(app.id).includes(w)) return 6;
  return -1;
}

/** Apps matching every word of the query, best first (by name, then keywords, then the description). */
export function searchApps(apps: readonly AppInfo[], query: string): AppInfo[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  const scored: [number, AppInfo][] = [];
  for (const a of apps) {
    let total = 0;
    let ok = true;
    for (const w of words) {
      const s = wordScore(a, w);
      if (s < 0) {
        ok = false;
        break;
      }
      total += s;
    }
    if (ok) scored.push([total, a]);
  }
  return scored.sort((x, y) => x[0] - y[0] || byName(x[1], y[1])).map(([, a]) => a);
}

export function paginate<T>(list: readonly T[], perPage: number): T[][] {
  const n = Math.max(1, Math.floor(perPage));
  const pages: T[][] = [];
  for (let i = 0; i < list.length; i += n) pages.push(list.slice(i, i + n));
  return pages.length ? pages : [[]];
}

/** Columns and rows of roster icons that fit the given area (cells of about 132 x 124 px). */
export function rosterGrid(width: number, height: number): { cols: number; rows: number } {
  const cols = Math.max(3, Math.min(8, Math.floor(width / 132)));
  const rows = Math.max(2, Math.min(5, Math.floor(height / 124)));
  return { cols, rows };
}

/**
 * Keyboard movement in a paged grid. `sel` is an index into the whole list
 * (-1 = nothing yet); returns the new index.
 */
export function moveSelection(sel: number, key: string, count: number, cols: number, perPage: number): number {
  if (count <= 0) return -1;
  if (sel < 0) return 0;
  const page = Math.floor(sel / perPage);
  const pages = Math.ceil(count / perPage);
  let next = sel;
  switch (key) {
    case 'ArrowRight':
      next = sel + 1;
      break;
    case 'ArrowLeft':
      next = sel - 1;
      break;
    case 'ArrowDown':
      next = sel + cols;
      break;
    case 'ArrowUp':
      next = sel - cols;
      break;
    case 'PageDown':
      next = Math.min(page + 1, pages - 1) * perPage;
      break;
    case 'PageUp':
      next = Math.max(page - 1, 0) * perPage;
      break;
    case 'Home':
      next = 0;
      break;
    case 'End':
      next = count - 1;
      break;
    default:
      return sel;
  }
  return Math.max(0, Math.min(count - 1, next));
}

// ---------- notifications ----------

/** Notification times may be seconds or milliseconds. */
export function toMs(time: number): number {
  return time > 0 && time < 1e12 ? time * 1000 : time;
}

/** A short age: "now", minutes, hours, or null when older than a day (show the date). */
export function age(timeMs: number, nowMs: number): { unit: 'now' } | { unit: 'min' | 'hour'; n: number } | null {
  const s = Math.max(0, (nowMs - timeMs) / 1000);
  if (s < 60) return { unit: 'now' };
  if (s < 3600) return { unit: 'min', n: Math.floor(s / 60) };
  if (s < 86400) return { unit: 'hour', n: Math.floor(s / 3600) };
  return null;
}

export interface NotifyGroup {
  key: string;
  app: string;
  source: NotifyItem['source'];
  icon: string;
  items: NotifyItem[];
}

/** Notifications grouped by app (NoCapOS's own as one group), the group with the newest item first. */
export function groupNotifications(items: readonly NotifyItem[]): NotifyGroup[] {
  const sorted = [...items].sort((a, b) => toMs(b.time) - toMs(a.time));
  const groups = new Map<string, NotifyGroup>();
  for (const n of sorted) {
    const key = n.source === 'nocapos' ? 'nocapos' : `app:${n.app.toLowerCase()}`;
    let g = groups.get(key);
    if (!g) groups.set(key, (g = { key, app: n.app, source: n.source, icon: n.icon, items: [] }));
    if (!g.icon && n.icon) g.icon = n.icon;
    g.items.push(n);
  }
  return [...groups.values()];
}

/** Add or replace a notification, newest first. */
export function upsertNotification(list: readonly NotifyItem[], item: NotifyItem): NotifyItem[] {
  return [item, ...list.filter((n) => n.id !== item.id)];
}

/** The banner stack after a new notification: newest on top, at most `max`. */
export function pushBanner<T extends { id: unknown }>(list: readonly T[], item: T, max = 3): T[] {
  return [item, ...list.filter((n) => n.id !== item.id)].slice(0, max);
}

// ---------- people ----------

/** Up to two initials for an avatar: "Alex Rivera" → "AR", "sam" → "S". */
export function initials(name: string): string {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (!parts.length) return '?';
  const first = [...parts[0]][0] ?? '';
  const last = parts.length > 1 ? ([...parts[parts.length - 1]][0] ?? '') : '';
  return (first + last).toUpperCase();
}

/** A stable hue (0–359) for a name, for letter tiles and avatars. */
export function hueOf(name: string): number {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return h % 360;
}
