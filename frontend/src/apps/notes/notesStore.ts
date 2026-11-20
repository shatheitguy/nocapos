// Sticky Notes state: everyone's own notes, loaded once per sign-in and kept
// in step with the server (the "notes" topic) so the app, the desktop
// stickies and the widget all show the same thing. Edits apply at once and
// are saved shortly after (autosave), one request per note at a time.
import { create } from 'zustand';
import { api } from '../../api/client';
import { onSocketStatus, subscribe } from '../../api/socket';
import { tr } from '../../i18n';
import { userKey } from '../../state/prefs';
import { showLocalNotice } from '../../state/notifications';
import { toast } from '../../state/toasts';
import {
  applyPatch,
  blocksFromPlain,
  mergeNote,
  mergePatch,
  newNoteId,
  normalizeBlocks,
  plainText,
  displayTitle,
  type Desk,
  type Note,
  type NotePatch,
} from './noteDoc';

/** This browser tab, so it can skip the echo of its own changes. */
const CLIENT = newNoteId();
const HEADERS = { 'X-Notes-Client': CLIENT };

const SAVE_MS = 600;

export type SaveStatus = 'saving' | 'saved' | 'error';

interface ListReply {
  items: Note[];
  limit: number;
  max_bytes: number;
}

export const notesApi = {
  list: () => api<ListReply>('/api/v1/notes', { headers: HEADERS }),
  create: (n: Partial<Note> & { id: string }) => api<Note>('/api/v1/notes', { method: 'POST', body: toWire(n), headers: HEADERS }),
  patch: (id: string, p: NotePatch) => api<Note>(`/api/v1/notes/${encodeURIComponent(id)}`, { method: 'PATCH', body: wirePatch(p), headers: HEADERS }),
  remove: (id: string) => api<void>(`/api/v1/notes/${encodeURIComponent(id)}`, { method: 'DELETE', headers: HEADERS }),
  emptyTrash: () => api<{ deleted: number }>('/api/v1/notes/trash', { method: 'DELETE', headers: HEADERS }),
  desk: (items: { id: string; desk: Desk | null }[]) => api<{ items: Note[] }>('/api/v1/notes/desk', { method: 'PUT', body: { items }, headers: HEADERS }),
};

function toWire(n: Partial<Note> & { id: string }) {
  return {
    id: n.id,
    title: n.title ?? '',
    body: normalizeBlocks(n.body ?? []),
    color: n.color ?? 'glass',
    pinned: !!n.pinned,
    ...(n.remind_at ? { remind_at: n.remind_at } : {}),
    ...(n.desk ? { desk: n.desk } : {}),
  };
}

function wirePatch(p: NotePatch) {
  return p.body ? { ...p, body: normalizeBlocks(p.body) } : p;
}

/** The server's note, with a body that's always a list. */
function fromWire(n: Note): Note {
  return { ...n, body: Array.isArray(n.body) && n.body.length ? n.body : [{ type: 'p', spans: [] }] };
}

interface NotesState {
  notes: Note[];
  loaded: boolean;
  loadError: string | null;
  /** Save state per note, for the "Saved" indicator. */
  status: Record<string, SaveStatus>;
  /** A note to open in the app (from a sticky, the widget or a reminder). */
  focusId: string | null;
  load: () => Promise<void>;
  /** Makes a note now (saved in the background) and returns it. */
  create: (init?: Partial<Note>) => Note;
  update: (id: string, p: NotePatch, opts?: { now?: boolean }) => void;
  /** Moves a sticky on the desktop (saved when the drag ends). */
  moveDesk: (id: string, desk: Desk | null) => void;
  removeForever: (id: string) => Promise<boolean>;
  emptyTrash: () => Promise<boolean>;
  setFocus: (id: string | null) => void;
  _reset: () => void;
}

const pending = new Map<string, NotePatch>();
const timers = new Map<string, number>();
const inflight = new Map<string, Promise<void>>();
const creating = new Map<string, Promise<boolean>>();

export const useNotes = create<NotesState>((set, get) => {
  const setStatus = (id: string, s: SaveStatus | null) => {
    const status = { ...get().status };
    if (s) status[id] = s;
    else delete status[id];
    set({ status });
  };
  const replace = (n: Note) => {
    // Our own unsent edits stay on top of what the server says.
    const p = pending.get(n.id);
    const next = p ? applyPatch(n, p, n.updated_at) : n;
    set({ notes: get().notes.map((x) => (x.id === n.id ? next : x)) });
  };

  async function flush(id: string): Promise<void> {
    window.clearTimeout(timers.get(id));
    timers.delete(id);
    const running = inflight.get(id);
    if (running) {
      await running;
      if (pending.has(id) && !timers.has(id)) return flush(id);
      return;
    }
    const p = pending.get(id);
    if (!p) return;
    pending.delete(id);
    const job = (async () => {
      const made = creating.get(id);
      if (made && !(await made)) {
        pending.set(id, mergePatch(p, pending.get(id) ?? {}));
        setStatus(id, 'error');
        return;
      }
      const r = await notesApi.patch(id, p);
      if (r.ok) {
        replace(fromWire(r.data));
        if (!pending.has(id)) setStatus(id, 'saved');
      } else if (r.status === 404) {
        set({ notes: get().notes.filter((n) => n.id !== id) });
        setStatus(id, null);
      } else {
        // Keep the change and try again in a bit.
        pending.set(id, mergePatch(p, pending.get(id) ?? {}));
        setStatus(id, 'error');
        timers.set(id, window.setTimeout(() => void flush(id), 5000));
      }
    })();
    inflight.set(id, job);
    try {
      await job;
    } finally {
      inflight.delete(id);
    }
    if (pending.has(id) && !timers.has(id)) await flush(id);
  }

  return {
    notes: [],
    loaded: false,
    loadError: null,
    status: {},
    focusId: null,

    load: async () => {
      const r = await notesApi.list();
      if (!r.ok) {
        set({ loadError: r.error ?? 'error', loaded: true });
        return;
      }
      const items = r.data.items.map(fromWire);
      // Unsent local edits and notes still being made stay.
      const local = get().notes.filter((n) => creating.has(n.id) && !items.some((x) => x.id === n.id));
      set({ notes: [...local, ...items.map((n) => (pending.has(n.id) ? applyPatch(n, pending.get(n.id)!, n.updated_at) : n))], loaded: true, loadError: null });
      migrateOldNote();
    },

    create: (init = {}) => {
      const now = Date.now();
      const n: Note = {
        id: init.id ?? newNoteId(),
        title: init.title ?? '',
        body: init.body ?? [{ type: 'p', spans: [] }],
        text: init.body ? plainText(init.body) : '',
        color: init.color ?? 'glass',
        pinned: !!init.pinned,
        archived: false,
        reminded: false,
        ...(init.desk ? { desk: init.desk } : {}),
        ...(init.remind_at ? { remind_at: init.remind_at } : {}),
        created_at: now,
        updated_at: now,
        version: 0,
      };
      set({ notes: [n, ...get().notes] });
      setStatus(n.id, 'saving');
      const job = notesApi.create(n).then((r) => {
        creating.delete(n.id);
        if (!r.ok) {
          setStatus(n.id, 'error');
          set({ notes: get().notes.filter((x) => x.id !== n.id) });
          toast('error', tr('notes.saveFailed'), r.error);
          return false;
        }
        replace(fromWire(r.data));
        if (!pending.has(n.id)) setStatus(n.id, 'saved');
        return true;
      });
      creating.set(n.id, job);
      return n;
    },

    update: (id, p, opts) => {
      const cur = get().notes.find((n) => n.id === id);
      if (!cur) return;
      set({ notes: get().notes.map((n) => (n.id === id ? applyPatch(n, p, Date.now()) : n)) });
      pending.set(id, mergePatch(pending.get(id) ?? {}, p));
      setStatus(id, 'saving');
      window.clearTimeout(timers.get(id));
      if (opts?.now) {
        timers.delete(id);
        void flush(id);
      } else timers.set(id, window.setTimeout(() => void flush(id), SAVE_MS));
    },

    moveDesk: (id, desk) => get().update(id, { desk }, { now: true }),

    removeForever: async (id) => {
      pending.delete(id);
      window.clearTimeout(timers.get(id));
      const before = get().notes;
      set({ notes: before.filter((n) => n.id !== id) });
      const r = await notesApi.remove(id);
      if (!r.ok && r.status !== 404) {
        set({ notes: before });
        return false;
      }
      setStatus(id, null);
      return true;
    },

    emptyTrash: async () => {
      const before = get().notes;
      set({ notes: before.filter((n) => n.trashed_at == null) });
      const r = await notesApi.emptyTrash();
      if (!r.ok) set({ notes: before });
      return r.ok;
    },

    setFocus: (focusId) => set({ focusId }),

    _reset: () => {
      for (const t of timers.values()) window.clearTimeout(t);
      timers.clear();
      pending.clear();
      set({ notes: [], loaded: false, loadError: null, status: {}, focusId: null });
    },
  };
});

/** Sends a note's waiting edit now, if it has one. */
export function flushNote(id: string) {
  if (pending.has(id)) useNotes.getState().update(id, {}, { now: true });
}

/** Sends every waiting edit now (closing a note, leaving the page). */
export function flushNotes() {
  const s = useNotes.getState();
  for (const id of [...pending.keys()]) s.update(id, {}, { now: true });
}

// ---------- the old desktop note ----------

/** The desktop's old one-box note (kept in this browser) becomes a real note, once. */
function migrateOldNote() {
  let text = '';
  const flag = userKey('alfa.notes.moved');
  try {
    if (localStorage.getItem(flag)) return;
    text = localStorage.getItem(userKey('alfa.notes')) ?? '';
    localStorage.setItem(flag, '1');
  } catch {
    return;
  }
  if (!text.trim()) return;
  const n = useNotes.getState().create({ title: tr('notes.movedTitle'), body: blocksFromPlain(text) });
  try {
    localStorage.setItem(userKey('alfa.notes.widget'), n.id);
  } catch {
    /* ignore */
  }
}

// ---------- live ----------

interface NoteEvent {
  type: 'upsert' | 'delete' | 'reminder';
  note?: Note;
  ids?: string[];
  client?: string;
}

/** Loads the notes and follows changes and reminders; returns a stop function. */
export function watchNotes(): () => void {
  const st = useNotes.getState();
  void st.load();
  const off = subscribe('notes', (d) => {
    const e = d as NoteEvent;
    const s = useNotes.getState();
    if (e.type === 'delete' && e.ids) {
      const gone = new Set(e.ids);
      useNotes.setState({ notes: s.notes.filter((n) => !gone.has(n.id)) });
      return;
    }
    if (!e.note) return;
    const n = fromWire(e.note);
    if (e.type === 'reminder') remind(n);
    if (e.client === CLIENT && e.type === 'upsert') return; // ours: already applied
    const merged = mergeNote(s.notes, n);
    const p = pending.get(n.id);
    useNotes.setState({ notes: p ? merged.map((x) => (x.id === n.id ? applyPatch(x, p, x.updated_at) : x)) : merged });
  });
  // Back online: catch up on anything missed.
  let first = true;
  const offStatus = onSocketStatus((online) => {
    if (online && !first) void useNotes.getState().load();
    first = false;
  });
  const onHide = () => document.visibilityState === 'hidden' && flushNotes();
  document.addEventListener('visibilitychange', onHide);
  window.addEventListener('pagehide', flushNotes);
  return () => {
    off();
    offStatus();
    document.removeEventListener('visibilitychange', onHide);
    window.removeEventListener('pagehide', flushNotes);
    flushNotes();
    useNotes.getState()._reset();
  };
}

let localIds = -1;

/** A reminder came due: a NoCapOS notification that opens the note. */
function remind(n: Note) {
  const title = displayTitle(n) || tr('notes.untitled');
  showLocalNotice({
    id: localIds--,
    key: 'note:' + n.id + ':' + (n.remind_at ?? 0),
    kind: 'note',
    level: 'info',
    title: tr('notes.reminderTitle', { title }),
    body: n.title ? n.text.split('\n').find((l) => l.trim())?.slice(0, 160) : undefined,
    icon: 'notes',
    action: { app: 'note', props: { note: n.id } },
    created_at: new Date().toISOString(),
  });
}
