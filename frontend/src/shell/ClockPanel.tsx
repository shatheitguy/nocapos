import { useRef, type RefObject } from 'react';
import { openApp } from '../apps/meta';
import { Icon } from '../components/Icon';
import { MonthCalendar } from '../components/MonthCalendar';
import { useClock, useDismiss } from '../lib/hooks';
import { deviceTimeZone, fmtDate, fmtTime, useTimePrefs } from '../lib/time';

/** Clock & calendar flyout, opened from the menu bar / tray clock. */
export function ClockPanel({ anchor, onClose }: { anchor: RefObject<HTMLElement | null>; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(true, onClose, ref, anchor);
  const tp = useTimePrefs();
  const now = useClock(1000);

  return (
    <div ref={ref} className="popover quick-settings clock-panel" role="dialog" aria-label="Clock and calendar">
      <div className="clock-panel-now">
        <span className="clock-panel-time">{fmtTime(now, tp, true)}</span>
        <span className="clock-panel-date">{fmtDate(now, tp, 'long')}</span>
        <span className="clock-panel-zone">{(tp.timeZone || deviceTimeZone()).replace(/_/g, ' ')}</span>
      </div>
      <MonthCalendar />
      <button
        type="button"
        className="ghost small qs-wide"
        onClick={() => {
          openApp('settings', { props: { section: 'datetime' } });
          onClose();
        }}
      >
        <Icon name="clock" size={14} /> Date & Time settings…
      </button>
    </div>
  );
}
