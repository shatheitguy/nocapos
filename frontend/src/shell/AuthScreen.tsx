import { useEffect, useState, type FormEvent } from 'react';
import { accountsMode, login, setup } from '../api/client';
import { resetPassword } from '../api/account';
import { Icon } from '../components/Icon';
import { Logo, LogoMark } from '../components/Logo';
import { UserAvatar } from '../components/UserAvatar';
import { forgetLastUser, lastUser } from '../lib/avatar';
import { useClock } from '../lib/hooks';
import { fmtTime } from '../lib/time';
import { usePrefs } from '../state/prefs';

export type AuthMode = 'setup' | 'login' | 'locked';

/** macOS-style login / lock screen: big clock, user photo, password pill. */
export function AuthScreen({ mode, lockedUser }: { mode: AuthMode; lockedUser?: string }) {
  const now = useClock(1000);
  const tp = usePrefs.getState();
  const [view, setView] = useState<'main' | 'forgot'>('main');

  return (
    <div className={`auth-screen mac ${mode === 'locked' ? 'lock' : ''}`}>
      <div className="mac-clock">
        <div className="mac-date">
          {now.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long', ...(tp.timeZone ? { timeZone: tp.timeZone } : {}) })}
        </div>
        <div className="mac-time">{fmtTime(now, { ...tp, clockSeconds: false }, false).replace(/\s?[AP]M$/i, '')}</div>
      </div>
      {view === 'forgot' ? (
        <ForgotPassword initialUser={lockedUser ?? lastUser()?.username} onBack={() => setView('main')} />
      ) : mode === 'setup' ? (
        <SetupForm />
      ) : (
        <LoginForm mode={mode} lockedUser={lockedUser} onForgot={() => setView('forgot')} />
      )}
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
}: {
  value: string;
  onChange: (v: string) => void;
  autoFocus?: boolean;
  autoComplete: string;
  placeholder: string;
  busy: boolean;
  shake: number;
}) {
  const [shown, setShown] = useState(false);
  return (
    <div className="mac-pass-wrap">
      <div key={shake} className={`mac-pill pass ${shake ? 'shake' : ''}`}>
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
        <button type="submit" className="mac-go" aria-label="Sign in" disabled={busy || !value}>
          {busy ? <span className="spinner sm" /> : <Icon name="chevronRight" size={16} />}
        </button>
      </div>
    </div>
  );
}

function LoginForm({ mode, lockedUser, onForgot }: { mode: AuthMode; lockedUser?: string; onForgot: () => void }) {
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
  useEffect(() => {
    void accountsMode().then((m) => setLinux(m === 'system'));
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = (known ?? username).trim();
    if (!name || !password) return;
    setError('');
    setBusy(true);
    const err = await login(name, password);
    setBusy(false);
    if (err) {
      setError(err);
      setPassword('');
      setShake((n) => n + 1);
    }
  };

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
            autoFocus
            spellCheck={false}
            required
          />
        </div>
      )}
      <PasswordPill
        value={password}
        onChange={setPassword}
        autoFocus={!!known}
        autoComplete="current-password"
        placeholder={mode === 'locked' ? 'Enter Password to unlock' : 'Enter Password'}
        busy={busy}
        shake={shake}
      />
      <p className={`mac-hint ${error ? 'err' : ''}`} role="alert">
        {error || (linux && !known ? "Use this computer's Linux account" : '')}
      </p>
      <button type="button" className="mac-link" onClick={onForgot}>
        Forgot password?
      </button>

      <div className="mac-actions">
        {mode === 'locked' ? (
          <MacAction icon="users" label="Switch User" onClick={() => window.location.reload()} />
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

function MacAction({ icon, label, onClick }: { icon: 'users' | 'user' | 'chevronLeft' | 'close'; label: string; onClick: () => void }) {
  return (
    <button type="button" className="mac-action" onClick={onClick}>
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
    if (!token.trim()) return setError('Enter the setup token printed in the NoCapOS console.');
    if (password.length < 10) {
      setShake((n) => n + 1);
      return setError('Use a password of at least 10 characters.');
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
      <p className="mac-sub">Create the administrator account. The one-time setup token is printed in the NoCapOS console / log.</p>
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

function ForgotPassword({ initialUser, onBack }: { initialUser?: string; onBack: () => void }) {
  const [username, setUsername] = useState(initialUser ?? '');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (password.length < 10) return setError('New password must be at least 10 characters.');
    if (password !== confirm) return setError('Passwords do not match.');
    if (code.trim().length !== 6) return setError('Enter the 6-digit code from your authenticator app.');
    setBusy(true);
    const err = await resetPassword(username.trim(), code.trim(), password);
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    setDone(true);
    setTimeout(onBack, 2200);
  };

  if (done) {
    return (
      <div className="auth-card glass">
        <LogoMark size={56} className="auth-mark" />
        <h1>Password changed</h1>
        <p className="muted">You can now sign in with your new password.</p>
        <button type="button" onClick={onBack}>
          Back to sign in
        </button>
      </div>
    );
  }

  return (
    <form className="auth-card glass" onSubmit={submit}>
      <h1>Reset password</h1>
      <p className="muted">
        Enter your username and the 6-digit code from your authenticator app, then choose a new password. (Only works if
        you set up two-factor authentication.)
      </p>
      <label>
        Username
        <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required autoFocus={!initialUser} />
      </label>
      <label>
        Authenticator code
        <input
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
          inputMode="numeric"
          placeholder="123456"
          autoFocus={!!initialUser}
        />
      </label>
      <label>
        New password
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
      </label>
      <label>
        Confirm password
        <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
      </label>
      <p className="error" role="alert">
        {error}
      </p>
      <button type="submit" disabled={busy}>
        {busy ? 'Please wait…' : 'Reset password'}
      </button>
      <button type="button" className="link" onClick={onBack}>
        Back
      </button>
    </form>
  );
}
