import { useRef, useState } from 'react';
import { useT } from '../i18n';
import { AccountPanel } from './AccountPanel';
import { TransfersButton } from './Transfers';
import type { User } from '../api/types';
import { APPS } from '../apps/meta';
import { Icon } from '../components/Icon';
import { UserAvatar } from '../components/UserAvatar';
import { useClock } from '../lib/hooks';
import { fmtDate, fmtTime, useTimePrefs } from '../lib/time';
import { usePrefs } from '../state/prefs';
import { useSystem } from '../state/system';
import { useWM } from '../state/windows';
import { ClockPanel } from './ClockPanel';
import { useSpotlight } from '../state/spotlight';
import { QuickSettings } from './QuickSettings';
import { NotifyBell } from './Notifications';
import { PhoneTray } from './LinkPhones';
import { useLink } from '../state/link';

/**
 * macOS-style menu bar (Settings → Windows → buttons on the Left). The clock,
 * status icons and Control Center live here instead of the dock's tray.
 */
export function MenuBar({ user, onLock }: { user: User; onLock: () => void }) {
  const tp = useTimePrefs();
  const now = useClock(tp.clockSeconds ? 1000 : 15_000);
  const focusMode = usePrefs((s) => s.focusMode);
  const online = useSystem((s) => s.online);
  // A string, so moving the front window doesn't re-render the menu bar.
  const frontTitle = useWM((s) => {
    const w = s.windows.find((x) => x.id === s.focused && !x.minimized);
    return w ? (APPS[w.appId]?.title ?? w.title) : '';
  });
  const [brandMenu, setBrandMenu] = useState(false);
  const brandRef = useRef<HTMLButtonElement>(null);
  const [ccOpen, setCcOpen] = useState(false);
  const ccRef = useRef<HTMLButtonElement>(null);
  const [clockOpen, setClockOpen] = useState(false);
  const clockRef = useRef<HTMLButtonElement>(null);
  const isAdmin = user.role === 'admin';
  const hasPhone = useLink((s) => s.phones.length > 0);
  const t = useT();


  return (
    <header className="menubar" aria-label={t('mb.label')}>
      <button
        ref={brandRef}
        type="button"
        className={`mb-brand ${brandMenu ? 'open' : ''}`}
        aria-label={t('mb.userMenu', { name: user.username })}
        aria-expanded={brandMenu}
        title={user.username}
        onClick={() => setBrandMenu((v) => !v)}
      >
        <UserAvatar name={user.username} />
      </button>
      <b className="mb-app">{frontTitle || t('mb.desktop')}</b>

      <span className="spacer" />

      <div className="mb-status">
        {focusMode && (
          <button type="button" className="mb-icon" title={t('mb.focusOn')} onClick={() => usePrefs.getState().set({ focusMode: false })}>
            <Icon name="moon" size={15} />
          </button>
        )}
        <TransfersButton className="mb-icon" />
        <PhoneTray className="mb-icon" />
        {(isAdmin || hasPhone) && <NotifyBell className="mb-icon" size={15} />}
        <button type="button" className="mb-icon" title={t('mb.search')} aria-label={t('mb.searchLabel')} onClick={() => useSpotlight.getState().toggle()}>
          <Icon name="search" size={15} />
        </button>
        <button
          ref={ccRef}
          type="button"
          className={`mb-icon mb-status-btn ${ccOpen ? 'open' : ''}`}
          title={online ? t('mb.cc') : t('mb.ccOffline')}
          aria-label={t('dock.controlCenter')}
          onClick={() => setCcOpen((v) => !v)}
        >
          <Icon name="wifi" size={15} />
          <Icon name="sound" size={15} />
          <Icon name="sliders" size={15} />
          {!online && <span className="mb-dot" />}
        </button>
        <button ref={clockRef} type="button" className={`mb-icon mb-clock ${clockOpen ? 'open' : ''}`} title={t('dock.clock')} onClick={() => setClockOpen((v) => !v)}>
          {fmtDate(now, tp, 'short')}&nbsp;&nbsp;{fmtTime(now, tp)}
        </button>
      </div>

      {brandMenu && <AccountPanel user={user} anchor={brandRef} onClose={() => setBrandMenu(false)} onLock={onLock} side="left" />}
      {clockOpen && <ClockPanel anchor={clockRef} onClose={() => setClockOpen(false)} />}
      {ccOpen && <QuickSettings user={user} anchor={ccRef} onClose={() => setCcOpen(false)} />}
    </header>
  );
}
