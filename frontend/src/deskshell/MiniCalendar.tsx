// The month grid from the web OS's clock panel (components/MonthCalendar),
// without its prefs store: weeks start on the locale's usual day.
import { useState } from 'react';
import { Icon } from '../components/Icon';
import { useT } from './i18n';

function weekStartsMonday(): boolean {
  try {
    const info = (new Intl.Locale(navigator.language) as Intl.Locale & { weekInfo?: { firstDay: number }; getWeekInfo?: () => { firstDay: number } });
    const w = info.getWeekInfo?.() ?? info.weekInfo;
    return w ? w.firstDay === 1 : false;
  } catch {
    return false;
  }
}
const MONDAY = weekStartsMonday();

export function MiniCalendar({ now }: { now: Date }) {
  const t = useT();
  const today = { year: now.getFullYear(), month: now.getMonth(), day: now.getDate() };
  const [view, setView] = useState({ year: today.year, month: today.month });

  const first = (new Date(view.year, view.month, 1).getDay() + (MONDAY ? 6 : 0)) % 7;
  const days = new Date(view.year, view.month + 1, 0).getDate();
  const cells: (number | null)[] = [...Array.from({ length: first }, () => null), ...Array.from({ length: days }, (_, i) => i + 1)];
  const isThisMonth = view.year === today.year && view.month === today.month;
  const shift = (delta: number) =>
    setView((v) => {
      const d = new Date(v.year, v.month + delta, 1);
      return { year: d.getFullYear(), month: d.getMonth() };
    });
  const goToday = () => setView({ year: today.year, month: today.month });
  const title = new Date(view.year, view.month, 1).toLocaleDateString([], { month: 'long', year: 'numeric' });
  // Day initials in the locale's language: 2024-01-07 was a Sunday.
  const dow = Array.from({ length: 7 }, (_, i) => new Date(2024, 0, 7 + i + (MONDAY ? 1 : 0)).toLocaleDateString([], { weekday: 'narrow' }));

  return (
    <div className="mcal compact">
      <div className="mcal-head">
        <button type="button" className="mcal-title" title={t('nsh.cal.today')} onClick={goToday}>
          {title}
        </button>
        <span className="spacer" />
        {!isThisMonth && (
          <button type="button" className="mcal-today" onClick={goToday}>
            {t('nsh.cal.today')}
          </button>
        )}
        <button type="button" className="mcal-nav" aria-label={t('nsh.cal.prev')} onClick={() => shift(-1)}>
          <Icon name="chevronLeft" size={14} />
        </button>
        <button type="button" className="mcal-nav" aria-label={t('nsh.cal.next')} onClick={() => shift(1)}>
          <Icon name="chevronRight" size={14} />
        </button>
      </div>
      <div className="cal-grid" onWheel={(e) => Math.abs(e.deltaY) > 20 && shift(e.deltaY > 0 ? 1 : -1)}>
        {dow.map((d, i) => (
          <span key={i} className="cal-dow">
            {d}
          </span>
        ))}
        {cells.map((d, i) =>
          d === null ? (
            <span key={i} />
          ) : (
            <span key={i} className={`cal-day nsh-day ${isThisMonth && d === today.day ? 'today' : ''}`}>
              {d}
            </span>
          ),
        )}
      </div>
    </div>
  );
}
