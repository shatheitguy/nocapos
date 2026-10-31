import { useEffect, useMemo, useState } from 'react';
import QRCode from 'qrcode';
import { accountApi, downloadBackup } from '../api/account';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { playCue, type Cue } from '../lib/uiSound';
import { deviceTimeZone, fmtDate, fmtTime, timeZones, useTimePrefs } from '../lib/time';
import {
  LANGUAGES,
  LOCK_TIMEOUTS,
  SAVER_DELAYS,
  SCREENSAVERS,
  usePrefs,
} from '../state/prefs';
import { toast } from '../state/toasts';
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
  return (
    <div className="stack settings-page">
      <div className="settings-card datetime-hero">
        <div className="datetime-time">{fmtTime(now, tp, true)}</div>
        <div className="muted">{fmtDate(now, tp)}</div>
        <div className="muted small">
          {active.replace(/_/g, ' ')} · {utcOffset(now, active)}
        </div>
      </div>

      <Section title="Time format">
        <Toggle label="24-hour time" hint={`Shows ${fmtTime(now, { ...tp, clock24: true }, false)} instead of ${fmtTime(now, { ...tp, clock24: false }, false)}`}
          checked={prefs.clock24} onChange={(clock24) => set({ clock24 })} />
        <Toggle label="Show seconds" hint="In the Dock and the desktop clock" checked={prefs.clockSeconds} onChange={(clockSeconds) => set({ clockSeconds })} />
        <Toggle label="Show the date in the Dock" hint={`e.g. ${fmtDate(now, tp, 'short')}`} checked={prefs.clockDate} onChange={(clockDate) => set({ clockDate })} />
      </Section>

      <Section title="Time zone" hint="Every clock in NoCapOS follows this zone. Times come from this device's clock.">
        <Toggle label="Set time zone automatically" hint={`Uses this device's zone (${device.replace(/_/g, ' ')})`}
          checked={!prefs.timeZone} onChange={(auto) => set({ timeZone: auto ? '' : device })} />
        {prefs.timeZone && (
          <Row label="Time zone">
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

      <Section title="Calendar">
        <Row label="First day of the week" hint="Used by the calendar widget">
          <Choice value={prefs.weekStart} onChange={(weekStart) => set({ weekStart })} options={[
            { id: 'sun', label: 'Sunday' }, { id: 'mon', label: 'Monday' },
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

const SOUND_EVENTS: { key: 'soundClicks' | 'soundWindows' | 'soundAlerts' | 'soundLock'; label: string; hint: string; cue: Cue }[] = [
  { key: 'soundClicks', label: 'Clicking buttons and icons', hint: 'A soft little tick', cue: 'click' },
  { key: 'soundWindows', label: 'Opening and closing windows', hint: 'A rising or falling chirp', cue: 'open' },
  { key: 'soundAlerts', label: 'Notifications', hint: 'Different tones for wins, info and errors', cue: 'success' },
  { key: 'soundLock', label: 'Locking and unlocking', hint: 'When you lock the screen or sign back in', cue: 'unlock' },
];

export function SoundSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title="Sound effects">
        <Toggle label="Play sound effects" hint="Short synth tones — every theme has its own vibe"
          checked={prefs.uiSounds} onChange={(uiSounds) => set({ uiSounds })} />
        <Slider label="Volume" value={prefs.soundVolume} min={0} max={100} step={5}
          format={(v) => (v ? `${v}%` : 'Muted')} onChange={(soundVolume) => set({ soundVolume })} />
      </Section>

      <Section title="Play a sound when" hint={prefs.uiSounds ? undefined : 'Turn on sound effects above to hear these. Previewing still works.'}>
        {SOUND_EVENTS.map((e) => (
          <div key={e.key} className={`settings-row ${prefs.uiSounds ? '' : 'dimmed'}`}>
            <span className="settings-row-text">
              <span className="settings-row-label">{e.label}</span>
              <span className="settings-row-hint">{e.hint}</span>
            </span>
            <div className="settings-row-control">
              <button type="button" className="ghost small" onClick={() => playCue(e.cue, true)} title={`Preview: ${e.label}`}>
                <Icon name="play" size={12} /> Preview
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
  return (
    <div className="stack settings-page">
      <Section title="Lock">
        <Row label="Lock the screen after" hint="Inactivity before NoCapOS locks. You unlock with your password.">
          <select className="select" value={prefs.lockTimeout} onChange={(e) => prefs.set({ lockTimeout: Number(e.target.value) })}>
            {LOCK_TIMEOUTS.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Row>
      </Section>

      <Section title="Screen saver">
        <Row label="Style" hint="Kicks in when you’ve been idle a while; any movement wakes it">
          <Choice value={prefs.screensaver} onChange={(screensaver) => prefs.set({ screensaver })}
            options={SCREENSAVERS.map((s) => ({ id: s.id, label: s.name }))} />
        </Row>
        {prefs.screensaver !== 'off' && (
          <Row label="Start after">
            <select className="select" value={prefs.saverDelay} onChange={(e) => prefs.set({ saverDelay: Number(e.target.value) })}>
              {SAVER_DELAYS.map((m) => (
                <option key={m} value={m}>
                  {m} minute{m > 1 ? 's' : ''}
                </option>
              ))}
            </select>
          </Row>
        )}
      </Section>
      <p className="settings-tip">Forgot your password? It happens. An administrator can reset it in Users &amp; Groups.</p>
    </div>
  );
}

// ---------------- Language & Region ----------------

export function LanguageRegion() {
  const prefs = usePrefs();
  const locale = Intl.DateTimeFormat().resolvedOptions().locale;
  const sample = new Date();
  return (
    <div className="stack settings-page">
      <Section title="Language">
        <Row label="NoCapOS language" hint="More languages are on the way">
          <select className="select" value={prefs.language} onChange={(e) => prefs.set({ language: e.target.value as typeof prefs.language })}>
            {LANGUAGES.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </Row>
      </Section>
      <Section title="Region formats" hint={`Follows this browser's region (${locale}).`}>
        <Row label="Date">
          <span className="settings-value">{sample.toLocaleDateString()}</span>
        </Row>
        <Row label="Number">
          <span className="settings-value">{(1234567.89).toLocaleString()}</span>
        </Row>
        <Row label="Currency">
          <span className="settings-value">{(1234.5).toLocaleString(undefined, { style: 'currency', currency: currencyFor(locale) })}</span>
        </Row>
      </Section>
    </div>
  );
}

function currencyFor(locale: string): string {
  const region = locale.split('-')[1]?.toUpperCase();
  const map: Record<string, string> = { AE: 'AED', US: 'USD', GB: 'GBP', IN: 'INR', SA: 'SAR', JP: 'JPY', CN: 'CNY', EU: 'EUR', DE: 'EUR', FR: 'EUR' };
  return map[region ?? ''] ?? 'USD';
}

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

  const load = async () => {
    const r = await accountApi.totpStatus();
    if (r.ok) setEnabled(r.data.enabled);
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
      setAskErr(r.error ?? 'Couldn’t start setup. Try again?');
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
    const r = await accountApi.totpEnable(code.trim());
    if (!r.ok) {
      setEnrollErr(r.error ?? 'That code’s not it. Check your authenticator app and try again.');
      return;
    }
    setEnroll(null);
    toast('success', 'Two-factor is on — your account just leveled up');
    void load();
  };

  const disable = async () => {
    setBusy(true);
    const r = await accountApi.totpDisable(password, offCode);
    setBusy(false);
    if (!r.ok) {
      setAskErr(r.error ?? 'Couldn’t turn off two-factor authentication');
      setOffCode('');
      return;
    }
    closeAsk();
    toast('success', 'Two-factor authentication is off', 'Signing in needs just your password now.');
    void load();
  };

  return (
    <Section title="Two-factor authentication"
      hint="Lock your account down with an authenticator app. Signing in then needs your password and a code from the app.">
      <Toggle label="Ask for a code when signing in" hint={enabled === null ? 'Checking…' : enabled ? 'On' : enroll ? 'Finish setting up below' : 'Off'}
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
              {ask === 'on'
                ? 'Enter your password to set up two-factor authentication.'
                : 'Enter your password and a current code from your authenticator app to turn two-factor authentication off.'}
            </p>
            <label>
              Password
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" autoFocus required />
            </label>
            {ask === 'off' && (
              <label>
                Code from your authenticator app
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
                {ask === 'on' ? 'Continue' : 'Turn off'}
              </button>
              <button type="button" className="ghost" onClick={closeAsk}>
                Cancel
              </button>
            </div>
          </form>
        </div>
      )}
      {enroll && (
        <div className="settings-row full">
          <div className="totp-enroll">
            <img src={enroll.qr} alt="Scan this QR code" className="totp-qr" />
            <p className="small">Scan the QR code, or enter this key manually:</p>
            <code className="totp-secret">{enroll.secret}</code>
            <label>
              Enter the 6-digit code to confirm
              <input
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                inputMode="numeric"
                placeholder="123456"
              />
            </label>
            {enrollErr && <p className="error">{enrollErr}</p>}
            <div className="btn-group">
              <button type="button" disabled={code.length !== 6} onClick={() => void confirm()}>
                Enable
              </button>
              <button type="button" className="ghost" onClick={() => setEnroll(null)}>
                Cancel
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

  const submit = async () => {
    setError('');
    if (next.length < 10) return setError('Too short — your new password needs at least 10 characters.');
    if (next !== confirm) return setError('Those passwords don’t match. Try again?');
    setBusy(true);
    const r = await accountApi.changePassword(current, next);
    setBusy(false);
    if (!r.ok) {
      setError(r.error ?? 'Couldn’t change your password');
      return;
    }
    setCurrent('');
    setNext('');
    setConfirm('');
    toast('success', 'Bet — password changed');
  };

  return (
    <Section title="Change password" hint="At least 10 characters.">
      <div className="settings-row full">
        <form
          className="set-password"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
        >
          <label>
            Current password
            <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" />
          </label>
          <label>
            New password
            <input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" />
          </label>
          <label>
            Confirm new password
            <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
          </label>
          {error && <p className="error">{error}</p>}
          <button type="submit" className="pill" disabled={busy || !current || !next}>
            Update password
          </button>
        </form>
      </div>
    </Section>
  );
}

// ---------------- Backup ----------------

export function Backup() {
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    const err = await downloadBackup();
    setBusy(false);
    if (err) toast('error', 'Backup didn’t download', err);
    else toast('success', 'Backup downloaded — keep it somewhere safe');
  };
  return (
    <div className="stack settings-page">
      <Section title="NoCapOS itself">
        <Row label="Download a backup" hint="One file with your accounts, settings, AI providers, conversations and saved memory. Keep it somewhere safe.">
          <button type="button" className="ghost small" disabled={busy} onClick={() => void run()}>
            <Icon name="download" size={13} /> {busy ? 'Preparing…' : 'Download'}
          </button>
        </Row>
        <Row label="Restore" hint="Stop NoCapOS, replace nocap.db in the data folder with your backup, and start it again. One-click restore is on the way.">
          <span />
        </Row>
      </Section>
    </div>
  );
}
