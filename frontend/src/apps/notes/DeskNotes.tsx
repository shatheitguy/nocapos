import { useEffect, useRef, useState, type PointerEvent as RPointerEvent } from 'react';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';
import { workArea } from '../../state/prefs';
import { openNote } from './openNote';
import { NoteEditor } from './NoteEditor';
import { ColorDots, Popover } from './NoteParts';
import { clampDesk, displayTitle, type Desk, type Note } from './noteDoc';
import { useNotes } from './notesStore';

// Notes stuck to the desktop: glass stickies you can drag by their top edge,
// resize from the corner and type in. Where each one sits is saved with the
// note, so it's in the same spot on every screen you sign in from.

export function DeskNotes() {
  const notes = useNotes((s) => s.notes);
  const stuck = notes.filter((n) => n.desk && n.trashed_at == null && !n.archived);
  const [area, setArea] = useState(() => workArea());
  useEffect(() => {
    const onResize = () => setArea(workArea());
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);
  if (!stuck.length) return null;
  return (
    <div className="nt-desk-layer">
      {stuck.map((n) => (
        <DeskNote key={n.id} note={n} area={area} />
      ))}
    </div>
  );
}

function DeskNote({ note, area }: { note: Note; area: { w: number; h: number } }) {
  const t = useT();
  // While dragging, the spot lives here; it's saved when the drag ends.
  const [live, setLive] = useState<Desk | null>(null);
  const [raised, setRaised] = useState(false);
  const drag = useRef<{ px: number; py: number; start: Desk; mode: 'move' | 'size' } | null>(null);
  const d = clampDesk(live ?? note.desk!, area);

  const down = (mode: 'move' | 'size') => (e: RPointerEvent<HTMLElement>) => {
    if (e.button !== 0 || (mode === 'move' && (e.target as HTMLElement).closest('button, input'))) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { px: e.clientX, py: e.clientY, start: d, mode };
    setRaised(true);
  };
  const move = (e: RPointerEvent<HTMLElement>) => {
    const g = drag.current;
    if (!g) return;
    const dx = e.clientX - g.px;
    const dy = e.clientY - g.py;
    setLive(clampDesk(g.mode === 'move' ? { ...g.start, x: g.start.x + dx, y: g.start.y + dy } : { ...g.start, w: g.start.w + dx, h: g.start.h + dy }, area));
  };
  const up = () => {
    const g = drag.current;
    drag.current = null;
    if (!g || !live) {
      setLive(null);
      return;
    }
    if (live.x !== g.start.x || live.y !== g.start.y || live.w !== g.start.w || live.h !== g.start.h) useNotes.getState().moveDesk(note.id, live);
    setLive(null);
  };

  const s = useNotes.getState();
  return (
    <section
      className={`nt-sticky nt-tint ${raised ? 'raised' : ''} ${live ? 'dragging' : ''}`}
      data-color={note.color}
      style={{ left: d.x, top: d.y, width: d.w, height: d.h }}
      aria-label={displayTitle(note) || t('notes.untitled')}
      onPointerDown={() => setRaised(true)}
      onBlur={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setRaised(false)}
    >
      <header className="nt-sticky-grip" onPointerDown={down('move')} onPointerMove={move} onPointerUp={up} onPointerCancel={up} title={t('notes.dragHint')}>
        <span className="nt-sticky-title">{note.title.trim() || ''}</span>
        <span className="nt-sticky-acts">
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
          <button
            type="button"
            className="ne-btn"
            title={t('notes.openInApp')}
            aria-label={t('notes.openInApp')}
            onClick={() => openNote(note.id)}
          >
            <Icon name="maximize" size={14} />
          </button>
          <button type="button" className="ne-btn" title={t('notes.unstick')} aria-label={t('notes.unstick')} onClick={() => s.moveDesk(note.id, null)}>
            <Icon name="close" size={14} />
          </button>
        </span>
      </header>
      <NoteEditor note={note} toolbar={false} compact />
      <div className="nt-sticky-size" onPointerDown={down('size')} onPointerMove={move} onPointerUp={up} onPointerCancel={up} aria-hidden="true" />
    </section>
  );
}
