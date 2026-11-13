import { useEffect, useState, type FormEvent } from 'react';
import { accountsMode, login, loginCode, setup, setupStatus } from '../api/client';
import { useT } from '../i18n';
import { Icon } from '../components/Icon';
import { Logo } from '../components/Logo';
import { UserAvatar } from '../components/UserAvatar';
import { forgetLastUser, lastUser } from '../lib/avatar';
import { useClock } from '../lib/hooks';
import { fmtTime } from '../lib/time';
import { usePrefs } from '../state/prefs';
import { layoutKeyFor, useWM } from '../state/windows';

export type AuthMode = 'setup' | 'login' | 'locked';

/** macOS-style login / lock screen: big clock, user photo, password pill. */
export function AuthScreen({ mode, lockedUser, lockedUserId }: { mode: AuthMode; lockedUser?: string; lockedUserId?: string }) {
  const now = useClock(1000);
  const tp = usePrefs.getState();

  return (
    <div className={`auth-screen mac ${mode === 'locked' ? 'lock' : ''}`}>
      <div className="mac-clock">
        <div className="mac-date">
          {now.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long', ...(tp.timeZone ? { timeZone: tp.timeZone } : {}) })}
        </div>
        <div className="mac-time">{fmtTime(now, { ...tp, clockSeconds: false }, false).replace(/\s?[AP]M$/i, '')}</div>
      </div>
      {mode === 'setup' ? <SetupForm /> : <LoginForm mode={mode} lockedUser={lockedUser} lockedUserId={lockedUserId} />}
    </div>
  );
}

function PasswordPill({
  value,
  onChange,
  autoFocus,
  autoComplete,
  placeholder,
  busy,
  shake,
  code = false,
}: {
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
  autoComplete: string;
  placeholder: string;
  busy: boolean;
  shake: number;
  /** The 6-digit code from an authenticator app instead of a password. */
  code?: boolean;
}) {
  const [shown, setShown] = useState(false);
  return (
    <div className="mac-pass-wrap">
      <div key={shake} className={`mac-pill pass ${shake ? 'shake' : ''}`}>
        {code ? (
          <input
            type="text"
            value={value}
            onChange={(e) => onChange(e.target.value.replace(/\D/g, '').slice(0, 6))}
            autoComplete="one-time-code"
            inputMode="numeric"
            placeholder={placeholder}
            aria-label="Code from your authenticator app"
            autoFocus={autoFocus}
            spellCheck={false}
            required
          />
        ) : (
          <>
            <input
              type={shown ? 'text' : 'password'}
              value={value}
              onChange={(e) => onChange(e.target.value)}
              autoComplete={autoComplete}
              placeholder={placeholder}
              aria-label="Password"
              autoFocus={autoFocus}
              spellCheck={false}
              required
            />
            <button
              type="button"
              className="mac-eye"
              aria-label={shown ? 'Hide password' : 'Show password'}
              title={shown ? 'Hide password' : 'Show password'}
              onMouseDown={(e) => e.preventDefault()} // keep focus in the field
              onClick={() => setShown(!shown)}
            >
              <Icon name="eye" size={14} />
            </button>
          </>
        )}
        <button type="submit" className="mac-go" aria-label="Sign in" disabled={busy || !value || (code && value.length !== 6)}>
          {busy ? <span className="spinner sm" /> : <Icon name="chevronRight" size={16} />}
        </button>
      </div>
    </div>
  );
}

function LoginForm({ mode, lockedUser, lockedUserId }: { mode: AuthMode; lockedUser?: string; lockedUserId?: string }) {
  const remembered = mode === 'locked' ? null : lastUser();
  const [other, setOther] = useState(false);
  const known = lockedUser ?? (other ? undefined : remembered?.username);
  const photo = lockedUser ? (lastUser()?.username === lockedUser ? lastUser()?.avatar : null) : remembered?.avatar;
  const [username, setUsername] = useState(known ?? '');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [shake, setShake] = useState(0);
  const [linux, setLinux] = useState(false);
  const [forgot, setForgot] = useState(false);
  // Two-factor step: the password was right, now the code.
  const [mfaToken, setMfaToken] = useState<string | null>(null);
  const [code, setCode] = useState('');
  useEffect(() => {
    void accountsMode().then((m) => setLinux(m === 'system'));
  }, []);

  const fail = (msg: string) => {
    setError(msg);
    setShake((n) => n + 1);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (mfaToken) {
      if (code.length !== 6) return;
      setBusy(true);
      const r = await loginCode(mfaToken, code);
      setBusy(false);
      setCode('');
      if (r.error) {
        if (r.expired) {
          setMfaToken(null); // start again from the password
          setPassword('');
        }
        fail(r.error);
      }
      return;
    }
    const name = (known ?? username).trim();
    if (!name || !password) return;
    setBusy(true);
    const r = await login(name, password);
    setBusy(false);
    setPassword('');
    if (r.ok) return;
    if ('mfaToken' in r) {
      setMfaToken(r.mfaToken);
      return;
    }
    fail(r.error);
  };

  const cancelCode = () => {
    setMfaToken(null);
    setCode('');
    setError('');
  };

  // Log Out from the lock screen: the windows kept for unlocking are closed
  // and the next sign-in starts with a clean desktop.
  const logOutFromLock = () => {
    useWM.getState().reset();
    if (lockedUserId) {
      try {
        localStorage.removeItem(layoutKeyFor(lockedUserId));
      } catch {
        /* storage unavailable */
      }
    }
    window.location.reload();
  };

  const hint =
    error ||
    (mfaToken
      ? 'Enter the 6-digit code from your authenticator app'
      : mode === 'locked'
        ? 'Locked — your apps and windows are right where you left them'
        : linux && !known
          ? "Use this computer's Linux account"
          : '');

  return (
    <form className="mac-login" onSubmit={submit}>
      <UserAvatar name={known ?? (username || '?')} size="xxl" src={known ? (photo ?? null) : null} />
      {known ? (
        <div className="mac-name">{known}</div>
      ) : (
        <div className="mac-pill user">
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            placeholder="Name"
            aria-label="User name"
            maxLength={32}
            autoFocus={!mfaToken}
            spellCheck={false}
            readOnly={!!mfaToken}
            required
          />
        </div>
      )}
      {mfaToken ? (
        <PasswordPill key="code" code value={code} onChange={setCode} autoFocus autoComplete="one-time-code" placeholder="6-digit code" busy={busy} shake={shake} />
      ) : (
        <PasswordPill
          key="password"
          value={password}
          onChange={setPassword}
          autoFocus={!!known}
          autoComplete="current-password"
          placeholder={mode === 'locked' ? 'Enter Password to unlock' : 'Enter Password'}
          busy={busy}
          shake={shake}
        />
      )}
      <p className={`mac-hint ${error ? 'err' : ''}`} role="alert">
        {hint}
      </p>
      {mfaToken ? (
        <button type="button" className="mac-link" onClick={cancelCode}>
          Back to password
        </button>
      ) : (
        <button type="button" className="mac-link" aria-expanded={forgot} onClick={() => setForgot((v) => !v)}>
          Forgot password?
        </button>
      )}
      {forgot && !mfaToken && (
        <p className="mac-sub">
          Ask an administrator to reset it in Settings → Users &amp; Groups. If you are the only administrator, run{' '}
          <code>sudo nocapos reset-password {known ?? (username.trim() || '<user>')}</code> on the server.
        </p>
      )}

      <div className="mac-actions">
        {mode === 'locked' ? (
          <>
            <MacAction
              icon="users"
              label="Switch User"
              title="Shows the sign-in screen; your windows stay for when you come back"
              onClick={() => window.location.reload()}
            />
            <MacAction icon="logout" label="Log Out" title="Closes all your windows and ends your session" onClick={logOutFromLock} />
          </>
        ) : known ? (
          <MacAction icon="user" label="Other User…" onClick={() => setOther(true)} />
        ) : remembered ? (
          <MacAction icon="chevronLeft" label="Back" onClick={() => setOther(false)} />
        ) : null}
        {mode !== 'locked' && remembered && known && (
          <MacAction
            icon="close"
            label="Forget"
            onClick={() => {
              forgetLastUser();
              setOther(true);
            }}
          />
        )}
      </div>
    </form>
  );
}

function MacAction({
  icon,
  label,
  title,
  onClick,
}: {
  icon: 'users' | 'user' | 'chevronLeft' | 'close' | 'logout';
  label: string;
  title?: string;
  onClick: () => void;
}) {
  return (
    <button type="button" className="mac-action" title={title} onClick={onClick}>
      <span>
        <Icon name={icon} size={18} />
      </span>
      {label}
    </button>
  );
}

/**
 * First start: create the administrator, like any computer's first boot.
 * From the server's own screen or its local network that's a name and a
 * password; from anywhere else the server also asks for the one-time setup
 * token, and this explains where to find it.
 */
function SetupForm() {
  const t = useT();
  const [needsToken, setNeedsToken] = useState(false);
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [again, setAgain] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [shake, setShake] = useState(0);
  useEffect(() => {
    void setupStatus().then((s) => setNeedsToken(s.tokenRequired));
  }, []);

  const fail = (msg: string) => {
    setError(msg);
    setShake((n) => n + 1);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    const name = username.trim();
    if (!/^[a-z0-9][a-z0-9._-]{2,31}$/i.test(name)) return fail(t('setup.nameRule'));
    if (password.length < 10) return fail(t('setup.passwordShort'));
    if (password !== again) return fail(t('setup.passwordMismatch'));
    if (needsToken && !token.trim()) return fail(t('setup.tokenMissing'));
    setBusy(true);
    const r = await setup(needsToken ? token.trim() : '', name, password);
    setBusy(false);
    if (!r) return;
    if (r.done) {
      window.location.reload(); // someone else finished first: sign in instead
      return;
    }
    if (r.needsToken) setNeedsToken(true);
    fail(r.error);
  };

  return (
    <form className="mac-login setup setup-create" onSubmit={submit}>
      <Logo size={64} />
      <h1 className="setup-create-title">{t('setup.createTitle')}</h1>
      <p className="mac-sub">{t('setup.createSub')}</p>
      <div className="mac-pill user">
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder={t('setup.namePh')}
          aria-label={t('setup.namePh')}
          autoComplete="username"
          minLength={3}
          maxLength={32}
          spellCheck={false}
          autoCapitalize="none"
          autoFocus
          required
        />
      </div>
      <div className="mac-pill user">
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t('setup.passwordPh')}
          aria-label={t('setup.passwordPh')}
          autoComplete="new-password"
          required
        />
      </div>
      {needsToken && (
        <>
          <div className="mac-pill user">
            <input type="password" value={again} onChange={(e) => setAgain(e.target.value)} placeholder={t('setup.againPh')} aria-label={t('setup.againPh')} autoComplete="new-password" required />
          </div>
          <p className="setup-token-why">{t('setup.tokenWhy')}</p>
          <PasswordPill value={token} onChange={setToken} autoComplete="off" placeholder={t('setup.tokenPh')} busy={busy} shake={shake} />
        </>
      )}
      {!needsToken && (
        <PasswordPill value={again} onChange={setAgain} autoComplete="new-password" placeholder={t('setup.againPh')} busy={busy} shake={shake} />
      )}
      <p className={`mac-hint ${error ? 'err' : ''}`} role="alert">
        {error || (needsToken ? '' : t('setup.localHint'))}
      </p>
    </form>
  );
}
