import { useEffect, useRef, type MouseEvent as RMouseEvent, type PointerEvent as RPointerEvent } from 'react';
import type { StoreApp } from '../api/appstore';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { pressApp } from '../state/appDrag';
import { launchStoreApp, storeKey, uninstallStoreApp } from '../state/storeApps';

const HOLD_MS = 500;

/** Esc ends wiggle mode. */
export function useWiggleEscape(jiggle: boolean, setJiggle: (on: boolean) => void) {
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
}

/**
 * One installed App Store app in Launchpad. Click opens it; click and hold
 * starts "wiggle" mode, where installed apps show an ✕ to uninstall them.
 * Dragging it out puts a shortcut on the desktop.
 */
export function StoreTile({ app, index, jiggle, setJiggle, size = 64, onOpened, onMenu, buttonClass = 'store-tile-btn' }: {
  app: StoreApp;
  index: number;
  jiggle: boolean;
  setJiggle: (on: boolean) => void;
  size?: number;
  onOpened?: () => void;
  onMenu?: (e: RMouseEvent, app: StoreApp) => void;
  buttonClass?: string;
}) {
  const hold = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null);
  const job = app.job && !app.job.done ? app.job : null;
  const stopped = app.installed && app.installed.status !== 'running';

  const down = (e: RPointerEvent<HTMLButtonElement>) => {
    if (e.button !== 0) return;
    if (!jiggle && app.installed) pressApp(e, storeKey(app.id), 'launchpad', { touchHold: 350 });
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
  const open = () => {
    // The press that started wiggle mode, or any click while wiggling, doesn't open.
    const held = hold.current?.fired;
    hold.current = null;
    if (held || jiggle) return;
    launchStoreApp(app);
    onOpened?.();
  };

  return (
    <div className={`store-tile ${job ? 'busy' : ''} ${jiggle ? 'jiggle' : ''}`} style={{ ['--d' as string]: `${-(index % 5) * 0.07}s` }}>
      <button
        type="button"
        className={buttonClass}
        title={job ? `${app.name}: ${job.action === 'uninstall' ? 'removing' : 'installing'}…` : jiggle ? app.name : `Open ${app.name} · click and hold to remove apps`}
        onPointerDown={down}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={up}
        onClick={open}
        onContextMenu={(e) => {
          e.preventDefault();
          if (!jiggle) onMenu?.(e, app);
        }}
      >
        <span className="store-tile-icon">
          <AppIcon app={app} size={size} />
          {job && (
            <span className="store-tile-progress" style={{ ['--p' as string]: `${Math.max(4, job.percent)}%` }}>
              <span />
            </span>
          )}
          {stopped && !job && <span className="store-tile-dot" title="Stopped" />}
        </span>
        <span className="store-tile-name">{job ? (job.action === 'uninstall' ? 'Removing…' : `${app.name}…`) : app.name}</span>
      </button>
      {jiggle && !job && app.installed && (
        <button type="button" className="store-tile-x" aria-label={`Uninstall ${app.name}`} title={`Uninstall ${app.name}`} onClick={() => void uninstallStoreApp(app)}>
          <Icon name="close" size={11} />
        </button>
      )}
    </div>
  );
}
