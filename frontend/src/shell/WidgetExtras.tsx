import { useEffect, useState } from 'react';
import { api } from '../api/client';
import type { Container } from '../api/types';
import { Bar } from '../components/Charts';
import { Icon } from '../components/Icon';
import { MonthCalendar } from '../components/MonthCalendar';
import { fmtBytes, fmtUptime } from '../lib/format';
import { useClock, usePoll } from '../lib/hooks';
import { useTimePrefs, zoned } from '../lib/time';
import { APPS, openApp } from '../apps/meta';
import { NoteBody } from '../apps/notes/NoteParts';
import { displayTitle, sortNotes } from '../apps/notes/noteDoc';
import { useNotes } from '../apps/notes/notesStore';
import { useT } from '../i18n';
import { userKey } from '../state/prefs';
import { useSystem } from '../state/system';

// ---- Analog clock ----
export function AnalogClock() {
  const now = useClock(1000);
  const { timeZone } = useTimePrefs();
  const z = zoned(now, timeZone);
  const s = z.second;
  const m = z.minute;
  const h = z.hour % 12;
  const hand = (deg: number, len: number, w: number, color: string) => (
    <line x1="50" y1="50" x2={50 + len * Math.sin((deg * Math.PI) / 180)} y2={50 - len * Math.cos((deg * Math.PI) / 180)} stroke={color} strokeWidth={w} strokeLinecap="round" />
  );
  return (
    <div className="widget glass analog-wrap">
      <svg viewBox="0 0 100 100" className="analog">
        <circle cx="50" cy="50" r="46" className="analog-face" />
        {Array.from({ length: 12 }, (_, i) => (
          <line key={i} x1="50" y1="8" x2="50" y2="13" stroke="currentColor" strokeWidth="1.5" transform={`rotate(${i * 30} 50 50)`} opacity="0.5" />
        ))}
        {hand((h + m / 60) * 30, 24, 3.2, 'currentColor')}
        {hand((m + s / 60) * 6, 34, 2.4, 'currentColor')}
        {hand(s * 6, 37, 1, 'var(--accent)')}
        <circle cx="50" cy="50" r="2.6" fill="var(--accent)" />
      </svg>
    </div>
  );
}

// ---- Calendar (current month) ----
export function CalendarWidget() {
  return (
    <div className="widget glass cal-widget">
      <MonthCalendar compact />
    </div>
  );
}

// ---- Sticky note: one of your Sticky Notes, live ----
export function NotesWidget() {
  const t = useT();
  const notes = useNotes((s) => s.notes);
  const loaded = useNotes((s) => s.loaded);
  const [chosen, setChosen] = useState<string | null>(() => {
    try {
      return localStorage.getItem(userKey('alfa.notes.widget'));
    } catch {
      return null;
    }
  });
  const choose = (id: string) => {
    setChosen(id);
    try {
      localStorage.setItem(userKey('alfa.notes.widget'), id);
    } catch {
      /* ignore */
    }
  };
  const live = sortNotes(
    notes.filter((n) => n.trashed_at == null && !n.archived),
    'edited',
    true,
  );
  // The note you picked, else your first pinned or latest one.
  const note = live.find((n) => n.id === chosen) ?? live[0];
  const open = (id?: string) => openApp('notes', id ? { props: { note: id, at: String(Date.now()) } } : {});
  const next = () => {
    if (live.length < 2 || !note) return;
    choose(live[(live.indexOf(note) + 1) % live.length].id);
  };
  const add = () => {
    const n = useNotes.getState().create();
    choose(n.id);
    open(n.id);
  };
  return (
    <div className="widget glass notes-widget nt-tint" data-color={note?.color ?? 'glass'}>
      <div className="widget-head">
        <span className="notes-widget-title">
          <Icon name="stickyNote" size={14} /> {note ? displayTitle(note, 40) || t('notes.untitled') : APPS.notes.title}
        </span>
        <span className="notes-widget-acts">
          {live.length > 1 && (
            <button type="button" className="ne-btn" title={t('notes.widgetNext')} aria-label={t('notes.widgetNext')} onClick={next}>
              <Icon name="swap" size={13} />
            </button>
          )}
          <button type="button" className="ne-btn" title={t('notes.new')} aria-label={t('notes.new')} onClick={add}>
            <Icon name="plus" size={13} />
          </button>
        </span>
      </div>
      {note ? (
        <div
          className="notes-widget-body"
          role="button"
          tabIndex={0}
          onClick={() => open(note.id)}
          onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && open(note.id)}
          title={t('notes.openInApp')}
        >
          <NoteBody note={note.title.trim() ? note : { ...note, body: note.body.slice(1) }} tick />
        </div>
      ) : (
        <button type="button" className="notes-widget-body empty muted" onClick={add}>
          {loaded ? t('notes.widgetEmpty') : t('notes.loading')}
        </button>
      )}
    </div>
  );
}

// ---- Storage (disks) ----
export function StorageWidget() {
  const latest = useSystem((s) => s.latest);
  const disks = latest?.disks ?? [];
  return (
    <button type="button" className="widget glass widget-col storage-widget" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span><Icon name="disk" size={15} /> Storage</span>
      </div>
      {disks.length === 0 ? (
        <p className="muted small">No volumes reported.</p>
      ) : (
        <div className="widget-list">
          {disks.map((d) => (
            <div key={d.path} className="widget-row">
              <div className="row-head small">
                <span className="widget-title">{d.path}</span>
                <span className="muted widget-aside">{fmtBytes(d.free, 0)} free</span>
              </div>
              <Bar value={d.percent} />
            </div>
          ))}
        </div>
      )}
    </button>
  );
}

// ---- Uptime + load ----
export function UptimeWidget() {
  const latest = useSystem((s) => s.latest);
  const info = useSystem((s) => s.info);
  if (!latest) return <div className="widget glass skeleton" />;
  return (
    <div className="widget glass uptime-widget">
      <div className="widget-head">
        <span><Icon name="power" size={15} /> Uptime</span>
      </div>
      <div className="big">{fmtUptime(latest.uptime)}</div>
      <div className="muted small uptime-os">{info?.host.os}</div>
      {(latest.cpu.load1 || latest.cpu.load5) > 0 && (
        <div className="muted small uptime-load">
          load {latest.cpu.load1.toFixed(2)} · {latest.cpu.load5.toFixed(2)} · {latest.cpu.load15.toFixed(2)}
        </div>
      )}
    </div>
  );
}

// ---- Containers status ----
export function ContainersWidget() {
  const [data, setData] = useState<{ running: number; total: number } | null>(null);
  const load = async () => {
    const r = await api<Container[]>('/api/v1/containers');
    if (r.ok) setData({ running: r.data.filter((c) => c.state === 'running').length, total: r.data.length });
    else setData({ running: 0, total: 0 });
  };
  useEffect(() => {
    void load();
  }, []);
  usePoll(load, 10_000);
  return (
    <button type="button" className="widget glass cont-widget" onClick={() => openApp('containers')}>
      <div className="widget-head">
        <span><Icon name="containers" size={15} /> Containers</span>
      </div>
      {data ? (
        <div className="cont-stat">
          <div>
            <span className="big">{data.running}</span>
            <div className="muted small">running</div>
          </div>
          <div>
            <span className="big">{data.total}</span>
            <div className="muted small">total</div>
          </div>
        </div>
      ) : (
        <p className="muted small">Loading…</p>
      )}
    </button>
  );
}
