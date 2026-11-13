import { useState } from 'react';
import { tr, useT, type T } from '../i18n';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { relTime, useNotifications } from '../state/notifications';
import { toast } from '../state/toasts';
import { linkApi, useLink, type Carry, type Phone } from '../state/link';
import { carryLabel, openCarry, openUrlOnPhone, webUrl } from './linkActions';
import { PHONE_TILE } from './LinkSync';

// NoCap Link phone status in the Notification Center: each phone's
// connection (Nearby / Remote), battery and network, "Find my phone",
// "Send a link to phone" and recent "Open on NoCap" carries. Plus the
// phone battery in the tray.

type Look = 'mac' | 'windows';

/** A small battery: outline, fill for the level, a bolt while charging. */
export function Battery({ level, charging, size = 18 }: { level: number; charging: boolean; size?: number }) {
  const w = Math.max(0, Math.min(100, level)) / 100;
  const low = level >= 0 && level <= 15 && !charging;
  return (
    <svg className={`link-battery ${low ? 'low' : ''} ${charging ? 'charging' : ''}`} width={size} height={size * 0.56} viewBox="0 0 25 14" aria-hidden="true">
      <rect x="0.75" y="0.75" width="20.5" height="12.5" rx="3" fill="none" stroke="currentColor" strokeOpacity="0.55" strokeWidth="1.5" />
      <rect x="22.5" y="4.5" width="2" height="5" rx="1" fill="currentColor" fillOpacity="0.55" />
      <rect className="link-battery-fill" x="2.5" y="2.5" width={17 * w} height="9" rx="1.6" />
      {charging && <path d="M12.6 2.2 7.8 7.6h3.4l-1 4.2 4.8-5.4h-3.4z" fill="currentColor" stroke="var(--surface, #000)" strokeWidth="0.6" />}
    </svg>
  );
}

const ROUTE_KEY = { local: 'link.route.local', remote: 'link.route.remote' } as const;

function networkLabel(p: Phone, t: T): string {
  const n = p.state?.network;
  return n ? t(`link.net.${n}`) : '';
}

/** "Online · Nearby · 80% · Wi-Fi" as chips. */
function PhoneStatus({ p }: { p: Phone }) {
  const t = useT();
  const s = p.state;
  const route = p.route || s?.route || '';
  const net = networkLabel(p, t);
  return (
    <div className="link-status">
      <span className={`link-dot ${p.online ? 'on' : 'off'}`} />
      <span>{p.online ? t('link.online') : t('link.offline')}</span>
      {p.online && route && <span className={`link-badge ${route}`}>{t(ROUTE_KEY[route])}</span>}
      {s && s.battery >= 0 && (
        <span className="link-batt" title={s.charging ? t('link.charging') : undefined}>
          <Battery level={s.battery} charging={s.charging} size={16} />
          {s.battery}%
        </span>
      )}
      {net && (
        <span className="link-net" title={s && s.signal >= 0 ? t('link.signal', { n: s.signal }) : undefined}>
          <Icon name={s?.network === 'ethernet' ? 'ethernet' : s?.network === 'wifi' ? 'wifi' : 'network'} size={11} />
          {net}
        </span>
      )}
    </div>
  );
}

function setRinging(id: string, ringing: boolean) {
  const phones = useLink.getState().phones.map((p) =>
    p.id === id ? { ...p, state: { ...(p.state ?? { battery: -1, charging: false, network: '', signal: -1, route: '', at: '' }), ringing } } : p,
  );
  useLink.setState({ phones });
}

function PhoneRow({ p, look }: { p: Phone; look: Look }) {
  const t = useT();
  const now = useClock(60_000);
  const [busy, setBusy] = useState(false);
  const ringing = !!p.state?.ringing;
  const ring = async () => {
    setBusy(true);
    const r = await linkApi.action(p.id, { kind: ringing ? 'stop_ring' : 'ring' });
    setBusy(false);
    if (!r.ok) {
      toast('error', tr(ringing ? 'link.stopRingFailed' : 'link.ringFailed', { device: p.name }), r.error);
      return;
    }
    setRinging(p.id, !ringing);
    if (!ringing) toast('success', tr('link.ringing', { device: p.name }));
  };
  return (
    <div className={`nc-card ${look} lvl-info link-card link-phone ${p.online ? '' : 'offline'}`}>
      <AppIcon app={{ icon: p.platform === 'android' ? 'android' : 'phone', tile: PHONE_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{p.name}</b>
          {!p.online && p.last_seen && <span className="nb-time">{t('link.seen', { when: relTime(p.last_seen, now.getTime()) })}</span>}
        </div>
        <PhoneStatus p={p} />
      </div>
      <button
        type="button"
        className={`nb-btn link-ring ${ringing ? 'on' : ''}`}
        disabled={busy}
        aria-pressed={ringing}
        title={ringing ? t('link.stopRing') : t('link.findHint')}
        onClick={() => void ring()}
      >
        <Icon name={ringing ? 'stop' : 'sound'} size={13} />
        {ringing ? t('link.stopRing') : t('link.find')}
      </button>
    </div>
  );
}

/** "Open on phone" for a typed web address. */
export function SendLinkForm({ onDone, initial = '' }: { onDone?: () => void; initial?: string }) {
  const t = useT();
  const phones = useLink((s) => s.phones);
  const [url, setUrl] = useState(initial);
  const [target, setTarget] = useState('');
  const [busy, setBusy] = useState(false);
  if (!phones.length) return null;
  const phone = phones.find((p) => p.id === target) ?? phones.find((p) => p.online) ?? phones[0];
  const valid = !!webUrl(url);
  return (
    <form
      className="link-sendlink"
      onSubmit={(e) => {
        e.preventDefault();
        if (!valid) return;
        setBusy(true);
        void openUrlOnPhone(phone, url).then((ok) => {
          setBusy(false);
          if (!ok) return;
          setUrl('');
          onDone?.();
        });
      }}
    >
      <input
        autoFocus
        type="url"
        inputMode="url"
        value={url}
        placeholder={t('link.urlPlaceholder')}
        aria-label={t('link.sendLink')}
        spellCheck={false}
        autoComplete="off"
        onChange={(e) => setUrl(e.target.value)}
      />
      <div className="link-compose-row">
        {phones.length > 1 && (
          <select value={phone.id} onChange={(e) => setTarget(e.target.value)} aria-label={t('link.sendTo')}>
            {phones.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
        <span className="spacer" />
        {onDone && (
          <button type="button" className="nb-btn" onClick={onDone}>
            {t('link.cancel')}
          </button>
        )}
        <button type="submit" className="nb-btn primary" disabled={busy || !valid}>
          {t('link.openOnPhone')}
        </button>
      </div>
    </form>
  );
}

function CarryRow({ h, look }: { h: Carry; look: Look }) {
  const t = useT();
  const now = useClock(30_000);
  const isFile = !!(h.root && h.path);
  return (
    <div className={`nc-card ${look} lvl-info link-card link-carry`}>
      <Icon name={isFile ? 'file' : 'globe'} size={16} />
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{carryLabel(h)}</b>
          <span className="nb-time">{relTime(h.at, now.getTime())}</span>
        </div>
        <span className="link-from">{t('link.from', { device: h.device_name })}</span>
      </div>
      <button type="button" className="nb-btn" onClick={() => openCarry(h)}>
        {t('link.open')}
      </button>
    </div>
  );
}

/** Phones, Find my phone, Send a link, recent carries. */
export function PhonesSection({ look }: { look: Look }) {
  const t = useT();
  const phones = useLink((s) => s.phones);
  const carries = useLink((s) => s.carries);
  const [sending, setSending] = useState(false);
  if (!phones.length) return null;
  return (
    <section className="nc-group link-group link-phones">
      <div className={look === 'mac' ? 'nc-ghead' : 'nc-app'}>
        {look === 'windows' && <AppIcon app={{ icon: 'phone', tile: PHONE_TILE }} size={16} />}
        <b>{t('link.phones')}</b>
        {look === 'windows' && <span className="spacer" />}
        <button type="button" className={look === 'mac' ? 'nc-pill' : 'nc-clear'} aria-expanded={sending} onClick={() => setSending((v) => !v)}>
          {t('link.sendLink')}
        </button>
      </div>
      {sending && (
        <div className={`nc-card ${look} link-card link-compose`}>
          <SendLinkForm onDone={() => setSending(false)} />
        </div>
      )}
      {phones.map((p) => (
        <PhoneRow key={p.id} p={p} look={look} />
      ))}
      {carries.length > 0 && <div className="link-sub">{t('link.recentCarries')}</div>}
      {carries.slice(0, 3).map((h) => (
        <CarryRow key={h.id} h={h} look={look} />
      ))}
    </section>
  );
}

/** Tray / menu bar: the first online phone's battery; opens the Notification Center. */
export function PhoneTray({ className }: { className: string }) {
  const t = useT();
  const phone = useLink((s) => s.phones.find((p) => p.online && p.state && p.state.battery >= 0));
  const open = useNotifications((s) => s.centerOpen);
  if (!phone?.state) return null;
  const { battery, charging } = phone.state;
  const label = t(charging ? 'link.trayCharging' : 'link.tray', { device: phone.name, n: battery });
  return (
    <button
      type="button"
      data-notify-bell
      className={`${className} link-tray`}
      title={label}
      aria-label={label}
      onClick={() => useNotifications.getState().setCenter(!open)}
    >
      <Icon name="phone" size={13} />
      <Battery level={battery} charging={charging} size={18} />
    </button>
  );
}
