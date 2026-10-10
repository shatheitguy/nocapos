import { useEffect, useRef, type PointerEvent as RPointerEvent } from 'react';
import { appURL, openStoreApp, type StoreApp } from '../api/appstore';
import { openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { uninstallStoreApp } from '../state/storeApps';

const HOLD_MS = 500;

/**
 * Installed App Store apps as a phone-style grid. Click opens an app; click and
 * hold starts "wiggle" mode, where each app shows an ✕ to uninstall it.
 */
export function StoreAppGrid({ apps, jiggle, setJiggle, size = 64, onOpened, className = '' }: {
  apps: StoreApp[];
  jiggle: boolean;
  setJiggle: (on: boolean) => void;
  size?: number;
  onOpened?: () => void;
  className?: string;
}) {
  const hold = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null);

  // Esc ends wiggle mode.
  useEffect(() => {
    if (!jiggle) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setJiggle(false);
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [jiggle, setJiggle]);

  const down = (e: RPointerEvent) => {
    if (e.button !== 0) return;
    const h = { x: e.clientX, y: e.clientY, fired: false, timer: 0 };
    h.timer = window.setTimeout(() => {
      h.fired = true;
      setJiggle(true);
      navigator.vibrate?.(15);
    }, HOLD_MS);
    hold.current = h;
  };
  const move = (e: RPointerEvent) => {
    const h = hold.current;
    if (h && !h.fired && Math.hypot(e.clientX - h.x, e.clientY - h.y) > 8) {
      window.clearTimeout(h.timer);
      hold.current = null;
    }
  };
  const up = () => {
    if (hold.current && !hold.current.fired) window.clearTimeout(hold.current.timer);
  };

  const open = (a: StoreApp) => {
    // The press that started wiggle mode, or any click while wiggling, doesn't open.
    if (hold.current?.fired || jiggle) {
      hold.current = null;
      return;
    }
    hold.current = null;
    if (a.installed && appURL(a)) openStoreApp(a);
    else openApp('store', { props: { app: a.id } });
    onOpened?.();
  };

  return (
    <div className={`store-grid ${jiggle ? 'jiggle' : ''} ${className}`} onContextMenu={(e) => e.preventDefault()}>
      {apps.map((a, i) => {
        const job = a.job && !a.job.done ? a.job : null;
        const stopped = a.installed && a.installed.status !== 'running';
        return (
          <div key={a.id} className={`store-tile ${job ? 'busy' : ''}`} style={{ ['--d' as string]: `${-(i % 5) * 0.07}s` }}>
            <button
              type="button"
              className="store-tile-btn"
              title={job ? `${a.name}: ${job.action === 'uninstall' ? 'removing' : 'installing'}…` : jiggle ? a.name : `Open ${a.name} · click and hold to remove apps`}
              onPointerDown={down}
              onPointerMove={move}
              onPointerUp={up}
              onPointerCancel={up}
              onClick={() => open(a)}
            >
              <span className="store-tile-icon">
                <AppIcon app={a} size={size} />
                {job && (
                  <span className="store-tile-progress" style={{ ['--p' as string]: `${Math.max(4, job.percent)}%` }}>
                    <span />
                  </span>
                )}
                {stopped && !job && <span className="store-tile-dot" title="Stopped" />}
              </span>
              <span className="store-tile-name">{job ? (job.action === 'uninstall' ? 'Removing…' : `${a.name}…`) : a.name}</span>
            </button>
            {jiggle && !job && a.installed && (
              <button type="button" className="store-tile-x" aria-label={`Uninstall ${a.name}`} title={`Uninstall ${a.name}`}
                onClick={() => void uninstallStoreApp(a)}>
                <Icon name="close" size={11} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
