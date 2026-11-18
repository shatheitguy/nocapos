// Sticky Notes: the note model and plain helpers (no React, DOM or storage),
// so they can be unit tested. A note's body is a list of lines ("blocks"),
// each a paragraph, a bullet or a checklist item, made of text runs with
// bold, italic or a link. The server checks the same format (internal/notes).

export type BlockType = 'p' | 'li' | 'todo';

export interface Span {
  text: string;
  bold?: boolean;
  italic?: boolean;
  href?: string;
}

export interface Block {
  type: BlockType;
  done?: boolean;
  spans: Span[];
}

export type NoteColor = 'glass' | 'red' | 'ember' | 'sun' | 'mint' | 'sky' | 'grape' | 'ink';

/** NoCap's note palette, glass first. */
export const NOTE_COLORS: NoteColor[] = ['glass', 'red', 'ember', 'sun', 'mint', 'sky', 'grape', 'ink'];

/** Each colour's tint (the glass card is washed with it), for swatches and the phone. */
export const COLOR_TINT: Record<NoteColor, string> = {
  glass: '#9aa3b2',
  red: '#e8232b',
  ember: '#ff7a3d',
  sun: '#f5b82e',
  mint: '#2fc6a0',
  sky: '#3b9bff',
  grape: '#8a5cf6',
  ink: '#2a2d36',
};

export interface Desk {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Note {
  id: string;
  title: string;
  body: Block[];
  /** The body as plain lines (from the server). */
  text: string;
  color: NoteColor;
  pinned: boolean;
  archived: boolean;
  /** Unix ms; set while in the trash. */
  trashed_at?: number;
  /** Unix ms. */
  remind_at?: number;
  reminded: boolean;
  /** Where it sticks on the desktop. */
  desk?: Desk;
  created_at: number;
  updated_at: number;
  version: number;
}

/** What can change on a note (null clears remind_at and desk). */
export interface NotePatch {
  title?: string;
  body?: Block[];
  color?: NoteColor;
  pinned?: boolean;
  archived?: boolean;
  trashed?: boolean;
  remind_at?: number | null;
  desk?: Desk | null;
}

export const MAX_NOTE_BYTES = 100 * 1024;
export const MAX_TITLE = 300;

export type NoteView = 'notes' | 'archive' | 'trash';
export type NoteSort = 'edited' | 'created';

// ---------- spans and blocks ----------

const sameMarks = (a: Span, b: Span) => !!a.bold === !!b.bold && !!a.italic === !!b.italic && (a.href ?? '') === (b.href ?? '');

/** Drops empty runs and joins neighbours with the same marks; marks that are off are left out. */
export function normalizeSpans(spans: Span[]): Span[] {
  const out: Span[] = [];
  for (const s of spans) {
    if (!s.text) continue;
    const clean: Span = { text: s.text.replace(/[\r\n]+/g, ' ') };
    if (s.bold) clean.bold = true;
    if (s.italic) clean.italic = true;
    if (s.href) clean.href = s.href;
    const last = out[out.length - 1];
    if (last && sameMarks(last, clean)) last.text += clean.text;
    else out.push(clean);
  }
  return out;
}

export function normalizeBlocks(blocks: Block[]): Block[] {
  return blocks.map((b) => {
    const type: BlockType = b.type === 'li' || b.type === 'todo' ? b.type : 'p';
    const out: Block = { type, spans: normalizeSpans(b.spans ?? []) };
    if (type === 'todo' && b.done) out.done = true;
    return out;
  });
}

export const blockText = (b: Block) => b.spans.map((s) => s.text).join('');

/** Plain lines: "- " for bullets, "[ ] " / "[x] " for checklist items (as the server writes it). */
export function plainText(blocks: Block[]): string {
  return blocks
    .map((b) => (b.type === 'li' ? '- ' : b.type === 'todo' ? (b.done ? '[x] ' : '[ ] ') : '') + blockText(b))
    .join('\n');
}

/** The reverse, for text typed or kept somewhere plain (the old desktop note, the phone). */
export function blocksFromPlain(text: string): Block[] {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  while (lines.length > 1 && lines[lines.length - 1].trim() === '') lines.pop();
  return lines.map((line) => {
    const sc = lineShortcut(line);
    const rest = sc ? line.slice(sc.strip) : line;
    const block: Block = { type: sc?.type ?? 'p', spans: normalizeSpans([{ text: rest }]) };
    if (sc?.done) block.done = true;
    return block;
  });
}

/** Typing "- ", "* ", "[] ", "[ ] " or "[x] " at the start of a line turns it into a list item. */
export function lineShortcut(text: string): { type: BlockType; done?: boolean; strip: number } | null {
  const m = /^(?:([-*•])|\[( |x|X)?\])[  ]/.exec(text);
  if (!m) return null;
  if (m[1]) return { type: 'li', strip: m[0].length };
  return { type: 'todo', done: m[2] === 'x' || m[2] === 'X', strip: m[0].length };
}

export function toggleTodo(blocks: Block[], index: number): Block[] {
  return blocks.map((b, i) => (i === index && b.type === 'todo' ? { ...b, done: !b.done } : b));
}

/** How many checklist items there are and how many are done. */
export function todoProgress(blocks: Block[]): { done: number; total: number } {
  let done = 0;
  let total = 0;
  for (const b of blocks) {
    if (b.type !== 'todo') continue;
    total++;
    if (b.done) done++;
  }
  return { done, total };
}

export function sameBlocks(a: Block[], b: Block[]): boolean {
  return JSON.stringify(normalizeBlocks(a)) === JSON.stringify(normalizeBlocks(b));
}

// ---------- links ----------

/** A link people may open: http(s) or mailto; "example.org" becomes https://example.org. */
export function safeHref(raw: string): string | null {
  let h = raw.trim();
  if (!h || h.length > 2048 || /[\u0000-\u001f\u007f]/.test(h)) return null;
  if (/^[\w.+-]+@[\w-]+(\.[\w-]+)+$/.test(h)) h = 'mailto:' + h;
  else if (!/^[a-z][a-z0-9+.-]*:/i.test(h)) h = 'https://' + h.replace(/^\/+/, '');
  let u: URL;
  try {
    u = new URL(h);
  } catch {
    return null;
  }
  if (u.protocol === 'http:' || u.protocol === 'https:') return u.hostname ? h : null;
  if (u.protocol === 'mailto:') return u.pathname ? h : null;
  return null;
}

// ---------- notes ----------

export function isEmptyNote(n: Pick<Note, 'title' | 'body'>): boolean {
  return !n.title.trim() && n.body.every((b) => !blockText(b).trim());
}

/** A note's title, or its first line, or nothing. */
export function displayTitle(n: Pick<Note, 'title' | 'body'>, max = 80): string {
  const t = n.title.trim() || n.body.map(blockText).find((x) => x.trim())?.trim() || '';
  return t.length > max ? t.slice(0, max - 1) + '…' : t;
}

/** Roughly how big a note is as stored (the server's limit is 100 KB). */
export function noteBytes(n: Pick<Note, 'title' | 'body'>): number {
  return new TextEncoder().encode(n.title + JSON.stringify(normalizeBlocks(n.body))).length;
}

export function inView(n: Note, view: NoteView): boolean {
  if (view === 'trash') return n.trashed_at != null;
  if (n.trashed_at != null) return false;
  return view === 'archive' ? n.archived : !n.archived;
}

/** Every word of the query appears in the title or text (any case, accents ignored). */
export function matches(n: Note, query: string): boolean {
  const words = fold(query).split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = fold(n.title + '\n' + (n.text || plainText(n.body)));
  return words.every((w) => hay.includes(w));
}

const fold = (s: string) =>
  s
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '');

/** Newest first by the chosen time; pinned ones first in the Notes view. */
export function sortNotes(list: Note[], by: NoteSort, pinnedFirst: boolean): Note[] {
  const key = (n: Note) => (by === 'created' ? n.created_at : n.updated_at);
  return [...list].sort((a, b) => {
    if (pinnedFirst && a.pinned !== b.pinned) return a.pinned ? -1 : 1;
    return key(b) - key(a) || (a.id < b.id ? -1 : 1);
  });
}

/** What a view shows: filtered, searched and sorted. */
export function visibleNotes(all: Note[], view: NoteView, query: string, by: NoteSort): Note[] {
  return sortNotes(
    all.filter((n) => inView(n, view) && matches(n, query)),
    view === 'trash' ? 'edited' : by,
    view === 'notes',
  );
}

/** A change from elsewhere replaces ours only when it's newer. */
export function mergeNote(list: Note[], incoming: Note): Note[] {
  const i = list.findIndex((n) => n.id === incoming.id);
  if (i < 0) return [incoming, ...list];
  if (list[i].version > incoming.version) return list;
  const out = list.slice();
  out[i] = incoming;
  return out;
}

/** Applies a change locally, the way the server will. */
export function applyPatch(n: Note, p: NotePatch, now: number): Note {
  const out: Note = { ...n };
  let edited = false;
  const set = <K extends 'title' | 'color' | 'pinned' | 'archived'>(k: K) => {
    const v = p[k];
    if (v !== undefined && v !== n[k]) {
      (out as Pick<Note, K>)[k] = v as Note[K];
      edited = true;
    }
  };
  set('title');
  if (p.body !== undefined) {
    out.body = p.body;
    out.text = plainText(p.body);
    edited = true;
  }
  set('color');
  set('pinned');
  set('archived');
  if (p.trashed !== undefined && p.trashed !== (n.trashed_at != null)) {
    edited = true;
    if (p.trashed) {
      out.trashed_at = now;
      delete out.desk;
    } else delete out.trashed_at;
  }
  if (p.remind_at !== undefined) {
    if (p.remind_at === null) delete out.remind_at;
    else out.remind_at = p.remind_at;
    out.reminded = false;
    edited = true;
  }
  if (p.desk !== undefined) {
    if (p.desk === null) delete out.desk;
    else out.desk = p.desk;
  }
  if (edited) out.updated_at = now;
  return out;
}

/** Folds a later change into one still waiting to be sent. */
export function mergePatch(a: NotePatch, b: NotePatch): NotePatch {
  return { ...a, ...b };
}

export function newNoteId(): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** A fresh, empty note (not saved yet). */
export function blankNote(now: number, color: NoteColor = 'glass'): Note {
  return { id: newNoteId(), title: '', body: [{ type: 'p', spans: [] }], text: '', color, pinned: false, archived: false, reminded: false, created_at: now, updated_at: now, version: 0 };
}

// ---------- desktop stickies ----------

export const DESK_MIN = { w: 160, h: 120 };
export const DESK_MAX = { w: 900, h: 900 };
export const DESK_DEFAULT = { w: 240, h: 220 };

/** Keeps a sticky inside the desktop area and within its size limits. */
export function clampDesk(d: Desk, area: { w: number; h: number }): Desk {
  const w = Math.round(Math.min(Math.max(d.w, DESK_MIN.w), DESK_MAX.w, Math.max(DESK_MIN.w, area.w)));
  const h = Math.round(Math.min(Math.max(d.h, DESK_MIN.h), DESK_MAX.h, Math.max(DESK_MIN.h, area.h)));
  return {
    x: Math.round(Math.min(Math.max(d.x, 0), Math.max(0, area.w - w))),
    y: Math.round(Math.min(Math.max(d.y, 0), Math.max(0, area.h - h))),
    w,
    h,
  };
}

/** Where a newly stuck note goes: right side, cascading so it doesn't cover the last one. */
export function nextDeskSpot(taken: Desk[], area: { w: number; h: number }): Desk {
  const base = { x: area.w - DESK_DEFAULT.w - 40, y: 40, ...DESK_DEFAULT };
  let spot = base;
  for (let i = 1; i < 12 && taken.some((d) => Math.abs(d.x - spot.x) < 16 && Math.abs(d.y - spot.y) < 16); i++) {
    spot = { ...base, x: base.x - i * 28, y: base.y + i * 28 };
  }
  return clampDesk(spot, area);
}

// ---------- reminders ----------

/** Presets offered in the reminder menu, as times from now. */
export function reminderPresets(now: Date): { key: 'later' | 'tonight' | 'tomorrow' | 'nextWeek'; at: number }[] {
  const later = new Date(now.getTime() + 60 * 60 * 1000);
  later.setSeconds(0, 0);
  const tonight = new Date(now);
  tonight.setHours(20, 0, 0, 0);
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);
  tomorrow.setHours(9, 0, 0, 0);
  const nextWeek = new Date(now);
  nextWeek.setDate(nextWeek.getDate() + ((8 - nextWeek.getDay()) % 7 || 7));
  nextWeek.setHours(9, 0, 0, 0);
  const out: { key: 'later' | 'tonight' | 'tomorrow' | 'nextWeek'; at: number }[] = [{ key: 'later', at: later.getTime() }];
  if (tonight.getTime() > now.getTime() + 15 * 60 * 1000) out.push({ key: 'tonight', at: tonight.getTime() });
  out.push({ key: 'tomorrow', at: tomorrow.getTime() }, { key: 'nextWeek', at: nextWeek.getTime() });
  return out;
}

/** "2026-11-17T09:30" for a datetime-local input, in local time. */
export function toLocalInput(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function fromLocalInput(v: string): number | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(v);
  if (!m) return null;
  const d = new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return Number.isNaN(d.getTime()) ? null : d.getTime();
}
