import { useEffect, useRef, useState, type MouseEvent as RMouseEvent } from 'react';
import { TransfersButton } from './Transfers';
import { UserAvatar } from '../components/UserAvatar';
import { APPS, canMultiWindow, openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon } from '../components/Icon';
import { fmtDate, fmtTime } from '../lib/time';
import { useClock, useViewport } from '../lib/hooks';
import { pressApp, useAppDrag } from '../state/appDrag';
import { useDesktopIcons } from '../state/desktopIcons';
import { useDock } from '../state/dock';
import { effectiveDock, usePrefs } from '../state/prefs';
import { useRecents } from '../state/recents';
import { useSystem } from '../state/system';
import { useWM } from '../state/windows';
import type { User } from '../api/types';
import { ClockPanel } from './ClockPanel';
import { QuickSettings } from './QuickSettings';
import { NotifyBell } from './Notifications';

type Menu = { x: number; y: number; appId?: string };

/** Bring an app forward, or minimise it if it's already in front (macOS dock). */
function activate(appId: string) {
  const wm = useWM.getState();
  const mine = wm.windows.filter((w) => w.appId === appId).sort((a, b) => b.z - a.z);
  if (!mine.length) {
    openApp(appId);
    return;
  }
  const top = mine[0];
  if (top.id === wm.focused && !top.minimized) wm.minimize(top.id);
  else wm.focus(top.id);
}

/** Shared app context menu: dock, Launchpad and desktop icons. */
export function appMenuItems(appId: string, extra: MenuItem[] = []): MenuItem[] {
  const dock = useDock.getState();
  const desk = useDesktopIcons.getState();
  const wm = useWM.getState();
  const open = wm.windows.filter((w) => w.appId === appId);
  const items: MenuItem[] = [{ label: open.length ? 'Show' : 'Open', icon: 'launcher', onClick: () => activate(appId) }];
  if (canMultiWindow(appId)) items.push({ label: 'New window', icon: 'plus', onClick: () => openApp(appId, { newWindow: true }) });
  items.push(
    dock.has(appId)
      ? { label: 'Remove from Dock', icon: 'pin', onClick: () => dock.remove(appId) }
      : { label: 'Add to Dock', icon: 'pin', onClick: () => dock.add(appId) },
  );
  items.push(
    desk.has(appId)
      ? { label: 'Remove from Desktop', icon: 'desktop', onClick: () => desk.remove(appId) }
      : { label: 'Add to Desktop', icon: 'desktop', onClick: () => desk.place(appId) },
  );
  items.push(...extra);
  if (open.length) {
    items.push({
      label: open.length > 1 ? `Close ${open.length} windows` : 'Close window',
      icon: 'close',
      danger: true,
      onClick: () => open.forEach((w) => wm.close(w.id)),
    });
  }
  return items;
}

/** Shift-click or middle-click opens another window. */
function clickApp(e: RMouseEvent, appId: string) {
  if (e.shiftKey && canMultiWindow(appId)) openApp(appId, { newWindow: true });
  else activate(appId);
}

function DockItem({ id, pinned, onMenu }: { id: string; pinned: boolean; onMenu: (e: RMouseEvent, id: string) => void }) {
  const count = useWM((s) => s.windows.filter((w) => w.appId === id).length);
  const active = useWM((s) => s.windows.some((w) => w.appId === id && w.id === s.focused && !w.minimized));
  const dragging = useAppDrag((s) => s.drag?.appId === id && s.drag.source === 'dock');
  const size = usePrefs((s) => s.dockSize);
  return (
    <button
      type="button"
      data-dock-pin={pinned ? id : undefined}
      className={`dock-item draggable ${active ? 'active' : ''} ${dragging ? 'dragging' : ''}`}
      title={`${APPS[id].title}${canMultiWindow(id) ? ' — Shift+click for a new window' : ''}`}
      aria-label={APPS[id].title}
      onPointerDown={(e) => pressApp(e, id, 'dock')}
      onClick={(e) => clickApp(e, id)}
      onAuxClick={(e) => {
        if (e.button === 1 && canMultiWindow(id)) {
          e.preventDefault();
          openApp(id, { newWindow: true });
        }
      }}
      onContextMenu={(e) => onMenu(e, id)}
    >
      <AppIcon app={APPS[id]} size={size} />
      {count > 0 && <span className={`dot ${count > 1 ? 'multi' : ''}`} />}
    </button>
  );
}

export function Shelf({ user, onLauncher, onLock, showTray = true }: { user: User; onLauncher: () => void; onLock: () => void; showTray?: boolean }) {
  const appIds = useWM((s) => [...new Set(s.windows.map((w) => w.appId))].join(','));
  const online = useSystem((s) => s.online);
  const recents = useRecents((s) => s.recents);
  const pins = useDock((s) => s.pins);
  const over = useAppDrag((s) => s.over);
  const dragging = useAppDrag((s) => !!s.drag);
  const prefs = usePrefs();
  useViewport(); // phones force a bottom dock, so follow the viewport
  const { position, autoHide } = effectiveDock(prefs);
  const now = useClock(prefs.clockSeconds ? 1000 : 15_000);
  const [qsOpen, setQsOpen] = useState(false);
  const [menu, setMenu] = useState<Menu | null>(null);
  const [revealed, setRevealed] = useState(false);
  const hideTimer = useRef(0);
  const trayRef = useRef<HTMLButtonElement>(null);
  const [clockOpen, setClockOpen] = useState(false);
  const clockRef = useRef<HTMLButtonElement>(null);

  // Auto-hide: push the pointer against the dock's screen edge to bring it back.
  useEffect(() => {
    if (!autoHide) return;
    const onMove = (e: PointerEvent) => {
      const near =
        position === 'bottom' ? e.clientY >= window.innerHeight - 4 : position === 'left' ? e.clientX <= 4 : e.clientX >= window.innerWidth - 4;
      if (near) {
        window.clearTimeout(hideTimer.current);
        setRevealed(true);
      }
    };
    window.addEventListener('pointermove', onMove);
    return () => window.removeEventListener('pointermove', onMove);
  }, [autoHide, position]);
  const hidden = autoHide && !revealed && !qsOpen && !clockOpen && !menu;

  const isAdmin = user.role === 'admin';
  const allowed = (id: string) => !!APPS[id] && !APPS[id].hidden && (isAdmin || !APPS[id].adminOnly);

  const pinned = pins.filter(allowed);
  const running = (appIds ? appIds.split(',') : []).filter(allowed);
  const runningExtra = running.filter((id) => !pinned.includes(id));
  const recentExtra = prefs.dockRecents
    ? recents.filter((id) => allowed(id) && !pinned.includes(id) && !running.includes(id)).slice(0, 3)
    : [];

  const openMenu = (e: RMouseEvent, appId: string) => {
    e.preventDefault();
    e.stopPropagation();
    setMenu({ x: e.clientX, y: e.clientY, appId });
  };

  const menuItems = (m: Menu): MenuItem[] =>
    m.appId ? appMenuItems(m.appId) : [{ label: 'Reset Dock to default', icon: 'pin', onClick: () => useDock.getState().resetDefaults() }];

  const openTrash = () => openApp('files', { title: 'Recycle Bin', props: { path: '/.recycle' }, key: 'files:recycle' });

  return (
    <footer
      className={`shelf ${hidden ? 'hidden' : ''}`}
      onPointerEnter={() => window.clearTimeout(hideTimer.current)}
      onPointerLeave={() => {
        if (!autoHide) return;
        window.clearTimeout(hideTimer.current);
        hideTimer.current = window.setTimeout(() => setRevealed(false), 700);
      }}
    >
      <nav
        className={`dock ${dragging && over?.kind === 'dock' ? 'drop-target' : ''}`}
        aria-label="Dock"
        data-drop="dock"
        onContextMenu={(e) => {
          if ((e.target as HTMLElement).closest('.dock-item')) return;
          e.preventDefault();
          setMenu({ x: e.clientX, y: e.clientY });
        }}
      >
        <button type="button" className="dock-item launchpad" title="Launchpad" aria-label="Launchpad" onClick={onLauncher}>
          <span className="launchpad-icon">
            <Icon name="launcher" size={22} />
          </span>
        </button>

        <span className="dock-sep" />

        {pinned.map((id) => (
          <DockItem key={id} id={id} pinned onMenu={openMenu} />
        ))}
        {runningExtra.map((id) => (
          <DockItem key={id} id={id} pinned={false} onMenu={openMenu} />
        ))}

        {recentExtra.length > 0 && (
          <>
            <span className="dock-sep" />
            {recentExtra.map((id) => (
              <DockItem key={id} id={id} pinned={false} onMenu={openMenu} />
            ))}
          </>
        )}

        <span className="dock-sep" />
        {isAdmin && (
          <button
            type="button"
            className={`dock-item trash ${dragging && over?.kind === 'trash' ? 'drop-over' : ''}`}
            title="Recycle Bin — drop an icon here to remove it"
            aria-label="Recycle Bin"
            data-drop="trash"
            onClick={openTrash}
          >
            <span className="trash-icon">
              <Icon name="trash" size={22} />
            </span>
          </button>
        )}
      </nav>

      {showTray && (
        <div className="tray">
          <TransfersButton className="tray-btn" />
          {isAdmin && <NotifyBell className="tray-btn" />}
          <button
            ref={trayRef}
            type="button"
            className={`tray-btn tray-status ${qsOpen ? 'open' : ''}`}
            onClick={() => setQsOpen((v) => !v)}
            aria-label="Control Center"
            title={`Control Center — Wi-Fi, network, sound${online ? '' : ' (reconnecting…)'}`}
          >
            <span className={`status-dot ${online ? 'on' : 'off'}`} />
            <Icon name="network" size={16} />
            <Icon name="sound" size={16} />
          </button>
          <button ref={clockRef} type="button" className={`tray-btn tray-clock ${clockOpen ? 'open' : ''}`} onClick={() => setClockOpen((v) => !v)} title="Clock & calendar">
            {prefs.clockDate && <span className="tray-date">{fmtDate(now, prefs, 'short')}</span>}
            {fmtTime(now, prefs)}
          </button>
          <button type="button" className="tray-btn tray-avatar" onClick={() => setQsOpen((v) => !v)} aria-label="Account and Control Center">
            <UserAvatar name={user.username} />
          </button>
        </div>
      )}
      {showTray && clockOpen && <ClockPanel anchor={clockRef} onClose={() => setClockOpen(false)} />}
      {showTray && qsOpen && <QuickSettings user={user} anchor={trayRef} onClose={() => setQsOpen(false)} onLock={onLock} />}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu)} onClose={() => setMenu(null)} />}
    </footer>
  );
}
