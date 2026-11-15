// The control center + notification center, under the bar on the right
// (380 px wide, full height). Opened by the bar's NoCap button, status icons,
// bell and clock. Escape or a click outside the panel hides it.
import { useEffect, useMemo, useRef, useState } from 'react';
import { Icon, type IconName } from '../../components/Icon';
import { call, send } from '../bridge';
import { fmtLongDate, useEvent, useHostValue, useMinuteClock, useShellInfo, useSurfaceShown } from '../hooks';
import { useT } from '../i18n';
import { groupNotifications, upsertNotification } from '../logic';
import { MiniCalendar } from '../MiniCalendar';
import { Note, NoteIcon } from '../Note';
import type { AudioState, BrightnessState, NetworkInfo, NocapApp, NotifyItem, PowerAction } from '../protocol';
import { Avatar } from '../ui';

const hide = () => send('surface.hide', { name: 'center' });
const openNocap = (app: NocapApp) => {
  send('nocapos.open', { app });
  hide();
};

/** Sends at most one call per frame while a slider is dragged; the last value always goes out. */
function useLiveSend(method: string, key: string) {
  const pending = useRef<number | null>(null);
  const frame = useRef(0);
  return (value: number) => {
    pending.current = value;
    if (frame.current) return;
    frame.current = requestAnimationFrame(() => {
      frame.current = 0;
      if (pending.current != null) send(method, { [key]: pending.current });
      pending.current = null;
    });
  };
}

function Slider({ icon, label, value, min, muted, onChange, onIcon }: {
  icon: IconName; label: string; value: number; min: number; muted?: boolean; onChange: (v: number) => void; onIcon?: () => void;
}) {
  const t = useT();
  const pct = ((value - min) / (100 - min)) * 100;
  return (
    <div className={`cc-slider ${muted ? 'muted' : ''}`}>
      <span className="cc-slider-label">{label}</span>
      <div className="cc-slider-track">
        <button
          type="button"
          className="cc-slider-icon"
          onClick={onIcon}
          disabled={!onIcon}
          aria-label={onIcon ? (muted ? t('nsh.cc.unmute') : t('nsh.cc.mute')) : label}
          title={onIcon ? (muted ? t('nsh.cc.unmute') : t('nsh.cc.mute')) : undefined}
        >
          <Icon name={icon} size={15} />
        </button>
        <input
          type="range"
          min={min}
          max={100}
          step={1}
          value={value}
          aria-label={label}
          style={{ backgroundImage: `linear-gradient(90deg, var(--accent) ${pct}%, transparent ${pct}%)` }}
          onChange={(e) => onChange(Number(e.target.value))}
        />
        <span className="cc-slider-value">{muted ? t('qs.off') : `${value}%`}</span>
      </div>
    </div>
  );
}

function Tile({ icon, title, sub, on, onClick }: { icon: IconName; title: string; sub: string; on: boolean; onClick: () => void }) {
  return (
    <div className={`cc-tile ${on ? 'on' : ''}`}>
      <button type="button" className="cc-tile-main" onClick={onClick} aria-pressed={on}>
        <span className="cc-tile-icon">
          <Icon name={icon} size={17} />
        </span>
        <span className="cc-tile-text">
          <b>{title}</b>
          <span>{sub}</span>
        </span>
      </button>
    </div>
  );
}

const POWER: { action: PowerAction; icon: IconName; label: string; confirm?: string; ok?: string }[] = [
  { action: 'lock', icon: 'lock', label: 'nsh.power.lock' },
  { action: 'logout', icon: 'logout', label: 'nsh.power.logout', confirm: 'nsh.power.logoutAsk', ok: 'nsh.power.logoutOk' },
  { action: 'reboot', icon: 'restart', label: 'nsh.power.restart', confirm: 'nsh.power.restartAsk', ok: 'nsh.power.restartOk' },
  { action: 'poweroff', icon: 'power', label: 'nsh.power.off', confirm: 'nsh.power.offAsk', ok: 'nsh.power.offOk' },
];

/** Lock, log out, restart, shut down; the last three ask first, right in the row. */
export function PowerRow({ resetKey }: { resetKey: number }) {
  const t = useT();
  const [asking, setAsking] = useState<PowerAction | null>(null);
  useEffect(() => setAsking(null), [resetKey]);
  const ask = POWER.find((p) => p.action === asking);
  const run = (action: PowerAction) => {
    setAsking(null);
    hide();
    send('power.action', { action });
  };
  if (ask?.confirm) {
    return (
      <div className="nsh-power ask" role="alertdialog" aria-label={t(ask.confirm)}>
        <span className={`nsh-power-ico ${ask.action === 'lock' ? '' : 'danger'}`}>
          <Icon name={ask.icon} size={16} />
        </span>
        <b className="nsh-power-q">{t(ask.confirm)}</b>
        <button type="button" className="ghost small" onClick={() => setAsking(null)} autoFocus>
          {t('nsh.cancel')}
        </button>
        <button type="button" className="small danger-solid" onClick={() => run(ask.action)}>
          {t(ask.ok ?? ask.label)}
        </button>
      </div>
    );
  }
  return (
    <div className="nsh-power">
      {POWER.map((p) => (
        <button
          key={p.action}
          type="button"
          className="nsh-power-btn"
          title={t(p.label)}
          onClick={() => (p.confirm ? setAsking(p.action) : run(p.action))}
        >
          <span className={`nsh-power-ico ${p.action === 'poweroff' || p.action === 'reboot' ? 'danger' : ''}`}>
            <Icon name={p.icon} size={16} />
          </span>
          <span>{t(p.label)}</span>
        </button>
      ))}
    </div>
  );
}

type Tab = 'controls' | 'notes';

export function Center() {
  const info = useShellInfo();
  const t = useT();
  const now = useMinuteClock();
  const { visible, shown } = useSurfaceShown('center');
  const [audio, setAudio] = useHostValue<AudioState | null>('audio.get', 'audio.changed', null);
  const [bright, setBright] = useHostValue<BrightnessState | null>('brightness.get', 'brightness.changed', null);
  const [net, , reloadNet] = useHostValue<NetworkInfo | null>('network.get', 'network.changed', null);
  const [notes, setNotes] = useState<NotifyItem[]>([]);
  const [tab, setTab] = useState<Tab>('controls');
  const panel = useRef<HTMLDivElement>(null);
  const sendVolume = useLiveSend('audio.set', 'volume');
  const sendBright = useLiveSend('brightness.set', 'percent');

  // Fresh data every time the panel opens; it starts on notifications when there are unread ones.
  useEffect(() => {
    if (!visible) return;
    reloadNet();
    call<NotifyItem[]>('notify.list')
      .then((list) => {
        setNotes(list);
        setTab(list.some((n) => !n.read) ? 'notes' : 'controls');
      })
      .catch(() => {});
    panel.current?.focus();
  }, [visible, shown]);

  useEvent('notify.added', (p) => setNotes((list) => upsertNotification(list, p as NotifyItem)));
  useEvent('notify.closed', (p) => {
    const id = (p as { id?: unknown })?.id;
    setNotes((list) => list.filter((n) => n.id !== id));
  });

  // Seen: mark everything read while the notifications are on screen.
  const unread = notes.filter((n) => !n.read).length;
  useEffect(() => {
    if (!visible || tab !== 'notes' || !unread) return;
    const timer = window.setTimeout(() => {
      send('notify.read');
      setNotes((list) => list.map((n) => (n.read ? n : { ...n, read: true })));
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [visible, tab, unread]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && hide();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const groups = useMemo(() => groupNotifications(notes), [notes]);
  const muted = !!audio && (audio.muted || audio.volume === 0);
  const volume = audio ? Math.min(100, audio.volume) : 0;

  const setVolume = (v: number) => {
    if (!audio) return;
    setAudio({ ...audio, volume: v, muted: v === 0 ? audio.muted : false });
    sendVolume(v);
    if (audio.muted && v > 0) send('audio.set', { muted: false });
  };
  const toggleMute = () => {
    if (!audio) return;
    setAudio({ ...audio, muted: !audio.muted });
    send('audio.set', { muted: !audio.muted });
  };
  const setBrightness = (v: number) => {
    if (!bright) return;
    setBright({ ...bright, percent: v });
    sendBright(v);
  };
  const dismiss = (n: NotifyItem) => {
    setNotes((list) => list.filter((x) => x.id !== n.id));
    send('notify.dismiss', { id: n.id });
  };
  const invoke = (n: NotifyItem, key: string) => {
    send('notify.invoke', { id: n.id, key });
    hide();
  };
  const open = (n: NotifyItem) => {
    if (n.actions.some((a) => a.key === 'default')) invoke(n, 'default');
  };

  const netSub = !net
    ? t('qs.checking')
    : net.state === 'offline'
      ? t('nsh.net.offline')
      : net.state === 'connecting'
        ? t('nsh.net.connecting')
        : net.name || (net.kind === 'ethernet' ? t('nsh.net.wired') : t('nsh.net.online'));
  const userName = info?.realName || info?.user || '';

  return (
    <div className="nsh-center-surface" onPointerDown={(e) => e.target === e.currentTarget && hide()}>
      <div ref={panel} className="nsh-center" role="dialog" aria-label={t('qs.label')} tabIndex={-1}>
        <div className="nsh-center-head">
          <Avatar name={userName || '?'} size={38} />
          <div className="nsh-center-who">
            <b className="ellipsis">{userName || t('nsh.loading')}</b>
            <span className="muted small">{fmtLongDate(now)}</span>
          </div>
          <button type="button" className="icon-btn ghost nsh-round" title={t('nsh.about')} aria-label={t('nsh.about')} onClick={() => openNocap('home')}>
            <Icon name="info" size={16} />
          </button>
        </div>

        <div className="segmented nsh-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === 'controls'} className={tab === 'controls' ? 'on' : ''} onClick={() => setTab('controls')}>
            {t('nsh.tab.controls')}
          </button>
          <button type="button" role="tab" aria-selected={tab === 'notes'} className={tab === 'notes' ? 'on' : ''} onClick={() => setTab('notes')}>
            {t('nc.title')}
            {unread > 0 && <span className="nsh-count inline">{unread}</span>}
          </button>
        </div>

        {tab === 'controls' ? (
          <div className="nsh-center-body">
            <div className="nsh-card">
              <MiniCalendar now={now} />
            </div>
            <div className="cc-grid">
              <Tile
                icon={net?.kind === 'ethernet' ? 'ethernet' : 'wifi'}
                title={t('nsh.net.title')}
                sub={netSub}
                on={net?.state === 'online'}
                onClick={() => openNocap('settings')}
              />
              <Tile
                icon={muted ? 'mute' : 'sound'}
                title={t('qs.sound')}
                sub={!audio ? t('qs.checking') : !audio.available ? t('qs.na') : muted ? t('nsh.cc.muted') : `${volume}%`}
                on={!!audio?.available && !muted}
                onClick={toggleMute}
              />
            </div>
            <div className="cc-sliders">
              {audio?.available && (
                <Slider icon={muted ? 'mute' : 'sound'} label={t('nsh.cc.volume')} value={muted ? 0 : volume} min={0} muted={muted} onChange={setVolume} onIcon={toggleMute} />
              )}
              {bright?.available && (
                <Slider icon={bright.percent < 50 ? 'moon' : 'sun'} label={t('qs.brightness')} value={bright.percent} min={1} onChange={setBrightness} />
              )}
              {!audio?.available && !bright?.available && <span className="muted small">{t('nsh.cc.noSliders')}</span>}
            </div>
            <div className="cc-actions">
              <button type="button" className="ghost" onClick={() => openNocap('settings')}>
                <Icon name="settings" size={16} />
                {t('nsh.open.settings')}
              </button>
              <button type="button" className="ghost" onClick={() => openNocap('appstore')}>
                <Icon name="store" size={16} />
                {t('nsh.open.store')}
              </button>
              <button type="button" className="ghost" onClick={() => openNocap('files')}>
                <Icon name="files" size={16} />
                {t('nsh.open.files')}
              </button>
              <button type="button" className="ghost" onClick={() => openNocap('home')}>
                <Icon name="home" size={16} />
                {t('nsh.open.home')}
              </button>
            </div>
          </div>
        ) : (
          <div className="nsh-center-body notes">
            <div className="nsh-notes-head">
              <span className="muted small">{notes.length ? t('nsh.note.count', { n: notes.length }) : ''}</span>
              {notes.length > 0 && (
                <button type="button" className="nc-clear nsh-pill" onClick={() => send('notify.clear')}>
                  {t('nc.clearAll')}
                </button>
              )}
            </div>
            <div className="nsh-notes" role="list">
              {groups.length === 0 && (
                <div className="nsh-empty">
                  <Icon name="bell" size={28} />
                  <span>{t('nc.empty')}</span>
                </div>
              )}
              {groups.map((g) => (
                <section key={g.key} className="nsh-group" aria-label={g.source === 'nocapos' ? 'NoCapOS' : g.app}>
                  <div className="nsh-group-head">
                    <NoteIcon item={g} size={18} />
                    <b className="ellipsis">{g.source === 'nocapos' ? 'NoCapOS' : g.app}</b>
                    <button
                      type="button"
                      className="nsh-group-x"
                      title={t('nc.clearFrom', { name: g.source === 'nocapos' ? 'NoCapOS' : g.app })}
                      aria-label={t('nc.clearFrom', { name: g.source === 'nocapos' ? 'NoCapOS' : g.app })}
                      onClick={() => g.items.forEach(dismiss)}
                    >
                      <Icon name="close" size={12} />
                    </button>
                  </div>
                  {g.items.map((n) => (
                    <Note key={n.id} item={n} now={now.getTime()} onOpen={() => open(n)} onDismiss={() => dismiss(n)} onAction={(k) => invoke(n, k)} />
                  ))}
                </section>
              ))}
            </div>
          </div>
        )}

        <PowerRow resetKey={shown} />
      </div>
    </div>
  );
}
