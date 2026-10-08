import { useEffect, useMemo, useRef, useState } from 'react';
import { canMultiWindow, openApp, visibleApps } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon } from '../components/Icon';
import { pressApp } from '../state/appDrag';
import { appMenuItems } from './Shelf';

const OPENS_WINDOW = new Set(['Open', 'Show', 'New window']);

export function Launcher({ isAdmin, onClose }: { isAdmin: boolean; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [menu, setMenu] = useState<{ x: number; y: number; appId: string } | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const apps = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return visibleApps(isAdmin).filter(
      (a) => !needle || a.title.toLowerCase().includes(needle) || a.description.toLowerCase().includes(needle),
    );
  }, [q, isAdmin]);

  useEffect(() => {
    input.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const launch = (id: string, newWindow = false) => {
    openApp(id, { newWindow: newWindow && canMultiWindow(id) });
    onClose();
  };

  // Items that put a window on screen also dismiss Launchpad.
  const launcherMenu = (appId: string): MenuItem[] =>
    appMenuItems(appId).map((it) =>
      OPENS_WINDOW.has(it.label)
        ? {
            ...it,
            onClick: () => {
              it.onClick();
              onClose();
            },
          }
        : it,
    );

  return (
    <div className="launcher" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="launcher-panel">
        <label className="search">
          <Icon name="search" size={18} />
          <input
            ref={input}
            value={q}
            placeholder="Search apps"
            aria-label="Search apps"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && apps[0] && launch(apps[0].id, e.shiftKey)}
          />
        </label>
        <div className="launcher-grid">
          {apps.map((a) => (
            <button
              key={a.id}
              type="button"
              className="launcher-app"
              title="Drag to the Dock or Desktop · Shift+click for a new window"
              // Dragging an app out hands it to the shared drag system; Launchpad
              // closes so the Dock and Desktop underneath can take the drop.
              onPointerDown={(e) => pressApp(e, a.id, 'launchpad', { onStart: onClose })}
              onClick={(e) => launch(a.id, e.shiftKey)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ x: e.clientX, y: e.clientY, appId: a.id });
              }}
            >
              <AppIcon app={a} size={56} />
              <span>{a.title}</span>
            </button>
          ))}
          {!apps.length && <p className="muted">No apps match “{q}”.</p>}
        </div>
        <p className="launcher-tip muted small">Tip: drag an app onto the Dock or the Desktop to keep it there.</p>
      </div>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={launcherMenu(menu.appId)} onClose={() => setMenu(null)} />}
    </div>
  );
}
