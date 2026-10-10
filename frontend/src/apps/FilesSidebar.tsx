import { useState, type DragEvent, type MouseEvent } from 'react';
import { RECYCLE, baseName, inRecycle, type ExternalDrive, type Favorite, type FileRoot } from '../api/files';
import type { NetShare } from '../api/netdrive';
import { Bar } from '../components/Charts';
import type { MenuEntry } from '../components/Dialog';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { NetworkSections, SideSection } from './FilesNetwork';
import { FileGlyph } from './FilesParts';
import type { useNetwork } from './NetworkDrives';
import type { Special } from './Files';

// The Files sidebar, laid out like umbrelOS: Home, Recents and the other
// locations, then Favorites, Shared folders, Network devices, External
// storage, and Trash at the bottom.

/** Items dragged out of a Files pane. */
export interface DragFiles {
  root: string;
  paths: string[];
  dirs: boolean[];
}
export const DRAG_TYPE = 'application/x-nocap-files';

export function readDrag(e: DragEvent): DragFiles | null {
  try {
    const raw = e.dataTransfer.getData(DRAG_TYPE);
    return raw ? (JSON.parse(raw) as DragFiles) : null;
  } catch {
    return null;
  }
}
const isInternal = (e: DragEvent) => e.dataTransfer.types.includes(DRAG_TYPE);

export function FilesSidebar({
  roots,
  home,
  current,
  favs,
  external,
  net,
  rootLabel,
  onGo,
  onAddFavs,
  onRemoveFav,
  onDropInto,
  onDropTrash,
  onNetChanged,
  onUnshare,
  openMenu,
}: {
  roots: FileRoot[];
  home?: string;
  current?: { root: string; path: string; special: Special };
  favs: Favorite[];
  external: ExternalDrive[];
  net: ReturnType<typeof useNetwork>;
  rootLabel: (id: string) => string;
  onGo: (root: string, path: string, special?: Special) => void;
  onAddFavs: (root: string, paths: string[]) => void;
  onRemoveFav: (root: string, path: string) => void;
  onDropInto: (d: DragFiles, root: string, path: string, copy: boolean) => void;
  onDropTrash: (d: DragFiles) => void;
  onNetChanged: () => Promise<void>;
  onUnshare: (sh: NetShare) => void;
  openMenu: (e: MouseEvent, items: MenuEntry[]) => void;
}) {
  const [over, setOver] = useState<string | null>(null);
  const special = current?.special ?? null;
  const at = (root: string, path: string) => !special && current?.root === root && current.path === path;
  const inside = (root: string) => !special && current?.root === root && !inRecycle(current.path);
  const locals = roots.filter((r) => !r.id.startsWith('net:'));
  const others = locals.filter((r) => r.id !== home);
  const cur = roots.find((r) => r.id === current?.root) ?? roots.find((r) => r.id === home);

  // A sidebar folder that accepts items dragged from a pane.
  const dropTarget = (key: string, onDrop: (d: DragFiles, copy: boolean) => void) => ({
    onDragOver: (e: DragEvent) => {
      if (!isInternal(e)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = e.ctrlKey || e.altKey ? 'copy' : 'move';
      setOver(key);
    },
    onDragLeave: () => setOver((k) => (k === key ? null : k)),
    onDrop: (e: DragEvent) => {
      setOver(null);
      const d = readDrag(e);
      if (!d) return;
      e.preventDefault();
      e.stopPropagation();
      onDrop(d, e.ctrlKey || e.altKey);
    },
  });

  // Dropping folders on the Favorites group pins them.
  const favDrop = {
    onDragOver: (e: DragEvent) => {
      if (!isInternal(e)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'link';
      setOver('favs');
    },
    onDragLeave: (e: DragEvent) => {
      if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) setOver((k) => (k === 'favs' ? null : k));
    },
    onDrop: (e: DragEvent) => {
      setOver(null);
      const d = readDrag(e);
      if (!d) return;
      e.preventDefault();
      const dirs = d.paths.filter((_, i) => d.dirs[i]);
      if (!dirs.length) return;
      onAddFavs(d.root, dirs);
    },
  };

  return (
    <nav className="sidebar files-sidebar fs-side">
      <h2 className="app-title fs-title">Files</h2>

      <div className="fs-section">
        {home && (
          <button type="button" className={`fs-item ${inside(home) && !favs.some((f) => at(f.root, f.path)) ? 'on' : ''} ${over === 'home' ? 'drop' : ''}`} onClick={() => onGo(home, '/')} {...dropTarget('home', (d, c) => onDropInto(d, home, '/', c))}>
            <Icon name="home" size={17} />
            <span className="fs-name">Home</span>
          </button>
        )}
        <button type="button" className={`fs-item ${special === 'recents' ? 'on' : ''}`} onClick={() => home && onGo(home, '/', 'recents')}>
          <Icon name="clock" size={17} />
          <span className="fs-name">Recents</span>
        </button>
        {others.map((r) => (
          <button key={r.id} type="button" className={`fs-item ${inside(r.id) ? 'on' : ''}`} title={r.total ? `${fmtBytes(r.free, 0)} free of ${fmtBytes(r.total, 0)}` : r.name} onClick={() => onGo(r.id, '/')}>
            <Icon name={r.id === 'home' ? 'user' : 'drive'} size={17} />
            <span className="fs-name">{r.name}</span>
          </button>
        ))}
      </div>

      <SideSection title="Favorites" className={over === 'favs' ? 'drop' : ''} {...favDrop}>
        {favs.map((f) => {
          const key = `fav:${f.root}:${f.path}`;
          const name = f.path === '/' ? rootLabel(f.root) : baseName(f.path);
          const missing = !roots.some((r) => r.id === f.root);
          return (
            <button
              key={key}
              type="button"
              className={`fs-item ${at(f.root, f.path) ? 'on' : ''} ${over === key ? 'drop' : ''} ${missing ? 'missing' : ''}`}
              title={missing ? `${name} — not connected` : `${rootLabel(f.root)}${f.path === '/' ? '' : f.path}`}
              disabled={missing}
              onClick={() => onGo(f.root, f.path)}
              onContextMenu={(e) =>
                openMenu(e, [
                  { label: 'Open', icon: 'folder', onClick: () => onGo(f.root, f.path) },
                  { label: 'Remove from Favorites', icon: 'star', onClick: () => onRemoveFav(f.root, f.path) },
                ])
              }
              {...dropTarget(key, (d, c) => onDropInto(d, f.root, f.path, c))}
            >
              <FileGlyph name={name} dir size={18} />
              <span className="fs-name">{name}</span>
            </button>
          );
        })}
        {!favs.length && <p className="fs-empty">Drag folders here, or right-click a folder and choose Add to Favorites.</p>}
      </SideSection>

      <NetworkSections
        drives={net.drives}
        sharing={net.sharing}
        current={current && !special ? { root: current.root, path: current.path } : undefined}
        roots={roots}
        onGo={(r, p) => onGo(r, p ?? '/')}
        onChanged={onNetChanged}
        onUnshare={onUnshare}
        openMenu={openMenu}
      />

      {external.length > 0 && (
        <SideSection title="External storage">
          {external.map((d) => (
            <button
              key={d.path}
              type="button"
              className={`fs-item fs-ext ${at(d.root, d.path) ? 'on' : ''}`}
              title={`${d.device} — to eject it, unmount it on the server or unplug it once nothing is copying`}
              onClick={() => onGo(d.root, d.path)}
            >
              <Icon name="drive" size={17} />
              <span className="fs-ext-text">
                <span className="fs-name">{d.name}</span>
                {d.total > 0 && <span className="fs-sub">{fmtBytes(d.free, 0)} free of {fmtBytes(d.total, 0)}</span>}
              </span>
            </button>
          ))}
        </SideSection>
      )}

      <div className="side-spacer" />

      {cur && cur.total > 0 && (
        <div className="fs-usage" title={`${rootLabel(cur.id)}: ${fmtBytes(cur.total - cur.free)} used`}>
          <span className="fs-usage-line">
            <span>{rootLabel(cur.id)}</span>
            <span className="muted">{fmtBytes(cur.free, 0)} free</span>
          </span>
          <Bar value={((cur.total - cur.free) / cur.total) * 100} />
        </div>
      )}
      <button
        type="button"
        className={`fs-item fs-trash ${!special && inRecycle(current?.path ?? '/') ? 'on' : ''} ${over === 'trash' ? 'drop' : ''}`}
        disabled={!current}
        onClick={() => current && onGo(special ? (home ?? current.root) : current.root, RECYCLE)}
        {...dropTarget('trash', (d) => onDropTrash(d))}
      >
        <Icon name="trash" size={17} />
        <span className="fs-name">Trash</span>
      </button>
    </nav>
  );
}
