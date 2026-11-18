import { useEffect, useMemo, useRef, useState, type KeyboardEvent as RKeyboardEvent } from 'react';
import { AppIcon } from '../../components/AppTile';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';
import { confirmDialog } from '../../state/confirm';
import { userKey, workArea } from '../../state/prefs';
import { toast } from '../../state/toasts';
import type { WinState } from '../../state/windows';
import { APPS } from '../meta';
import { MOD, NoteEditor } from './NoteEditor';
import { ColorDots, NoteBody, Popover, ReminderMenu, agoLabel, whenLabel } from './NoteParts';
import { MAX_TITLE, displayTitle, isEmptyNote, nextDeskSpot, todoProgress, visibleNotes, type Note, type NoteSort, type NoteView } from './noteDoc';
import { flushNote, useNotes, type SaveStatus } from './notesStore';

// Sticky Notes: a board (or list) of your notes with search, pins, colours,
// archive and trash; a note opens in a sheet where it's edited in place and
// saved as you type. Notes can be stuck to the desktop and remind you.

type Layout = 'board' | 'list';

function readPref<T extends string>(key: string, ok: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(userKey(key)) as T | null;
    return v && ok.includes(v) ? v : fallback;
  } catch {
    return fallback;
  }
}
function writePref(key: string, v: string) {
  try {
    localStorage.setItem(userKey(key), v);
  } catch {
    /* ignore */
  }
}

export function Notes({ win }: { win: WinState }) {
  const t = useT();
  const notes = useNotes((s) => s.notes);
  const loaded = useNotes((s) => s.loaded);
  const loadError = useNotes((s) => s.loadError);
  const [view, setView] = useState<NoteView>('notes');
  const [query, setQuery] = useState('');
  const [sort, setSortState] = useState<NoteSort>(() => readPref('nocap.notes.sort', ['edited', 'created'] as const, 'edited'));
  const [layout, setLayoutState] = useState<Layout>(() => readPref('nocap.notes.layout', ['board', 'list'] as const, 'board'));
  const [openId, setOpenId] = useState<string | null>(null);
  const [fresh, setFresh] = useState<string | null>(null);
  const search = useRef<HTMLInputElement>(null);

  const setSort = (s: NoteSort) => {
    setSortState(s);
    writePref('nocap.notes.sort', s);
  };
  const setLayout = (l: Layout) => {
    setLayoutState(l);
    writePref('nocap.notes.layout', l);
  };

  // Opened for a note (a reminder, a sticky, the widget).
  const wantNote = win.props?.note;
  const wantAt = win.props?.at;
  useEffect(() => {
    if (!wantNote) return;
    setOpenId(wantNote);
    setView('notes');
  }, [wantNote, wantAt]);
  const focusId = useNotes((s) => s.focusId);
  useEffect(() => {
    if (!focusId) return;
    setOpenId(focusId);
    useNotes.getState().setFocus(null);
  }, [focusId]);

  const list = useMemo(() => visibleNotes(notes, view, query, sort), [notes, view, query, sort]);
  const counts = useMemo(() => {
    let active = 0;
    let archived = 0;
    let trash = 0;
    for (const n of notes) {
      if (n.trashed_at != null) trash++;
      else if (n.archived) archived++;
      else active++;
    }
    return { notes: active, archive: archived, trash };
  }, [notes]);
  const open = openId ? (notes.find((n) => n.id === openId) ?? null) : null;

  const newNote = () => {
    const n = useNotes.getState().create();
    setView('notes');
    setQuery('');
    setFresh(n.id);
    setOpenId(n.id);
  };

  const close = () => {
    if (open) {
      flushNote(open.id);
      // A note opened fresh and left empty isn't kept.
      if (open.id === fresh && isEmptyNote(open) && !open.pinned && open.remind_at == null) void useNotes.getState().removeForever(open.id);
    }
    setFresh(null);
    setOpenId(null);
  };

  const onKeyDown = (e: RKeyboardEvent<HTMLDivElement>) => {
    const typing = (e.target as HTMLElement).closest('input, textarea, [contenteditable="true"]');
    const mod = e.ctrlKey || e.metaKey;
    if (e.altKey && !mod && e.code === 'KeyN') {
      e.preventDefault();
      newNote();
    } else if ((mod && e.key.toLowerCase() === 'f') || (!typing && e.key === '/')) {
      e.preventDefault();
      if (open) close();
      search.current?.focus();
      search.current?.select();
    } else if (e.key === 'Escape' && !e.defaultPrevented) {
      if (open) {
        e.preventDefault();
        close();
      } else if (query) {
        e.preventDefault();
        setQuery('');
      }
    }
  };

  const emptyTrash = async () => {
    if (!(await confirmDialog({ title: t('notes.emptyTrashTitle'), message: t('notes.emptyTrashMsg', { n: counts.trash }), confirmLabel: t('notes.emptyTrash'), danger: true }))) return;
    if (!(await useNotes.getState().emptyTrash())) toast('error', t('notes.actionFailed'));
  };

  const pinned = view === 'notes' && !query ? list.filter((n) => n.pinned) : [];
  const others = pinned.length ? list.filter((n) => !n.pinned) : list;

  return (
    <div className="nt-shell">
      <div className="nt-app" onKeyDown={onKeyDown}>
        <aside className="nt-side">
          <div className="nt-brand">
            <AppIcon app={APPS.notes} size={30} />
            <b>{APPS.notes.title}</b>
          </div>
          <button type="button" className="nt-new" onClick={newNote} title={`${t('notes.new')} (Alt+N)`}>
            <Icon name="plus" size={16} /> {t('notes.new')}
          </button>
          <nav className="nt-nav">
            {(
              [
                ['notes', 'stickyNote'],
                ['archive', 'archiveBox'],
                ['trash', 'trash'],
              ] as const
            ).map(([v, icon]) => (
              <button
                key={v}
                type="button"
                className={view === v ? 'on' : ''}
                onClick={() => {
                  setView(v);
                  setOpenId(null);
                }}
              >
                <Icon name={icon} size={16} />
                <span>{t(`notes.view.${v}`)}</span>
                {counts[v] > 0 && <span className="nt-count">{counts[v]}</span>}
              </button>
            ))}
          </nav>
          <span className="spacer" />
          <Shortcuts />
        </aside>

        <main className="nt-main">
          <header className="nt-top">
            <label className="nt-search">
              <Icon name="search" size={15} />
              <input ref={search} type="search" value={query} placeholder={t('notes.search')} aria-label={t('notes.search')} onChange={(e) => setQuery(e.target.value)} />
            </label>
            {view !== 'trash' && (
              <label className="nt-sort" title={t('notes.sortLabel')}>
                <Icon name="sortArrows" size={14} />
                <select value={sort} onChange={(e) => setSort(e.target.value as NoteSort)} aria-label={t('notes.sortLabel')}>
                  <option value="edited">{t('notes.sort.edited')}</option>
                  <option value="created">{t('notes.sort.created')}</option>
                </select>
              </label>
            )}
            <div className="segmented nt-layout" role="group" aria-label={t('notes.layoutLabel')}>
              <button type="button" className={layout === 'board' ? 'on' : ''} title={t('notes.layout.board')} aria-label={t('notes.layout.board')} onClick={() => setLayout('board')}>
                <Icon name="launcher" size={14} />
              </button>
              <button type="button" className={layout === 'list' ? 'on' : ''} title={t('notes.layout.list')} aria-label={t('notes.layout.list')} onClick={() => setLayout('list')}>
                <Icon name="rows" size={14} />
              </button>
            </div>
            {view === 'trash' && counts.trash > 0 && (
              <button type="button" className="ghost small nt-danger" onClick={() => void emptyTrash()}>
                <Icon name="trash" size={14} /> {t('notes.emptyTrash')}
              </button>
            )}
          </header>

          <div className="nt-scroll">
            {view === 'trash' && counts.trash > 0 && <p className="nt-hint muted small">{t('notes.trashHint')}</p>}
            {!loaded ? (
              <div className="nt-empty muted">{t('notes.loading')}</div>
            ) : loadError ? (
              <div className="nt-empty">
                <p>{t('notes.loadFailed')}</p>
                <button type="button" className="small" onClick={() => void useNotes.getState().load()}>
                  {t('notes.retry')}
                </button>
              </div>
            ) : list.length === 0 ? (
              <EmptyState view={view} searching={!!query} onNew={newNote} />
            ) : (
              <>
                {pinned.length > 0 && (
                  <>
                    <h3 className="nt-section">{t('notes.pinned')}</h3>
                    <Grid notes={pinned} layout={layout} onOpen={setOpenId} />
                    <h3 className="nt-section">{t('notes.others')}</h3>
                  </>
                )}
                <Grid notes={others} layout={layout} onOpen={setOpenId} />
              </>
            )}
          </div>
        </main>

        {open && <NoteSheet key={open.id} note={open} fresh={open.id === fresh} onClose={close} />}
      </div>
    </div>
  );
}

function EmptyState({ view, searching, onNew }: { view: NoteView; searching: boolean; onNew: () => void }) {
  const t = useT();
  if (searching) return <div className="nt-empty muted">{t('notes.noMatches')}</div>;
  return (
    <div className="nt-empty">
      <div className="nt-empty-art" aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
      <b>{t(`notes.empty.${view}`)}</b>
      <span className="muted small">{t(`notes.empty.${view}Hint`)}</span>
      {view === 'notes' && (
        <button type="button" className="small" onClick={onNew}>
          <Icon name="plus" size={14} /> {t('notes.new')}
        </button>
      )}
    </div>
  );
}

function Grid({ notes, layout, onOpen }: { notes: Note[]; layout: Layout; onOpen: (id: string) => void }) {
  return (
    <div className={layout === 'board' ? 'nt-board' : 'nt-list'}>
      {notes.map((n) => (
        <NoteCard key={n.id} note={n} row={layout === 'list'} onOpen={() => onOpen(n.id)} />
      ))}
    </div>
  );
}

function NoteCard({ note, row, onOpen }: { note: Note; row: boolean; onOpen: () => void }) {
  const t = useT();
  const s = useNotes.getState();
  const title = displayTitle(note);
  const trashed = note.trashed_at != null;
  const prog = todoProgress(note.body);
  const showBody = !!note.title.trim() || note.body.length > 1;
  return (
    <article
      className={`nt-card nt-tint ${row ? 'row' : ''} ${note.pinned ? 'pinned' : ''}`}
      data-color={note.color}
      tabIndex={0}
      aria-label={title || t('notes.untitled')}
      onClick={() => !trashed && onOpen()}
      onKeyDown={(e) => {
        if ((e.key === 'Enter' || e.key === ' ') && e.target === e.currentTarget && !trashed) {
          e.preventDefault();
          onOpen();
        }
      }}
    >
      <div className="nt-card-main">
        {title ? <h4 className={note.title.trim() ? '' : 'from-body'}>{title}</h4> : <h4 className="muted">{t('notes.untitled')}</h4>}
        {showBody && !row && <NoteBody note={note} maxLines={12} tick={!trashed} />}
        {row && <p className="nt-snip muted">{rowSnippet(note)}</p>}
      </div>
      <footer className="nt-card-foot">
        {note.pinned && !trashed && <Icon name="pin" size={13} />}
        {prog.total > 0 && (
          <span className="nt-chip" title={t('notes.progress', { done: prog.done, total: prog.total })}>
            <Icon name="checklist" size={12} /> {prog.done}/{prog.total}
          </span>
        )}
        {note.remind_at != null && !trashed && (
          <span className={`nt-chip ${note.reminded ? 'past' : ''}`} title={t('notes.remind.title')}>
            <Icon name="bell" size={12} /> {whenLabel(note.remind_at)}
          </span>
        )}
        {note.desk && (
          <span className="nt-chip" title={t('notes.onDesktop')}>
            <Icon name="desktop" size={12} />
          </span>
        )}
        <span className="nt-when muted">{agoLabel(trashed ? note.trashed_at! : note.updated_at)}</span>
        <span className="nt-card-acts" onClick={(e) => e.stopPropagation()}>
          {trashed ? (
            <>
              <button type="button" className="ne-btn" title={t('notes.restore')} aria-label={t('notes.restore')} onClick={() => s.update(note.id, { trashed: false }, { now: true })}>
                <Icon name="undo" size={15} />
              </button>
              <button type="button" className="ne-btn danger" title={t('notes.deleteForever')} aria-label={t('notes.deleteForever')} onClick={() => void deleteForever(note, t)}>
                <Icon name="close" size={15} />
              </button>
            </>
          ) : (
            <>
              <button type="button" className={`ne-btn ${note.pinned ? 'on' : ''}`} title={note.pinned ? t('notes.unpin') : t('notes.pin')} aria-label={note.pinned ? t('notes.unpin') : t('notes.pin')} onClick={() => s.update(note.id, { pinned: !note.pinned }, { now: true })}>
                <Icon name="pin" size={15} />
              </button>
              <button type="button" className="ne-btn" title={note.archived ? t('notes.unarchive') : t('notes.archive')} aria-label={note.archived ? t('notes.unarchive') : t('notes.archive')} onClick={() => s.update(note.id, { archived: !note.archived }, { now: true })}>
                <Icon name="archiveBox" size={15} />
              </button>
              <button type="button" className="ne-btn" title={t('notes.trash')} aria-label={t('notes.trash')} onClick={() => trashNote(note, t)}>
                <Icon name="trash" size={15} />
              </button>
            </>
          )}
        </span>
      </footer>
    </article>
  );
}

/** The lines after the title, for list rows. */
function rowSnippet(note: Note): string {
  const lines = note.text.split('\n').filter((l) => l.trim());
  return (note.title.trim() ? lines : lines.slice(1)).slice(0, 3).join(' · ');
}

function trashNote(note: Note, t: ReturnType<typeof useT>) {
  useNotes.getState().update(note.id, { trashed: true }, { now: true });
  toast('info', t('notes.trashed'), t('notes.trashedBody'));
}

async function deleteForever(note: Note, t: ReturnType<typeof useT>) {
  const ok = await confirmDialog({ title: t('notes.deleteTitle'), message: t('notes.deleteMsg', { title: displayTitle(note) || t('notes.untitled') }), confirmLabel: t('notes.deleteForever'), danger: true });
  if (ok && !(await useNotes.getState().removeForever(note.id))) toast('error', t('notes.actionFailed'));
}

/** Stick a note on the desktop, or take it off. */
export function toggleDesk(note: Note) {
  const s = useNotes.getState();
  if (note.desk) {
    s.moveDesk(note.id, null);
    return;
  }
  const taken = s.notes.flatMap((n) => (n.desk ? [n.desk] : []));
  s.moveDesk(note.id, nextDeskSpot(taken, workArea()));
}

function NoteSheet({ note, fresh, onClose }: { note: Note; fresh: boolean; onClose: () => void }) {
  const t = useT();
  const status = useNotes((s) => s.status[note.id]);
  const s = useNotes.getState();
  const [title, setTitle] = useState(note.title);
  const titleFocused = useRef(false);
  useEffect(() => {
    if (!titleFocused.current) setTitle(note.title);
  }, [note.title]);

  const onKeyDown = (e: RKeyboardEvent<HTMLDivElement>) => {
    if (!e.altKey || e.ctrlKey || e.metaKey) return;
    const act: Record<string, () => void> = {
      KeyP: () => s.update(note.id, { pinned: !note.pinned }, { now: true }),
      KeyD: () => toggleDesk(note),
      KeyA: () => {
        s.update(note.id, { archived: !note.archived }, { now: true });
        onClose();
      },
      Backspace: () => {
        trashNote(note, t);
        onClose();
      },
      Delete: () => {
        trashNote(note, t);
        onClose();
      },
    };
    const fn = act[e.code];
    if (fn) {
      e.preventDefault();
      fn();
    }
  };

  return (
    <div className="nt-sheet-scrim" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="nt-sheet nt-tint" data-color={note.color} role="dialog" aria-label={displayTitle(note) || t('notes.untitled')} onKeyDown={onKeyDown}>
        <div className="nt-sheet-top">
          <input
            className="nt-title"
            value={title}
            maxLength={MAX_TITLE}
            placeholder={t('notes.titlePlaceholder')}
            aria-label={t('notes.titleLabel')}
            onFocus={() => (titleFocused.current = true)}
            onBlur={() => {
              titleFocused.current = false;
              flushNote(note.id);
            }}
            onChange={(e) => {
              setTitle(e.target.value);
              s.update(note.id, { title: e.target.value });
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                (e.currentTarget.closest('.nt-sheet')?.querySelector('.ne-body') as HTMLElement | null)?.focus();
              }
            }}
          />
          <button type="button" className="ne-btn" title={`${t('notes.close')} (Esc)`} aria-label={t('notes.close')} onClick={onClose}>
            <Icon name="close" size={16} />
          </button>
        </div>
        <NoteEditor note={note} autoFocus={fresh} />
        <div className="nt-sheet-foot">
          <div className="nt-acts">
            <button type="button" className={`ne-btn ${note.pinned ? 'on' : ''}`} title={`${note.pinned ? t('notes.unpin') : t('notes.pin')} (Alt+P)`} aria-label={note.pinned ? t('notes.unpin') : t('notes.pin')} onClick={() => s.update(note.id, { pinned: !note.pinned }, { now: true })}>
              <Icon name="pin" size={16} />
            </button>
            <Popover icon="palette" label={t('notes.colorLabel')}>
              {(close) => (
                <ColorDots
                  value={note.color}
                  onPick={(c) => {
                    s.update(note.id, { color: c }, { now: true });
                    close();
                  }}
                />
              )}
            </Popover>
            <Popover icon="bell" label={t('notes.remind.title')} active={note.remind_at != null && !note.reminded}>
              {(close) => <ReminderMenu note={note} close={close} />}
            </Popover>
            <button type="button" className={`ne-btn ${note.desk ? 'on' : ''}`} title={`${note.desk ? t('notes.unstick') : t('notes.stick')} (Alt+D)`} aria-label={note.desk ? t('notes.unstick') : t('notes.stick')} onClick={() => toggleDesk(note)}>
              <Icon name="desktop" size={16} />
            </button>
            <button
              type="button"
              className="ne-btn"
              title={`${note.archived ? t('notes.unarchive') : t('notes.archive')} (Alt+A)`}
              aria-label={note.archived ? t('notes.unarchive') : t('notes.archive')}
              onClick={() => {
                s.update(note.id, { archived: !note.archived }, { now: true });
                onClose();
              }}
            >
              <Icon name="archiveBox" size={16} />
            </button>
            <button
              type="button"
              className="ne-btn"
              title={`${t('notes.trash')} (Alt+Delete)`}
              aria-label={t('notes.trash')}
              onClick={() => {
                trashNote(note, t);
                onClose();
              }}
            >
              <Icon name="trash" size={16} />
            </button>
          </div>
          <span className="spacer" />
          {note.remind_at != null && (
            <span className={`nt-chip ${note.reminded ? 'past' : ''}`}>
              <Icon name="bell" size={12} /> {whenLabel(note.remind_at)}
            </span>
          )}
          <SavedBadge status={status} at={note.updated_at} />
        </div>
      </div>
    </div>
  );
}

function SavedBadge({ status, at }: { status?: SaveStatus; at: number }) {
  const t = useT();
  if (status === 'saving') return <span className="nt-saved saving">{t('notes.saving')}</span>;
  if (status === 'error') return <span className="nt-saved error">{t('notes.saveError')}</span>;
  return (
    <span className="nt-saved" title={new Date(at).toLocaleString()}>
      <Icon name="check" size={12} /> {status === 'saved' ? t('notes.saved') : t('notes.edited', { when: agoLabel(at) })}
    </span>
  );
}

function Shortcuts() {
  const t = useT();
  const rows: [string, string][] = [
    ['Alt+N', t('notes.key.new')],
    [`${MOD}+F  /`, t('notes.key.search')],
    ['Esc', t('notes.key.close')],
    [`${MOD}+B`, t('notes.fmt.bold')],
    [`${MOD}+I`, t('notes.fmt.italic')],
    [`${MOD}+K`, t('notes.fmt.link')],
    [`${MOD}+Shift+8`, t('notes.fmt.bullets')],
    [`${MOD}+Shift+9`, t('notes.fmt.checklist')],
    [`${MOD}+Enter`, t('notes.key.tick')],
    ['Alt+P', t('notes.pin')],
    ['Alt+D', t('notes.stick')],
    ['Alt+A', t('notes.archive')],
    ['Alt+Delete', t('notes.trash')],
  ];
  return (
    <Popover icon="info" label={t('notes.shortcuts')} className="nt-keys-wrap">
      {() => (
        <div className="nt-keys">
          <b>{t('notes.shortcuts')}</b>
          {rows.map(([k, label]) => (
            <div key={k} className="nt-key-row">
              <kbd>{k}</kbd>
              <span>{label}</span>
            </div>
          ))}
          <span className="muted small">{t('notes.key.markdown')}</span>
        </div>
      )}
    </Popover>
  );
}
