import { useRef, type RefObject } from 'react';
import type { User } from '../api/types';
import { Icon } from '../components/Icon';
import { UserAvatar } from '../components/UserAvatar';
import { useDismiss } from '../lib/hooks';
import { useSystem } from '../state/system';
import { accountMenuItems } from './accountMenu';

/**
 * The account menu behind your photo: a panel in the same place and style as
 * the Control Center, with who you are at the top and the account actions
 * below (the same items as the menu in the corner of a Mac).
 */
export function AccountPanel({
  user,
  anchor,
  onClose,
  onLock,
  side = 'right',
}: {
  user: User;
  anchor: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
  onLock: () => void;
  /** Which edge it opens from: under the menu bar's photo (left) or above the tray (right). */
  side?: 'left' | 'right';
}) {
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(true, onClose, ref, anchor);
  const host = useSystem((s) => s.info?.host.hostname);
  const items = accountMenuItems(user, onLock);
  return (
    <div ref={ref} className={`popover quick-settings acct-panel ${side}`} role="menu" aria-label={`${user.username} menu`}>
      <div className="qs-user">
        <UserAvatar name={user.username} size="lg" />
        <div>
          <b>{user.username}</b>
          <div className="muted small">
            {user.role === 'admin' ? 'Administrator' : 'User'}
            {host ? ` · ${host}` : ''}
          </div>
        </div>
      </div>
      <div className="acct-list">
        {items.map((it, i) => [
          it.divider ? <hr key={`d${i}`} className="acct-sep" /> : null,
          <button
            key={i}
            type="button"
            role="menuitem"
            className={`acct-item ${it.danger ? 'danger' : ''}`}
            disabled={it.disabled}
            onClick={() => {
              onClose();
              it.onClick();
            }}
          >
            {it.icon && <Icon name={it.icon} size={16} />}
            {it.hint ? (
              <span className="acct-text">
                <span>{it.label}</span>
                <small>{it.hint}</small>
              </span>
            ) : (
              <span>{it.label}</span>
            )}
          </button>,
        ])}
      </div>
    </div>
  );
}
