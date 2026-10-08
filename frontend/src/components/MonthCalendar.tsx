import { useState } from 'react';
import { useClock } from '../lib/hooks';
import { useTimePrefs, zoned } from '../lib/time';
import { Icon } from './Icon';

/** Month grid you can page through and pick a day on (widget + clock panel). */
export function MonthCalendar({ compact = false }: { compact?: boolean }) {
  const now = useClock(60_000);
  const tp = useTimePrefs();
  const today = zoned(now, tp.timeZone);
  const [view, setView] = useState({ year: today.year, month: today.month });
  const [picked, setPicked] = useState<{ year: number; month: number; day: number } | null>(null);

  const monday = tp.weekStart === 'mon';
  const first = (new Date(view.year, view.month, 1).getDay() + (monday ? 6 : 0)) % 7;
  const days = new Date(view.year, view.month + 1, 0).getDate();
  const cells: (number | null)[] = [...Array.from<unknown, number | null>({ length: first }, () => null), ...Array.from({ length: days }, (_, i) => i + 1)];
  const isThisMonth = view.year === today.year && view.month === today.month;

  const shift = (delta: number) =>
    setView((v) => {
      const d = new Date(v.year, v.month + delta, 1);
      return { year: d.getFullYear(), month: d.getMonth() };
    });
  const goToday = () => {
    setView({ year: today.year, month: today.month });
    setPicked(null);
  };

  const title = new Date(view.year, view.month, 1).toLocaleDateString([], { month: 'long', year: 'numeric' });
  const pickedDate = picked ? new Date(picked.year, picked.month, picked.day) : null;
  const diff = pickedDate ? Math.round((pickedDate.getTime() - new Date(today.year, today.month, today.day).getTime()) / 86_400_000) : 0;

  return (
    <div className={`mcal ${compact ? 'compact' : ''}`}>
      <div className="mcal-head">
        <button type="button" className="mcal-title" title="Back to today" onClick={goToday}>
          {title}
        </button>
        <span className="spacer" />
        {!isThisMonth && (
          <button type="button" className="mcal-today" onClick={goToday}>
            Today
          </button>
        )}
        <button type="button" className="mcal-nav" aria-label="Previous month" onClick={() => shift(-1)}>
          <Icon name="chevronLeft" size={14} />
        </button>
        <button type="button" className="mcal-nav" aria-label="Next month" onClick={() => shift(1)}>
          <Icon name="chevronRight" size={14} />
        </button>
      </div>
      <div
        className="cal-grid"
        onWheel={(e) => {
          if (Math.abs(e.deltaY) > 20) shift(e.deltaY > 0 ? 1 : -1);
        }}
      >
        {(monday ? ['M', 'T', 'W', 'T', 'F', 'S', 'S'] : ['S', 'M', 'T', 'W', 'T', 'F', 'S']).map((d, i) => (
          <span key={i} className="cal-dow">
            {d}
          </span>
        ))}
        {cells.map((d, i) =>
          d === null ? (
            <span key={i} />
          ) : (
            <button
              key={i}
              type="button"
              className={`cal-day ${isThisMonth && d === today.day ? 'today' : ''} ${picked && picked.year === view.year && picked.month === view.month && picked.day === d ? 'picked' : ''}`}
              onClick={() => setPicked({ ...view, day: d })}
            >
              {d}
            </button>
          ),
        )}
      </div>
      {pickedDate && (
        <div className="mcal-picked">
          <b>{pickedDate.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })}</b>
          <span>{diff === 0 ? 'Today' : diff === 1 ? 'Tomorrow' : diff === -1 ? 'Yesterday' : diff > 0 ? `In ${diff} days` : `${-diff} days ago`}</span>
        </div>
      )}
    </div>
  );
}
