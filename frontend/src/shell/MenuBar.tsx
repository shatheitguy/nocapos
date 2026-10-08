import { useRef, useState } from 'react';
import { LogoMark } from '../components/Logo';
import { logout } from '../api/client';
import type { User } from '../api/types';
import { APPS, openApp } from '../apps/meta';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon } from '../components/Icon';
import { powerHost } from '../lib/hostActions';
import { useClock } from '../lib/hooks';
import { fmtDate, fmtTime, useTimePrefs } from '../lib/time';
import { usePrefs } from '../state/prefs';
import { useSystem } from '../state/system';
import { useWM } from '../state/windows';
import { ClockPanel } from './ClockPanel';
import { QuickSettings } from './QuickSettings';

/**
 * macOS-style menu bar (Settings → Windows → buttons on the Left). The clock,
 * status icons and Control Center live here instead of the dock's tray.
 */
export function MenuBar({ user, onLock, onLauncher }: { user: User; onLock: () => void; onLauncher: () => void }) {
  const tp = useTimePrefs();
  const now = useClock(tp.clockSeconds ? 1000 : 15_000);
  const focusMode = usePrefs((s) => s.focusMode);
  const online = useSystem((s) => s.online);
  const front = useWM((s) => s.windows.find((w) => w.id === s.focused && !w.minimized));
  const [brandMenu, setBrandMenu] = useState<{ x: number; y: number } | null>(null);
  const [ccOpen, setCcOpen] = useState(false);
  const ccRef = useRef<HTMLButtonElement>(null);
  const [clockOpen, setClockOpen] = useState(false);
  const clockRef = useRef<HTMLButtonElement>(null);
  const isAdmin = user.role === 'admin';

  const brandItems = (): MenuItem[] => [
    { label: 'About', icon: 'info', onClick: () => openApp('settings', { props: { section: 'about' } }) },
    { label: 'Settings…', icon: 'settings', onClick: () => openApp('settings') },
    { label: 'Launchpad', icon: 'launcher', onClick: onLauncher },
    { label: 'Lock Screen', icon: 'lock', onClick: onLock },
    ...(isAdmin
      ? [
          { label: 'Restart…', icon: 'restart' as const, onClick: () => void powerHost('reboot') },
          { label: 'Shut Down…', icon: 'power' as const, danger: true, onClick: () => void powerHost('shutdown') },
        ]
      : []),
    { label: `Sign Out ${user.username}`, icon: 'logout', onClick: () => void logout() },
  ];

  return (
    <header className="menubar" aria-label="Menu bar">
      <button
        type="button"
        className={`mb-brand ${brandMenu ? 'open' : ''}`}
        aria-label="NoCapOS menu"
        onClick={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setBrandMenu({ x: r.left, y: r.bottom + 4 });
        }}
      >
        <LogoMark size={15} />
      </button>
      <b className="mb-app">{front ? APPS[front.appId]?.title ?? front.title : 'Desktop'}</b>

      <span className="spacer" />

      <div className="mb-status">
        {focusMode && (
          <button type="button" className="mb-icon" title="Focus is on" onClick={() => usePrefs.getState().set({ focusMode: false })}>
            <Icon name="moon" size={15} />
          </button>
        )}
        <button
          ref={ccRef}
          type="button"
          className={`mb-icon mb-status-btn ${ccOpen ? 'open' : ''}`}
          title={`Control Center — Wi-Fi, network, sound${online ? '' : ' (reconnecting…)'}`}
          aria-label="Control Center"
          onClick={() => setCcOpen((v) => !v)}
        >
          <Icon name="wifi" size={15} />
          <Icon name="sound" size={15} />
          <Icon name="sliders" size={15} />
          {!online && <span className="mb-dot" />}
        </button>
        <button ref={clockRef} type="button" className={`mb-icon mb-clock ${clockOpen ? 'open' : ''}`} title="Clock & calendar" onClick={() => setClockOpen((v) => !v)}>
          {fmtDate(now, tp, 'short')}&nbsp;&nbsp;{fmtTime(now, tp)}
        </button>
      </div>

      {brandMenu && <ContextMenu x={brandMenu.x} y={brandMenu.y} items={brandItems()} onClose={() => setBrandMenu(null)} />}
      {clockOpen && <ClockPanel anchor={clockRef} onClose={() => setClockOpen(false)} />}
      {ccOpen && <QuickSettings user={user} anchor={ccRef} onClose={() => setCcOpen(false)} onLock={onLock} showActions={false} />}
    </header>
  );
}
