// The top bar (32 px, always shown): the NoCap button, the front window's
// title, then status icons, notifications and the clock. Everything that
// needs room opens the control center surface.
import { useEffect, useState } from 'react';
import { Icon } from '../../components/Icon';
import { call, send } from '../bridge';
import { fmtDate, fmtTime, useEvent, useHostValue, useMinuteClock, useShellInfo } from '../hooks';
import { useT } from '../i18n';
import type { AudioState, NetworkInfo, NotifyItem, WindowInfo } from '../protocol';
import { LogoMark } from '../ui';

const toggleCenter = () => send('surface.toggle', { name: 'center' });

function Clock() {
  // Its own component, so the minute tick re-renders only the clock.
  const now = useMinuteClock();
  return (
    <>
      {fmtDate(now)}&nbsp;&nbsp;{fmtTime(now)}
    </>
  );
}

export function Bar() {
  useShellInfo();
  const t = useT();
  const [windows] = useHostValue<WindowInfo[]>('windows.list', 'windows.changed', []);
  const [net] = useHostValue<NetworkInfo | null>('network.get', 'network.changed', null);
  const [audio] = useHostValue<AudioState | null>('audio.get', 'audio.changed', null);
  const [unread, setUnread] = useState(0);

  useEffect(() => {
    call<NotifyItem[]>('notify.list')
      .then((list) => setUnread(list.filter((n) => !n.read).length))
      .catch(() => {});
  }, []);
  useEvent('notify.unread', (p) => setUnread(Math.max(0, Number((p as { count?: number })?.count) || 0)));

  const front = windows.find((w) => w.active && !w.minimized);
  const online = net?.state === 'online';
  const netIcon = net?.kind === 'ethernet' ? 'ethernet' : 'wifi';
  const netLabel = !net
    ? t('nsh.net.checking')
    : net.state === 'offline'
      ? t('nsh.net.offline')
      : net.state === 'connecting'
        ? t('nsh.net.connecting')
        : net.name || t('nsh.net.online');
  const muted = !audio?.available || audio.muted || audio.volume === 0;

  return (
    <header className="menubar nsh-bar" aria-label={t('mb.label')}>
      <button type="button" className="mb-brand" title={t('nsh.menu')} aria-label={t('nsh.menu')} onClick={toggleCenter}>
        <LogoMark size={18} />
      </button>
      <b className="mb-app ellipsis">{front?.title || t('mb.desktop')}</b>

      <span className="spacer" />

      <div className="mb-status">
        <button type="button" className="mb-icon mb-status-btn" title={`${netLabel} · ${t('dock.controlCenter')}`} aria-label={t('dock.controlCenter')} onClick={toggleCenter}>
          <Icon name={netIcon} size={15} className={online ? '' : 'dim'} />
          {audio?.available !== false && <Icon name={muted ? 'mute' : 'sound'} size={15} />}
          {net && !online && <span className="mb-dot" />}
        </button>
        <button
          type="button"
          className="mb-icon nsh-bell"
          title={unread ? t('nc.bellUnread', { n: unread }) : t('nc.bell')}
          aria-label={unread ? t('nc.bellUnread', { n: unread }) : t('nc.bell')}
          onClick={toggleCenter}
        >
          <Icon name="bell" size={15} />
          {unread > 0 && <span className="nsh-count">{unread > 99 ? '99+' : unread}</span>}
        </button>
        <button type="button" className="mb-icon mb-clock" title={t('dock.clock')} onClick={toggleCenter}>
          <Clock />
        </button>
      </div>
    </header>
  );
}
