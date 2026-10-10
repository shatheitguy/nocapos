import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { create } from 'zustand';
import {
  PREVIEWABLE,
  RECYCLE,
  baseName,
  downloadFile,
  fileApi,
  fileKind,
  fileTicket,
  inRecycle,
  isArchive,
  joinPath,
  parentPath,
  rawUrl,
  type ExternalDrive,
  type Favorite,
  type FileEntry,
  type FileRoot,
} from '../api/files';
import { ContextMenu, Dialog, type DialogSpec, type MenuEntry } from '../components/Dialog';
import { Bar } from '../components/Charts';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { FileGlyph, FilePreview, InfoDialog, PermBadges, kindLabel } from './FilesParts';
import { ShareFolderDialog } from './FilesNetwork';
import { FilesSidebar, DRAG_TYPE, readDrag, type DragFiles } from './FilesSidebar';
import { useNetwork } from './NetworkDrives';
import { netApi, type NetShare } from '../api/netdrive';
import { FileLightbox } from './Viewer';
import { startArchiveJob, startTransfer, startUpload, useTransfers } from '../state/transfers';

// Clipboard shared by all Files windows.
interface Clip {
  root: string;
  paths: string[];
  move: boolean;
}
const useClipboard = create<{ clip: Clip | null; set: (c: Clip | null) => void }>((set) => ({
  clip: null,
  set: (clip) => set({ clip }),
}));

type SortKey = 'name' | 'mod' | 'size' | 'kind';
type View = 'grid' | 'list';
/** Views that are not a folder: recently changed files and search results. */
export type Special = 'recents' | 'search' | null;

const SORT_LABEL: Record<SortKey, string> = { name: 'Name', mod: 'Date modified', size: 'Size', kind: 'Kind' };

function loadView(): View {
  try {
    return localStorage.getItem('alfa.files.view') === 'list' ? 'list' : 'grid';
  } catch {
    return 'grid';
  }
}
function loadSort(): { key: SortKey; asc: boolean } {
  try {
    const [k, d] = (localStorage.getItem('alfa.files.sort') ?? '').split(':');
    if (k in SORT_LABEL) return { key: k as SortKey, asc: d !== 'desc' };
  } catch {
    /* ignore */
  }
  return { key: 'name', asc: true };
}
function loadFlag(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}
function saveLocal(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    /* ignore */
  }
}

/** What a pane tells the window: where it is and what is selected. */
interface PaneState {
  root: string;
  path: string;
  special: Special;
  /** The single selected entry (for the preview), if exactly one. */
  sel: FileEntry | null;
  selPath: string;
  unix: boolean;
}
interface NavReq {
  root: string;
  path: string;
  special?: Special;
  n: number;
}

/** One thing shown in a pane: a folder entry, or a hit from Recents / search. */
interface Item {
  key: string;
  root: string;
  /** Full path inside root, e.g. "/Photos/cat.png". */
  path: string;
  entry: FileEntry;
}

const isLocal = (r: FileRoot) => r.id !== 'system' && !r.id.startsWith('net:');

/**
 * Files: a sidebar (Home, Recents, Favorites, shared folders,
 * network devices, external storage, Trash) with one or two panes and an
 * optional preview panel.
 */
export function Files({ win }: { win: WinState }) {
  const [roots, setRoots] = useState<FileRoot[] | null>(null);
  const [rootsError, setRootsError] = useState<string | null>(null);
  const [dual, setDual] = useState(() => loadFlag('alfa.files.dual'));
  const [preview, setPreview] = useState(() => loadFlag('alfa.files.preview'));
  const [active, setActive] = useState<0 | 1>(0);
  const [states, setStates] = useState<[PaneState | null, PaneState | null]>([null, null]);
  const [nav, setNav] = useState<[NavReq | null, NavReq | null]>([null, null]);
  const [reloadN, setReloadN] = useState<[number, number]>([0, 0]);
  const [favs, setFavs] = useState<Favorite[]>([]);
  const [external, setExternal] = useState<ExternalDrive[]>([]);
  const [sideOpen, setSideOpen] = useState(false);
  const [winMenu, setWinMenu] = useState<{ x: number; y: number; items: MenuEntry[] } | null>(null);
  const shell = useRef<HTMLDivElement>(null);

  const loadRoots = useCallback(async () => {
    const r = await fileApi.roots();
    if (!r.ok) {
      setRootsError(r.error ?? 'Could not load storage locations');
      return;
    }
    setRootsError(null);
    setRoots(r.data);
  }, []);
  useEffect(() => void loadRoots(), [loadRoots]);
  useEffect(() => {
    void fileApi.favorites().then((r) => r.ok && setFavs(r.data.favorites));
    void fileApi.external().then((r) => r.ok && setExternal(r.data));
  }, []);

  // Network drives and shared folders.
  const net = useNetwork();
  const netChanged = useCallback(async () => {
    await Promise.all([net.load(), loadRoots()]);
  }, [net.load, loadRoots]);
  const [shareReq, setShareReq] = useState<{ root: string; path: string; name: string } | null>(null);
  const shares = net.sharing?.shares ?? [];
  const unshare = async (sh: NetShare) => {
    const r = await netApi.removeShare(sh.id);
    if (!r.ok) return toast('error', 'Could not stop sharing', r.error);
    toast('success', `Stopped sharing “${sh.name}”`);
    void net.load();
  };

  // ---------- favorites ----------
  const saveFavs = async (next: Favorite[]) => {
    const before = favs;
    setFavs(next);
    const r = await fileApi.setFavorites(next);
    if (!r.ok) {
      setFavs(before);
      toast('error', 'Could not save favorites', r.error);
    }
  };
  const isFav = (root: string, path: string) => favs.some((f) => f.root === root && f.path === path);
  const addFavs = (root: string, paths: string[]) => {
    const add = paths.filter((p) => !isFav(root, p)).map((path) => ({ root, path }));
    if (add.length) void saveFavs([...favs, ...add]);
  };
  const removeFav = (root: string, path: string) => void saveFavs(favs.filter((f) => !(f.root === root && f.path === path)));

  const toggleDual = () => {
    saveLocal('alfa.files.dual', dual ? '0' : '1');
    if (dual) setActive(0);
    setDual(!dual);
  };
  const togglePreview = () => {
    saveLocal('alfa.files.preview', preview ? '0' : '1');
    setPreview(!preview);
  };

  const go = (root: string, path: string, special: Special = null) => {
    setSideOpen(false);
    setNav((n) => {
      const next: [NavReq | null, NavReq | null] = [n[0], n[1]];
      next[active] = { root, path, special, n: (n[active]?.n ?? 0) + 1 };
      return next;
    });
  };
  const report = useCallback((side: 0 | 1, st: PaneState) => setStates((s) => (side === 0 ? [st, s[1]] : [s[0], st])), []);
  const bump = (side: 0 | 1) => setReloadN((r) => (side === 0 ? [r[0] + 1, r[1]] : [r[0], r[1] + 1]));

  const openMenu = (e: MouseEvent, items: MenuEntry[]) => {
    e.preventDefault();
    e.stopPropagation();
    const box = shell.current?.getBoundingClientRect();
    setWinMenu({ x: e.clientX - (box?.left ?? 0), y: e.clientY - (box?.top ?? 0), items });
  };

  // Something dragged from a pane onto a sidebar folder: move it there (Ctrl copies).
  const dropInto = async (data: DragFiles, root: string, path: string, copy: boolean) => {
    if (data.root !== root) return toast('error', 'Moving between storage locations is not supported yet');
    const paths = data.paths.filter((p) => p !== path && parentPath(p) !== path);
    if (!paths.length) return;
    if (await startTransfer(root, paths, path, !copy)) [0, 1].forEach((s) => bump(s as 0 | 1));
  };
  const dropToTrash = async (data: DragFiles) => {
    const r = await fileApi.remove(data.root, data.paths, false);
    if (r.ok) toast('success', `Moved ${data.paths.length === 1 ? `“${baseName(data.paths[0])}”` : `${data.paths.length} items`} to Trash`);
    else toast('error', 'Could not move to Trash', r.error);
    [0, 1].forEach((s) => bump(s as 0 | 1));
    void loadRoots();
  };

  if (rootsError) {
    return (
      <div className="empty">
        <Icon name="alert" size={32} />
        <p className="muted">{rootsError}</p>
        <button type="button" onClick={() => void loadRoots()}>
          Retry
        </button>
      </div>
    );
  }

  const cur = states[active];
  const home = roots?.find(isLocal) ?? roots?.[0];
  const firstRoot = home?.id ?? '';
  const rootLabel = (id: string) => (id === home?.id ? 'Home' : (roots?.find((r) => r.id === id)?.name ?? 'Files'));
  const pane = (side: 0 | 1) => {
    const other = states[side === 0 ? 1 : 0];
    return (
      <FilePane
        key={side}
        side={side}
        roots={roots}
        initialRoot={side === 0 ? (win.props?.root ?? firstRoot) : (states[0]?.root ?? firstRoot)}
        initialPath={side === 0 ? (win.props?.path ?? '/') : (states[0]?.path ?? '/')}
        active={!dual || active === side}
        dual={dual}
        preview={preview}
        onActivate={() => setActive(side)}
        onState={report}
        navReq={nav[side]}
        reloadN={reloadN[side]}
        other={dual && other && !other.special ? { root: other.root, path: other.path } : null}
        onTransferred={() => bump(side === 0 ? 1 : 0)}
        onRootsChanged={() => void loadRoots()}
        onToggleDual={toggleDual}
        onTogglePreview={togglePreview}
        onSwitchPane={() => dual && setActive(side === 0 ? 1 : 0)}
        onToggleSidebar={() => setSideOpen((v) => !v)}
        shares={shares}
        onShare={(root, path, name) => setShareReq({ root, path, name })}
        onUnshare={(sh) => void unshare(sh)}
        isFav={isFav}
        onAddFavs={addFavs}
        onRemoveFav={removeFav}
        rootLabel={rootLabel}
      />
    );
  };

  return (
    <div ref={shell} className={`files fx ${dual ? 'dual' : ''} ${preview ? 'with-preview' : ''} ${sideOpen ? 'side-open' : ''}`}>
      <FilesSidebar
        roots={roots ?? []}
        home={home?.id}
        current={cur ? { root: cur.root, path: cur.path, special: cur.special } : undefined}
        favs={favs}
        external={external}
        net={net}
        rootLabel={rootLabel}
        onGo={go}
        onAddFavs={addFavs}
        onRemoveFav={removeFav}
        onDropInto={(d, root, path, copy) => void dropInto(d, root, path, copy)}
        onDropTrash={(d) => void dropToTrash(d)}
        onNetChanged={netChanged}
        onUnshare={(sh) => void unshare(sh)}
        openMenu={openMenu}
      />
      {sideOpen && <div className="fs-scrim" onPointerDown={() => setSideOpen(false)} />}

      <div className="files-panes">
        {roots && pane(0)}
        {roots && dual && pane(1)}
      </div>

      {preview && cur && !cur.special && (
        <FilePreview root={cur.root} path={cur.sel ? cur.selPath : cur.path} entry={cur.sel} unix={cur.unix} onClose={togglePreview} />
      )}
      {shareReq && <ShareFolderDialog {...shareReq} onClose={() => setShareReq(null)} onShared={() => void net.load()} />}
      {winMenu && <ContextMenu x={winMenu.x} y={winMenu.y} items={winMenu.items} onClose={() => setWinMenu(null)} />}
    </div>
  );
}

interface PaneProps {
  side: 0 | 1;
  roots: FileRoot[] | null;
  initialRoot: string;
  initialPath: string;
  active: boolean;
  dual: boolean;
  preview: boolean;
  onActivate: () => void;
  onState: (side: 0 | 1, s: PaneState) => void;
  navReq: NavReq | null;
  reloadN: number;
  /** The other pane's folder, in dual-pane mode. */
  other: { root: string; path: string } | null;
  onTransferred: () => void;
  onRootsChanged: () => void;
  onToggleDual: () => void;
  onTogglePreview: () => void;
  onSwitchPane: () => void;
  onToggleSidebar: () => void;
  /** Folders shared on the network, and sharing / unsharing one. */
  shares: NetShare[];
  onShare: (root: string, path: string, name: string) => void;
  onUnshare: (share: NetShare) => void;
  isFav: (root: string, path: string) => boolean;
  onAddFavs: (root: string, paths: string[]) => void;
  onRemoveFav: (root: string, path: string) => void;
  rootLabel: (id: string) => string;
}

type Loc = { root: string; path: string; special: Special; query: string };

function hitEntry(h: { name: string; dir: boolean; size: number; mod_time: string }): FileEntry {
  return { name: h.name, dir: h.dir, size: h.size, mod_time: h.mod_time, mode: '', perm: 0 };
}

/** Recents groups: Today, This week, Earlier. */
function ageGroup(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  if (d.getTime() >= start) return 'Today';
  if (d.getTime() >= start - 6 * 86400_000) return 'This week';
  return 'Earlier';
}

function FilePane({
  side,
  roots,
  initialRoot,
  initialPath,
  active,
  dual,
  preview,
  onActivate,
  onState,
  navReq,
  reloadN,
  other,
  onTransferred,
  onRootsChanged,
  onToggleDual,
  onTogglePreview,
  onSwitchPane,
  onToggleSidebar,
  shares,
  onShare,
  onUnshare,
  isFav,
  onAddFavs,
  onRemoveFav,
  rootLabel,
}: PaneProps) {
  const [root, setRoot] = useState<string>(initialRoot);
  const [path, setPath] = useState<string>(initialPath);
  const [special, setSpecial] = useState<Special>(null);
  const [query, setQuery] = useState('');
  const [unixPerms, setUnixPerms] = useState(true);
  const [back, setBack] = useState<Loc[]>([]);
  const [fwd, setFwd] = useState<Loc[]>([]);
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string | null>(null);
  const [view, setViewState] = useState<View>(loadView);
  const [sort, setSortState] = useState(loadSort);
  const [filter, setFilter] = useState('');
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuEntry[] } | null>(null);
  const [dialog, setDialog] = useState<DialogSpec | null>(null);
  const [info, setInfo] = useState<Item | null>(null);
  const [look, setLook] = useState<number | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [dropKey, setDropKey] = useState<string | null>(null);
  const [tickets, setTickets] = useState<Record<string, string>>({});
  const [marquee, setMarquee] = useState<{ x: number; y: number; w: number; h: number } | null>(null);
  const clip = useClipboard((s) => s.clip);
  const setClip = useClipboard((s) => s.set);
  const pane = useRef<HTMLDivElement>(null);
  const uploadInput = useRef<HTMLInputElement>(null);
  const dragDepth = useRef(0);
  const pendingSel = useRef<string | null>(null);
  const marq = useRef<{ x: number; y: number; base: Set<string>; moved: boolean } | null>(null);
  const skipClick = useRef(false);

  const recycle = !special && inRecycle(path);
  const rootInfo = roots?.find((r) => r.id === root);
  const sharedPaths = useMemo(() => new Set(shares.filter((s) => s.root === root).map((s) => s.path)), [shares, root]);

  // ---------- data ----------

  useEffect(() => {
    if (roots?.length && !roots.some((r) => r.id === root)) setRoot(roots[0].id);
  }, [roots, root]);

  const reload = useCallback(async () => {
    if (!root) return;
    setLoading(true);
    setNote(null);
    if (special) {
      const r = special === 'recents' ? await fileApi.recent(150) : await fileApi.search(query, 100);
      setLoading(false);
      if (!r.ok) {
        setListError(r.error ?? 'Could not load');
        setItems([]);
        return;
      }
      setListError(null);
      if (r.data.building && !r.data.indexed) setNote('Still indexing your files…');
      const list = r.data.results.map((h) => ({ key: `${h.root}:${h.path}`, root: h.root, path: h.path, entry: hitEntry(h) }));
      setItems(list);
      setSelected((sel) => new Set([...sel].filter((k) => list.some((i) => i.key === k))));
      return;
    }
    const r = await fileApi.list(root, path);
    setLoading(false);
    if (!r.ok) {
      setListError(r.error ?? 'Could not open folder');
      setItems([]);
      return;
    }
    setListError(null);
    const list = r.data.entries.map((e) => ({ key: e.name, root, path: joinPath(path, e.name), entry: e }));
    setItems(list);
    setUnixPerms(r.data.unix_perms !== false);
    if (pendingSel.current && list.some((i) => i.key === pendingSel.current)) {
      setSelected(new Set([pendingSel.current]));
      setAnchor(pendingSel.current);
      pendingSel.current = null;
    } else {
      setSelected((sel) => new Set([...sel].filter((k) => list.some((i) => i.key === k))));
    }
  }, [root, path, special, query]);

  useEffect(() => void reload(), [reload]);
  useEffect(() => {
    if (reloadN) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadN]);
  const transfersVersion = useTransfers((s) => s.version);
  useEffect(() => {
    if (!transfersVersion) return;
    void reload();
    onRootsChanged();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [transfersVersion]);
  const incoming = useTransfers((s) => s.list).filter((t) => t.status === 'running' && t.root === root && t.dest === path);

  // Scoped links for image and video thumbnails: the folder, or each location's top for Recents / search.
  const thumbRoots = useMemo(() => {
    if (view !== 'grid') return [];
    const media = items.filter((i) => ['image', 'video'].includes(fileKind(i.entry.name, i.entry.dir)));
    return [...new Set(media.map((i) => i.root))];
  }, [items, view]);
  useEffect(() => {
    let live = true;
    setTickets({});
    for (const r of thumbRoots) {
      void fileTicket(r, special ? '/' : path).then((t) => live && t && setTickets((m) => ({ ...m, [r]: t })));
    }
    return () => {
      live = false;
    };
  }, [thumbRoots, special, path]);

  const shown = useMemo(() => {
    const needle = special ? '' : filter.trim().toLowerCase();
    const list = needle ? items.filter((i) => i.entry.name.toLowerCase().includes(needle)) : items.slice();
    if (special === 'recents' && sort.key === 'name' && sort.asc) {
      return list; // newest first unless another order was picked
    }
    if (special === 'search' && sort.key === 'name' && sort.asc) return list; // best matches first
    const dir = sort.asc ? 1 : -1;
    const byName = (a: Item, b: Item) => a.entry.name.localeCompare(b.entry.name, undefined, { numeric: true, sensitivity: 'base' });
    list.sort((a, b) => {
      if (a.entry.dir !== b.entry.dir) return a.entry.dir ? -1 : 1;
      if (sort.key === 'size') return (a.entry.size - b.entry.size) * dir || byName(a, b);
      if (sort.key === 'mod') return (new Date(a.entry.mod_time).getTime() - new Date(b.entry.mod_time).getTime()) * dir;
      if (sort.key === 'kind') return kindLabel(a.entry.name, a.entry.dir).localeCompare(kindLabel(b.entry.name, b.entry.dir)) * dir || byName(a, b);
      return byName(a, b) * dir;
    });
    return list;
  }, [items, filter, sort, special]);

  // ---------- navigation ----------

  const here: Loc = { root, path, special, query };

  const goTo = (loc: Loc, history = true) => {
    if (loc.root === root && loc.path === path && loc.special === special && loc.query === query) return;
    if (history) {
      setBack((b) => [...b, here].slice(-50));
      setFwd([]);
    }
    setRoot(loc.root);
    setPath(loc.path);
    setSpecial(loc.special);
    setQuery(loc.query);
    setSelected(new Set());
    setAnchor(null);
    setFilter(loc.special === 'search' ? loc.query : '');
    setLook(null);
    pane.current?.focus();
  };
  const navigate = (p: string, nextRoot = root) => goTo({ root: nextRoot, path: p, special: null, query: '' });

  useEffect(() => {
    if (navReq) goTo({ root: navReq.root, path: navReq.path, special: navReq.special ?? null, query: '' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navReq?.n]);

  const single = selected.size === 1 ? (items.find((i) => selected.has(i.key)) ?? null) : null;
  useEffect(() => {
    if (root) onState(side, { root, path, special, sel: single?.entry ?? null, selPath: single?.path ?? path, unix: unixPerms });
  }, [side, root, path, special, single, unixPerms, onState]);

  const goBack = () => {
    const prev = back[back.length - 1];
    if (!prev) return;
    setBack((b) => b.slice(0, -1));
    setFwd((f) => [here, ...f]);
    goTo(prev, false);
  };
  const goFwd = () => {
    const next = fwd[0];
    if (!next) return;
    setFwd((f) => f.slice(1));
    setBack((b) => [...b, here]);
    goTo(next, false);
  };
  const goUp = () => !special && path !== '/' && navigate(parentPath(path));

  const setView = (v: View) => {
    setViewState(v);
    saveLocal('alfa.files.view', v);
  };
  const setSort = (s: { key: SortKey; asc: boolean }) => {
    setSortState(s);
    saveLocal('alfa.files.sort', `${s.key}:${s.asc ? 'asc' : 'desc'}`);
  };

  const runSearch = (q: string) => {
    if (!q.trim()) return;
    goTo({ root, path, special: 'search', query: q.trim() });
  };

  // ---------- actions ----------

  const selItems = () => shown.filter((i) => selected.has(i.key));
  /** The one location all selected items are in (Recents can mix them). */
  const selRoot = () => {
    const rs = new Set(selItems().map((i) => i.root));
    if (rs.size > 1) {
      toast('error', 'Those items are in different storage locations', 'Pick items from one location at a time.');
      return null;
    }
    return [...rs][0] ?? root;
  };

  const previewable = (i: Item) => !i.entry.dir && PREVIEWABLE.includes(fileKind(i.entry.name));
  const lookList = shown.filter(previewable);
  const open = (i: Item) => {
    if (i.entry.dir) {
      navigate(i.path, i.root);
      return;
    }
    const idx = lookList.findIndex((x) => x.key === i.key);
    if (idx >= 0) setLook(idx);
    else openInWindow(i);
  };
  const openInWindow = (i: Item) => openApp('viewer', { title: i.entry.name, props: { root: i.root, path: i.path }, key: `viewer:${i.root}:${i.path}` });
  const showInFolder = (i: Item) => {
    pendingSel.current = i.entry.name;
    navigate(parentPath(i.path), i.root);
  };

  const newFolder = () =>
    setDialog({
      kind: 'prompt',
      title: 'New folder',
      label: 'Name',
      initial: 'New folder',
      confirm: 'Create',
      onSubmit: async (name) => {
        const r = await fileApi.mkdir(root, path, name);
        if (!r.ok) toast('error', 'Could not create folder', r.error);
        pendingSel.current = name;
        await reload();
      },
    });

  const rename = (i: Item) => {
    const name = i.entry.name;
    const dot = name.lastIndexOf('.');
    setDialog({
      kind: 'prompt',
      title: 'Rename',
      label: 'New name',
      initial: name,
      selectTo: !i.entry.dir && dot > 0 ? dot : undefined,
      confirm: 'Rename',
      onSubmit: async (next) => {
        if (next === name) return;
        const r = await fileApi.rename(i.root, i.path, next);
        if (!r.ok) toast('error', `Could not rename ${name}`, r.error);
        if (!special) pendingSel.current = next;
        await reload();
      },
    });
  };

  const remove = (list: Item[], forcePermanent = false) => {
    if (!list.length) return;
    const r0 = selRoot();
    if (!r0) return;
    const permanent = forcePermanent || recycle;
    const what = list.length === 1 ? `“${list[0].entry.name}”` : `${list.length} items`;
    const run = async () => {
      const r = await fileApi.remove(r0, list.map((i) => i.path), permanent);
      if (r.ok) toast('success', permanent ? `Deleted ${what}` : `Moved ${what} to Trash`);
      else toast('error', 'Delete failed', r.error);
      setSelected(new Set());
      await reload();
      onRootsChanged();
    };
    if (permanent) {
      setDialog({ kind: 'confirm', title: `Permanently delete ${what}?`, body: 'This cannot be undone.', confirm: 'Delete forever', danger: true, onSubmit: run });
    } else {
      void run();
    }
  };

  const emptyBin = () =>
    setDialog({
      kind: 'confirm',
      title: 'Empty the Trash?',
      body: `Everything in the Trash of ${rootLabel(root)} will be permanently deleted.`,
      confirm: 'Empty Trash',
      danger: true,
      onSubmit: async () => {
        const r = await fileApi.remove(root, [RECYCLE], true);
        if (r.ok) toast('success', 'Trash emptied');
        else toast('error', 'Could not empty the Trash', r.error);
        await reload();
        onRootsChanged();
      },
    });

  const copyOrCut = (move: boolean) => {
    const list = selItems();
    const r0 = list.length ? selRoot() : null;
    if (!r0) return;
    setClip({ root: r0, paths: list.map((i) => i.path), move });
    toast('info', `${list.length} item${list.length > 1 ? 's' : ''} ${move ? 'cut' : 'copied'}`, 'Open a folder and paste');
  };

  const paste = async (into = path) => {
    if (!clip || special) return;
    if (clip.root !== root) {
      toast('error', 'Paste across storage locations is not supported yet');
      return;
    }
    const ok = await startTransfer(root, clip.paths, into, clip.move);
    if (ok && clip.move) setClip(null);
    if (into !== path) void reload();
  };

  const duplicate = async (list: Item[]) => {
    if (!list.length || special) return;
    await startTransfer(root, list.map((i) => i.path), path, false, 'rename');
  };

  const compress = (list: Item[]) => {
    if (!list.length || special) return;
    const first = list[0].entry;
    const stem = list.length === 1 ? (first.dir || first.name.lastIndexOf('.') <= 0 ? first.name : first.name.slice(0, first.name.lastIndexOf('.'))) : 'Archive';
    setDialog({
      kind: 'prompt',
      title: list.length === 1 ? `Compress “${first.name}”` : `Compress ${list.length} items`,
      label: 'Zip file name',
      initial: `${stem}.zip`,
      selectTo: stem.length,
      confirm: 'Compress',
      onSubmit: async (name) => {
        const out = await startArchiveJob('compress', root, list.map((i) => i.path), path, name);
        if (out?.[0]) {
          pendingSel.current = baseName(out[0]);
          toast('success', `Created ${baseName(out[0])}`);
        }
      },
    });
  };

  const extract = async (i: Item) => {
    const dest = parentPath(i.path);
    const out = await startArchiveJob('extract', i.root, [i.path], dest);
    if (out?.[0]) {
      if (!special && dest === path) pendingSel.current = baseName(out[0]);
      toast('success', `Extracted to “${baseName(out[0])}”`);
    }
  };

  const toOther = async (move: boolean) => {
    const list = selItems();
    if (!other || !list.length || special) return;
    if (other.root !== root) {
      toast('error', 'The other pane is on a different storage location', 'Copy and move work within one location for now.');
      return;
    }
    if (other.path === path) {
      toast('error', 'Both panes show the same folder');
      return;
    }
    setSelected(new Set());
    if (await startTransfer(root, list.map((i) => i.path), other.path, move)) onTransferred();
  };

  const download = async (list: Item[]) => {
    for (const i of list) {
      if (i.entry.dir) continue;
      if (!(await downloadFile(i.root, i.path))) toast('error', `Could not download ${i.entry.name}`);
    }
  };

  const doUpload = async (list: File[], dir = path) => {
    if (!list.length || !root || special) return;
    await startUpload(root, dir, list);
  };

  // ---------- selection ----------

  const clickItem = (e: MouseEvent, key: string) => {
    e.stopPropagation();
    pane.current?.focus();
    if (e.shiftKey && anchor) {
      const keys = shown.map((x) => x.key);
      const [a, b] = [keys.indexOf(anchor), keys.indexOf(key)].sort((x, y) => x - y);
      setSelected(new Set(keys.slice(a, b + 1)));
      return;
    }
    if (e.ctrlKey || e.metaKey) {
      setSelected((s) => {
        const n = new Set(s);
        if (n.has(key)) n.delete(key);
        else n.add(key);
        return n;
      });
    } else {
      setSelected(new Set([key]));
    }
    setAnchor(key);
  };

  const showMenu = (e: MouseEvent, list: MenuEntry[]) => {
    e.preventDefault();
    e.stopPropagation();
    const box = pane.current?.closest('.files-main')?.getBoundingClientRect();
    setMenu({ x: e.clientX - (box?.left ?? 0), y: e.clientY - (box?.top ?? 0), items: list });
  };
  const onContext = (e: MouseEvent, key: string | null) => {
    let sel = selected;
    if (key && !selected.has(key)) {
      sel = new Set([key]);
      setSelected(sel);
      setAnchor(key);
    }
    if (!key) {
      sel = new Set();
      setSelected(sel);
    }
    showMenu(e, menuItems(key ? shown.filter((i) => sel.has(i.key)) : []));
  };

  // Marquee: drag on empty space to select a rectangle of items.
  const onPanePointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest('[data-key], button, input, a, th, .empty-actions')) return;
    marq.current = { x: e.clientX, y: e.clientY, base: e.ctrlKey || e.metaKey || e.shiftKey ? new Set(selected) : new Set(), moved: false };
  };
  const onPanePointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const m = marq.current;
    const el = pane.current;
    if (!m || !el) return;
    if (!m.moved) {
      if (Math.abs(e.clientX - m.x) + Math.abs(e.clientY - m.y) < 5) return;
      m.moved = true;
      el.setPointerCapture(e.pointerId);
    }
    const box = el.getBoundingClientRect();
    const l = Math.min(m.x, e.clientX), t = Math.min(m.y, e.clientY);
    const r = Math.max(m.x, e.clientX), b = Math.max(m.y, e.clientY);
    setMarquee({ x: l - box.left + el.scrollLeft, y: t - box.top + el.scrollTop, w: r - l, h: b - t });
    const next = new Set(m.base);
    el.querySelectorAll<HTMLElement>('[data-key]').forEach((node) => {
      const rc = node.getBoundingClientRect();
      if (rc.right > l && rc.left < r && rc.bottom > t && rc.top < b) next.add(node.dataset.key!);
    });
    setSelected(next);
  };
  const onPanePointerUp = () => {
    if (marq.current?.moved) skipClick.current = true;
    marq.current = null;
    setMarquee(null);
  };

  const onKey = (e: KeyboardEvent) => {
    if ((e.target as HTMLElement).closest('input, textarea') || dialog || info || look !== null) return;
    const list = selItems();
    const mod = e.ctrlKey || e.metaKey;
    if (e.key === 'F5' && other) {
      void toOther(false);
    } else if (e.key === 'F6' && other) {
      void toOther(true);
    } else if (e.key === 'Tab' && dual && !mod) {
      onSwitchPane();
    } else if (e.key === ' ' && !mod) {
      if (list.length === 1 && previewable(list[0])) open(list[0]);
      else onTogglePreview();
    } else if (e.key === 'Delete') {
      remove(list, e.shiftKey);
    } else if (e.key === 'F2' && list.length === 1) {
      rename(list[0]);
    } else if (e.key === 'Enter' && list.length === 1) {
      open(list[0]);
    } else if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
      if (special) goBack();
      else goUp();
    } else if (e.altKey && e.key === 'ArrowLeft') {
      goBack();
    } else if (e.altKey && e.key === 'ArrowRight') {
      goFwd();
    } else if (mod && e.key.toLowerCase() === 'a') {
      setSelected(new Set(shown.map((x) => x.key)));
    } else if (mod && e.key.toLowerCase() === 'c') {
      copyOrCut(false);
    } else if (mod && e.key.toLowerCase() === 'x') {
      copyOrCut(true);
    } else if (mod && e.key.toLowerCase() === 'v') {
      void paste();
    } else if (mod && e.key.toLowerCase() === 'd') {
      void duplicate(list);
    } else if (mod && e.key.toLowerCase() === 'i' && list.length === 1) {
      setInfo(list[0]);
    } else if (e.key === 'Escape') {
      setSelected(new Set());
    } else {
      return;
    }
    e.preventDefault();
  };

  // ---------- drag & drop ----------

  const hasFiles = (e: DragEvent) => e.dataTransfer.types.includes('Files');
  const canDrop = !recycle && !special;
  const dnd = {
    onDragEnter: (e: DragEvent) => {
      if (!hasFiles(e) || !canDrop) return;
      e.preventDefault();
      dragDepth.current++;
      setDragOver(true);
    },
    onDragOver: (e: DragEvent) => {
      if (!canDrop) return;
      if (hasFiles(e)) {
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
      }
    },
    onDragLeave: () => {
      dragDepth.current = Math.max(0, dragDepth.current - 1);
      if (!dragDepth.current) setDragOver(false);
    },
    onDrop: (e: DragEvent) => {
      dragDepth.current = 0;
      setDragOver(false);
      if (!hasFiles(e) || !canDrop) return;
      e.preventDefault();
      void doUpload([...e.dataTransfer.files]);
    },
  };

  const dragStart = (e: DragEvent, i: Item) => {
    let list = selItems();
    if (!selected.has(i.key)) {
      setSelected(new Set([i.key]));
      list = [i];
    }
    if (new Set(list.map((x) => x.root)).size > 1) list = [i];
    const data: DragFiles = { root: i.root, paths: list.map((x) => x.path), dirs: list.map((x) => x.entry.dir) };
    e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(data));
    e.dataTransfer.effectAllowed = 'copyMove';
  };
  // A folder (tile, row or breadcrumb) accepts dragged items and dropped files.
  const folderDrop = (key: string, root2: string, target: string) => ({
    onDragOver: (e: DragEvent) => {
      const internal = e.dataTransfer.types.includes(DRAG_TYPE);
      if (!internal && !hasFiles(e)) return;
      if (internal && selected.has(key)) return;
      e.preventDefault();
      e.stopPropagation();
      e.dataTransfer.dropEffect = internal && !(e.ctrlKey || e.altKey) ? 'move' : 'copy';
      setDropKey(key);
    },
    onDragLeave: () => setDropKey((k) => (k === key ? null : k)),
    onDrop: (e: DragEvent) => {
      setDropKey(null);
      dragDepth.current = 0;
      setDragOver(false);
      const data = readDrag(e);
      if (data) {
        e.preventDefault();
        e.stopPropagation();
        if (data.root !== root2) return toast('error', 'Moving between storage locations is not supported yet');
        const paths = data.paths.filter((p) => p !== target && parentPath(p) !== target && !target.startsWith(p + '/'));
        if (paths.length) void startTransfer(root2, paths, target, !(e.ctrlKey || e.altKey)).then((ok) => ok && onTransferred());
      } else if (hasFiles(e)) {
        e.preventDefault();
        e.stopPropagation();
        void startUpload(root2, target, [...e.dataTransfer.files]);
      }
    },
  });

  // ---------- menus ----------

  const shareItem = (folder: string, name: string): MenuEntry | null => {
    if (root === 'system' || root.startsWith('net:') || special) return null;
    const existing = shares.find((sh) => sh.root === root && sh.path === folder);
    return existing
      ? { label: `Stop Sharing “${existing.name}”`, icon: 'network', onClick: () => onUnshare(existing) }
      : { label: 'Share on Network…', icon: 'network', onClick: () => onShare(root, folder, name) };
  };
  const favItem = (r: string, p: string): MenuEntry =>
    isFav(r, p)
      ? { label: 'Remove from Favorites', icon: 'star', onClick: () => onRemoveFav(r, p) }
      : { label: 'Add to Favorites', icon: 'star', onClick: () => onAddFavs(r, [p]) };

  const menuItems = (list: Item[]): MenuEntry[] => {
    const out: MenuEntry[] = [];
    const push = (...e: (MenuEntry | null | false)[]) => e.forEach((x) => x && out.push(x));
    if (!list.length) {
      if (special) return [{ label: 'Refresh', icon: 'restart', onClick: () => void reload() }];
      if (recycle) {
        return [
          { label: 'Empty Trash', icon: 'trash', danger: true, onClick: emptyBin, disabled: !items.length },
          'sep',
          { label: 'Refresh', icon: 'restart', onClick: () => void reload() },
        ];
      }
      const folderName = path === '/' ? rootLabel(root) : baseName(path);
      push(
        { label: 'New Folder', icon: 'folderPlus', onClick: newFolder },
        { label: 'Upload Files…', icon: 'upload', onClick: () => uploadInput.current?.click() },
        { label: 'Paste', icon: 'clipboard', shortcut: 'Ctrl+V', disabled: !clip, onClick: () => void paste() },
        'sep',
        { label: 'Select All', shortcut: 'Ctrl+A', onClick: () => setSelected(new Set(shown.map((x) => x.key))) },
        { label: 'Refresh', icon: 'restart', onClick: () => void reload() },
        'sep',
        path !== '/' || root !== 'system' ? favItem(root, path) : null,
        shareItem(path, folderName),
      );
      return out;
    }
    const one = list.length === 1 ? list[0] : null;
    const files = list.filter((i) => !i.entry.dir);
    if (one) {
      push({ label: 'Open', icon: one.entry.dir ? 'folder' : 'eye', shortcut: 'Enter', onClick: () => open(one) });
      if (!one.entry.dir) push({ label: 'Open in Window', icon: 'external', onClick: () => openInWindow(one) });
      if (special) push({ label: 'Show in Folder', icon: 'folder', onClick: () => showInFolder(one) });
    }
    push('sep');
    if (one && !recycle) push({ label: 'Rename…', icon: 'edit', shortcut: 'F2', onClick: () => rename(one) });
    if (!recycle) push({ label: 'Copy', icon: 'copy', shortcut: 'Ctrl+C', onClick: () => copyOrCut(false) });
    push({ label: 'Cut', icon: 'scissors', shortcut: 'Ctrl+X', onClick: () => copyOrCut(true) });
    if (one?.entry.dir && clip && !special && !recycle) push({ label: `Paste into “${one.entry.name}”`, icon: 'clipboard', onClick: () => void paste(one.path) });
    if (!special && !recycle) push({ label: 'Duplicate', icon: 'copy', shortcut: 'Ctrl+D', onClick: () => void duplicate(list) });
    if (!recycle) {
      push('sep');
      if (!special) push({ label: list.length === 1 ? 'Compress to .zip' : `Compress ${list.length} Items to .zip`, icon: 'archiveBox', onClick: () => compress(list) });
      if (one && !one.entry.dir && isArchive(one.entry.name)) push({ label: 'Extract Here', icon: 'archiveBox', onClick: () => void extract(one) });
      if (one?.entry.dir) push(favItem(one.root, one.path), shareItem(one.path, one.entry.name));
      else if (list.every((i) => i.entry.dir) && list.length > 1 && !special) push({ label: 'Add to Favorites', icon: 'star', onClick: () => onAddFavs(root, list.map((i) => i.path)) });
    }
    if (files.length) push({ label: files.length > 1 ? `Download ${files.length} Files` : 'Download', icon: 'download', onClick: () => void download(files) });
    if (one) push({ label: 'Get Info', icon: 'info', shortcut: 'Ctrl+I', onClick: () => setInfo(one) });
    if (other && !recycle && !special) {
      push('sep');
      push({ label: 'Copy to Other Pane', icon: 'copy', shortcut: 'F5', onClick: () => void toOther(false) });
      push({ label: 'Move to Other Pane', icon: 'swap', shortcut: 'F6', onClick: () => void toOther(true) });
    }
    push('sep');
    push({ label: recycle ? 'Delete Forever' : 'Move to Trash', icon: 'trash', shortcut: 'Del', danger: true, onClick: () => remove(list) });
    if (!recycle) push({ label: 'Delete Forever', shortcut: 'Shift+Del', danger: true, onClick: () => remove(list, true) });
    // Drop separators at the ends and doubled ones.
    return out.filter((x, i, a) => x !== 'sep' || (i > 0 && i < a.length - 1 && a[i - 1] !== 'sep'));
  };

  const sortMenu = (e: MouseEvent) =>
    showMenu(e, [
      ...(Object.keys(SORT_LABEL) as SortKey[]).map((k) => ({
        label: SORT_LABEL[k],
        icon: sort.key === k ? ('check' as const) : undefined,
        onClick: () => setSort({ key: k, asc: k === 'mod' || k === 'size' ? false : true }),
      })),
      'sep',
      { label: 'Ascending', icon: sort.asc ? 'check' : undefined, onClick: () => setSort({ ...sort, asc: true }) },
      { label: 'Descending', icon: !sort.asc ? 'check' : undefined, onClick: () => setSort({ ...sort, asc: false }) },
    ]);
  const moreMenu = (e: MouseEvent) =>
    showMenu(e, [
      { label: dual ? 'Single Pane' : 'Dual Pane', icon: 'columns', shortcut: 'F5/F6', onClick: onToggleDual },
      { label: preview ? 'Hide Preview Panel' : 'Show Preview Panel', icon: 'sidebarRight', onClick: onTogglePreview },
      'sep',
      ...(recycle ? [{ label: 'Empty Trash', icon: 'trash' as const, danger: true, disabled: !items.length, onClick: emptyBin }] : []),
      { label: 'Refresh', icon: 'restart', onClick: () => void reload() },
    ]);

  // ---------- render ----------

  const crumbs = special || path === '/' ? [] : path.slice(1).split('/');
  const selList = selItems();
  const selSize = selList.reduce((a, i) => a + (i.entry.dir ? 0 : i.entry.size), 0);
  const isCut = (i: Item) => !!clip?.move && clip.root === i.root && clip.paths.includes(i.path);
  const subtitle = (i: Item) => (i.entry.dir ? 'Folder' : fmtBytes(i.entry.size));
  const where = (i: Item) => {
    const p = parentPath(i.path);
    return p === '/' ? rootLabel(i.root) : baseName(p);
  };

  const groups: { title: string | null; list: Item[] }[] =
    special === 'recents' && sort.key === 'name' && sort.asc
      ? ['Today', 'This week', 'Earlier'].map((t) => ({ title: t, list: shown.filter((i) => ageGroup(i.entry.mod_time) === t) })).filter((g) => g.list.length)
      : [{ title: null, list: shown }];

  const thumb = (i: Item, size: number) => {
    const kind = fileKind(i.entry.name, i.entry.dir);
    const t = tickets[i.root];
    if (kind === 'image' && t) return <img className="ft-img" src={rawUrl(i.root, i.path, t)} alt="" loading="lazy" draggable={false} />;
    if (kind === 'video' && t)
      return (
        <span className="ft-video">
          <video src={`${rawUrl(i.root, i.path, t)}#t=0.5`} preload="metadata" muted playsInline />
          <span className="ft-play">
            <Icon name="play" size={14} />
          </span>
        </span>
      );
    return <FileGlyph name={i.entry.name} dir={i.entry.dir} size={size} shared={i.entry.dir && i.root === root && sharedPaths.has(i.path)} />;
  };

  const itemProps = (i: Item) => ({
    'data-key': i.key,
    draggable: !recycle,
    onDragStart: (e: DragEvent) => dragStart(e, i),
    onClick: (ev: MouseEvent) => clickItem(ev, i.key),
    onDoubleClick: () => open(i),
    onContextMenu: (ev: MouseEvent) => onContext(ev, i.key),
    ...(i.entry.dir && !recycle ? folderDrop(i.key, i.root, i.path) : {}),
  });

  const emptyState = () => {
    if (filter && !special) return <EmptyState icon="search" title="No matches" text={`Nothing in this folder matches “${filter}”.`} />;
    if (special === 'recents') return <EmptyState icon="clock" title="No recent files yet" text={note ?? 'Files you add or change show up here.'} />;
    if (special === 'search') return <EmptyState icon="search" title="Nothing found" text={note ?? `No file or folder names match “${query}”.`} />;
    if (recycle) return <EmptyState icon="trash" title="Trash is empty" text="Deleted items stay here until you empty the Trash." />;
    return (
      <EmptyState glyph title="This folder is empty" text="Drop files here, or upload them.">
        <button type="button" className="pill" onClick={() => uploadInput.current?.click()}>
          <Icon name="upload" size={15} /> Upload
        </button>
        <button type="button" className="pill ghost" onClick={newFolder}>
          <Icon name="folderPlus" size={15} /> New Folder
        </button>
      </EmptyState>
    );
  };

  const crumbIcon = root === rootInfo?.id && rootLabel(root) === 'Home' ? 'home' : 'drive';
  return (
    <div className={`files-main ${dual ? 'in-dual' : ''} ${dual && active ? 'active' : ''}`} onPointerDownCapture={onActivate} onFocusCapture={onActivate}>
      <div className="toolbar files-toolbar fx-toolbar">
        <button type="button" className="ghost icon-btn fx-side-toggle" aria-label="Show sidebar" onClick={onToggleSidebar}>
          <Icon name="menu" size={16} />
        </button>
        <div className="fx-nav">
          <button type="button" className="ghost icon-btn" aria-label="Back" title="Back (Alt+←)" disabled={!back.length} onClick={goBack}>
            <Icon name="chevronLeft" size={16} />
          </button>
          <button type="button" className="ghost icon-btn" aria-label="Forward" title="Forward (Alt+→)" disabled={!fwd.length} onClick={goFwd}>
            <Icon name="chevronRight" size={16} />
          </button>
        </div>

        <div className="crumbs fx-crumbs">
          {special ? (
            <span className="fx-crumb current">
              <Icon name={special === 'recents' ? 'clock' : 'search'} size={13} />
              {special === 'recents' ? 'Recents' : `Search: “${query}”`}
            </span>
          ) : (
            <>
              <button type="button" className={`fx-crumb ${crumbs.length ? '' : 'current'}`} onClick={() => navigate('/')} {...folderDrop('crumb:/', root, '/')}>
                <Icon name={crumbIcon} size={13} />
                {rootLabel(root)}
              </button>
              {crumbs.map((c, i) => {
                const p = '/' + crumbs.slice(0, i + 1).join('/');
                const last = i === crumbs.length - 1;
                return (
                  <span key={p} className="fx-crumb-wrap">
                    <Icon name="chevronRight" size={12} />
                    <button type="button" className={`fx-crumb ${last ? 'current' : ''} ${dropKey === 'crumb:' + p ? 'drop' : ''}`} onClick={() => navigate(p)} {...(last ? {} : folderDrop('crumb:' + p, root, p))}>
                      <Icon name={i === 0 && c === '.recycle' ? 'trash' : 'folder'} size={13} />
                      {i === 0 && c === '.recycle' ? 'Trash' : c}
                    </button>
                  </span>
                );
              })}
            </>
          )}
        </div>

        <div className="fx-actions">
          {recycle ? (
            <button type="button" className="pill danger fx-pill" disabled={!items.length} onClick={emptyBin}>
              <Icon name="trash" size={14} /> Empty Trash
            </button>
          ) : (
            !special && (
              <>
                <button type="button" className="pill fx-pill" title="New folder" onClick={newFolder}>
                  <Icon name="plus" size={14} /> <span className="fx-pill-label">Folder</span>
                </button>
                <button type="button" className="pill fx-pill" title="Upload files" onClick={() => uploadInput.current?.click()}>
                  <Icon name="upload" size={14} /> <span className="fx-pill-label">Upload</span>
                </button>
              </>
            )
          )}
          <label className="fx-search" title="Type to filter this folder; press Enter to search everywhere">
            <Icon name="search" size={14} />
            <input
              placeholder="Search"
              value={filter}
              onChange={(e) => {
                setFilter(e.target.value);
                if (!e.target.value && special === 'search') goBack();
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') runSearch(filter);
                else if (e.key === 'Escape') {
                  setFilter('');
                  if (special === 'search') goBack();
                  pane.current?.focus();
                }
              }}
            />
          </label>
          <div className="segmented fx-views" role="group" aria-label="View">
            <button type="button" className={view === 'grid' ? 'on' : ''} aria-label="Grid view" title="Grid view" onClick={() => setView('grid')}>
              <Icon name="grid" size={14} />
            </button>
            <button type="button" className={view === 'list' ? 'on' : ''} aria-label="List view" title="List view" onClick={() => setView('list')}>
              <Icon name="list" size={14} />
            </button>
          </div>
          <button type="button" className="ghost icon-btn" aria-label="Sort" title={`Sort by ${SORT_LABEL[sort.key].toLowerCase()}`} onClick={sortMenu}>
            <Icon name="sortArrows" size={16} />
          </button>
          <button type="button" className={`ghost icon-btn ${dual || preview ? 'on' : ''}`} aria-label="More" title="More" onClick={moreMenu}>
            <Icon name="moreDots" size={18} />
          </button>
        </div>
        <input
          ref={uploadInput}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            void doUpload([...(e.target.files ?? [])]);
            e.target.value = '';
          }}
        />
      </div>

      {recycle && (
        <div className="bin-banner small">
          <Icon name="info" size={14} /> Deleted items stay here until you empty the Trash. To restore one, cut it and paste it into a folder.
        </div>
      )}

      <div
        ref={pane}
        className={`files-pane fx-pane ${dragOver ? 'drag-over' : ''}`}
        tabIndex={0}
        onKeyDown={onKey}
        onClick={() => {
          if (skipClick.current) {
            skipClick.current = false;
            return;
          }
          setSelected(new Set());
        }}
        onContextMenu={(e) => onContext(e, null)}
        onPointerDown={onPanePointerDown}
        onPointerMove={onPanePointerMove}
        onPointerUp={onPanePointerUp}
        onPointerCancel={onPanePointerUp}
        {...dnd}
      >
        {listError ? (
          <EmptyState icon="alert" title="Couldn't open this folder" text={listError}>
            <button type="button" className="pill ghost" onClick={() => navigate('/')}>
              Go to the top
            </button>
          </EmptyState>
        ) : !shown.length ? (
          loading ? (
            <div className="fx-loading">
              <span className="spinner" />
            </div>
          ) : (
            emptyState()
          )
        ) : view === 'grid' ? (
          groups.map((g) => (
            <section key={g.title ?? 'all'} className="fx-group">
              {g.title && <h4 className="fx-group-title">{g.title}</h4>}
              <div className="fx-grid">
                {g.list.map((i) => (
                  <div key={i.key} className={`fx-tile ${selected.has(i.key) ? 'selected' : ''} ${isCut(i) ? 'cut' : ''} ${dropKey === i.key ? 'drop' : ''}`} title={special ? `${i.entry.name}\n${rootLabel(i.root)}${parentPath(i.path) === '/' ? '' : parentPath(i.path)}` : i.entry.name} {...itemProps(i)}>
                    <div className="fx-tile-icon">{thumb(i, 68)}</div>
                    <span className="fx-tile-name">{i.entry.name}</span>
                    <span className="fx-tile-sub">{special ? `${subtitle(i)} · ${where(i)}` : subtitle(i)}</span>
                  </div>
                ))}
              </div>
            </section>
          ))
        ) : (
          <table className="table files-table fx-table">
            <thead>
              <tr>
                {(
                  [
                    ['name', 'Name'],
                    ['mod', 'Modified'],
                    ['size', 'Size'],
                    ['kind', 'Kind'],
                  ] as [SortKey, string][]
                ).map(([k, label]) => (
                  <th key={k} className={`col-${k} ${k === 'size' ? 'num' : ''}`}>
                    <button
                      type="button"
                      className="th-sort"
                      onClick={(ev) => {
                        ev.stopPropagation();
                        setSort({ key: k, asc: sort.key === k ? !sort.asc : true });
                      }}
                    >
                      {label} {sort.key === k && <Icon name={sort.asc ? 'chevronUp' : 'chevronDown'} size={12} />}
                    </button>
                  </th>
                ))}
                {!special && <th className="perm-col">Permissions</th>}
              </tr>
            </thead>
            {groups.map((g) => (
              <tbody key={g.title ?? 'all'}>
                {g.title && (
                  <tr className="fx-group-row">
                    <td colSpan={5}>{g.title}</td>
                  </tr>
                )}
                {g.list.map((i) => (
                  <tr key={i.key} className={`${selected.has(i.key) ? 'selected' : ''} ${isCut(i) ? 'cut' : ''} ${dropKey === i.key ? 'drop' : ''}`} {...itemProps(i)}>
                    <td>
                      <div className="name-cell">
                        <FileGlyph name={i.entry.name} dir={i.entry.dir} size={24} shared={i.entry.dir && i.root === root && sharedPaths.has(i.path)} />
                        <span className="name-text" title={i.entry.name}>
                          {i.entry.name}
                          {special && <span className="fx-where"> — {where(i)}</span>}
                        </span>
                        {i.entry.symlink && <span className="chip tiny">link</span>}
                      </div>
                    </td>
                    <td className="muted col-mod">{new Date(i.entry.mod_time).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}</td>
                    <td className="num muted col-size">{i.entry.dir ? '—' : fmtBytes(i.entry.size)}</td>
                    <td className="muted col-kind">{kindLabel(i.entry.name, i.entry.dir)}</td>
                    {!special && (
                      <td className="perm-col">
                        <PermBadges entry={i.entry} compact unix={unixPerms} />
                        {unixPerms && i.entry.owner && <span className="perm-owner">{i.entry.owner}</span>}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            ))}
          </table>
        )}
        {marquee && <div className="fx-marquee" style={{ left: marquee.x, top: marquee.y, width: marquee.w, height: marquee.h }} />}
        {dragOver && (
          <div className="drop-hint">
            <Icon name="upload" size={32} />
            Drop to upload to {path === '/' ? rootLabel(root) : baseName(path)}
          </div>
        )}
      </div>

      <div className="statusbar small">
        {incoming.length ? (
          <span className="upload-status">
            <Icon name={incoming[0].kind === 'upload' ? 'upload' : incoming[0].kind === 'compress' || incoming[0].kind === 'extract' ? 'archiveBox' : 'copy'} size={13} />{' '}
            {{ upload: 'Uploading', move: 'Moving', copy: 'Copying', compress: 'Compressing', extract: 'Extracting' }[incoming[0].kind]} {incoming[0].title}
            {incoming.length > 1 ? ` and ${incoming.length - 1} more` : ''}
            <span className="upload-bar">
              <Bar value={incoming[0].total ? (incoming[0].done / incoming[0].total) * 100 : 0} />
            </span>
          </span>
        ) : selected.size ? (
          <span>
            {selected.size} of {shown.length} selected{selSize ? ` · ${fmtBytes(selSize)}` : ''}
          </span>
        ) : (
          <span className="muted">
            {shown.length} item{shown.length === 1 ? '' : 's'}
            {loading ? ' · loading…' : ''}
            {note && shown.length ? ` · ${note}` : ''}
          </span>
        )}
        {clip && (
          <span className="muted">
            {clip.paths.length} item{clip.paths.length > 1 ? 's' : ''} on clipboard ({clip.move ? 'cut' : 'copy'})
          </span>
        )}
      </div>

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={menu.items}
          onClose={() => {
            setMenu(null);
            pane.current?.focus();
          }}
        />
      )}
      {dialog && (
        <Dialog
          spec={dialog}
          onClose={() => {
            setDialog(null);
            requestAnimationFrame(() => pane.current?.focus());
          }}
        />
      )}
      {info && pane.current?.closest('.files') && createPortal(
        <InfoDialog
          root={info.root}
          rootName={rootLabel(info.root)}
          path={info.path}
          entry={info.entry}
          unix={unixPerms && !special}
          shared={info.entry.dir && shares.some((s) => s.root === info.root && s.path === info.path)}
          onClose={() => {
            setInfo(null);
            requestAnimationFrame(() => pane.current?.focus());
          }}
        />,
        pane.current.closest('.files')!,
      )}
      {look !== null && lookList.length > 0 && pane.current?.closest('.files') && createPortal(
        <FileLightbox
          items={lookList.map((i) => ({ root: i.root, path: i.path, name: i.entry.name, size: i.entry.size }))}
          index={Math.min(look, lookList.length - 1)}
          onIndex={(n) => {
            setLook(n);
            setSelected(new Set([lookList[n].key]));
            setAnchor(lookList[n].key);
          }}
          onClose={() => {
            setLook(null);
            requestAnimationFrame(() => pane.current?.focus());
          }}
        />,
        pane.current.closest('.files')!,
      )}
    </div>
  );
}

function EmptyState({ icon, glyph, title, text, children }: { icon?: 'search' | 'clock' | 'trash' | 'alert'; glyph?: boolean; title: string; text: string; children?: ReactNode }) {
  return (
    <div className="fx-empty">
      <span className="fx-empty-icon">{glyph ? <FileGlyph name="" dir size={64} /> : <Icon name={icon ?? 'folder'} size={30} />}</span>
      <b>{title}</b>
      <p className="muted">{text}</p>
      {children && <div className="empty-actions">{children}</div>}
    </div>
  );
}
