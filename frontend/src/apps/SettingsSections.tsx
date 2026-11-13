import { useEffect, useMemo, useRef, useState } from 'react';
import QRCode from 'qrcode';
import { accountApi, downloadBackup, uploadRestore } from '../api/account';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { powerHost } from '../lib/hostActions';
import { missing } from '../lib/settingsKit';
import { confirmDialog } from '../state/confirm';
import { playCue, type Cue } from '../lib/uiSound';
import { deviceTimeZone, fmtDate, fmtTime, timeZones, useTimePrefs } from '../lib/time';
import {
  LOCK_TIMEOUTS,
  SAVER_DELAYS,
  SCREENSAVERS,
  usePrefs,
} from '../state/prefs';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';
import { Choice, Row, Section, Slider, Toggle } from './Personalize';

// ---------------- Date & Time ----------------

export function DateTime() {
  const prefs = usePrefs();
  const set = prefs.set;
  const now = useClock(1000);
  const tp = useTimePrefs();
  const device = deviceTimeZone();
  const zones = useMemo(timeZones, []);
  const active = prefs.timeZone || device;
  const t = useT();
  return (
    <div className="stack settings-page">
      <div className="settings-card datetime-hero">
        <div className="datetime-time">{fmtTime(now, tp, true)}</div>
        <div className="muted">{fmtDate(now, tp)}</div>
        <div className="muted small">
          {active.replace(/_/g, ' ')} · {utcOffset(now, active)}
        </div>
      </div>

      <Section title={t('sp.timeFormat')} hint={t('sp.timeFormatHint')}>
        <Toggle label={t('sp.clock24')} hint={t('sp.clock24Hint', { a: fmtTime(now, { ...tp, clock24: true }, false), b: fmtTime(now, { ...tp, clock24: false }, false) })}
          checked={prefs.clock24} onChange={(clock24) => set({ clock24 })} />
        <Toggle label={t('sp.seconds')} hint={t('sp.secondsHint')} checked={prefs.clockSeconds} onChange={(clockSeconds) => set({ clockSeconds })} />
        <Toggle label={t('sp.dateInDock')} hint={t('sp.dateInDockHint', { date: fmtDate(now, tp, 'short') })} checked={prefs.clockDate} onChange={(clockDate) => set({ clockDate })} />
      </Section>

      <Section title={t('sp.timeZone')} hint={t('sp.timeZoneHint')}>
        <Toggle label={t('sp.tzAuto')} hint={t('sp.tzAutoHint', { zone: device.replace(/_/g, ' ') })}
          checked={!prefs.timeZone} onChange={(auto) => set({ timeZone: auto ? '' : device })} />
        {prefs.timeZone && (
          <Row label={t('sp.timeZone')}>
            <select className="select" value={prefs.timeZone} onChange={(e) => set({ timeZone: e.target.value })}>
              {(zones.length ? zones : [prefs.timeZone]).map((z) => (
                <option key={z} value={z}>
                  {z.replace(/_/g, ' ')} ({utcOffset(now, z)})
                </option>
              ))}
            </select>
          </Row>
        )}
      </Section>

      <Section title={t('sp.calendar')} hint={t('sp.calendarHint')}>
        <Row label={t('sp.weekStart')} hint={t('sp.weekStartHint')}>
          <Choice value={prefs.weekStart} onChange={(weekStart) => set({ weekStart })} options={[
            { id: 'sun', label: t('sp.sunday') }, { id: 'mon', label: t('sp.monday') },
          ]} />
        </Row>
      </Section>
    </div>
  );
}

/** "UTC+04:00" for a zone at a moment. */
function utcOffset(d: Date, tz: string): string {
  try {
    const name = new Intl.DateTimeFormat('en-US', { timeZone: tz, timeZoneName: 'longOffset' })
      .formatToParts(d).find((p) => p.type === 'timeZoneName')?.value;
    return name === 'GMT' ? 'UTC' : (name ?? '').replace('GMT', 'UTC');
  } catch {
    return '';
  }
}

// ---------------- Sound ----------------

const soundEvents = (): { key: 'soundClicks' | 'soundWindows' | 'soundAlerts' | 'soundLock'; label: string; hint: string; cue: Cue }[] => [
  { key: 'soundClicks', label: tr('sp.sClicks'), hint: tr('sp.sClicksHint'), cue: 'click' },
  { key: 'soundWindows', label: tr('sp.sWindows'), hint: tr('sp.sWindowsHint'), cue: 'open' },
  { key: 'soundAlerts', label: tr('sp.sAlerts'), hint: tr('sp.sAlertsHint'), cue: 'success' },
  { key: 'soundLock', label: tr('sp.sLock'), hint: tr('sp.sLockHint'), cue: 'unlock' },
];

export function SoundSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  const t = useT();
  return (
    <div className="stack settings-page">
      <Section title={t('sp.soundFx')} hint={t('sp.soundFxHint')}>
        <Toggle label={t('sp.playFx')} hint={t('sp.playFxHint')}
          checked={prefs.uiSounds} onChange={(uiSounds) => set({ uiSounds })} />
        <Slider label={t('sp.volume')} value={prefs.soundVolume} min={0} max={100} step={5}
          format={(v) => (v ? `${v}%` : t('sp.muted'))} onChange={(soundVolume) => set({ soundVolume })} />
      </Section>

      <Section title={t('sp.playWhen')} hint={prefs.uiSounds ? t('sp.playWhenHint') : t('sp.playWhenOff')}>
        {soundEvents().map((e) => (
          <div key={e.key} className={`settings-row ${prefs.uiSounds ? '' : 'dimmed'}`}>
            <span className="settings-row-text">
              <span className="settings-row-label">{e.label}</span>
              <span className="settings-row-hint">{e.hint}</span>
            </span>
            <div className="settings-row-control">
              <button type="button" className="ghost small" onClick={() => playCue(e.cue, true)} title={t('sp.previewOf', { name: e.label })}>
                <Icon name="play" size={12} /> {t('sp.preview')}
              </button>
              <label className="toggle row sound-switch">
                <input type="checkbox" role="switch" aria-label={e.label} aria-checked={prefs[e.key]} checked={prefs[e.key]}
                  onChange={(ev) => set({ [e.key]: ev.target.checked })} />
              </label>
            </div>
          </div>
        ))}
      </Section>
    </div>
  );
}

// ---------------- Lock Screen ----------------

export function LockScreen() {
  const prefs = usePrefs();
  const t = useT();
  const lockLabel = (v: number) => (v === 0 ? t('sp.never') : v === 60 ? t('sp.oneHour') : t('sp.minutes', { n: v }));
  const saverLabel = (id: string) => (id === 'clock' ? t('sp.saverClock') : id === 'starfield' ? t('sp.saverStars') : t('sp.saverOff'));
  return (
    <div className="stack settings-page">
      <Section title={t('sp.lock')} hint={t('sp.lockHint')}>
        <Row label={t('sp.lockAfter')} hint={t('sp.lockAfterHint')}>
          <select className="select" value={prefs.lockTimeout} onChange={(e) => prefs.set({ lockTimeout: Number(e.target.value) })}>
            {LOCK_TIMEOUTS.map((o) => (
              <option key={o.value} value={o.value}>
                {lockLabel(o.value)}
              </option>
            ))}
          </select>
        </Row>
      </Section>

      <Section title={t('sp.saver')} hint={t('sp.saverHint')}>
        <Row label={t('sp.saverStyle')} hint={t('sp.saverStyleHint')}>
          <Choice value={prefs.screensaver} onChange={(screensaver) => prefs.set({ screensaver })}
            options={SCREENSAVERS.map((s) => ({ id: s.id, label: saverLabel(s.id) }))} />
        </Row>
        {prefs.screensaver !== 'off' && (
          <Row label={t('sp.startAfter')}>
            <select className="select" value={prefs.saverDelay} onChange={(e) => prefs.set({ saverDelay: Number(e.target.value) })}>
              {SAVER_DELAYS.map((m) => (
                <option key={m} value={m}>
                  {t('sp.minutes', { n: m })}
                </option>
              ))}
            </select>
          </Row>
        )}
      </Section>
      <p className="settings-tip">{t('sp.lockTip')}</p>
    </div>
  );
}

// ---------------- Language & Region ----------------

// ---------------- Security ----------------

export function Security({ autoStart = false }: { autoStart?: boolean }) {
  return (
    <div className="stack settings-page">
      <TwoFactor autoStart={autoStart} />
      <ChangePassword />
    </div>
  );
}

/** Two-factor authentication: a switch, plus the QR enrollment when turning it on. */
export function TwoFactor({ autoStart = false }: { autoStart?: boolean }) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [enroll, setEnroll] = useState<{ secret: string; uri: string; qr: string } | null>(null);
  const [code, setCode] = useState('');
  const [enrollErr, setEnrollErr] = useState('');
  // Turning 2FA on asks for your password first; turning it off asks for
  // your password and a current code.
  const [ask, setAsk] = useState<null | 'on' | 'off'>(null);
  const [password, setPassword] = useState('');
  const [offCode, setOffCode] = useState('');
  const [askErr, setAskErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [loadFail, setLoadFail] = useState(false);
  const t = useT();

  const load = async () => {
    setLoadFail(false);
    const r = await accountApi.totpStatus();
    if (r.ok && !missing(r)) setEnabled(r.data.enabled);
    else setLoadFail(true);
    return r.ok ? r.data.enabled : null;
  };

  const closeAsk = () => {
    setAsk(null);
    setPassword('');
    setOffCode('');
    setAskErr('');
  };

  const start = async () => {
    setBusy(true);
    const r = await accountApi.totpSetup(password);
    setBusy(false);
    if (!r.ok) {
      setAskErr(r.error ?? tr('sp.tfStartFail'));
      return;
    }
    closeAsk();
    const qr = await QRCode.toDataURL(r.data.uri, { margin: 1, width: 220 });
    setEnroll({ secret: r.data.secret, uri: r.data.uri, qr });
    setCode('');
    setEnrollErr('');
  };

  useEffect(() => {
    void load().then((on) => {
      if (autoStart && on === false) setAsk('on');
    });
    // Only on open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const confirm = async () => {
    if (busy) return;
    setBusy(true);
    const r = await accountApi.totpEnable(code.trim());
    setBusy(false);
    if (!r.ok) {
      setEnrollErr(r.error ?? tr('sp.tfBadCode'));
      return;
    }
    setEnroll(null);
    toast('success', tr('sp.tfOn'));
    void load();
  };

  const disable = async () => {
    setBusy(true);
    const r = await accountApi.totpDisable(password, offCode);
    setBusy(false);
    if (!r.ok) {
      setAskErr(r.error ?? tr('sp.tfOffFail'));
      setOffCode('');
      return;
    }
    closeAsk();
    toast('success', tr('sp.tfOff'), tr('sp.tfOffBody'));
    void load();
  };

  return (
    <Section title={t('sp.tf')} hint={t('sp.tfHint')}>
      {loadFail && enabled === null && (
        <Row label={t('su.tfLoadFail')}>
          <button type="button" className="ghost small" onClick={() => void load()}>{t('set.retry')}</button>
        </Row>
      )}
      <Toggle label={t('sp.tfAsk')} hint={enabled === null ? (loadFail ? t('set.loadFail') : t('sp.checking')) : enabled ? t('sp.on') : enroll ? t('sp.tfFinish') : t('sp.off')}
        checked={!!enabled || !!enroll || ask === 'on'} disabled={enabled === null}
        onChange={(on) => {
          setAskErr('');
          if (on) setAsk('on');
          else if (enroll) setEnroll(null);
          else if (ask === 'on') closeAsk();
          else setAsk('off');
        }} />
      {ask && (
        <div className="settings-row full">
          <form
            className="totp-enroll"
            onSubmit={(e) => {
              e.preventDefault();
              void (ask === 'on' ? start() : disable());
            }}
          >
            <p className="small">
              {ask === 'on' ? t('sp.tfPwOn') : t('sp.tfPwOff')}
            </p>
            <label>
              {t('sp.password')}
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" autoFocus required />
            </label>
            {ask === 'off' && (
              <label>
                {t('sp.tfCode')}
                <input
                  value={offCode}
                  onChange={(e) => setOffCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  placeholder="123456"
                  required
                />
              </label>
            )}
            {askErr && <p className="error">{askErr}</p>}
            <div className="btn-group">
              <button type="submit" className={ask === 'off' ? 'danger' : ''} disabled={busy || !password || (ask === 'off' && offCode.length !== 6)}>
                {ask === 'on' ? t('sp.continue') : t('sp.turnOff')}
              </button>
              <button type="button" className="ghost" onClick={closeAsk}>
                {t('sp.cancel')}
              </button>
            </div>
          </form>
        </div>
      )}
      {enroll && (
        <div className="settings-row full">
          <div className="totp-enroll">
            <img src={enroll.qr} alt={t('sp.scanQr')} className="totp-qr" />
            <p className="small">{t('sp.scanOrKey')}</p>
            <code className="totp-secret">{enroll.secret}</code>
            <label>
              {t('sp.enterCode')}
              <input
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                inputMode="numeric"
                placeholder="123456"
              />
            </label>
            {enrollErr && <p className="error">{enrollErr}</p>}
            <div className="btn-group">
              <button type="button" disabled={code.length !== 6 || busy} onClick={() => void confirm()}>
                {t('sp.enable')}
              </button>
              <button type="button" className="ghost" onClick={() => setEnroll(null)}>
                {t('sp.cancel')}
              </button>
            </div>
          </div>
        </div>
      )}
    </Section>
  );
}

export function ChangePassword() {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const t = useT();

  const submit = async () => {
    if (busy) return;
    setError('');
    if (next.length < 10) return setError(tr('sp.pwShort'));
    if (next !== confirm) return setError(tr('sp.pwMismatch'));
    setBusy(true);
    const r = await accountApi.changePassword(current, next);
    setBusy(false);
    if (!r.ok) {
      setError(r.error ?? tr('sp.pwFail'));
      return;
    }
    setCurrent('');
    setNext('');
    setConfirm('');
    toast('success', tr('sp.pwChanged'));
  };

  return (
    <Section title={t('sp.changePw')} hint={t('sp.changePwHint')}>
      <div className="settings-row full">
        <form
          className="set-password"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <label>
            {t('sp.pwCurrent')}
            <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" />
          </label>
          <label>
            {t('sp.pwNew')}
            <input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" />
          </label>
          <label>
            {t('sp.pwConfirm')}
            <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" className="pill" disabled={busy || !current || !next}>
            {t('sp.pwUpdate')}
          </button>
        </form>
      </div>
    </Section>
  );
}

// ---------------- Backup ----------------

export function Backup() {
  const [busy, setBusy] = useState(false);
  const t = useT();
  const run = async () => {
    if (busy) return;
    setBusy(true);
    const err = await downloadBackup();
    setBusy(false);
    if (err) toast('error', tr('sp.bkFail'), err);
    else toast('success', tr('sp.bkDone'));
  };
  return (
    <div className="stack settings-page">
      <Section title={t('sp.bkSelf')} hint={t('sp.bkSelfHint')}>
        <Row label={t('sp.bkDownload')} hint={t('sp.bkDownloadHint')}>
          <button type="button" className="ghost small" disabled={busy} onClick={() => void run()}>
            <Icon name="download" size={13} /> {busy ? t('sp.preparing') : t('sp.download')}
          </button>
        </Row>
        <Restore />
      </Section>
    </div>
  );
}

/** Restore a downloaded backup: choose the file, confirm with your password; it goes in at the next restart. */
function Restore() {
  const t = useT();
  // null while loading; 'missing' on a server that can't restore.
  const [pending, setPending] = useState<boolean | 'missing' | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    void accountApi.restoreStatus().then((r) => setPending(r.ok && !missing(r) ? !!r.data.pending : 'missing'));
  }, []);

  const close = () => {
    setFile(null);
    setPassword('');
    setError('');
  };
  const submit = async () => {
    if (!file || !password || busy) return;
    const ok = await confirmDialog({ title: tr('su.restoreTitle'), message: tr('su.restoreMsg'), confirmLabel: tr('su.restoreGo'), danger: true });
    if (!ok) return;
    setBusy(true);
    setError('');
    const err = await uploadRestore(file, password);
    setBusy(false);
    if (err) return setError(err);
    close();
    setPending(true);
    toast('success', tr('su.restoreStaged'), tr('su.restoreStagedBody'));
  };
  const cancel = async () => {
    if (busy) return;
    setBusy(true);
    const r = await accountApi.cancelRestore();
    setBusy(false);
    if (!r.ok) return toast('error', tr('sp.saveFail'), r.error);
    setPending(false);
    toast('success', tr('su.restoreCanceled'));
  };

  if (pending === 'missing') {
    return (
      <Row label={t('sp.restore')} hint={t('set.notAvailable')}>
        <span />
      </Row>
    );
  }
  if (pending === true) {
    return (
      <Row label={t('su.restoreReady')} hint={t('su.restoreReadyHint')}>
        <div className="btn-group">
          <button type="button" className="ghost small" disabled={busy} onClick={() => void cancel()}>{t('su.restoreCancel')}</button>
          <button type="button" className="small" onClick={() => void powerHost('reboot')}>{t('gu.restart')}</button>
        </div>
      </Row>
    );
  }
  return (
    <>
      <Row label={t('sp.restore')} hint={t('sp.restoreHint')}>
        <button type="button" className="ghost small" disabled={pending === null || busy} onClick={() => input.current?.click()}>
          <Icon name="upload" size={13} /> {t('su.restoreChoose')}
        </button>
        <input ref={input} type="file" accept=".db,application/octet-stream" hidden
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = '';
            if (f) {
              setFile(f);
              setPassword('');
              setError('');
            }
          }} />
      </Row>
      {file && (
        <div className="settings-row full">
          <form className="totp-enroll" onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}>
            <p className="small">{t('su.restorePw', { name: file.name })}</p>
            <label>
              {t('sp.password')}
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" autoFocus required />
            </label>
            {error && <p className="error">{error}</p>}
            <div className="btn-group">
              <button type="submit" className="danger" disabled={busy || !password}>
                {busy ? t('su.restoreChecking') : t('su.restoreGo')}
              </button>
              <button type="button" className="ghost" disabled={busy} onClick={close}>{t('sp.cancel')}</button>
            </div>
          </form>
        </div>
      )}
    </>
  );
}
