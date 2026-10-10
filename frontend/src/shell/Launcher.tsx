import { useEffect, useMemo, useRef, useState } from 'react';
import { canMultiWindow, openApp, visibleApps } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { FolderIcon } from '../components/FolderIcon';
import { Icon } from '../components/Icon';
import { dropHint, pressApp, useAppDrag } from '../state/appDrag';
import { useDesktopIcons } from '../state/desktopIcons';
import { folderKey, ungroup, useFolders } from '../state/folders';
import { FolderView } from './FolderView';
import { StoreAppGrid } from './StoreAppGrid';
import { shownOnHome, useStoreApps } from '../state/storeApps';
import { appMenuItems } from './Shelf';

const OPENS_WINDOW = new Set(['Open', 'Show', 'New window']);

export function Launcher({ isAdmin, onClose }: { isAdmin: boolean; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const [openFolder, setOpenFolder] = useState<string | null>(null);
  const folders = useFolders((s) => s.folders);
  const [jiggle, setJiggle] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const panel = useRef<HTMLDivElement>(null);

  // What the pointer is over while dragging, to highlight the folder-to-be.
  const target = useAppDrag((s) => {
    if (!s.drag || !dropHint(s.drag, s.over)) return null;
    return s.over?.kind === 'app' ? s.over.appId : s.over?.kind === 'folder' ? folderKey(s.over.folderId) : null;
  });

  const all = useMemo(() => visibleApps(isAdmin), [isAdmin]);
  const needle = q.trim().toLowerCase();
  // Searching lists every matching app, folders or not; otherwise folders come first.
  const matches = needle ? all.filter((a) => a.title.toLowerCase().includes(needle) || a.description.toLowerCase().includes(needle)) : [];
  const shownFolders = needle ? [] : folders.filter((f) => f.apps.some((id) => all.some((a) => a.id === id)));
  const inFolder = new Set(folders.flatMap((f) => f.apps));
  const apps = needle ? matches : all.filter((a) => !inFolder.has(a.id));
  const storeApps = useStoreApps((s) => s.apps).filter((a) => shownOnHome(a) && (!needle || `${a.name} ${a.tagline}`.toLowerCase().includes(needle)));

  useEffect(() => {
    input.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose(); // wiggle mode catches Esc first
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Launchpad stays open while an app is dragged around inside it, and gets out
  // of the way once the drag heads off the grid towards the Dock or Desktop.
  useEffect(
    () =>
      useAppDrag.subscribe(({ drag }) => {
        if (!drag || openFolder) return;
        const r = panel.current?.getBoundingClientRect();
        if (r && (drag.x < r.left - 24 || drag.x > r.right + 24 || drag.y < r.top - 24 || drag.y > r.bottom + 24)) onClose();
      }),
    [onClose, openFolder],
  );

  const launch = (id: string, newWindow = false) => {
    openApp(id, { newWindow: newWindow && canMultiWindow(id) });
    onClose();
  };

  // Items that put a window on screen also dismiss Launchpad.
  const appMenu = (appId: string): MenuItem[] =>
    appMenuItems(appId).map((it) =>
      OPENS_WINDOW.has(it.label)
        ? {
            ...it,
            onClick: () => {
              it.onClick();
              onClose();
            },
          }
        : it,
    );

  const folderMenu = (id: string): MenuItem[] => {
    const desk = useDesktopIcons.getState();
    const key = folderKey(id);
    return [
      { label: 'Open', icon: 'folder', onClick: () => setOpenFolder(id) },
      desk.has(key)
        ? { label: 'Remove from Desktop', icon: 'desktop', onClick: () => desk.remove(key) }
        : { label: 'Add to Desktop', icon: 'desktop', onClick: () => desk.place(key) },
      { label: 'Ungroup', icon: 'close', danger: true, onClick: () => ungroup(id) },
    ];
  };

  return (
    <div className="launcher" onPointerDown={(e) => e.target === e.currentTarget && (jiggle ? setJiggle(false) : onClose())}>
      <div
        className="launcher-panel"
        ref={panel}
        onPointerDownCapture={(e) => jiggle && !(e.target as Element).closest('.store-grid, .launcher-done') && setJiggle(false)}
      >
        <label className="search">
          <Icon name="search" size={18} />
          <input
            ref={input}
            value={q}
            placeholder="Search apps"
            aria-label="Search apps"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && apps[0] && launch(apps[0].id, e.shiftKey)}
          />
        </label>
        {isAdmin && storeApps.length > 0 && (
          <section className="launcher-section">
            <div className="launcher-section-head">
              <h3 className="section-label">Installed apps</h3>
              {jiggle ? (
                <button type="button" className="pill small launcher-done" onClick={() => setJiggle(false)}>Done</button>
              ) : (
                <span className="muted small">Click and hold to remove</span>
              )}
            </div>
            <StoreAppGrid apps={storeApps} jiggle={jiggle} setJiggle={setJiggle} size={56} onOpened={onClose} className="in-launcher" />
          </section>
        )}
        {isAdmin && storeApps.length > 0 && !needle && <h3 className="section-label launcher-builtin">NoCapOS apps</h3>}
        <div className="launcher-grid" data-drop="launchpad">
          {shownFolders.map((f) => (
            <button
              key={f.id}
              type="button"
              className={`launcher-app folder ${target === folderKey(f.id) ? 'drop-into' : ''}`}
              data-drop="folder"
              data-folder={f.id}
              data-scope="launchpad"
              title="Open folder · drag it to the Desktop"
              onPointerDown={(e) => pressApp(e, folderKey(f.id), 'launchpad')}
              onClick={() => setOpenFolder(f.id)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ x: e.clientX, y: e.clientY, items: folderMenu(f.id) });
              }}
            >
              <FolderIcon folder={f} size={56} />
              <span>{f.name}</span>
            </button>
          ))}
          {apps.map((a) => (
            <button
              key={a.id}
              type="button"
              className={`launcher-app ${target === a.id ? 'drop-into' : ''}`}
              data-drop="app"
              data-app={a.id}
              data-scope="launchpad"
              title="Drag onto another app to make a folder · Shift+click for a new window"
              onPointerDown={(e) => pressApp(e, a.id, 'launchpad')}
              onClick={(e) => launch(a.id, e.shiftKey)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ x: e.clientX, y: e.clientY, items: appMenu(a.id) });
              }}
            >
              <AppIcon app={a} size={56} />
              <span>{a.title}</span>
            </button>
          ))}
          {!apps.length && !shownFolders.length && !storeApps.length && <p className="muted">No apps match “{q}”.</p>}
        </div>
        <p className="launcher-tip muted small">Tip: drop an app onto another to make a folder, or drag it to the Dock or Desktop.</p>
      </div>
      {openFolder && (
        <FolderView folderId={openFolder} isAdmin={isAdmin} scope="launchpad" onClose={() => setOpenFolder(null)} onOpened={onClose} />
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}
