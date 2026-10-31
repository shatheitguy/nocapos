import { useEffect, useState, type MouseEvent as RMouseEvent } from 'react';
import { APPS, canMultiWindow, openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { FolderIcon } from '../components/FolderIcon';
import { dropHint, pressApp, useAppDrag } from '../state/appDrag';
import { iconCell, useDesktopIcons } from '../state/desktopIcons';
import { folderIdOf, folderKey, isFolderKey, ungroup, useFolders } from '../state/folders';
import { usePrefs } from '../state/prefs';
import { isStoreKey, launchStoreApp, shownOnHome, storeIdOf, uninstallStoreApp, useStoreApps } from '../state/storeApps';
import { isNativeKey, launchNativeApp, nativeIdOf, useNativeApps } from '../state/nativeApps';
import { FolderView } from './FolderView';
import { NativeAppIcon, nativeMenuItems } from './NativeTile';
import { appMenuItems } from './Shelf';

/** App shortcuts and folders on the desktop: drag anywhere, double-click to open, drop an app on another to make a folder. */
export function DesktopIcons({ isAdmin }: { isAdmin: boolean }) {
  const icons = useDesktopIcons((s) => s.icons);
  const folders = useFolders((s) => s.folders);
  const storeApps = useStoreApps((s) => s.apps);
  const storeLoaded = useStoreApps((s) => s.loaded);
  const nativeApps = useNativeApps((s) => s.apps);
  const nativeLoaded = useNativeApps((s) => s.loaded);
  usePrefs((s) => s.iconSize); // re-render when the icon size changes
  const cell = iconCell();
  const dragging = useAppDrag((s) => (s.drag?.source === 'desktop' ? s.drag.appId : null));
  const target = useAppDrag((s) => {
    if (!s.drag || !dropHint(s.drag, s.over)) return null;
    if (s.over?.kind === 'app' && s.over.scope === 'desktop') return s.over.appId;
    if (s.over?.kind === 'folder' && s.over.scope === 'desktop') return folderKey(s.over.folderId);
    return null;
  });
  const [selected, setSelected] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const [open, setOpen] = useState<{ id: string; x: number; y: number } | null>(null);

  // Clicking anywhere else clears the selection; Enter opens the selected icon.
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (!(e.target as HTMLElement).closest?.('.desktop-icon')) setSelected(null);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && selected && document.activeElement?.closest('.desktop-icon')) {
        const el = document.activeElement as HTMLElement;
        activate(selected, el.getBoundingClientRect());
      }
    };
    window.addEventListener('pointerdown', onDown, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('keydown', onKey);
    };
  });

  const folderById = (key: string) => folders.find((f) => f.id === folderIdOf(key));
  const storeById = (key: string) => storeApps.find((a) => a.id === storeIdOf(key) && shownOnHome(a));
  const nativeById = (key: string) => nativeApps.find((a) => a.id === nativeIdOf(key));
  const visible = icons.filter((i) =>
    isFolderKey(i.appId)
      ? !!folderById(i.appId)
      : isStoreKey(i.appId)
        ? !!storeById(i.appId)
        : isNativeKey(i.appId)
          ? !!nativeById(i.appId)
          : APPS[i.appId] && !APPS[i.appId].hidden && (isAdmin || !APPS[i.appId].adminOnly),
  );

  // An app that was uninstalled (or that this person may no longer use) takes its shortcut with it.
  useEffect(() => {
    if (!storeLoaded) return;
    const gone = icons.filter((i) => isStoreKey(i.appId) && !storeApps.some((a) => a.id === storeIdOf(i.appId) && shownOnHome(a)));
    gone.forEach((i) => useDesktopIcons.getState().remove(i.appId));
  }, [icons, storeApps, storeLoaded]);
  useEffect(() => {
    if (!nativeLoaded) return;
    const gone = icons.filter((i) => isNativeKey(i.appId) && !nativeApps.some((a) => a.id === nativeIdOf(i.appId)));
    gone.forEach((i) => useDesktopIcons.getState().remove(i.appId));
  }, [icons, nativeApps, nativeLoaded]);

  /** Open an app, or open a folder next to its icon. */
  const activate = (key: string, r: DOMRect, e?: RMouseEvent) => {
    if (isFolderKey(key)) setOpen({ id: folderIdOf(key), x: r.left + r.width / 2, y: r.bottom + 6 });
    else if (isStoreKey(key)) {
      const app = storeById(key);
      if (app) launchStoreApp(app);
    }
    else if (isNativeKey(key)) {
      const app = nativeById(key);
      if (app) void launchNativeApp(app);
    }
    else if (e?.shiftKey && canMultiWindow(key)) openApp(key, { newWindow: true });
    else openApp(key);
  };

  const folderMenu = (key: string, r: DOMRect): MenuItem[] => [
    { label: 'Open', icon: 'folder', onClick: () => activate(key, r) },
    { label: 'Remove from Desktop', icon: 'desktop', onClick: () => useDesktopIcons.getState().remove(key) },
    { label: 'Ungroup', icon: 'close', danger: true, onClick: () => ungroup(folderIdOf(key)) },
  ];

  const storeMenu = (key: string, r: DOMRect): MenuItem[] => {
    const app = storeById(key);
    return [
      { label: 'Open', icon: 'launcher', onClick: () => activate(key, r) },
      { label: 'Remove from Desktop', icon: 'desktop', onClick: () => useDesktopIcons.getState().remove(key) },
      ...(isAdmin && app
        ? [
            { label: 'Show in App Store', icon: 'store' as const, onClick: () => openApp('appcenter', { props: { app: app.id } }) },
            ...(app.installed ? [{ label: 'Uninstall…', icon: 'trash' as const, danger: true, onClick: () => void uninstallStoreApp(app) }] : []),
          ]
        : []),
    ];
  };

  return (
    <div className="desktop-icons" data-drop="desktop">
      {visible.map((i) => {
        const folder = isFolderKey(i.appId) ? folderById(i.appId) : undefined;
        const store = isStoreKey(i.appId) ? storeById(i.appId) : undefined;
        const native = isNativeKey(i.appId) ? nativeById(i.appId) : undefined;
        const title = folder ? folder.name : store ? store.name : native ? native.name : APPS[i.appId].title;
        return (
          <button
            key={i.appId}
            type="button"
            className={`desktop-icon ${selected === i.appId ? 'selected' : ''} ${dragging === i.appId ? 'dragging' : ''} ${target === i.appId ? 'drop-into' : ''}`}
            style={{ left: i.x, top: i.y, width: cell.w }}
            title={`${title} — double-click to open`}
            // Installed apps can't go into folders, so they are no drop target.
            data-drop={folder ? 'folder' : store || native ? undefined : 'app'}
            data-app={folder || store || native ? undefined : i.appId}
            data-folder={folder?.id}
            data-scope="desktop"
            onPointerDown={(e) => {
              setSelected(i.appId);
              pressApp(e, i.appId, 'desktop');
            }}
            onDoubleClick={(e) => activate(i.appId, e.currentTarget.getBoundingClientRect(), e)}
            onContextMenu={(e) => {
              e.preventDefault();
              e.stopPropagation();
              setSelected(i.appId);
              const r = e.currentTarget.getBoundingClientRect();
              setMenu({
                x: e.clientX,
                y: e.clientY,
                items: folder ? folderMenu(i.appId, r) : store ? storeMenu(i.appId, r) : native ? nativeMenuItems(native, isAdmin) : appMenuItems(i.appId),
              });
            }}
          >
            {folder ? (
              <FolderIcon folder={folder} size={cell.icon} />
            ) : native ? (
              <NativeAppIcon app={native} size={cell.icon} />
            ) : (
              <AppIcon app={store ?? APPS[i.appId]} size={cell.icon} />
            )}
            <span className="desktop-icon-label">{title}</span>
          </button>
        );
      })}
      {open && <FolderView folderId={open.id} isAdmin={isAdmin} scope="desktop" at={{ x: open.x, y: open.y }} onClose={() => setOpen(null)} />}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}
