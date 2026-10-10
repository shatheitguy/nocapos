import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { fmtDate, useTimePrefs, zoned } from '../lib/time';
import { useSpotlight } from '../state/spotlight';

/** Glass theme home: a centred greeting and a search pill, like a phone's home screen. */
export function HomeHeader({ username }: { username: string }) {
  const now = useClock(30_000);
  const tp = useTimePrefs();
  const hour = zoned(now, tp.timeZone).hour;
  const greeting = hour < 5 ? 'Good night' : hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
  const mac = /Mac|iPhone|iPad/.test(navigator.platform);
  return (
    <div className="home-header">
      <div className="home-date">{fmtDate(now, tp)}</div>
      <h1 className="home-greet">
        {greeting}, {username}.
      </h1>
      <button type="button" className="home-search" onClick={() => useSpotlight.getState().set(true)}>
        <Icon name="search" size={14} /> Search
        <kbd>{mac ? '⌃' : 'Ctrl'} Space</kbd>
      </button>
    </div>
  );
}
