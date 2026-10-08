import { useEffect, useState, type FormEvent } from 'react';
import { Logo, LogoMark } from '../components/Logo';
import { accountsMode, login, setup } from '../api/client';
import { resetPassword } from '../api/account';
import { useClock } from '../lib/hooks';
import { fmtDate, fmtTime } from '../lib/time';
import { usePrefs } from '../state/prefs';

export type AuthMode = 'setup' | 'login' | 'locked';

export function AuthScreen({ mode, lockedUser }: { mode: AuthMode; lockedUser?: string }) {
  const now = useClock(1000);
  const [view, setView] = useState<'main' | 'forgot'>('main');

  return (
    <div className={`auth-screen ${mode === 'locked' ? 'lock' : ''}`}>
      <div className="auth-clock">
        <div className="clock-time">{fmtTime(now, usePrefs.getState(), false)}</div>
        <div className="clock-date">{fmtDate(now, usePrefs.getState())}</div>
      </div>
      {view === 'forgot' ? (
        <ForgotPassword initialUser={lockedUser} onBack={() => setView('main')} />
      ) : (
        <MainForm mode={mode} lockedUser={lockedUser} onForgot={() => setView('forgot')} />
      )}
    </div>
  );
}

function MainForm({ mode, lockedUser, onForgot }: { mode: AuthMode; lockedUser?: string; onForgot: () => void }) {
  const [token, setToken] = useState('');
  const [username, setUsername] = useState(lockedUser ?? '');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const isSetup = mode === 'setup';
  const [linux, setLinux] = useState(false); // signing in with this machine's Linux users
  useEffect(() => {
    void accountsMode().then((m) => setLinux(m === 'system'));
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    if (isSetup) {
      if (!token.trim()) return setError('Enter the setup token printed in the NoCapOS console.');
      if (password.length < 10) return setError('Use a password of at least 10 characters.');
    }
    setBusy(true);
    const err = isSetup ? await setup(token.trim(), username.trim(), password) : await login(username.trim(), password);
    setBusy(false);
    if (err) {
      setError(err);
      setPassword('');
    }
  };

  return (
    <form className="auth-card glass" onSubmit={submit}>
      {mode === 'locked' ? (
        <>
          <span className="avatar xl">{(lockedUser ?? '?').slice(0, 1).toUpperCase()}</span>
          <h1>{lockedUser}</h1>
          <p className="muted">Locked. Enter your password to continue.</p>
        </>
      ) : (
        <>
          <Logo size={76} />
          <h1>{isSetup ? 'Welcome' : 'Sign in'}</h1>
          <p className="muted">
            {isSetup
              ? 'Create the administrator account. The one-time setup token is printed in the NoCapOS console / log.'
              : linux
                ? "Sign in with this machine's Linux account."
                : 'Your self-hosted cloud, apps and agents.'}
          </p>
        </>
      )}

      {isSetup && (
        <label>
          Setup token
          <input
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            placeholder="xxxxxxxx-xxxxxxxx-xxxxxxxx-xxxxxxxx"
            autoFocus
          />
        </label>
      )}
      {mode !== 'locked' && (
        <label>
          Username
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
            minLength={isSetup ? 3 : 1}
            maxLength={32}
            autoFocus={!isSetup}
          />
        </label>
      )}
      <label>
        Password
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete={isSetup ? 'new-password' : 'current-password'}
          required
          autoFocus={mode === 'locked'}
        />
      </label>
      <p className="error" role="alert">
        {error}
      </p>
      <button type="submit" disabled={busy}>
        {busy ? 'Please wait…' : isSetup ? 'Create administrator' : mode === 'locked' ? 'Unlock' : 'Sign in'}
      </button>
      {mode !== 'setup' && (
        <button type="button" className="link" onClick={onForgot}>
          Forgot password?
        </button>
      )}
      {mode === 'locked' && (
        <button type="button" className="link" onClick={() => window.location.reload()}>
          Switch user
        </button>
      )}
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
