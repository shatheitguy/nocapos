import { useEffect } from 'react';
import { Logo } from '../components/Logo';
import { Icon } from '../components/Icon';
import { usePowerState, waitForReturn } from '../lib/hostActions';

/** Shown after Restart / Shut down; a restart reloads once NoCapOS is back. */
export function PowerOverlay() {
  const action = usePowerState((s) => s.action);
  useEffect(() => {
    if (action !== 'reboot') return;
    return waitForReturn(() => window.location.reload());
  }, [action]);
  if (!action) return null;
  return (
    <div className="power-overlay" role="status">
      <Logo size={96} tagline={false} />
      {action === 'reboot' ? (
        <>
          <div className="spinner" />
          <h2>Restarting…</h2>
          <p>This page reconnects automatically when NoCapOS is back.</p>
        </>
      ) : (
        <>
          <Icon name="power" size={28} />
          <h2>Shutting down</h2>
          <p>The machine is powering off. You can close this tab.</p>
        </>
      )}
    </div>
  );
}
