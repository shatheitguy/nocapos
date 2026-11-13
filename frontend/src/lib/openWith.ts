// "Open with": which NoCapOS apps can open a file, and each person's chosen
// default per file type. Pure (no DOM, no network), so it is tested on its own.
import type { FileKind } from '../api/files';

export type HandlerId = 'photo' | 'video' | 'music' | 'text' | 'quicklook' | 'browser' | 'archive';

/** Every app, in the order offered for each kind (the first one is the usual choice). */
const BY_KIND: Record<FileKind, HandlerId[]> = {
  image: ['photo', 'quicklook', 'browser'],
  video: ['video', 'quicklook', 'browser'],
  audio: ['music', 'quicklook', 'browser'],
  pdf: ['browser', 'quicklook'],
  text: ['text', 'quicklook', 'browser'],
  code: ['text', 'quicklook'],
  archive: ['archive', 'text'],
  file: ['text'],
  folder: [],
};

/** Apps that aren't installed in this build are left out (set by the app list at start-up). */
let available: Set<HandlerId> | null = null;
export function setAvailableHandlers(ids: HandlerId[]) {
  available = new Set(ids);
}

/** "photo.JPG" → "jpg"; "Makefile" → "". */
export function extOf(name: string): string {
  const n = name.toLowerCase();
  const dot = n.lastIndexOf('.');
  return dot > 0 && dot < n.length - 1 ? n.slice(dot + 1) : '';
}

/** The key a default is saved under: the extension, or the kind for files without one. */
export const typeKey = (name: string, kind: FileKind) => extOf(name) || `:${kind}`;

/** The apps that can open a file, best first. */
export function handlersFor(kind: FileKind): HandlerId[] {
  return (BY_KIND[kind] ?? []).filter((h) => !available || available.has(h));
}

/** The person's chosen app for this file, if they picked one and it can still open it. */
export function defaultFor(name: string, kind: FileKind, prefs: Record<string, string>): HandlerId | null {
  const id = prefs[typeKey(name, kind)] as HandlerId | undefined;
  return id && handlersFor(kind).includes(id) ? id : null;
}

/** The saved defaults with one choice added (or removed when `id` is null). */
export function withDefault(prefs: Record<string, string>, name: string, kind: FileKind, id: HandlerId | null): Record<string, string> {
  const next = { ...prefs };
  const k = typeKey(name, kind);
  if (id) next[k] = id;
  else delete next[k];
  return next;
}

/** Keeps only sensible entries from saved prefs (strings, known apps, at most 200 types). */
export function cleanDefaults(raw: unknown): Record<string, string> {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const known = new Set<string>(Object.values(BY_KIND).flat());
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>).slice(0, 200)) {
    if (typeof v === 'string' && known.has(v) && k.length <= 24) out[k] = v;
  }
  return out;
}
