import { logout } from '../api/client';
import type { User } from '../api/types';
import { openApp } from '../apps/meta';
import type { MenuItem } from '../components/ContextMenu';
import { powerHost } from '../lib/hostActions';
import { confirmDialog } from '../state/confirm';
import { useWM } from '../state/windows';

// The account menu behind the profile photo (menu bar and tray): the same
// items, in the same order, as the menu in the corner of a Mac.

async function quitAll() {
  const wm = useWM.getState();
  if (!wm.windows.length) return;
  const ok = await confirmDialog({
    title: 'Close all apps?',
    message: `Closes every open window (${wm.windows.length}). Anything unsaved in them is lost.`,
    confirmLabel: 'Close all',
    danger: true,
  });
  if (ok) for (const w of useWM.getState().windows) useWM.getState().close(w.id);
}

export function accountMenuItems(user: User, onLock: () => void): MenuItem[] {
  const isAdmin = user.role === 'admin';
  return [
    { label: 'About NoCapOS', icon: 'info', onClick: () => openApp('settings', { props: { section: 'about' } }) },
    { label: 'Settings…', icon: 'settings', divider: true, onClick: () => openApp('settings') },
    ...(isAdmin ? [{ label: 'App Store…', icon: 'store' as const, onClick: () => openApp('appcenter') }] : []),
    { label: 'Close All Apps…', icon: 'close', divider: true, onClick: () => void quitAll() },
    ...(isAdmin
      ? [
          { label: 'Restart…', icon: 'restart' as const, divider: true, onClick: () => void powerHost('reboot') },
          { label: 'Shut Down…', icon: 'power' as const, onClick: () => void powerHost('shutdown') },
        ]
      : []),
    { label: 'Lock Screen', icon: 'lock', divider: true, onClick: onLock },
    { label: `Log Out ${user.username}…`, icon: 'logout', onClick: () => void logout() },
  ];
}
