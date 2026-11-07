import type { User } from '../api/types';
import { tr } from '../i18n';
import { openApp } from '../apps/meta';
import type { MenuItem } from '../components/ContextMenu';
import { powerHost } from '../lib/hostActions';
import { logOutClean } from '../lib/signOut';
import { confirmDialog } from '../state/confirm';
import { useWM } from '../state/windows';

// The account menu behind the profile photo (menu bar and tray): the same
// items, in the same order, as the menu in the corner of a Mac.

async function quitAll() {
  const wm = useWM.getState();
  if (!wm.windows.length) return;
  const ok = await confirmDialog({
    title: tr('acct.closeAllTitle'),
    message: tr('acct.closeAllMsg', { n: wm.windows.length }),
    confirmLabel: tr('acct.closeAllOk'),
    danger: true,
  });
  if (ok) for (const w of useWM.getState().windows) useWM.getState().close(w.id);
}

export function accountMenuItems(user: User, onLock: () => void): MenuItem[] {
  const isAdmin = user.role === 'admin';
  return [
    { label: tr('acct.about'), icon: 'info', onClick: () => openApp('settings', { props: { section: 'about' } }) },
    { label: tr('acct.settings'), icon: 'settings', divider: true, onClick: () => openApp('settings') },
    ...(isAdmin ? [{ label: tr('acct.store'), icon: 'store' as const, onClick: () => openApp('appcenter') }] : []),
    { label: tr('acct.closeAll'), icon: 'close', divider: true, onClick: () => void quitAll() },
    ...(isAdmin
      ? [
          { label: tr('acct.restart'), icon: 'restart' as const, divider: true, onClick: () => void powerHost('reboot') },
          { label: tr('acct.shutdown'), icon: 'power' as const, onClick: () => void powerHost('shutdown') },
        ]
      : []),
    {
      label: tr('acct.lock'),
      hint: tr('acct.lockHint'),
      icon: 'lock',
      divider: true,
      onClick: onLock,
    },
    {
      label: tr('acct.logout', { name: user.username }),
      hint: tr('acct.logoutHint'),
      icon: 'logout',
      onClick: () => void logOutClean(),
    },
  ];
}
