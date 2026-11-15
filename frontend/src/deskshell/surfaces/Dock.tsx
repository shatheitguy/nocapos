// The dock (bottom edge, 76 px): the roster button, pinned apps, then apps
// with open windows. Only the pill is painted; the surface is full width.
// The surface is too short for a menu or tooltips above the icons, so the
// name shows in a small tag on the pill's rim and the right-click menu
// replaces the icons inside the pill until it is closed.
import { useEffect, useMemo, useRef, useState, type PointerEvent as RPointerEvent } from 'react';
import { Icon } from '../../components/Icon';
import { call, send } from '../bridge';
import { useEvent, useHostValue, useShellInfo } from '../hooks';
import { useT } from '../i18n';
import { dockClick, dockEntries, reorder, togglePin, type DockEntry } from '../logic';
import type { AppInfo, WindowInfo } from '../protocol';
import { AppIcon } from '../ui';

const ICON = 44;

function run(e: DockEntry) {
  const c = dockClick(e);
  if (!c) return;
  if (c.kind === 'launch') send('apps.launch', { id: c.id });
  else if (c.kind === 'focus') send('windows.focus', { id: c.window.id });
  else send('windows.minimize', { id: c.window.id });
}

interface Drag {
  id: string;
  from: number;
  startX: number;
  over: number;
  moved: boolean;
}

export function Dock() {
  useShellInfo();
  const t = useT();
  const [apps, , reloadApps] = useHostValue<AppInfo[]>('apps.list', null, []);
  const [windows] = useHostValue<WindowInfo[]>('windows.list', 'windows.changed', []);
  const [pinned, setPinned] = useState<string[]>([]);
  const [menu, setMenu] = useState<string | null>(null);
  const [hover, setHover] = useState<{ id: string; x: number } | null>(null);
  const [drag, setDrag] = useState<Drag | null>(null);
  const pinRefs = useRef(new Map<string, HTMLElement>());
  const pill = useRef<HTMLElement>(null);

  useEffect(() => {
    call<{ pinned: string[] }>('dock.get')
      .then((d) => setPinned(Array.isArray(d?.pinned) ? d.pinned : []))
      .catch(() => {});
  }, []);
  useEvent('apps.changed', () => reloadApps());

  const entries = useMemo(() => dockEntries(pinned, windows, apps), [pinned, windows, apps]);
  const pinnedEntries = entries.filter((e) => e.pinned);
  const running = entries.filter((e) => !e.pinned);
  const menuEntry = menu ? entries.find((e) => e.id === menu) : undefined;

  const savePins = (next: string[]) => {
    setPinned(next);
    send('dock.set', { pinned: next });
  };

  // The menu closes on a press elsewhere, Escape, or after a while untouched.
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const onDown = (e: PointerEvent) => !(e.target as Element).closest?.('.nsh-dock-menu') && close();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close();
    const timer = window.setTimeout(close, 8000);
    window.addEventListener('pointerdown', onDown, true);
    window.addEventListener('keydown', onKey);
    window.addEventListener('blur', close);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('blur', close);
    };
  }, [menu]);

  // ---- drag to reorder pinned apps ----
  const slotAt = (x: number): number => {
    const ids = pinnedEntries.map((e) => e.id);
    for (let i = 0; i < ids.length; i++) {
      const r = pinRefs.current.get(ids[i])?.getBoundingClientRect();
      if (r && x < r.left + r.width / 2) return i;
    }
    return ids.length - 1;
  };
  const onDown = (e: RPointerEvent, entry: DockEntry, index: number) => {
    if (e.button !== 0 || !entry.pinned) return;
    setDrag({ id: entry.id, from: index, startX: e.clientX, over: index, moved: false });
  };
  const onMove = (e: RPointerEvent) => {
    if (!drag) return;
    const moved = drag.moved || Math.abs(e.clientX - drag.startX) > 6;
    if (moved && !drag.moved) (e.target as Element).setPointerCapture?.(e.pointerId);
    const over = moved ? slotAt(e.clientX) : drag.from;
    if (moved !== drag.moved || over !== drag.over) setDrag({ ...drag, moved, over });
  };
  const onUp = () => {
    if (!drag) return;
    if (drag.moved && drag.over !== drag.from) savePins(reorder(pinnedEntries.map((p) => p.id), drag.from, drag.over));
    // Let the click that follows know it was a drag.
    window.setTimeout(() => setDrag(null), 0);
  };

  // While dragging, show the pinned list in its would-be order.
  const shownPinned = drag?.moved ? reorder(pinnedEntries, drag.from, drag.over) : pinnedEntries;

  const item = (entry: DockEntry, index: number) => {
    const name = entry.app?.name ?? entry.title;
    const multi = entry.windows.length > 1;
    return (
      <button
        key={entry.id}
        ref={(el) => {
          if (entry.pinned && el) pinRefs.current.set(entry.id, el);
          else pinRefs.current.delete(entry.id);
        }}
        type="button"
        className={`dock-item ${entry.active ? 'active' : ''} ${drag?.moved && drag.id === entry.id ? 'dragging' : ''}`}
        aria-label={name}
        onPointerEnter={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setHover({ id: entry.id, x: r.left + r.width / 2 });
        }}
        onPointerLeave={() => setHover((h) => (h?.id === entry.id ? null : h))}
        onPointerDown={(e) => onDown(e, entry, index)}
        onClick={() => {
          if (drag?.moved) return;
          run(entry);
        }}
        onAuxClick={(e) => {
          if (e.button === 1 && entry.app) {
            e.preventDefault();
            send('apps.launch', { id: entry.app.id });
          }
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          setHover(null);
          setMenu(entry.id);
        }}
      >
        <AppIcon icon={entry.app?.icon ?? ''} name={name} size={ICON} />
        {entry.windows.length > 0 && <span className={`dot ${multi ? 'multi' : ''}`} />}
      </button>
    );
  };

  const hoverEntry = hover && !menu && !drag?.moved ? entries.find((e) => e.id === hover.id) : undefined;

  return (
    <div className="nsh-dock-surface" onPointerMove={onMove} onPointerUp={onUp} onPointerCancel={onUp}>
      {menuEntry ? (
        <nav ref={pill} className="dock nsh-dock nsh-dock-menu" aria-label={menuEntry.app?.name ?? menuEntry.title} role="menu">
          <AppIcon icon={menuEntry.app?.icon ?? ''} name={menuEntry.app?.name ?? menuEntry.title} size={32} />
          <b className="nsh-dock-menu-name ellipsis">{menuEntry.app?.name ?? menuEntry.title}</b>
          {menuEntry.app && (
            <button
              type="button"
              role="menuitem"
              className="nsh-chip"
              onClick={() => {
                send('apps.launch', { id: menuEntry.app!.id });
                setMenu(null);
              }}
            >
              <Icon name="plus" size={14} />
              {menuEntry.windows.length ? t('dock.newWindow') : t('lp.open')}
            </button>
          )}
          {menuEntry.app && (
            <button
              type="button"
              role="menuitem"
              className="nsh-chip"
              onClick={() => {
                savePins(togglePin(pinned, menuEntry.app!.id));
                setMenu(null);
              }}
            >
              <Icon name="pin" size={14} />
              {pinned.includes(menuEntry.app.id) ? t('dock.removeFromDock') : t('dock.addToDock')}
            </button>
          )}
          {menuEntry.windows.length > 0 && (
            <button
              type="button"
              role="menuitem"
              className="nsh-chip danger"
              onClick={() => {
                menuEntry.windows.forEach((w) => send('windows.close', { id: w.id }));
                setMenu(null);
              }}
            >
              <Icon name="close" size={14} />
              {menuEntry.windows.length > 1 ? t('nsh.dock.closeAll', { n: menuEntry.windows.length }) : t('nsh.dock.close')}
            </button>
          )}
          <button type="button" className="nsh-chip ghosty" aria-label={t('nsh.cancel')} title={t('nsh.cancel')} onClick={() => setMenu(null)}>
            <Icon name="chevronLeft" size={14} />
          </button>
        </nav>
      ) : (
        <nav ref={pill} className="dock nsh-dock" aria-label={t('dock.label')}>
          <button
            type="button"
            className="dock-item launchpad"
            aria-label={t('dock.launchpad')}
            onPointerEnter={(e) => {
              const r = e.currentTarget.getBoundingClientRect();
              setHover({ id: '#roster', x: r.left + r.width / 2 });
            }}
            onPointerLeave={() => setHover(null)}
            onClick={() => send('surface.toggle', { name: 'launcher' })}
          >
            <span className="launchpad-icon">
              <Icon name="launcher" size={22} />
            </span>
          </button>
          {(shownPinned.length > 0 || running.length > 0) && <span className="dock-sep" />}
          {shownPinned.map((e) => item(e, pinnedEntries.indexOf(e)))}
          {running.length > 0 && shownPinned.length > 0 && <span className="dock-sep" />}
          {running.map((e) => item(e, -1))}
        </nav>
      )}
      {(hoverEntry || hover?.id === '#roster') && hover && (
        <span className="nsh-dock-tag" style={{ left: hover.x }}>
          {hover.id === '#roster' ? t('dock.launchpad') : (hoverEntry?.app?.name ?? hoverEntry?.title)}
        </span>
      )}
    </div>
  );
}
