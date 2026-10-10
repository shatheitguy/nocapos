import { useEffect, useState } from 'react';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { fmtDate, useTimePrefs, zoned } from '../lib/time';
import { useSpotlight } from '../state/spotlight';
import { shownOnHome, useStoreApps } from '../state/storeApps';
import { StoreAppGrid } from './StoreAppGrid';

/** Glass theme home: a centred greeting, a search pill and your installed apps, like a phone's home screen. */
export function HomeHeader({ username, isAdmin }: { username: string; isAdmin: boolean }) {
  const now = useClock(30_000);
  const tp = useTimePrefs();
  const hour = zoned(now, tp.timeZone).hour;
  const greeting = hour < 5 ? 'Good night' : hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
  const mac = /Mac|iPhone|iPad/.test(navigator.platform);
  const apps = useStoreApps((s) => s.apps).filter(shownOnHome);
  const [jiggle, setJiggle] = useState(false);

  // Clicking anywhere else ends wiggle mode (dialogs it opened don't count).
  useEffect(() => {
    if (!jiggle) return;
    const onDown = (e: PointerEvent) => {
      if (!(e.target as Element).closest?.('.store-grid, .dialog, .dialog-backdrop, .home-done')) setJiggle(false);
    };
    window.addEventListener('pointerdown', onDown, true);
    return () => window.removeEventListener('pointerdown', onDown, true);
  }, [jiggle]);
  useEffect(() => {
    if (jiggle && !apps.some((a) => a.installed)) setJiggle(false);
  }, [jiggle, apps]);

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
      {isAdmin && apps.length > 0 && (
        <div className="home-apps">
          {jiggle && (
            <button type="button" className="pill home-done" onClick={() => setJiggle(false)}>
              Done
            </button>
          )}
          <StoreAppGrid apps={apps} jiggle={jiggle} setJiggle={setJiggle} />
        </div>
      )}
    </div>
  );
}
