import { useEffect, useRef, useState, type FormEvent } from 'react';
import { removeAvatar, uploadAvatar, useAvatar } from '../lib/avatar';
import { UserAvatar } from '../components/UserAvatar';
import { getUser, logout } from '../api/client';
import { usersApi, type PeopleList, type Person } from '../api/users';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { useWM } from '../state/windows';
import { Choice, Row, Section } from './Personalize';

// Settings → Users & Roles. On a native Linux install these are the machine's
// real Linux accounts; in Docker / on Windows they're NoCapOS accounts.

/** Your profile photo: click to change it; it shows on the login screen too. */
function PhotoPicker({ username }: { username: string }) {
  const input = useRef<HTMLInputElement>(null);
  const has = useAvatar((s) => !!s.url);
  const [busy, setBusy] = useState(false);
  const pick = async (file: File | undefined) => {
    if (!file) return;
    setBusy(true);
    const err = await uploadAvatar(file, username).catch(() => 'That image could not be read.');
    setBusy(false);
    if (err) toast('error', 'Could not set your photo', err);
    else toast('success', 'Profile photo updated', 'It also shows on the login screen of this device.');
  };
  return (
    <div className="photo-picker">
      <button type="button" className="photo-btn" title="Change photo" disabled={busy} onClick={() => input.current?.click()}>
        <UserAvatar name={username} size="xl" />
        <span className="photo-edit">{busy ? <span className="spinner sm" /> : <Icon name="edit" size={13} />}</span>
      </button>
      {has && (
        <button type="button" className="link small" onClick={() => void removeAvatar(username)}>
          Remove
        </button>
      )}
      <input
        ref={input}
        type="file"
        accept="image/png,image/jpeg,image/webp,image/gif"
        hidden
        onChange={(e) => {
          void pick(e.target.files?.[0]);
          e.target.value = '';
        }}
      />
    </div>
  );
}

/** Your own account: shown to everyone at the top of Users & Roles. */
function YourAccount({ onSecurity }: { onSecurity: () => void }) {
  const user = getUser();
  if (!user) return null;
  const closeOthers = () => {
    const wm = useWM.getState();
    for (const w of wm.windows) if (w.appId !== 'settings') wm.close(w.id);
  };
  return (
    <Section title="Your account">
      <div className="settings-row account-card">
        <PhotoPicker username={user.username} />
        <span className="settings-row-text">
          <span className="account-name">{user.username}</span>
          <span className="settings-row-hint">
            {user.role === 'admin' ? 'Administrator' : 'Standard user'} · member since {new Date(user.created_at).toLocaleDateString()}
          </span>
        </span>
      </div>
      <Row label="Password & two-factor" hint="Change your password or set up an authenticator app">
        <button type="button" className="ghost small" onClick={onSecurity}>
          <Icon name="shield" size={13} /> Open Security
        </button>
      </Row>
      <Row label="Close other windows" hint="Tidy up: closes every window except Settings">
        <button type="button" className="ghost small" onClick={closeOthers}>
          Close windows
        </button>
      </Row>
      <Row label="Sign out" hint="Sessions last up to 30 days; signing out ends this one on the server right away">
        <button type="button" className="ghost small danger" onClick={() => void logout()}>
          <Icon name="logout" size={13} /> Sign out
        </button>
      </Row>
    </Section>
  );
}

export function UsersSettings({ isAdmin, onSecurity }: { isAdmin: boolean; onSecurity: () => void }) {
  if (!isAdmin) {
    return (
      <div className="stack settings-page">
        <YourAccount onSecurity={onSecurity} />
        <p className="settings-tip">Only administrators can add people or change roles.</p>
      </div>
    );
  }
  return <ManagePeople onSecurity={onSecurity} />;
}

function ManagePeople({ onSecurity }: { onSecurity: () => void }) {
  const [data, setData] = useState<PeopleList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [resetFor, setResetFor] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = async () => {
    const r = await usersApi.list();
    if (r.ok) setData(r.data);
    else setError(r.error ?? 'Could not load users');
  };
  useEffect(() => {
    void load();
  }, []);

  const act = async (name: string, fn: () => Promise<{ ok: boolean; error?: string }>, done: string) => {
    setBusy(name);
    const r = await fn();
    setBusy(null);
    if (!r.ok) {
      toast('error', 'That didn’t work', r.error);
      return false;
    }
    toast('success', done);
    await load();
    return true;
  };

  const remove = async (p: Person) => {
    const ok = await confirmDialog({
      title: `Delete ${p.username}?`,
      message: linux
        ? `The Linux account "${p.username}" is removed from this machine and signed out of NoCapOS.`
        : `"${p.username}" can no longer sign in, and their chats and settings in NoCapOS are deleted.`,
      confirmLabel: 'Delete user',
      danger: true,
    });
    if (!ok) return;
    let removeHome = false;
    if (linux && p.home) {
      removeHome = await confirmDialog({
        title: `Also delete ${p.home}?`,
        message: `Their home folder and every file in it will be permanently erased. Choose Keep files to leave ${p.home} on disk.`,
        confirmLabel: 'Delete files too',
        danger: true,
      });
    }
    await act(p.username, () => usersApi.remove(p.username, removeHome), `${p.username} was deleted`);
  };

  if (error) return <p className="error">{error}</p>;
  if (!data) return <p className="muted">Loading users…</p>;
  const linux = data.mode === 'system';

  return (
    <div className="stack settings-page">
      <YourAccount onSecurity={onSecurity} />
      <div className={`users-mode ${linux ? 'linux' : ''}`}>
        <Icon name={linux ? 'terminal' : 'users'} size={18} />
        <div>
          <b>{linux ? "This machine's Linux accounts" : 'NoCapOS accounts'}</b>
          <span>
            {linux
              ? 'Everyone here is a real Linux user. Administrators are in the sudo/wheel group; changes run useradd, usermod and chpasswd on the host.'
              : 'NoCapOS is running in Docker or on Windows, so it keeps its own accounts. On a native Linux install these become the machine’s real Linux users.'}
          </span>
        </div>
      </div>

      <Section title="People">
        {data.users.map((p) => (
          <div key={p.username} className={`settings-row user-row ${p.disabled ? 'dimmed' : ''}`}>
            <span className="user-avatar">{p.username.slice(0, 1).toUpperCase()}</span>
            <span className="settings-row-text">
              <span className="settings-row-label">
                {p.full_name || p.username}
                {p.full_name && <span className="muted"> · {p.username}</span>}
              </span>
              <span className="user-badges">
                <span className={`badge ${p.role === 'admin' ? 'admin' : ''}`}>{p.role === 'admin' ? 'Administrator' : 'Standard'}</span>
                {p.root && <span className="badge root">root</span>}
                {p.you && <span className="badge you">You</span>}
                {p.disabled && <span className="badge off">Disabled</span>}
                {p.totp && <span className="badge">2FA</span>}
                {linux && p.uid >= 0 && <span className="muted small">uid {p.uid} · {p.home}</span>}
              </span>
              {resetFor === p.username && (
                <ResetPassword
                  name={p.username}
                  busy={busy === p.username}
                  onCancel={() => setResetFor(null)}
                  onSave={async (pw) => {
                    if (await act(p.username, () => usersApi.setPassword(p.username, pw), `Password changed for ${p.username}`)) setResetFor(null);
                  }}
                />
              )}
            </span>
            <div className="settings-row-control user-actions">
              <select
                className="select"
                aria-label={`Role for ${p.username}`}
                value={p.role}
                disabled={p.root || p.you || busy === p.username}
                title={p.root ? 'root is always an administrator' : p.you ? "You can't change your own role" : undefined}
                onChange={(e) => void act(p.username, () => usersApi.setRole(p.username, e.target.value as Person['role']), `${p.username} is now ${e.target.value === 'admin' ? 'an administrator' : 'a standard user'}`)}
              >
                <option value="admin">Administrator</option>
                <option value="user">Standard</option>
              </select>
              <button type="button" className="icon-btn neutral" title="Reset password" aria-label={`Reset password for ${p.username}`}
                onClick={() => setResetFor(resetFor === p.username ? null : p.username)}>
                <Icon name="key" size={15} />
              </button>
              <button type="button" className="icon-btn neutral" disabled={p.root || p.you || busy === p.username}
                title={p.disabled ? 'Enable account' : 'Disable account'} aria-label={`${p.disabled ? 'Enable' : 'Disable'} ${p.username}`}
                onClick={() => void act(p.username, () => usersApi.setDisabled(p.username, !p.disabled), p.disabled ? `${p.username} can sign in again` : `${p.username} is disabled and signed out`)}>
                <Icon name={p.disabled ? 'check' : 'lock'} size={15} />
              </button>
              <button type="button" className="icon-btn" disabled={p.root || p.you || busy === p.username} title="Delete user" aria-label={`Delete ${p.username}`}
                onClick={() => void remove(p)}>
                <Icon name="trash" size={15} />
              </button>
            </div>
          </div>
        ))}
      </Section>

      <AddPerson linux={linux} onAdded={load} />
    </div>
  );
}

function ResetPassword({ name, busy, onSave, onCancel }: { name: string; busy: boolean; onSave: (pw: string) => void; onCancel: () => void }) {
  const [pw, setPw] = useState('');
  return (
    <form
      className="user-reset"
      onSubmit={(e) => {
        e.preventDefault();
        if (pw.length >= 10) onSave(pw);
      }}
    >
      <input type="password" autoFocus autoComplete="new-password" placeholder={`New password for ${name}`} value={pw} onChange={(e) => setPw(e.target.value)} />
      <button type="submit" className="small" disabled={busy || pw.length < 10}>
        Save
      </button>
      <button type="button" className="ghost small" onClick={onCancel}>
        Cancel
      </button>
      {pw.length > 0 && pw.length < 10 && <span className="muted small">At least 10 characters</span>}
    </form>
  );
}

function AddPerson({ linux, onAdded }: { linux: boolean; onAdded: () => void }) {
  const [username, setUsername] = useState('');
  const [fullName, setFullName] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [role, setRole] = useState<'admin' | 'user'>('user');
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const nameOk = linux ? /^[a-z_][a-z0-9_-]{0,31}$/.test(username) : /^[a-zA-Z0-9][a-zA-Z0-9._-]{2,31}$/.test(username);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    if (!nameOk) return setErr(linux ? 'Use lowercase letters, digits, - or _, starting with a letter.' : 'Use 3–32 letters, digits, ., _ or -.');
    if (pw.length < 10) return setErr('Passwords need at least 10 characters.');
    if (pw !== pw2) return setErr("The passwords don't match.");
    setBusy(true);
    const r = await usersApi.create({ username, full_name: fullName || undefined, password: pw, role });
    setBusy(false);
    if (!r.ok) return setErr(r.error ?? 'Could not add that person');
    toast('success', `${username} was added`, linux ? 'A Linux account and home folder were created.' : undefined);
    setUsername('');
    setFullName('');
    setPw('');
    setPw2('');
    setRole('user');
    onAdded();
  };

  return (
    <Section title="Add a person" hint={linux ? 'Creates a real Linux account with a home folder and a bash shell.' : undefined}>
      <form className="user-add" onSubmit={(e) => void submit(e)}>
        <Row label="User name" hint={linux ? 'Lowercase, used to sign in to NoCapOS and to Linux' : 'Used to sign in'}>
          <input value={username} autoComplete="off" spellCheck={false} placeholder={linux ? 'alex' : 'alex'}
            onChange={(e) => setUsername(linux ? e.target.value.toLowerCase() : e.target.value)} />
        </Row>
        {linux && (
          <Row label="Full name" hint="Optional">
            <input value={fullName} placeholder="Alex Smith" onChange={(e) => setFullName(e.target.value)} />
          </Row>
        )}
        <Row label="Password" hint="At least 10 characters">
          <input type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
        </Row>
        <Row label="Confirm password">
          <input type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} />
        </Row>
        <Row label="Role" hint={role === 'admin' ? (linux ? 'Full control of NoCapOS, and sudo on this machine' : 'Full control of NoCapOS') : 'Uses apps; can’t manage the system'}>
          <Choice value={role} onChange={setRole} options={[{ id: 'user', label: 'Standard' }, { id: 'admin', label: 'Administrator' }]} />
        </Row>
        <div className="user-add-foot">
          {err && <span className="error small">{err}</span>}
          <button type="submit" disabled={busy}>
            <Icon name="plus" size={14} /> {busy ? 'Adding…' : 'Add person'}
          </button>
        </div>
      </form>
    </Section>
  );
}
