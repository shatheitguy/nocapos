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
import { openApp } from './meta';
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

      <Section title="Time zone" hint="Every clock in NoCapOS uses this zone. Times come from this device's clock.">
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
  { key: 'soundClicks', label: 'Clicking buttons and icons', hint: 'A soft tick', cue: 'click' },
  { key: 'soundWindows', label: 'Opening and closing windows', hint: 'A rising or falling chirp', cue: 'open' },
  { key: 'soundAlerts', label: 'Notifications', hint: 'Different tones for success, info and errors', cue: 'success' },
  { key: 'soundLock', label: 'Locking and unlocking', hint: 'When you lock the screen or sign back in', cue: 'unlock' },
];

export function SoundSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title="Sound effects">
        <Toggle label="Play sound effects" hint="Short synthesized tones — each theme has its own voice"
          checked={prefs.uiSounds} onChange={(uiSounds) => set({ uiSounds })} />
        <Slider label="Volume" value={prefs.soundVolume} min={0} max={100} step={5}
          format={(v) => (v ? `${v}%` : 'Muted')} onChange={(soundVolume) => set({ soundVolume })} />
      </Section>

      <Section title="Play a sound when" hint={prefs.uiSounds ? undefined : 'Turn on sound effects above to hear these. You can still preview them.'}>
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
        <Row label="Style" hint="Shows after a while idle; any movement wakes it">
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
      <p className="settings-tip">Forgot your password? Turn on two-factor authentication in Security to reset it from the lock screen.</p>
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
        <Row label="NoCapOS language" hint="More languages are coming">
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

  const load = async () => {
    const r = await accountApi.totpStatus();
    if (r.ok) setEnabled(r.data.enabled);
    return r.ok ? r.data.enabled : null;
  };

  const start = async () => {
    const r = await accountApi.totpSetup();
    if (!r.ok) {
      toast('error', 'Could not start setup', r.error);
      return;
    }
    const qr = await QRCode.toDataURL(r.data.uri, { margin: 1, width: 220 });
    setEnroll({ secret: r.data.secret, uri: r.data.uri, qr });
    setCode('');
    setEnrollErr('');
  };

  useEffect(() => {
    void load().then((on) => {
      if (autoStart && on === false) void start();
    });
    // Only on open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const confirm = async () => {
    const r = await accountApi.totpEnable(code.trim());
    if (!r.ok) {
      setEnrollErr(r.error ?? 'Wrong code');
      return;
    }
    setEnroll(null);
    toast('success', 'Two-factor authentication enabled');
    void load();
  };

  const disable = async () => {
    const pw = window.prompt('Enter your password to turn off two-factor authentication:');
    if (!pw) return;
    const r = await accountApi.totpDisable(pw);
    if (!r.ok) {
      toast('error', 'Could not disable 2FA', r.error);
      return;
    }
    toast('success', 'Two-factor authentication disabled');
    void load();
  };

  return (
    <Section title="Two-factor authentication"
      hint="Protect your account with an authenticator app (Google Authenticator, Authy, 1Password…). It's also how you reset your password from the lock screen if you forget it.">
      <Toggle label="Ask for a code when signing in" hint={enabled === null ? 'Checking…' : enabled ? 'On' : enroll ? 'Finish setting up below' : 'Off'}
        checked={!!enabled || !!enroll} disabled={enabled === null}
        onChange={(on) => (on ? void start() : enroll ? setEnroll(null) : void disable())} />
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
    if (next.length < 10) return setError('New password must be at least 10 characters.');
    if (next !== confirm) return setError('Passwords do not match.');
    setBusy(true);
    const r = await accountApi.changePassword(current, next);
    setBusy(false);
    if (!r.ok) {
      setError(r.error ?? 'Could not change password');
      return;
    }
    setCurrent('');
    setNext('');
    setConfirm('');
    toast('success', 'Password changed');
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
    if (err) toast('error', 'Backup failed', err);
    else toast('success', 'Backup downloaded');
  };
  return (
    <div className="stack settings-page">
      <Section title="Automatic backups">
        <Row label="Backups" hint="Back up your drives and NoCapOS on a schedule, encrypted, to a USB disk, another server or the cloud, and bring back any file from any day with Rewind.">
          <button type="button" className="ghost small" onClick={() => openApp('backups')}>
            <Icon name="rewind" size={13} /> Open Backups
          </button>
        </Row>
      </Section>
      <Section title="NoCapOS itself">
        <Row label="Download a backup" hint="One file with your accounts, settings, AI providers, conversations and saved memory. Keep it somewhere safe.">
          <button type="button" className="ghost small" disabled={busy} onClick={() => void run()}>
            <Icon name="download" size={13} /> {busy ? 'Preparing…' : 'Download'}
          </button>
        </Row>
        <Row label="Restore" hint="Stop NoCapOS, replace nocap.db in the data folder with your backup, and start it again. A one-click restore is coming soon.">
          <span />
        </Row>
      </Section>
    </div>
  );
}
