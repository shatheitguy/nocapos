// Date & time display, following Settings → Date & Time (12/24-hour, seconds,
// time zone, first day of the week). Every clock in NoCapOS goes through here.
import { usePrefs, type WeekStart } from '../state/prefs';

export interface TimePrefs {
  clock24: boolean;
  clockSeconds: boolean;
  timeZone: string; // '' = this device's zone
}

const zone = (tz: string) => (tz ? { timeZone: tz } : {});

/** The device's own time zone, e.g. "Asia/Dubai". */
export const deviceTimeZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone;

/** All IANA time zones the browser knows (empty if unsupported). */
export function timeZones(): string[] {
  try {
    return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf('timeZone');
  } catch {
    return [];
  }
}

export function fmtTime(d: Date, p: TimePrefs, seconds = p.clockSeconds): string {
  return d.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    ...(seconds ? { second: '2-digit' as const } : {}),
    hour12: !p.clock24,
    ...zone(p.timeZone),
  });
}

export type DateStyle = 'long' | 'short' | 'month';

export function fmtDate(d: Date, p: Pick<TimePrefs, 'timeZone'>, style: DateStyle = 'long'): string {
  const opts: Intl.DateTimeFormatOptions =
    style === 'long'
      ? { weekday: 'long', month: 'long', day: 'numeric' }
      : style === 'short'
        ? { weekday: 'short', month: 'short', day: 'numeric' }
        : { month: 'long', year: 'numeric' };
  return d.toLocaleDateString([], { ...opts, ...zone(p.timeZone) });
}

/** Calendar fields of `d` as seen in a time zone (month is 0-based, weekday 0 = Sunday). */
export function zoned(d: Date, tz: string) {
  const parts = new Intl.DateTimeFormat('en-US', {
    ...zone(tz),
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
    second: 'numeric',
    weekday: 'short',
  }).formatToParts(d);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '0';
  return {
    year: Number(get('year')),
    month: Number(get('month')) - 1,
    day: Number(get('day')),
    hour: Number(get('hour')) % 24,
    minute: Number(get('minute')),
    second: Number(get('second')),
    weekday: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].indexOf(get('weekday')),
  };
}

/** Subscribe to the date & time prefs (one primitive selector each, so no re-render loops). */
export function useTimePrefs(): TimePrefs & { clockDate: boolean; weekStart: WeekStart } {
  const clock24 = usePrefs((s) => s.clock24);
  const clockSeconds = usePrefs((s) => s.clockSeconds);
  const timeZone = usePrefs((s) => s.timeZone);
  const clockDate = usePrefs((s) => s.clockDate);
  const weekStart = usePrefs((s) => s.weekStart);
  return { clock24, clockSeconds, timeZone, clockDate, weekStart };
}
