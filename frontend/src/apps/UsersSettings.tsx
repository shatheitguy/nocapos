import { tr } from '../i18n';
import { YourSessions } from './SessionsPanel';
import { useRef, useState } from 'react';
import { removeAvatar, uploadAvatar, useAvatar } from '../lib/avatar';
import { UserAvatar } from '../components/UserAvatar';
import { getUser } from '../api/client';
import { logOutClean } from '../lib/signOut';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { Row, Section } from './Personalize';
import { ManagePeople } from './UsersPeople';

// Settings → Users & Groups. On a native Linux install these are the machine's
// real Linux accounts; in Docker / on Windows they're NoCapOS accounts. The
// administrators' people and groups manager is in UsersPeople.tsx.

/** Your profile photo: click to change it; it shows on the login screen too. */
function PhotoPicker({ username }: { username: string }) {
  const input = useRef<HTMLInputElement>(null);
  const has = useAvatar((s) => !!s.url);
  const [busy, setBusy] = useState(false);
  const pick = async (file: File | undefined) => {
    if (!file) return;
    setBusy(true);
    const err = await uploadAvatar(file, username).catch(() => tr('su.photoBad'));
    setBusy(false);
    if (err) toast('error', tr('su.photoFail'), err);
    else toast('success', tr('sp.photoUpdated'), tr('sp.photoUpdatedBody'));
  };
  return (
    <div className="photo-picker">
      <button type="button" className="photo-btn" title={tr('su.changePhoto')} aria-label={tr('su.changePhoto')} disabled={busy} onClick={() => input.current?.click()}>
        <UserAvatar name={username} size="xl" />
        <span className="photo-edit">{busy ? <span className="spinner sm" /> : <Icon name="edit" size={13} />}</span>
      </button>
      {has && (
        <button type="button" className="link small" onClick={() => void removeAvatar(username)}>
          {tr('su.removePhoto')}
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

/** Your own account: Settings → Account (and the top of Users for non-admins). */
export function YourAccount() {
  const user = getUser();
  if (!user) return null;
  return (
    <>
      <Section title={tr('su.account')}>
        <div className="settings-row account-card">
          <PhotoPicker username={user.username} />
          <span className="settings-row-text">
            <span className="account-name">{user.username}</span>
            <span className="settings-row-hint">
              {tr('su.memberSince', { role: tr(user.role === 'admin' ? 'set.admin' : 'set.standardUser'), date: new Date(user.created_at).toLocaleDateString() })}
            </span>
          </span>
        </div>
        <Row label={tr('su.signOut')} hint={tr('su.signOutHint')}>
          <button type="button" className="ghost small danger" onClick={() => void logOutClean()}>
            <Icon name="logout" size={13} /> {tr('su.signOut')}
          </button>
        </Row>
      </Section>
      <YourSessions />
    </>
  );
}

/** Settings → Users. `withAccount` = false leaves out your own account card (it has its own page). */
export function UsersSettings({ isAdmin, withAccount = true }: { isAdmin: boolean; withAccount?: boolean }) {
  if (!isAdmin) {
    return (
      <div className="stack settings-page">
        <YourAccount />
        <p className="settings-tip">{tr('ug.nonAdmin')}</p>
      </div>
    );
  }
  return (
    <div className="stack settings-page ug-host">
      {withAccount && <YourAccount />}
      <ManagePeople />
    </div>
  );
}
