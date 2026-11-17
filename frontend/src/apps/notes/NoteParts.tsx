// Shared Sticky Notes pieces: a note's body drawn read-only (cards, the
// widget), its colour dots, the reminder menu and friendly times.
import { useRef, useState, type ReactNode } from 'react';
import { Icon } from '../../components/Icon';
import { useDismiss } from '../../lib/hooks';
import { useT, type T } from '../../i18n';
import { usePrefs } from '../../state/prefs';
import {
  COLOR_TINT,
  NOTE_COLORS,
  fromLocalInput,
  reminderPresets,
  safeHref,
  toLocalInput,
  type Block,
  type Note,
  type NoteColor,
  type Span,
} from './noteDoc';
import { useNotes } from './notesStore';

/** A note's lines, read-only; checklist boxes still tick when `tick` is on. */
export function NoteBody({ note, maxLines, tick }: { note: Pick<Note, 'id' | 'body'>; maxLines?: number; tick?: boolean }) {
  const t = useT();
  const blocks = maxLines ? note.body.slice(0, maxLines) : note.body;
  const toggle = (i: number) => {
    const body = note.body.map((b, j) => (j === i && b.type === 'todo' ? { ...b, done: !b.done } : b));
    useNotes.getState().update(note.id, { body }, { now: true });
  };
  return (
    <div className="nt-body">
      {blocks.map((b, i) => (
        <Line key={i} b={b} onTick={tick ? () => toggle(i) : undefined} label={t('notes.tick')} />
      ))}
      {maxLines && note.body.length > maxLines && <div className="nt-more">…</div>}
    </div>
  );
}

function Line({ b, onTick, label }: { b: Block; onTick?: () => void; label: string }) {
  const content = b.spans.length ? b.spans.map((s, i) => <SpanView key={i} s={s} />) : <br />;
  if (b.type === 'todo') {
    return (
      <div className={`nt-line todo ${b.done ? 'done' : ''}`}>
        <button
          type="button"
          className="nt-box"
          role="checkbox"
          aria-checked={!!b.done}
          aria-label={label}
          disabled={!onTick}
          onClick={(e) => {
            e.stopPropagation();
            onTick?.();
          }}
        >
          {b.done && <Icon name="check" size={12} />}
        </button>
        <span>{content}</span>
      </div>
    );
  }
  return <div className={`nt-line ${b.type === 'li' ? 'li' : ''}`}>{content}</div>;
}

function SpanView({ s }: { s: Span }) {
  let node: ReactNode = s.text;
  if (s.italic) node = <i>{node}</i>;
  if (s.bold) node = <b>{node}</b>;
  const href = s.href ? safeHref(s.href) : null;
  if (href) {
    node = (
      <a href={href} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()}>
        {node}
      </a>
    );
  }
  return <>{node}</>;
}

export const colorName = (c: NoteColor, t: T) => t(`notes.color.${c}`);

/** The palette as round swatches. */
export function ColorDots({ value, onPick }: { value: NoteColor; onPick: (c: NoteColor) => void }) {
  const t = useT();
  return (
    <div className="nt-colors" role="radiogroup" aria-label={t('notes.colorLabel')}>
      {NOTE_COLORS.map((c) => (
        <button
          key={c}
          type="button"
          role="radio"
          aria-checked={c === value}
          className={`nt-dot ${c === value ? 'on' : ''}`}
          data-color={c}
          style={{ ['--tint' as string]: COLOR_TINT[c] }}
          title={colorName(c, t)}
          aria-label={colorName(c, t)}
          onClick={() => onPick(c)}
        />
      ))}
    </div>
  );
}

/** A small menu button with a popover under it. */
export function Popover({ icon, label, active, children, className }: { icon: Parameters<typeof Icon>[0]['name']; label: string; active?: boolean; children: (close: () => void) => ReactNode; className?: string }) {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const box = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), btn, box);
  return (
    <span className={`nt-pop-wrap ${className ?? ''}`}>
      <button ref={btn} type="button" className={`ne-btn ${active ? 'on' : ''}`} title={label} aria-label={label} aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <Icon name={icon} size={16} />
      </button>
      {open && (
        <div ref={box} className="nt-pop glass">
          {children(() => setOpen(false))}
        </div>
      )}
    </span>
  );
}

/** Pick when a note reminds you, or clear it. */
export function ReminderMenu({ note, close }: { note: Note; close: () => void }) {
  const t = useT();
  const [custom, setCustom] = useState(note.remind_at && note.remind_at > Date.now() ? toLocalInput(note.remind_at) : '');
  const set = (at: number | null) => {
    useNotes.getState().update(note.id, { remind_at: at }, { now: true });
    close();
  };
  const at = fromLocalInput(custom);
  return (
    <div className="nt-remind">
      <b>{t('notes.remind.title')}</b>
      {reminderPresets(new Date()).map((p) => (
        <button key={p.key} type="button" className="nt-menu-item" onClick={() => set(p.at)}>
          <span>{t(`notes.remind.${p.key}`)}</span>
          <span className="muted small">{whenLabel(p.at)}</span>
        </button>
      ))}
      <label className="nt-remind-custom">
        <span className="muted small">{t('notes.remind.pick')}</span>
        <input type="datetime-local" value={custom} onChange={(e) => setCustom(e.target.value)} />
      </label>
      <div className="nt-remind-actions">
        {note.remind_at != null && (
          <button type="button" className="ghost small" onClick={() => set(null)}>
            {t('notes.remind.clear')}
          </button>
        )}
        <button type="button" className="small" disabled={!at || at <= Date.now()} onClick={() => at && set(at)}>
          {t('notes.remind.set')}
        </button>
      </div>
    </div>
  );
}

/** "Today 14:30", "Tomorrow 09:00", "Mon 24 Nov 09:00". */
export function whenLabel(ms: number, now = new Date()): string {
  const d = new Date(ms);
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: !usePrefs.getState().clock24 });
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(d) - day(now)) / 86_400_000);
  const rel = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  if (diff === 0 || diff === 1 || diff === -1) {
    const word = rel.format(diff, 'day');
    return `${word.charAt(0).toUpperCase()}${word.slice(1)} ${time}`;
  }
  return `${d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' })} ${time}`;
}

/** "just now", "5 min. ago", "yesterday", then a date. */
export function agoLabel(ms: number, now = Date.now()): string {
  const s = Math.round((ms - now) / 1000);
  const rel = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'short' });
  const a = Math.abs(s);
  if (a < 45) return rel.format(0, 'second');
  if (a < 3600) return rel.format(Math.round(s / 60), 'minute');
  if (a < 86_400) return rel.format(Math.round(s / 3600), 'hour');
  if (a < 7 * 86_400) return rel.format(Math.round(s / 86_400), 'day');
  return new Date(ms).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: new Date(ms).getFullYear() === new Date(now).getFullYear() ? undefined : 'numeric' });
}
