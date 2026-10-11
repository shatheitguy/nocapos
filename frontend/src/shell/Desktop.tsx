import { useCallback, useEffect, useRef, useState } from 'react';
import { useShallow } from 'zustand/react/shallow';
import { api } from '../api/client';
import { connect, disconnect, onSocketStatus, subscribe } from '../api/socket';
import type { Snapshot, SystemInfo, User } from '../api/types';
import { openApp } from '../apps/meta';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { useDocumentVisible, useViewport } from '../lib/hooks';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { usePrefs } from '../state/prefs';
import { useDesktopIcons } from '../state/desktopIcons';
import { useFolders } from '../state/folders';
import { useDock } from '../state/dock';
import { useSystem } from '../state/system';
import { useWM } from '../state/windows';
import { useWidgets } from '../state/widgets';
import { ConfirmHost } from '../components/ConfirmHost';
import { hasMenuBar } from '../state/prefs';
import { DesktopIcons } from './DesktopIcons';
import { DragGhost } from './DragGhost';
import { HomeHeader } from './HomeHeader';
import { watchStoreApps } from '../state/storeApps';
import { watchNotifications } from '../state/notifications';
import { NotificationLayer } from './Notifications';
import { UpdateReady } from './UpdateReady';
import { Launcher } from './Launcher';
import { Spotlight } from './Spotlight';
import { useSpotlight } from '../state/spotlight';
import { MenuBar } from './MenuBar';
import { PowerOverlay } from './PowerOverlay';
import { Screensaver } from './Screensaver';
import { Shelf } from './Shelf';
import { Toasts } from './Toasts';
import { WidgetLayer } from './WidgetLayer';
import { Window } from './Window';

export function Desktop({ user, onLock }: { user: User; onLock: () => void }) {
  // Ids only: each Window subscribes to its own state, so a drag doesn't
  // re-render the whole desktop.
  const windowIds = useWM(useShallow((s) => s.windows.map((w) => w.id)));
  const widgets = usePrefs((s) => s.widgets);
  const lockTimeout = usePrefs((s) => s.lockTimeout);
  const screensaver = usePrefs((s) => s.screensaver);
  const saverDelay = usePrefs((s) => s.saverDelay);
  const vp = useViewport();
  const compact = vp.w < 640;
  const menuBar = usePrefs((s) => hasMenuBar(s)) && !compact;
  const [launcher, setLauncher] = useState(false);
  const [saverOn, setSaverOn] = useState(false);
  const [deskMenu, setDeskMenu] = useState<{ x: number; y: number } | null>(null);
  const isAdmin = user.role === 'admin';
  const glass = usePrefs((s) => s.uiTheme === 'glass');

  // Session lifetime: restore layouts, open the socket, stream metrics.
  useEffect(() => {
    useWM.getState().restoreLayout(user.id);
    useWidgets.getState().restore(user.id);
    useDock.getState().restore(user.id);
    useDesktopIcons.getState().restore(user.id);
    useFolders.getState().restore(user.id);
    const sys = useSystem.getState();
    void api<SystemInfo>('/api/v1/system/info').then((r) => r.ok && sys.setInfo(r.data));
    void api<Snapshot>('/api/v1/system/metrics').then((r) => r.ok && sys.push(r.data));
    void connect();
    const offStatus = onSocketStatus(sys.setOnline);
    return () => {
      offStatus();
      disconnect();
      useWM.getState().reset();
      useWidgets.getState()._reset();
      useDock.getState()._reset();
      useDesktopIcons.getState()._reset();
      useFolders.getState()._reset();
      useSpotlight.getState().set(false);
    };
  }, [user.id]);

  // Live metrics only while the tab is visible; nothing on screen needs them otherwise.
  const visible = useDocumentVisible();
  useEffect(() => {
    if (!visible) return;
    const sys = useSystem.getState();
    return subscribe('system.metrics', (d) => sys.push(d as Snapshot));
  }, [visible, user.id]);

  // System notifications (admins): updates, app problems, disk health, failed backups.
  useEffect(() => (isAdmin ? watchNotifications() : undefined), [isAdmin, user.id]);

  // Installed App Store apps for the home screen and Launchpad.
  useEffect(() => (isAdmin ? watchStoreApps() : undefined), [isAdmin]);

  // Keyboard: Ctrl+Space opens universal search (the OS keeps the Win/Meta key).
  const toggleLauncher = useCallback(() => setLauncher((v) => !v), []);
  const spotlight = useSpotlight((s) => s.open);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.code === 'Space') {
        e.preventDefault();
        setLauncher(false);
        useSpotlight.getState().toggle();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [toggleLauncher]);

  // Idle tracking for the screensaver and auto-lock. Any input resets the timer.
  const onLockRef = useRef(onLock);
  onLockRef.current = onLock;
  useEffect(() => {
    const saverMs = screensaver !== 'off' ? saverDelay * 60_000 : 0;
    const lockMs = lockTimeout > 0 ? lockTimeout * 60_000 : 0;
    if (!saverMs && !lockMs) return;

    let last = Date.now();
    const bump = () => {
      last = Date.now();
      setSaverOn((on) => (on ? false : on));
    };
    const evs: (keyof WindowEventMap)[] = ['pointermove', 'pointerdown', 'keydown', 'wheel', 'touchstart'];
    evs.forEach((e) => window.addEventListener(e, bump, { passive: true }));

    const tick = window.setInterval(() => {
      const idle = Date.now() - last;
      if (lockMs && idle >= lockMs) {
        onLockRef.current();
      } else if (saverMs && idle >= saverMs) {
        setSaverOn(true);
      }
    }, 2000);

    return () => {
      evs.forEach((e) => window.removeEventListener(e, bump));
      window.clearInterval(tick);
    };
  }, [lockTimeout, screensaver, saverDelay]);

  const openWidgetEdit = () => useWidgets.getState().setEditing(true);

  const deskMenuItems = (): MenuItem[] => {
    const prefs = usePrefs.getState();
    const icons = useDesktopIcons.getState();
    return [
      { label: 'Add apps to Desktop…', icon: 'plus', onClick: () => setLauncher(true) },
      ...(icons.icons.length ? [{ label: 'Clean Up icons', icon: 'launcher' as const, onClick: icons.cleanUp }] : []),
      { label: 'Edit widgets', icon: 'pencil', onClick: openWidgetEdit },
      {
        label: widgets ? 'Hide widgets' : 'Show widgets',
        icon: 'eye',
        onClick: () => prefs.set({ widgets: !widgets }),
      },
      { label: 'Open Launchpad', icon: 'launcher', onClick: () => setLauncher(true) },
      { label: 'Change wallpaper…', icon: 'image', onClick: () => openApp('settings', { props: { section: 'wallpaper' } }) },
      { label: 'Desktop & Dock settings…', icon: 'settings', onClick: () => openApp('settings', { props: { section: 'desktop' } }) },
    ];
  };

  return (
    <div className="desktop">
      {saverOn && <Screensaver kind={screensaver} onWake={() => setSaverOn(false)} />}
      {/* macOS-style desktop: wallpaper, the icons you put there, and floating widgets. */}
      <div
        className="desktop-surface clean"
        data-drop="desktop"
        onPointerDown={() => useWM.setState({ focused: null })}
        onContextMenu={(e) => {
          if (!compact && e.target === e.currentTarget) {
            e.preventDefault();
            setDeskMenu({ x: e.clientX, y: e.clientY });
          }
        }}
      >
        {widgets && compact && (
          <ErrorBoundary title="Widgets" fallback="hidden">
            <WidgetLayer username={user.username} compact />
          </ErrorBoundary>
        )}
      </div>

      {glass && !compact && !launcher && (
        <ErrorBoundary title="Home header" fallback="hidden">
          <HomeHeader username={user.username} isAdmin={isAdmin} />
        </ErrorBoundary>
      )}
      {!compact && (
        <ErrorBoundary title="Desktop icons" fallback="hidden">
          <DesktopIcons isAdmin={isAdmin} />
        </ErrorBoundary>
      )}
      {widgets && !compact && (
        <ErrorBoundary title="Widgets" fallback="hidden">
          <WidgetLayer username={user.username} compact={false} />
        </ErrorBoundary>
      )}

      <div className="window-layer">
        {windowIds.map((id) => (
          <Window key={id} id={id} compact={compact} />
        ))}
      </div>

      {menuBar && (
        <ErrorBoundary title="Menu bar" fallback="hidden">
          <MenuBar user={user} onLock={onLock} onLauncher={toggleLauncher} />
        </ErrorBoundary>
      )}
      <ErrorBoundary title="Dock" fallback="hidden">
        <Shelf user={user} onLauncher={toggleLauncher} onLock={onLock} showTray={!menuBar} />
      </ErrorBoundary>
      {launcher && (
        <ErrorBoundary title="Launchpad" fallback="hidden">
          <Launcher isAdmin={isAdmin} onClose={() => setLauncher(false)} />
        </ErrorBoundary>
      )}
      {spotlight && (
        <ErrorBoundary title="Search" fallback="hidden">
          <Spotlight isAdmin={isAdmin} onLock={onLock} />
        </ErrorBoundary>
      )}
      {deskMenu && <ContextMenu x={deskMenu.x} y={deskMenu.y} items={deskMenuItems()} onClose={() => setDeskMenu(null)} />}
      <DragGhost />
      <ConfirmHost />
      <PowerOverlay />
      {isAdmin && (
        <ErrorBoundary title="Notifications" fallback="hidden">
          <NotificationLayer />
        </ErrorBoundary>
      )}
      <ErrorBoundary title="Update notice" fallback="hidden">
        <UpdateReady />
      </ErrorBoundary>
      <ErrorBoundary title="Toasts" fallback="hidden">
        <Toasts />
      </ErrorBoundary>
    </div>
  );
}
