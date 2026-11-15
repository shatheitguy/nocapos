// Pure helpers for the Containers app (no React, no DOM), so they can be
// tested with `npm test`: grouping containers into projects, log levels,
// paths inside a container, local names and the window of a long list.

/** The bits of a container these helpers look at. */
export interface GroupItem {
  id: string;
  name: string;
  state: string;
  project?: string;
}

export interface Group<T extends GroupItem> {
  /** The Compose project, or '' for loose ("standalone") containers. */
  project: string;
  items: T[];
  running: number;
  /** running | partial | stopped */
  state: 'running' | 'partial' | 'stopped';
}

/** Containers grouped by Compose project (sorted by name), standalone ones last. */
export function groupContainers<T extends GroupItem>(list: T[]): Group<T>[] {
  const by = new Map<string, T[]>();
  for (const c of list) {
    const k = c.project ?? '';
    const arr = by.get(k);
    if (arr) arr.push(c);
    else by.set(k, [c]);
  }
  const groups: Group<T>[] = [];
  for (const [project, items] of by) {
    items.sort((a, b) => a.name.localeCompare(b.name));
    const running = items.filter((c) => c.state === 'running').length;
    groups.push({ project, items, running, state: running === 0 ? 'stopped' : running === items.length ? 'running' : 'partial' });
  }
  groups.sort((a, b) => (a.project === '' ? 1 : b.project === '' ? -1 : a.project.localeCompare(b.project)));
  return groups;
}

/** Sums a number over a group's containers, skipping the ones without a value. */
export function sumBy<T>(items: T[], value: (t: T) => number | undefined): number {
  let n = 0;
  for (const t of items) n += value(t) ?? 0;
  return n;
}

export type Level = 'error' | 'warn' | 'info' | 'debug' | 'other';

const LEVELS: [Level, RegExp][] = [
  ['error', /\b(error|err|fatal|crit(ical)?|panic|emerg|alert|exception|traceback)\b|level=(error|fatal)|"level":"(error|fatal)"/i],
  ['warn', /\b(warn(ing)?)\b|level=warn|"level":"warn/i],
  ['debug', /\b(debug|trace)\b|level=(debug|trace)|"level":"(debug|trace)"/i],
  ['info', /\b(info|notice)\b|level=info|"level":"info"/i],
];

/** A log line's level, guessed from its text; stderr without a level counts as a warning. */
export function logLevel(text: string, stream?: string): Level {
  const head = text.slice(0, 200);
  for (const [lvl, re] of LEVELS) if (re.test(head)) return lvl;
  return stream === 'stderr' ? 'warn' : 'other';
}

/** Whether a line passes the level filter ('all' shows everything; 'info' hides debug noise). */
export function levelShown(level: Level, filter: 'all' | 'error' | 'warn' | 'info'): boolean {
  if (filter === 'all') return true;
  if (filter === 'error') return level === 'error';
  if (filter === 'warn') return level === 'error' || level === 'warn';
  return level !== 'debug';
}

/** Joins a folder and a name inside a container ("/" + "etc" = "/etc"). */
export function joinPath(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir.replace(/\/+$/, '')}/${name}`;
}

/** The folder above a path ("/" stays "/"). */
export function parentPath(p: string): string {
  const i = p.replace(/\/+$/, '').lastIndexOf('/');
  return i <= 0 ? '/' : p.slice(0, i);
}

/** The steps of a path for breadcrumbs: [["/", "/"], ["etc", "/etc"], …]. */
export function crumbs(p: string): [string, string][] {
  const out: [string, string][] = [['/', '/']];
  let cur = '';
  for (const part of p.split('/').filter(Boolean)) {
    cur += `/${part}`;
    out.push([part, cur]);
  }
  return out;
}

/** Checks a path typed into the file browser the way the server does. */
export function validPath(p: string): boolean {
  return p.startsWith('/') && p.length <= 4096 && !p.split('/').includes('..') && !/[\u0000-\u001f\u007f]/.test(p);
}

/** Checks a file name for an upload (one path element). */
export function validName(n: string): boolean {
  return n.length > 0 && n.length <= 255 && n !== '.' && n !== '..' && !/[/\\\u0000-\u001f\u007f]/.test(n);
}

const LOCAL_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** What a nocap.domain label gives (null when the server would ignore it). */
export function localName(label: string): string | null {
  let v = label.trim().toLowerCase().replace(/\.$/, '').replace(/\.nocap\.local$/, '');
  if (!v || v.length > 120 || v === 'nocap.local') return null;
  const parts = v.split('.');
  if (parts.length > 3 || !parts.every((p) => LOCAL_LABEL.test(p))) return null;
  if (parts.length === 1 && ['www', 'localhost', 'nocap'].includes(v)) return null;
  v = parts.join('.');
  return `${v}.nocap.local`;
}

/** The rows of a long list worth drawing: the visible ones plus a few either side. */
export function visibleRange(total: number, rowHeight: number, scrollTop: number, viewport: number, overscan = 6): [number, number] {
  if (total <= 0 || rowHeight <= 0) return [0, 0];
  const first = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  const last = Math.min(total, Math.ceil((scrollTop + viewport) / rowHeight) + overscan);
  return [first, Math.max(first, last)];
}

/** "nginx:1.27" → ["nginx", "1.27"]; registry ports aren't tags. */
export function splitTag(ref: string): [string, string] {
  const slash = ref.lastIndexOf('/');
  const colon = ref.lastIndexOf(':');
  return colon > slash ? [ref.slice(0, colon), ref.slice(colon + 1)] : [ref, 'latest'];
}

/** The --device value for a host device mapped to a path inside the container. */
export function deviceFlag(host: string, inContainer?: string): string {
  return inContainer && inContainer !== host ? `${host}:${inContainer}` : host;
}
