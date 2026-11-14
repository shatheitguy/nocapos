import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import { createPortal } from 'react-dom';
import QRCode from 'qrcode';
import { getUser } from '../api/client';
import { devicesApi, type DeviceCap, type DeviceLimits, type LinkedDevice, type PairCode, type PairInfo, type UserDeviceLimit } from '../api/devices';
import { Icon, type IconName } from '../components/Icon';
import { tr, useT, type T } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Section as Group } from './Personalize';
import { copyWithToast, missing } from '../lib/settingsKit';
import { LinkAddressesSettings } from './SettingsLinkAddresses';
import { ClipboardSettings } from './SettingsClipboard';
import { ReceivedSettings } from './SettingsReceived';

// Settings → Linked Devices: a meter of how many devices you've linked out
// of your limit, a card per device (model, app, battery, address, NoCap Link
// features), linking a new one with a QR code or 6-digit code, and, for
// administrators, everyone's devices by person with their limits.

const APP_URL = 'https://github.com/shatheitguy/nocapos/releases/latest/download/nocap-os.apk';

const PLATFORM_ICON: Record<LinkedDevice['platform'], IconName> = { android: 'android', ios: 'phone', other: 'phone' };
const CAPS: { id: DeviceCap; icon: IconName }[] = [
  { id: 'notifications', icon: 'bell' },
  { id: 'clipboard', icon: 'clipboard' },
  { id: 'files', icon: 'folder' },
];
/** Choices offered for a limit (0 = no limit); the current value is added when it isn't one of them. */
const LIMIT_STEPS = [1, 2, 3, 4, 5, 6, 8, 10, 15, 20, 25, 50, 100];

function ago(t: T, iso: string) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return t('ld.ago.now');
  if (s < 3600) return t('ld.ago.min', { n: Math.floor(s / 60) });
  if (s < 86400) return t('ld.ago.hour', { n: Math.floor(s / 3600) });
  return t('ld.ago.day', { n: Math.floor(s / 86400) });
}

const fmtDate = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });

const isFull = (used: number, limit: number) => limit > 0 && used >= limit;

export function DevicesSettings() {
  const t = useT();
  const me = getUser();
  const isAdmin = me?.role === 'admin';
  const [info, setInfo] = useState<PairInfo | null>(null);
  const [list, setList] = useState<LinkedDevice[] | null>(null);
  const [limits, setLimits] = useState<DeviceLimits | null>(null);
  // The server couldn't say (older server or a failure): count what's listed, no limit shown.
  const [limitsGone, setLimitsGone] = useState(false);
  const [infoGone, setInfoGone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [linking, setLinking] = useState(false);

  const load = useCallback(() => {
    void devicesApi.list().then((r) => {
      if (r.ok) {
        setList(r.data);
        setError(null);
      } else setError(r.error ?? tr('ld.loadFail'));
    });
    void devicesApi.limits().then((r) => {
      if (r.ok && !missing(r)) {
        setLimits(r.data);
        setLimitsGone(false);
      } else setLimitsGone(true);
    });
  }, []);

  useEffect(() => {
    void devicesApi.info().then((r) => (r.ok && !missing(r) ? setInfo(r.data) : setInfoGone(true)));
    load();
    // Keep "online" and "last seen" fresh while the page is open.
    const id = window.setInterval(load, 30_000);
    return () => window.clearInterval(id);
  }, [load]);

  const mine = (list ?? []).filter((d) => !d.user_id || d.user_id === me?.id);

  const unlink = async (d: LinkedDevice) => {
    const ok = await confirmDialog({ title: tr('ld.unlinkTitle', { name: d.name }), message: tr('ld.unlinkMsg'), confirmLabel: tr('ld.unlink'), danger: true });
    if (!ok) return false;
    const r = await devicesApi.unlink(d.id);
    if (!r.ok && r.status !== 404) {
      toast('error', tr('ld.unlinkFail'), r.error);
      return false;
    }
    toast('success', tr('ld.unlinked', { name: d.name }));
    load();
    return true;
  };

  const rename = async (d: LinkedDevice, name: string) => {
    const r = await devicesApi.rename(d.id, name);
    if (!r.ok) {
      toast('error', tr('ld.renameFail'), r.error);
      return false;
    }
    toast('success', tr('ld.renamed', { name: r.data.name }));
    load();
    return true;
  };

  const copyId = () => info && void copyWithToast(info.nocap_id, tr('ld.idCopied'));

  const used = limits?.used ?? mine.length;
  const limit = limits?.limit ?? 0;
  const full = isFull(used, limit);
  const ready = !!limits || limitsGone;

  let status: string;
  if (!limits) status = '';
  else if (limit === 0) status = t('ld.noLimitHint');
  else if (used > limit) status = t('ld.over', { n: used - limit });
  else if (full) status = t('ld.full');
  else status = t('ld.roomFor', { n: limit - used });

  return (
    <div className="stack settings-page ld">
      <section className={`ucard ld-hero ${full ? 'is-full' : ''}`}>
        <Meter used={used} limit={limit} ready={ready} />
        <div className="ld-hero-body">
          <span className="ld-eyebrow">
            <Icon name="phone" size={12} /> {t('ld.eyebrow')}
          </span>
          <h3 className="ld-hero-title">{ready ? (limit > 0 ? t('ld.linkedOf', { used, limit }) : t('ld.linkedNoLimit', { used })) : '…'}</h3>
          {status && <p className={`ld-hero-status ${full ? 'warn' : ''}`}>{status}</p>}
          <div className="ld-hero-id">
            <span className="ld-hero-id-label">{t('ld.nocapId')}</span>
            <code>{info?.nocap_id ?? (infoGone ? t('set.notAvailable') : '…')}</code>
            {info && (
              <button type="button" className="icon-btn neutral" title={t('ld.copyId')} aria-label={t('ld.copyId')} onClick={copyId}>
                <Icon name="copy" size={14} />
              </button>
            )}
          </div>
          <span className="ld-hero-hint">{t('ld.nocapIdHint')}</span>
          <div className="ld-hero-actions">
            <button type="button" className="pill ld-cta" onClick={() => setLinking(true)}>
              <Icon name={full ? 'trash' : 'plus'} size={14} /> {full ? t('ld.makeRoom') : t('ld.link')}
            </button>
          </div>
        </div>
      </section>

      <Group title={t('ld.yours')} hint={t('ld.yoursHint')}>
        {error ? (
          <div className="settings-row full">
            <p className="error">{error}</p>
          </div>
        ) : !list ? (
          <div className="settings-row full">
            <p className="muted small">{t('ld.loading')}</p>
          </div>
        ) : mine.length === 0 ? (
          <div className="settings-row full ld-empty">
            <span className="ld-empty-icon">
              <Icon name="phone" size={26} />
            </span>
            <p className="muted small">{t('ld.empty')}</p>
          </div>
        ) : (
          <div className="ld-grid">
            {mine.map((d) => (
              <DeviceCard key={d.id} d={d} onUnlink={() => void unlink(d)} onRename={(n) => rename(d, n)} />
            ))}
          </div>
        )}
      </Group>

      {isAdmin && limits?.users && (
        <AdminView limits={limits} list={list ?? []} meId={me?.id ?? ''} onChanged={load} onUnlink={(d) => void unlink(d)} onRename={rename} />
      )}
      <ClipboardSettings />
      <ReceivedSettings />
      <LinkAddressesSettings />

      {linking && (
        <LinkSheet
          known={new Set((list ?? []).map((d) => d.id))}
          mine={mine}
          used={used}
          limit={limit}
          onUnlink={unlink}
          onChanged={load}
          onClose={() => {
            setLinking(false);
            load();
          }}
        />
      )}
    </div>
  );
}

/** The "3 / 5 linked" ring. */
function Meter({ used, limit, ready, size = 132 }: { used: number; limit: number; ready: boolean; size?: number }) {
  const t = useT();
  const grad = `ld-meter-grad-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`;
  const r = 52;
  const c = 2 * Math.PI * r;
  const pct = limit > 0 ? Math.min(1, used / limit) : used > 0 ? 1 : 0;
  const level = limit > 0 && used >= limit ? 'hot' : limit > 0 && used / limit >= 0.8 ? 'warm' : '';
  const label = limit > 0 ? t('ld.count', { used, limit }) : t('ld.countNoLimit', { used });
  return (
    <div className={`ld-meter ${level} ${limit === 0 ? 'unlimited' : ''}`} style={{ width: size, height: size }} role="img" aria-label={label}>
      <svg viewBox="0 0 120 120" width={size} height={size} aria-hidden="true">
        <defs>
          <linearGradient id={grad} x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" style={{ stopColor: 'var(--accent)' }} />
            <stop offset="100%" style={{ stopColor: 'var(--violet)' }} />
          </linearGradient>
        </defs>
        <circle className="ld-meter-track" cx="60" cy="60" r={r} />
        {limit > 0 &&
          limit <= 20 &&
          Array.from({ length: limit }, (_, i) => {
            const a = (i / limit) * 2 * Math.PI - Math.PI / 2;
            return <circle key={i} className={`ld-meter-tick ${i < used ? 'on' : ''}`} cx={60 + 60 * Math.cos(a) * 0.97} cy={60 + 60 * Math.sin(a) * 0.97} r="1.6" />;
          })}
        <circle
          className="ld-meter-fill"
          cx="60"
          cy="60"
          r={r}
          strokeDasharray={`${ready ? pct * c : 0} ${c}`}
          transform="rotate(-90 60 60)"
          stroke={level ? undefined : `url(#${grad})`}
        />
      </svg>
      <div className="ld-meter-text">
        <b>
          {used}
          {limit > 0 && <small> / {limit}</small>}
        </b>
        <span>{limit > 0 ? t('ld.meterLinked') : t('ld.meterNoLimit')}</span>
      </div>
    </div>
  );
}

function DeviceCard({ d, owner, onUnlink, onRename }: { d: LinkedDevice; owner?: string; onUnlink: () => void; onRename: (name: string) => Promise<boolean> }) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(d.name);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const n = name.trim();
    if (!n || n === d.name) return setEditing(false);
    setBusy(true);
    const ok = await onRename(n);
    setBusy(false);
    if (ok) setEditing(false);
  };

  const platform = t(`ld.platform.${d.platform}`);
  const model = [d.model || platform, d.os_version].filter(Boolean).join(' · ');
  const battery = d.battery;
  const caps = new Set(d.capabilities ?? []);
  const reported = (d.capabilities ?? []).length > 0 || !!d.model;

  return (
    <article className={`ld-card ${d.online ? 'online' : ''}`}>
      <header className="ld-card-head">
        <span className="ld-card-icon">
          <Icon name={PLATFORM_ICON[d.platform] ?? 'phone'} size={20} />
          <i className="ld-dot" aria-hidden="true" />
        </span>
        <div className="ld-card-title">
          {editing ? (
            <form className="ld-rename" onSubmit={(e) => void submit(e)}>
              <input
                autoFocus
                value={name}
                maxLength={64}
                aria-label={t('ld.nameLabel')}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => e.key === 'Escape' && (setName(d.name), setEditing(false))}
              />
              <button type="submit" className="small" disabled={busy}>
                {t('ld.save')}
              </button>
              <button type="button" className="ghost small" onClick={() => (setName(d.name), setEditing(false))}>
                {t('ld.cancel')}
              </button>
            </form>
          ) : (
            <>
              <b title={d.name}>{d.name}</b>
              <span className="ld-card-model">{model}</span>
            </>
          )}
        </div>
        <span className={`ld-status ${d.online ? 'on' : ''}`}>
          {d.online ? t('ld.online') : d.last_seen_at ? t('ld.seen', { ago: ago(t, d.last_seen_at) }) : t('ld.neverSeen')}
        </span>
      </header>

      {owner && <span className="ld-card-owner">{owner}</span>}

      <dl className="ld-facts">
        <div>
          <dt>{t('ld.factApp')}</dt>
          <dd>{d.app_version || t('ld.unknown')}</dd>
        </div>
        <div>
          <dt>{t('ld.factBattery')}</dt>
          <dd>
            {battery != null ? (
              <span className={`ld-batt ${battery <= 15 ? 'low' : battery <= 35 ? 'mid' : ''}`}>
                <span className="ld-batt-cell" aria-hidden="true">
                  <span style={{ width: `${battery}%` }} />
                </span>
                {battery}%
              </span>
            ) : (
              t('ld.unknown')
            )}
          </dd>
        </div>
        <div>
          <dt>{t('ld.factIp')}</dt>
          <dd className="ld-mono">{d.last_ip || t('ld.unknown')}</dd>
        </div>
        <div>
          <dt>{t('ld.factLinked')}</dt>
          <dd>{fmtDate(d.created_at)}</dd>
        </div>
      </dl>

      <div className="ld-caps">
        {reported ? (
          CAPS.map((c) => {
            const on = caps.has(c.id);
            const label = t(`ld.cap.${c.id}`);
            return (
              <span key={c.id} className={`ld-cap ${on ? 'on' : ''}`} title={on ? t('ld.capOn', { name: label }) : t('ld.capOff', { name: label })}>
                <Icon name={c.icon} size={12} /> {label}
              </span>
            );
          })
        ) : (
          <span className="muted small">{t('ld.capsPending')}</span>
        )}
      </div>

      {!editing && (
        <footer className="ld-card-actions">
          <button type="button" className="ghost small" onClick={() => setEditing(true)}>
            <Icon name="pencil" size={13} /> {t('ld.rename')}
          </button>
          <button type="button" className="ghost small danger" onClick={onUnlink}>
            <Icon name="trash" size={13} /> {t('ld.unlink')}
          </button>
        </footer>
      )}
    </article>
  );
}

function LimitSelect({ value, fallback, max, label, onChange }: { value: number | null; fallback?: number; max: number; label: string; onChange: (v: number | null) => void }) {
  const t = useT();
  const steps = [...new Set([...LIMIT_STEPS.filter((n) => n <= max), ...(value && value > 0 ? [value] : [])])].sort((a, b) => a - b);
  return (
    <select
      className="ld-limit-select"
      aria-label={label}
      title={label}
      value={value == null ? 'default' : String(value)}
      onChange={(e) => onChange(e.target.value === 'default' ? null : Number(e.target.value))}
    >
      {fallback != null && <option value="default">{fallback > 0 ? t('ld.useDefault', { n: fallback }) : t('ld.useDefaultNoLimit')}</option>}
      <option value="0">{t('ld.noLimit')}</option>
      {steps.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </select>
  );
}

function AdminView({
  limits,
  list,
  meId,
  onChanged,
  onUnlink,
  onRename,
}: {
  limits: DeviceLimits;
  list: LinkedDevice[];
  meId: string;
  onChanged: () => void;
  onUnlink: (d: LinkedDevice) => void;
  onRename: (d: LinkedDevice, name: string) => Promise<boolean>;
}) {
  const t = useT();
  const users = [...(limits.users ?? [])].sort((a, b) => Number(b.id === meId) - Number(a.id === meId) || b.used - a.used || a.username.localeCompare(b.username));
  const people = users.filter((u) => u.used > 0).length;

  const saved = (r: { ok: boolean; error?: string }) => {
    if (r.ok) toast('success', tr('ld.limitSaved'));
    else toast('error', tr('ld.limitFail'), r.error);
    onChanged();
  };

  return (
    <Group title={t('ld.adminTitle')} hint={t('ld.adminHint')}>
      <div className="ld-stats">
        <div className="ld-stat">
          <b>{limits.total ?? 0}</b>
          <span>{t('ld.statTotal')}</span>
        </div>
        <div className="ld-stat">
          <b>
            {people}
            <small> / {users.length}</small>
          </b>
          <span>{t('ld.statPeople')}</span>
        </div>
        <div className="ld-stat ld-stat-edit">
          <b>{limits.default > 0 ? limits.default : '∞'}</b>
          <span>{t('ld.statDefault')}</span>
        </div>
      </div>
      <div className="settings-row ld-default-row">
        <span className="settings-row-text">
          <span className="settings-row-label">{t('ld.defaultLabel')}</span>
          <span className="settings-row-hint">{t('ld.defaultHint')}</span>
        </span>
        <div className="settings-row-control">
          <LimitSelect
            value={limits.default}
            max={limits.max}
            label={t('ld.defaultLabel')}
            onChange={(v) => void devicesApi.setDefaultLimit(v ?? 0).then(saved)}
          />
        </div>
      </div>
      <div className="ld-people">
        {users.map((u) => (
          <PersonGroup
            key={u.id}
            u={u}
            isMe={u.id === meId}
            def={limits.default}
            max={limits.max}
            devices={u.id === meId ? [] : list.filter((d) => d.user_id === u.id)}
            onLimit={(v) => void devicesApi.setUserLimit(u.id, v).then(saved)}
            onUnlink={onUnlink}
            onRename={onRename}
          />
        ))}
      </div>
    </Group>
  );
}

function PersonGroup({
  u,
  isMe,
  def,
  max,
  devices,
  onLimit,
  onUnlink,
  onRename,
}: {
  u: UserDeviceLimit;
  isMe: boolean;
  def: number;
  max: number;
  devices: LinkedDevice[];
  onLimit: (v: number | null) => void;
  onUnlink: (d: LinkedDevice) => void;
  onRename: (d: LinkedDevice, name: string) => Promise<boolean>;
}) {
  const t = useT();
  const full = isFull(u.used, u.limit);
  const pct = u.limit > 0 ? Math.min(100, (u.used / u.limit) * 100) : 0;
  return (
    <section className={`ld-person ${full ? 'is-full' : ''}`}>
      <header className="ld-person-head">
        <span className="ld-avatar" aria-hidden="true">
          {u.username.slice(0, 1).toUpperCase()}
        </span>
        <span className="ld-person-name">
          <b>{u.username}</b>
          <span className="ld-person-tags">
            {isMe && <span className="ld-tag">{t('ld.you')}</span>}
            {u.role === 'admin' && <span className="ld-tag">{t('ld.admin')}</span>}
          </span>
        </span>
        <span className="ld-person-count">
          <b>{u.limit > 0 ? t('ld.count', { used: u.used, limit: u.limit }) : t('ld.countNoLimit', { used: u.used })}</b>
          {u.limit > 0 && (
            <span className="ld-bar" aria-hidden="true">
              <span style={{ width: `${pct}%` }} />
            </span>
          )}
        </span>
        <LimitSelect value={u.override} fallback={def} max={max} label={t('ld.limitFor', { name: u.username })} onChange={onLimit} />
      </header>
      {!isMe &&
        (devices.length === 0 ? (
          <p className="muted small ld-person-none">{t('ld.userNone')}</p>
        ) : (
          <div className="ld-grid">
            {devices.map((d) => (
              <DeviceCard key={d.id} d={d} onUnlink={() => onUnlink(d)} onRename={(n) => onRename(d, n)} />
            ))}
          </div>
        ))}
    </section>
  );
}

type Phase =
  | { kind: 'full' }
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'code'; pc: PairCode; qr: string }
  | { kind: 'linked'; name: string };

export function LinkSheet({
  known,
  mine,
  used,
  limit,
  onUnlink,
  onChanged,
  onClose,
}: {
  known: Set<string>;
  mine: LinkedDevice[];
  used: number;
  limit: number;
  onUnlink: (d: LinkedDevice) => Promise<boolean>;
  onChanged: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const [phase, setPhase] = useState<Phase>(isFull(used, limit) ? { kind: 'full' } : { kind: 'loading' });
  const [now, setNow] = useState(Date.now());
  const knownRef = useRef(known);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  const fresh = useCallback(async () => {
    setPhase({ kind: 'loading' });
    const r = await devicesApi.pairCode();
    if (r.status === 409) {
      onChanged();
      return setPhase({ kind: 'full' });
    }
    if (!r.ok) return setPhase({ kind: 'error', message: r.error ?? tr('ld.codeFail') });
    let qr = '';
    try {
      qr = await QRCode.toDataURL(r.data.pair_url, { margin: 1, width: 240 });
    } catch {
      /* the code alone still works */
    }
    setNow(Date.now());
    setPhase({ kind: 'code', pc: r.data, qr });
  }, [onChanged]);

  useEffect(() => {
    if (phase.kind === 'loading') void fresh();
    // Only on open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Countdown.
  useEffect(() => {
    if (phase.kind !== 'code') return;
    const id = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(id);
  }, [phase.kind]);

  // Watch for the new device while a code is showing.
  useEffect(() => {
    if (phase.kind !== 'code') return;
    const id = window.setInterval(() => {
      void devicesApi.list().then((r) => {
        if (!r.ok) return;
        const added = r.data.find((d) => !knownRef.current.has(d.id));
        if (added) setPhase({ kind: 'linked', name: added.name });
      });
    }, 3000);
    return () => window.clearInterval(id);
  }, [phase.kind]);

  // Celebrate, then close.
  useEffect(() => {
    if (phase.kind !== 'linked') return;
    toast('success', tr('ld.linkedName', { name: phase.name }));
    const id = window.setTimeout(() => closeRef.current(), 1800);
    return () => window.clearTimeout(id);
  }, [phase]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && closeRef.current();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const server = window.location.origin;
  const copyServer = () => void navigator.clipboard?.writeText(server).then(() => toast('success', tr('ld.serverCopied')));

  let body;
  if (phase.kind === 'full') {
    const room = !isFull(mine.length, limit);
    body = (
      <>
        <div className="ld-sheet-full">
          <Meter used={mine.length} limit={limit} ready size={92} />
          <div>
            <b>{t('ld.fullTitle', { used: mine.length, limit })}</b>
            <p>{t('ld.fullMsg')}</p>
          </div>
        </div>
        <ul className="ld-sheet-list">
          {[...mine]
            .sort((a, b) => (a.last_seen_at ?? '').localeCompare(b.last_seen_at ?? ''))
            .map((d) => (
              <li key={d.id}>
                <Icon name={PLATFORM_ICON[d.platform] ?? 'phone'} size={16} />
                <span>
                  <b>{d.name}</b>
                  <small>{d.online ? t('ld.online') : d.last_seen_at ? t('ld.seen', { ago: ago(t, d.last_seen_at) }) : t('ld.neverSeen')}</small>
                </span>
                <button type="button" className="ghost small danger" onClick={() => void onUnlink(d)}>
                  {t('ld.unlink')}
                </button>
              </li>
            ))}
        </ul>
        {room && (
          <button type="button" className="pill ld-cta align-start" onClick={() => void fresh()}>
            {t('ld.continue')} <Icon name="send" size={13} />
          </button>
        )}
      </>
    );
  } else if (phase.kind === 'loading') {
    body = (
      <div className="dev-link-wait">
        <span className="spinner" />
      </div>
    );
  } else if (phase.kind === 'error') {
    body = (
      <>
        <p className="error">{phase.message}</p>
        <button type="button" className="small align-start" onClick={() => void fresh()}>
          <Icon name="restart" size={13} /> {t('ld.tryAgain')}
        </button>
      </>
    );
  } else if (phase.kind === 'linked') {
    body = (
      <div className="dev-link-done">
        <span className="dev-link-check">
          <Icon name="check" size={30} />
        </span>
        <b>{t('ld.linkedName', { name: phase.name })}</b>
        <span className="muted small">{t('ld.allSet')}</span>
      </div>
    );
  } else {
    const ttl = 5 * 60;
    const left = Math.max(0, Math.ceil((new Date(phase.pc.expires_at).getTime() - now) / 1000));
    const expired = left === 0;
    const code = phase.pc.code;
    const time = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`;
    body = (
      <>
        <ol className="ld-steps">
          <li>{t('ld.step1')}</li>
          <li>{t('ld.step2')}</li>
          <li>{t('ld.step3')}</li>
        </ol>
        <div className={`ld-code ${expired ? 'expired' : ''}`}>
          {phase.qr && (
            <span className="ld-qr-frame">
              <img src={phase.qr} alt={t('ld.qrAlt')} className="ld-qr" />
              <i className="ld-scan" aria-hidden="true" />
            </span>
          )}
          <div className="ld-digits" aria-label={t('ld.codeAria', { code: code.split('').join(' ') })}>
            {code.split('').map((c, i) => (
              <span key={i} className={i === 3 ? 'gap' : ''}>
                {c}
              </span>
            ))}
          </div>
          {expired ? (
            <div className="dev-link-expired">
              <b>{t('ld.expired')}</b>
              <button type="button" className="small" onClick={() => void fresh()}>
                <Icon name="restart" size={13} /> {t('ld.newCode')}
              </button>
            </div>
          ) : (
            <div className="ld-timer">
              <span className="ld-timer-bar">
                <span style={{ width: `${Math.min(100, (left / ttl) * 100)}%` }} />
              </span>
              <span className="muted small">{t('ld.expiresIn', { time })}</span>
            </div>
          )}
          {!expired && (
            <span className="ld-waiting">
              <i aria-hidden="true" /> {t('ld.waiting')}
            </span>
          )}
        </div>
        <div className="dev-link-server">
          <span className="muted small">{t('ld.serverAddr')}</span>
          <code>{server}</code>
          <button type="button" className="icon-btn neutral" title={t('ld.copyServer')} aria-label={t('ld.copyServer')} onClick={copyServer}>
            <Icon name="copy" size={14} />
          </button>
        </div>
        <a className="set-link" href={APP_URL} target="_blank" rel="noreferrer">
          {t('ld.getApp')} <Icon name="external" size={12} />
        </a>
      </>
    );
  }

  return createPortal(
    <div className="confirm-backdrop ld-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="confirm-dialog glass ld-sheet" role="dialog" aria-modal="true" aria-labelledby="ld-sheet-title">
        <span className="ld-grabber" aria-hidden="true" />
        <div className="ld-sheet-head">
          <span className="ld-sheet-icon">
            <Icon name="phone" size={20} />
          </span>
          <h3 id="ld-sheet-title">{t('ld.sheetTitle')}</h3>
          <button type="button" className="icon-btn neutral" aria-label={t('ld.close')} title={t('ld.close')} onClick={onClose}>
            <Icon name="close" size={14} />
          </button>
        </div>
        {body}
        {phase.kind === 'linked' && (
          <div className="confirm-actions">
            <button type="button" onClick={onClose}>
              {t('ld.done')}
            </button>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
