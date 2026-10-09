import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { createPortal } from 'react-dom';
import { APPS, canMultiWindow, openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu } from '../components/ContextMenu';
import { pressApp, useAppDrag } from '../state/appDrag';
import { takeOut, useFolders } from '../state/folders';
import { appMenuItems } from './Shelf';

/**
 * An open folder: its name (click to rename) and the apps inside. Click an app
 * to open it; drag one out of the box to take it out of the folder, or onto the
 * Dock or Desktop.
 */
export function FolderView({
  folderId,
  isAdmin,
  scope,
  at,
  onClose,
  onOpened,
}: {
  folderId: string;
  isAdmin: boolean;
  scope: 'launchpad' | 'desktop';
  /** Desktop only: the icon's position, to open next to it. */
  at?: { x: number; y: number };
  onClose: () => void;
  /** Called after an app is opened (Launchpad closes then). */
  onOpened?: () => void;
}) {
  const folder = useFolders((s) => s.folders.find((f) => f.id === folderId));
  const rename = useFolders((s) => s.rename);
  const [name, setName] = useState(folder?.name ?? '');
  const [menu, setMenu] = useState<{ x: number; y: number; appId: string } | null>(null);
  const box = useRef<HTMLDivElement>(null);

  // The folder dissolved (its last-but-one app was taken out).
  useEffect(() => {
    if (!folder) onClose();
  }, [folder, onClose]);

  // Dragging an app out past the edge closes the folder, so it can be dropped outside.
  useEffect(
    () =>
      useAppDrag.subscribe(({ drag }) => {
        if (drag?.folderId !== folderId) return;
        const r = box.current?.getBoundingClientRect();
        if (r && (drag.x < r.left - 8 || drag.x > r.right + 8 || drag.y < r.top - 8 || drag.y > r.bottom + 8)) onClose();
      }),
    [folderId, onClose],
  );

  // Escape closes just the folder, not Launchpad behind it.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || menu) return;
      e.stopImmediatePropagation();
      onClose();
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose, menu]);

  if (!folder) return null;
  const apps = folder.apps.filter((id) => APPS[id] && !APPS[id].hidden && (isAdmin || !APPS[id].adminOnly));

  const launch = (id: string, newWindow: boolean) => {
    openApp(id, { newWindow: newWindow && canMultiWindow(id) });
    onClose();
    onOpened?.();
  };
  const save = () => rename(folderId, name);

  // On the desktop the folder opens beside its icon, kept on screen.
  const width = Math.min(380, window.innerWidth - 24);
  const style: CSSProperties | undefined = at
    ? { position: 'fixed', width, left: Math.max(12, Math.min(at.x - width / 2, window.innerWidth - width - 12)), top: Math.max(12, Math.min(at.y, window.innerHeight - 340)) }
    : undefined;

  const view = (
    <div
      className={`folder-backdrop ${scope}`}
      onPointerDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={box}
        className="folder-view"
        style={style}
        role="dialog"
        aria-label={folder.name}
        data-drop="folder"
        data-folder={folderId}
        data-scope={scope}
      >
        <input
          className="folder-name"
          value={name}
          aria-label="Folder name"
          maxLength={40}
          spellCheck={false}
          onChange={(e) => setName(e.target.value)}
          onBlur={save}
          onKeyDown={(e) => {
            if (e.key === 'Enter') e.currentTarget.blur();
          }}
        />
        <div className="folder-grid">
          {apps.map((id) => (
            <button
              key={id}
              type="button"
              className="launcher-app"
              title="Drag out to take it out of the folder"
              onPointerDown={(e) => pressApp(e, id, 'folder', { folderId })}
              onClick={(e) => launch(id, e.shiftKey)}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                setMenu({ x: e.clientX, y: e.clientY, appId: id });
              }}
            >
              <AppIcon app={APPS[id]} size={52} />
              <span>{APPS[id].title}</span>
            </button>
          ))}
        </div>
      </div>
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={appMenuItems(menu.appId, [{ label: 'Take out of Folder', icon: 'close', onClick: () => takeOut(folderId, menu.appId) }])}
          onClose={() => setMenu(null)}
        />
      )}
    </div>
  );
  // On the desktop it floats above windows; in Launchpad it stays inside Launchpad.
  return scope === 'desktop' ? createPortal(view, document.body) : view;
}
