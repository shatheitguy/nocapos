import { useEffect, useState, type FormEvent } from 'react';
import { accountsMode, login, loginCode, setup } from '../api/client';
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

function SetupForm() {
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [shake, setShake] = useState(0);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (!token.trim()) return setError('First up: enter the setup token printed in the NoCapOS console.');
    if (password.length < 10) {
      setShake((n) => n + 1);
      return setError('Too short — use a password of at least 10 characters.');
    }
    setBusy(true);
    const err = await setup(token.trim(), username.trim(), password);
    setBusy(false);
    if (err) {
      setError(err);
      setShake((n) => n + 1);
    }
  };

  return (
    <form className="mac-login setup" onSubmit={submit}>
      <Logo size={64} />
      <p className="mac-sub">Welcome in. Create the administrator account — the one-time setup token is printed in the NoCapOS console / log.</p>
      <div className="mac-pill user">
        <input value={token} onChange={(e) => setToken(e.target.value)} placeholder="Setup token" aria-label="Setup token" autoComplete="off" spellCheck={false} autoFocus />
      </div>
      <div className="mac-pill user">
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder="Administrator name"
          aria-label="Administrator name"
          autoComplete="username"
          minLength={3}
          maxLength={32}
          spellCheck={false}
          required
        />
      </div>
      <PasswordPill value={password} onChange={setPassword} autoComplete="new-password" placeholder="Choose a password (10+ characters)" busy={busy} shake={shake} />
      <p className={`mac-hint ${error ? 'err' : ''}`} role="alert">
        {error}
      </p>
    </form>
  );
}
