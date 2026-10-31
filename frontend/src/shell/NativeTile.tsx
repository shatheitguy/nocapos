import { useEffect, type MouseEvent as RMouseEvent } from 'react';
import type { NativeApp } from '../api/nativeApps';
import { openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import type { MenuItem } from '../components/ContextMenu';
import { pressApp } from '../state/appDrag';
import { useDesktopIcons } from '../state/desktopIcons';
import { ensureNativeIcon, launchNativeApp, nativeIconApp, nativeKey, uninstallNativeApp, useNativeIcons } from '../state/nativeApps';

/** A Linux app's real icon (loaded through NoCapOS), or a neutral tile until it arrives. */
export function NativeAppIcon({ app, size = 44 }: { app: { id: string }; size?: number }) {
  const url = useNativeIcons((s) => s.urls[app.id]);
  useEffect(() => ensureNativeIcon(app.id), [app.id]);
  return <AppIcon app={nativeIconApp(app, url)} size={size} />;
}

/** Menu for a Linux app: open, desktop shortcut on/off, and (admins) the App Store page. */
export function nativeMenuItems(app: NativeApp, isAdmin: boolean, after?: () => void): MenuItem[] {
  const desk = useDesktopIcons.getState();
  const key = nativeKey(app.id);
  const items: MenuItem[] = [
    {
      label: 'Open',
      icon: 'launcher',
      onClick: () => {
        void launchNativeApp(app);
        after?.();
      },
    },
    desk.has(key)
      ? { label: 'Remove from Desktop', icon: 'desktop', onClick: () => desk.remove(key) }
      : { label: 'Add to Desktop', icon: 'desktop', onClick: () => desk.place(key) },
  ];
  if (isAdmin) {
    items.push(
      {
        label: 'Show in App Store',
        icon: 'store',
        onClick: () => {
          openApp('appcenter', { props: { native: app.id } });
          after?.();
        },
      },
      { label: 'Remove app…', icon: 'trash', danger: true, onClick: () => void uninstallNativeApp(app) },
    );
  }
  return items;
}

/** One installed Linux app in Launchpad: click opens it, drag puts a shortcut on the desktop. */
export function NativeTile({ app, isAdmin, onOpened, onMenu, size = 56 }: {
  app: NativeApp;
  isAdmin: boolean;
  onOpened?: () => void;
  onMenu: (e: RMouseEvent, items: MenuItem[]) => void;
  size?: number;
}) {
  return (
    <button
      type="button"
      className="launcher-app"
      data-scope="launchpad"
      title={`Open ${app.name} · drag it to the Desktop`}
      onPointerDown={(e) => pressApp(e, nativeKey(app.id), 'launchpad', { touchHold: 350 })}
      onClick={() => {
        void launchNativeApp(app);
        onOpened?.();
      }}
      onContextMenu={(e) => {
        e.preventDefault();
        onMenu(e, nativeMenuItems(app, isAdmin, onOpened));
      }}
    >
      <NativeAppIcon app={app} size={size} />
      <span>{app.name}</span>
    </button>
  );
}
