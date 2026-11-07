import { useEffect, useState } from 'react';
import { BrandBar, BrandMark } from '../components/BrandLoader';
import { usePowerState } from '../lib/hostActions';
import { waitForServer } from '../lib/reconnect';

const SLOW_MS = 4 * 60_000;

/**
 * Shown after Restart / Shut down: the logo and a loading sweep while the
 * server is away. A restart reloads once NoCapOS answers again; after a shut
 * down, it reloads too if someone turns the machine back on.
 */
export function PowerOverlay() {
  const action = usePowerState((s) => s.action);
  const [gone, setGone] = useState(false);
  const [away, setAway] = useState(0);

  useEffect(() => {
    if (!action) return;
    setGone(false);
    setAway(0);
    return waitForServer({
      mustGoDown: true,
      // A restart that never seemed to go down (very fast, or the probe
      // missed it): after this long, reload anyway; it's up.
      downWithinMs: action === 'reboot' ? 90_000 : undefined,
      firstDelayMs: 2500,
      onAway: (ms) => {
        setGone(true);
        setAway(ms);
      },
      onBack: () => window.location.reload(),
    });
  }, [action]);

  if (!action) return null;
  const reboot = action === 'reboot';
  const off = !reboot && gone;
  return (
    <div className="power-overlay" role="status" aria-live="polite">
      <BrandMark size={112} busy={!off} />
      <h2>{reboot ? (gone ? 'Restarting — be right back' : 'Restarting…') : off ? 'It’s off. Sleep well.' : 'Shutting down…'}</h2>
      <p>
        {reboot
          ? away > SLOW_MS
            ? 'Taking longer than usual. Still trying — check the machine if it stays like this.'
            : 'This page reconnects by itself when NoCapOS is back.'
          : off
            ? 'You can close this tab. If the machine comes back on, this page reconnects.'
            : 'Closing apps and powering off the machine.'}
      </p>
      {!off && <BrandBar label={reboot ? 'Restarting' : 'Shutting down'} />}
    </div>
  );
}
