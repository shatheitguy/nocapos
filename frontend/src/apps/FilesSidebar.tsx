import { tr } from '../i18n';
import { Fragment, useEffect, useRef, useState, type DragEvent, type MouseEvent, type PointerEvent, type ReactNode } from 'react';
import { usePrefs } from '../state/prefs';
import { arrangeFavs, arrangePlaces, dropIndex, moveTo, setHidden, shownPlaces, step, type SidebarLayout } from '../lib/sidebarOrder';
import { useReceived } from '../state/received';
import { opensUnlisted } from '../lib/fileRoots';
import { RECYCLE, baseName, inRecycle, isAppData, type ExternalDrive, type Favorite, type FileRoot } from '../api/files';
import type { NetShare } from '../api/netdrive';
import { Bar } from '../components/Charts';
import type { MenuEntry } from '../components/Dialog';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { NetworkSections, SideSection } from './FilesNetwork';
import { getUser } from '../api/client';
import { FileGlyph } from './FilesParts';
import type { useNetwork } from './NetworkDrives';
import type { Special } from './Files';

// The Files sidebar: Home, Recents and the other
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

/** What a reorderable sidebar item spreads onto its button. */
interface PlaceProps {
  ref: (el: HTMLElement | null) => void;
  className: string;
  onPointerDown: (e: PointerEvent) => void;
  onPointerMove: (e: PointerEvent) => void;
  onPointerUp: (e: PointerEvent) => void;
  onPointerCancel: (e: PointerEvent) => void;
  onClickCapture: (e: MouseEvent) => void;
  onContextMenu?: (e: MouseEvent) => void;
}

/**
 * Rearranging a list by dragging: with a mouse once it moves a few pixels,
 * with touch after holding a moment. The others shift live to make room;
 * letting go saves the order.
 */
function useReorder(keys: string[], commit: (next: string[]) => void) {
  const [drag, setDrag] = useState<{ key: string; index: number } | null>(null);
  const els = useRef(new Map<string, HTMLElement>());
  const st = useRef<{ key: string; x: number; y: number; id: number; touch: boolean; active: boolean; timer: number; centres: number[] } | null>(null);
  const skipClick = useRef(false);
  const at = useRef(0); // where the dragged item would land
  const view = drag ? moveTo(keys, drag.key, drag.index) : keys;

  const begin = () => {
    const s = st.current;
    if (!s) return;
    s.active = true;
    s.centres = keys
      .filter((k) => k !== s.key)
      .map((k) => {
        const r = els.current.get(k)?.getBoundingClientRect();
        return r ? r.top + r.height / 2 : 0;
      });
    try {
      els.current.get(s.key)?.setPointerCapture(s.id);
    } catch {
      /* the pointer may be gone */
    }
    at.current = keys.indexOf(s.key);
    setDrag({ key: s.key, index: at.current });
  };
  const end = (save: boolean) => {
    const s = st.current;
    st.current = null;
    if (!s) return;
    window.clearTimeout(s.timer);
    if (s.active) {
      skipClick.current = true;
      setDrag(null);
      if (save && at.current !== keys.indexOf(s.key)) commit(moveTo(keys, s.key, at.current));
    }
  };

  const bind = (key: string): PlaceProps => ({
    ref: (el) => {
      if (el) els.current.set(key, el);
      else els.current.delete(key);
    },
    className: drag?.key === key ? 'fs-dragging' : drag ? 'fs-shifting' : '',
    onPointerDown: (e) => {
      if (e.button !== 0 || keys.length < 2) return;
      const touch = e.pointerType !== 'mouse';
      st.current = { key, x: e.clientX, y: e.clientY, id: e.pointerId, touch, active: false, timer: 0, centres: [] };
      if (touch) st.current.timer = window.setTimeout(begin, 450);
    },
    onPointerMove: (e) => {
      const s = st.current;
      if (!s || s.key !== key) return;
      if (!s.active) {
        const d = Math.hypot(e.clientX - s.x, e.clientY - s.y);
        if (s.touch) {
          if (d > 8) end(false); // scrolling, not holding
          return;
        }
        if (d < 5) return;
        begin();
      }
      e.preventDefault();
      at.current = dropIndex(e.clientY, s.centres);
      setDrag({ key, index: at.current });
    },
    onPointerUp: () => end(true),
    onPointerCancel: () => end(false),
    onClickCapture: (e) => {
      if (skipClick.current) {
        skipClick.current = false;
        e.preventDefault();
        e.stopPropagation();
      }
    },
  });
  return { view, bind, dragging: !!drag };
}

export function FilesSidebar({
  roots,
  home,
  current,
  favs,
  external,
  onEject,
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
  onEject: (d: ExternalDrive) => void;
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
  const [editing, setEditing] = useState(false);
  const layout = usePrefs((s) => s.filesSidebar);
  const setPrefs = usePrefs((s) => s.set);
  const saveLayout = (l: Partial<SidebarLayout>) => setPrefs({ filesSidebar: { ...usePrefs.getState().filesSidebar, ...l } });
  const special = current?.special ?? null;
  const received = useReceived((s) => s.info);
  useEffect(() => {
    void useReceived.getState().load();
  }, []);
  const recvPath = received?.available && received.root && received.path ? received.path : '';
  const inReceived = !!recvPath && !special && !!current && current.root === received?.root && (current.path === recvPath || current.path.startsWith(recvPath + '/'));
  // Looking at it: nothing is new any more.
  useEffect(() => {
    if (inReceived && received?.unread) useReceived.getState().markSeen();
  }, [inReceived, received?.unread]);
  const at = (root: string, path: string) => !special && current?.root === root && current.path === path;
  const inside = (root: string) => !special && current?.root === root && !inRecycle(current.path);
  const locals = roots.filter((r) => !r.id.startsWith('net:'));
  // Other people's homes stay out of the sidebar (admins can still reach them by path).
  // An administrator's Home is the main drive, so their personal home folder isn't listed twice.
  const admin = getUser()?.role === 'admin';
  const others = locals.filter((r) => r.id !== home && r.kind !== 'homes' && !(admin && (r.kind === 'home' || r.id === 'home')));
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

  // ---------- places, in the person's order ----------
  const places: Record<string, { label: string; render: (extra: PlaceProps) => ReactNode }> = {};
  if (home) {
    places.home = {
      label: tr('files.side.home'),
      render: (x) => (
        <button type="button" {...x} className={`fs-item ${x.className} ${inside(home) && !favs.some((f) => at(f.root, f.path)) ? 'on' : ''} ${over === 'home' ? 'drop' : ''}`} onClick={() => onGo(home, '/')} {...dropTarget('home', (d, c) => onDropInto(d, home, '/', c))}>
          <Icon name="home" size={17} />
          <span className="fs-name">Home</span>
        </button>
      ),
    };
  }
  places.recents = {
    label: tr('files.side.recents'),
    render: (x) => (
      <button type="button" {...x} className={`fs-item ${x.className} ${special === 'recents' ? 'on' : ''}`} onClick={() => home && onGo(home, '/', 'recents')}>
        <Icon name="clock" size={17} />
        <span className="fs-name">Recents</span>
      </button>
    ),
  };
  if (recvPath && received?.root) {
    places.drops = {
      label: received.folder,
      render: (x) => (
        <button
          type="button"
          {...x}
          className={`fs-item fs-received ${x.className} ${inReceived ? 'on' : ''}`}
          title={tr('recv.sideHint')}
          onClick={() => {
            onGo(received.root!, recvPath);
            useReceived.getState().markSeen();
          }}
        >
          <Icon name="download" size={17} />
          <span className="fs-name">{received.folder}</span>
          {received.unread > 0 && (
            <span className="fs-badge" aria-label={tr('recv.unread', { n: received.unread })}>
              {received.unread > 99 ? '99+' : received.unread}
            </span>
          )}
        </button>
      ),
    };
  }
  // App data and System stay administrators' only, whatever the layout says.
  if (admin) {
    places.appdata = {
      label: tr('files.appdata.title'),
      render: (x) => (
        <button type="button" {...x} className={`fs-item ${x.className} ${special === 'appdata' || (!special && current && isAppData(current.root)) ? 'on' : ''}`} onClick={() => home && onGo(home, '/', 'appdata')}>
          <Icon name="appData" size={17} />
          <span className="fs-name">{tr('files.appdata.title')}</span>
        </button>
      ),
    };
  }
  for (const r of others) {
    if (r.id === 'system' && !admin) continue;
    places[`root:${r.id}`] = {
      label: r.name,
      render: (x) => (
        <button type="button" {...x} className={`fs-item ${x.className} ${inside(r.id) ? 'on' : ''}`} title={r.total ? `${fmtBytes(r.free, 0)} free of ${fmtBytes(r.total, 0)}` : r.name} onClick={() => onGo(r.id, '/')}>
          <Icon name={r.kind === 'share' ? 'network' : r.kind === 'homes' ? 'users' : r.id === 'home' ? 'user' : 'drive'} size={17} />
          <span className="fs-name">{r.name}</span>
          {r.read_only && <span className="fs-sub" title="You can look at this folder but not change it">Read only</span>}
        </button>
      ),
    };
  }
  const placeOrder = arrangePlaces(layout.order, Object.keys(places));
  const shown = shownPlaces(placeOrder, layout.hidden);
  const placeDrag = useReorder(shown, (next) => {
    // Hidden places keep their slots; the shown ones take the new order.
    let i = 0;
    saveLayout({ order: placeOrder.map((k) => (layout.hidden.includes(k) ? k : next[i++])) });
  });
  const placeMenu = (key: string): MenuEntry[] => {
    const i = shown.indexOf(key);
    return [
      { label: tr('files.side.moveUp'), icon: 'arrowUp', disabled: i <= 0, onClick: () => saveLayout({ order: step(placeOrder, key, -1, layout.hidden) }) },
      { label: tr('files.side.moveDown'), icon: 'arrowDown', disabled: i < 0 || i >= shown.length - 1, onClick: () => saveLayout({ order: step(placeOrder, key, 1, layout.hidden) }) },
      { label: tr('files.side.hide'), icon: 'eye', onClick: () => saveLayout({ order: placeOrder, hidden: setHidden(layout.hidden, key, true) }) },
      { label: tr('files.side.edit'), icon: 'settings', onClick: () => setEditing(true) },
    ];
  };

  // ---------- favorites, in their own order ----------
  const favKey = (f: Favorite) => `${f.root}:${f.path}`;
  const favOrder = arrangeFavs(layout.favs, favs.map(favKey));
  const favByKey = new Map(favs.map((f) => [favKey(f), f]));
  const favDrag = useReorder(favOrder, (next) => saveLayout({ favs: next }));

  return (
    <nav className="sidebar files-sidebar fs-side">
      <div className="fs-title-row">
        <h2 className="app-title fs-title">{tr('files.title')}</h2>
        <button
          type="button"
          className="ghost icon-btn fs-more"
          aria-label={tr('files.side.more')}
          title={tr('files.side.more')}
          onClick={(e) => openMenu(e, [{ label: tr('files.side.edit'), icon: 'settings', onClick: () => setEditing(true) }])}
        >
          <Icon name="more" size={15} />
        </button>
      </div>

      {editing && (
        <div className="fs-edit" role="group" aria-label={tr('files.side.edit')}>
          <p className="fs-edit-title">{tr('files.side.editTitle')}</p>
          {placeOrder.map((k) => (
            <label key={k} className="fs-edit-row">
              <input type="checkbox" checked={!layout.hidden.includes(k)} onChange={(e) => saveLayout({ order: placeOrder, hidden: setHidden(layout.hidden, k, !e.target.checked) })} />
              <span>{places[k].label}</span>
            </label>
          ))}
          <div className="fs-edit-actions">
            <button type="button" className="ghost" onClick={() => saveLayout({ order: [], hidden: [], favs: [] })}>
              {tr('files.side.reset')}
            </button>
            <button type="button" onClick={() => setEditing(false)}>
              {tr('files.side.done')}
            </button>
          </div>
        </div>
      )}

      <div className="fs-section">
        {placeDrag.view.map((k) => (
          <Fragment key={k}>{places[k].render({ ...placeDrag.bind(k), onContextMenu: (e: MouseEvent) => openMenu(e, placeMenu(k)) })}</Fragment>
        ))}
      </div>

      <SideSection title="Favorites" className={over === 'favs' ? 'drop' : ''} {...favDrop}>
        {favDrag.view.map((fk) => {
          const f = favByKey.get(fk);
          if (!f) return null;
          const key = `fav:${f.root}:${f.path}`;
          const name = f.path === '/' ? rootLabel(f.root) : baseName(f.path);
          const missing = !roots.some((r) => r.id === f.root) && !opensUnlisted(f.root);
          const i = favOrder.indexOf(fk);
          const b = favDrag.bind(fk);
          return (
            <button
              key={key}
              type="button"
              {...b}
              className={`fs-item ${b.className} ${at(f.root, f.path) ? 'on' : ''} ${over === key ? 'drop' : ''} ${missing ? 'missing' : ''}`}
              title={missing ? `${name} — not connected` : `${rootLabel(f.root)}${f.path === '/' ? '' : f.path}`}
              disabled={missing}
              onClick={() => onGo(f.root, f.path)}
              onContextMenu={(e) =>
                openMenu(e, [
                  { label: 'Open', icon: 'folder', onClick: () => onGo(f.root, f.path) },
                  { label: tr('files.side.moveUp'), icon: 'arrowUp', disabled: i <= 0, onClick: () => saveLayout({ favs: step(favOrder, fk, -1) }) },
                  { label: tr('files.side.moveDown'), icon: 'arrowDown', disabled: i >= favOrder.length - 1, onClick: () => saveLayout({ favs: step(favOrder, fk, 1) }) },
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

      {getUser()?.role === 'admin' && (
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
      )}

      {external.length > 0 && (
        <SideSection title="External storage">
          {external.map((d) => (
            <div key={d.path} className={`fn-drive ${at(d.root, d.path) ? 'on' : ''}`}>
              <button
                type="button"
                className={`fs-item fs-ext fn-drive-main ${at(d.root, d.path) ? 'on' : ''}`}
                title={d.ejectable ? `${d.device}${d.fs_type ? ` · ${d.fs_type}` : ''}` : `${d.device} — to eject it, unmount it on the server or unplug it once nothing is copying`}
                onClick={() => onGo(d.root, d.path)}
                onContextMenu={(e) =>
                  openMenu(e, [
                    { label: 'Open', icon: 'folder', onClick: () => onGo(d.root, d.path) },
                    ...(d.ejectable ? [{ label: `Eject “${d.name}”`, icon: 'eject' as const, onClick: () => onEject(d) }] : []),
                  ])
                }
              >
                <Icon name="drive" size={17} />
                <span className="fs-ext-text">
                  <span className="fs-name">{d.name}</span>
                  {d.total > 0 && <span className="fs-sub">{fmtBytes(d.free, 0)} free of {fmtBytes(d.total, 0)}</span>}
                </span>
              </button>
              {d.ejectable && (
                <button type="button" className="fn-eject" aria-label={`Eject ${d.name}`} title="Eject" onClick={() => onEject(d)}>
                  <Icon name="eject" size={14} />
                </button>
              )}
            </div>
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
