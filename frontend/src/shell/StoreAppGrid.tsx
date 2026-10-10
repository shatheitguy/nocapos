import { useEffect, useRef, type PointerEvent as RPointerEvent } from 'react';
import { appURL, openStoreApp, type StoreApp } from '../api/appstore';
import { openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { uninstallStoreApp } from '../state/storeApps';

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
 * One installed App Store app. Click opens it; click and hold starts "wiggle"
 * mode, where installed apps show an ✕ to uninstall them.
 */
export function StoreTile({ app, index, jiggle, setJiggle, size = 64, onOpened, buttonClass = 'store-tile-btn' }: {
  app: StoreApp;
  index: number;
  jiggle: boolean;
  setJiggle: (on: boolean) => void;
  size?: number;
  onOpened?: () => void;
  buttonClass?: string;
}) {
  const hold = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null);
  const job = app.job && !app.job.done ? app.job : null;
  const stopped = app.installed && app.installed.status !== 'running';

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
  const open = () => {
    // The press that started wiggle mode, or any click while wiggling, doesn't open.
    const held = hold.current?.fired;
    hold.current = null;
    if (held || jiggle) return;
    if (app.installed && appURL(app)) openStoreApp(app);
    else openApp('store', { props: { app: app.id } });
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
        onContextMenu={(e) => e.preventDefault()}
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

/** Installed apps as a phone-style grid (the Glass home screen). */
export function StoreAppGrid({ apps, jiggle, setJiggle, size = 64, onOpened }: {
  apps: StoreApp[];
  jiggle: boolean;
  setJiggle: (on: boolean) => void;
  size?: number;
  onOpened?: () => void;
}) {
  useWiggleEscape(jiggle, setJiggle);
  return (
    <div className="store-grid">
      {apps.map((a, i) => (
        <StoreTile key={a.id} app={a} index={i} jiggle={jiggle} setJiggle={setJiggle} size={size} onOpened={onOpened} />
      ))}
    </div>
  );
}
