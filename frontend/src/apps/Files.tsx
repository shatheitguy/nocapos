import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent } from 'react';
import { create } from 'zustand';
import {
  RECYCLE,
  baseName,
  downloadFile,
  fileApi,
  fileKind,
  fileTicket,
  inRecycle,
  joinPath,
  parentPath,
  rawUrl,
  upload,
  type FileEntry,
  type FileKind,
  type FileRoot,
} from '../api/files';
import { ContextMenu, Dialog, type DialogSpec, type MenuEntry } from '../components/Dialog';
import { Bar } from '../components/Charts';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { FilePreview, PermBadges } from './FilesParts';

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

type SortKey = 'name' | 'size' | 'mod';
type View = 'grid' | 'list';

const KIND_ICON: Record<FileKind, IconName> = {
  folder: 'folder',
  image: 'fileImage',
  video: 'fileVideo',
  audio: 'fileAudio',
  pdf: 'filePdf',
  text: 'fileText',
  code: 'fileCode',
  archive: 'fileArchive',
  file: 'file',
};

function loadView(): View {
  try {
    return localStorage.getItem('alfa.files.view') === 'list' ? 'list' : 'grid';
  } catch {
    return 'grid';
  }
}

function loadFlag(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}
function saveFlag(key: string, on: boolean) {
  try {
    localStorage.setItem(key, on ? '1' : '0');
  } catch {
    /* ignore */
  }
}

/** What a pane tells the window: where it is and what is selected. */
interface PaneState {
  root: string;
  path: string;
  /** The single selected entry (for the preview), if exactly one. */
  sel: FileEntry | null;
  unix: boolean;
}
interface NavReq {
  root: string;
  path: string;
  n: number;
}

/**
 * Files: one or two panes (dual-pane, Commander style) with a shared sidebar
 * and an optional preview panel for the active pane's selection.
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

  const toggleDual = () => {
    saveFlag('alfa.files.dual', !dual);
    if (dual) setActive(0);
    setDual(!dual);
  };
  const togglePreview = () => {
    saveFlag('alfa.files.preview', !preview);
    setPreview(!preview);
  };

  const go = (root: string, path: string) =>
    setNav((n) => {
      const next: [NavReq | null, NavReq | null] = [n[0], n[1]];
      next[active] = { root, path, n: (n[active]?.n ?? 0) + 1 };
      return next;
    });
  const report = useCallback((side: 0 | 1, st: PaneState) => setStates((s) => (side === 0 ? [st, s[1]] : [s[0], st])), []);
  const bump = (side: 0 | 1) => setReloadN((r) => (side === 0 ? [r[0] + 1, r[1]] : [r[0], r[1] + 1]));

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
  const firstRoot = roots?.[0]?.id ?? '';
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
        other={dual && other ? { root: other.root, path: other.path } : null}
        onTransferred={() => bump(side === 0 ? 1 : 0)}
        onRootsChanged={() => void loadRoots()}
        onToggleDual={toggleDual}
        onTogglePreview={togglePreview}
        onSwitchPane={() => dual && setActive(side === 0 ? 1 : 0)}
      />
    );
  };

  return (
    <div className={`files ${dual ? 'dual' : ''} ${preview ? 'with-preview' : ''}`}>
      <nav className="sidebar files-sidebar">
        <div className="muted small upper side-label">Locations</div>
        {(roots ?? []).map((r) => {
          const used = r.total ? ((r.total - r.free) / r.total) * 100 : 0;
          const on = r.id === cur?.root && !inRecycle(cur?.path ?? '/');
          return (
            <button key={r.id} type="button" className={`root-btn ${on ? 'on' : ''}`} onClick={() => go(r.id, '/')}>
              <span className="root-line">
                <Icon name={r.id === 'home' ? 'home' : 'drive'} size={16} /> {r.name}
              </span>
              {r.total > 0 && (
                <>
                  <Bar value={used} />
                  <span className="muted small">{fmtBytes(r.free, 0)} free</span>
                </>
              )}
            </button>
          );
        })}
        <div className="side-spacer" />
        <button type="button" className={inRecycle(cur?.path ?? '/') ? 'on' : ''} onClick={() => cur && go(cur.root, RECYCLE)} disabled={!cur}>
          <Icon name="trash" size={16} /> Recycle Bin
        </button>
      </nav>

      <div className="files-panes">
        {roots && pane(0)}
        {roots && dual && pane(1)}
      </div>

      {preview && cur && (
        <FilePreview
          root={cur.root}
          path={cur.sel ? joinPath(cur.path, cur.sel.name) : cur.path}
          entry={cur.sel}
          unix={cur.unix}
          onClose={togglePreview}
        />
      )}
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
}: PaneProps) {
  const [root, setRoot] = useState<string>(initialRoot);
  const [path, setPath] = useState<string>(initialPath);
  const [unixPerms, setUnixPerms] = useState(true);
  const [back, setBack] = useState<string[]>([]);
  const [fwd, setFwd] = useState<string[]>([]);
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string | null>(null);
  const [view, setViewState] = useState<View>(loadView);
  const [sort, setSort] = useState<{ key: SortKey; asc: boolean }>({ key: 'name', asc: true });
  const [filter, setFilter] = useState('');
  const [menu, setMenu] = useState<{ x: number; y: number; target: string | null } | null>(null);
  const [dialog, setDialog] = useState<DialogSpec | null>(null);
  const [uploading, setUploading] = useState<{ names: string; loaded: number; total: number } | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [thumbTicket, setThumbTicket] = useState<string | null>(null);
  const clip = useClipboard((s) => s.clip);
  const setClip = useClipboard((s) => s.set);
  const pane = useRef<HTMLDivElement>(null);
  const uploadInput = useRef<HTMLInputElement>(null);
  const dragDepth = useRef(0);

  const recycle = inRecycle(path);
  const rootInfo = roots?.find((r) => r.id === root);

  // ---------- data ----------

  // Pick a location once they are known (or if ours went away).
  useEffect(() => {
    if (roots?.length && !roots.some((r) => r.id === root)) setRoot(roots[0].id);
  }, [roots, root]);

  const reload = useCallback(async () => {
    if (!root) return;
    setLoading(true);
    const r = await fileApi.list(root, path);
    setLoading(false);
    if (!r.ok) {
      setListError(r.error ?? 'Could not open folder');
      setEntries([]);
      return;
    }
    setListError(null);
    setEntries(r.data.entries);
    setUnixPerms(r.data.unix_perms !== false);
    setSelected((sel) => new Set([...sel].filter((n) => r.data.entries.some((e) => e.name === n))));
  }, [root, path]);

  useEffect(() => void reload(), [reload]);
  // The other pane changed this folder (copy / move into it).
  useEffect(() => {
    if (reloadN) void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadN]);

  // A folder-scoped link for image thumbnails.
  const hasImages = useMemo(() => entries.some((e) => fileKind(e.name, e.dir) === 'image'), [entries]);
  useEffect(() => {
    setThumbTicket(null);
    if (!root || view !== 'grid' || !hasImages) return;
    let live = true;
    void fileTicket(root, path).then((t) => live && setThumbTicket(t));
    return () => {
      live = false;
    };
  }, [root, path, view, hasImages]);

  const shown = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const list = needle ? entries.filter((e) => e.name.toLowerCase().includes(needle)) : entries.slice();
    const dir = sort.asc ? 1 : -1;
    list.sort((a, b) => {
      if (a.dir !== b.dir) return a.dir ? -1 : 1;
      if (sort.key === 'size') return (a.size - b.size) * dir;
      if (sort.key === 'mod') return (new Date(a.mod_time).getTime() - new Date(b.mod_time).getTime()) * dir;
      return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }) * dir;
    });
    return list;
  }, [entries, filter, sort]);

  // ---------- navigation ----------

  const navigate = (p: string, opts: { root?: string; history?: boolean } = {}) => {
    const nextRoot = opts.root ?? root;
    if (p === path && nextRoot === root) return;
    if (opts.history !== false && nextRoot === root) {
      setBack((b) => [...b, path].slice(-50));
      setFwd([]);
    } else if (nextRoot !== root) {
      setBack([]);
      setFwd([]);
    }
    setRoot(nextRoot);
    setPath(p);
    setSelected(new Set());
    setAnchor(null);
    setFilter('');
    pane.current?.focus();
  };

  // The sidebar navigates the active pane.
  useEffect(() => {
    if (navReq) navigate(navReq.path, { root: navReq.root });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navReq?.n]);

  // Tell the window where we are and what is selected (for the preview).
  const single = selected.size === 1 ? (entries.find((e) => selected.has(e.name)) ?? null) : null;
  useEffect(() => {
    if (root) onState(side, { root, path, sel: single, unix: unixPerms });
  }, [side, root, path, single, unixPerms, onState]);

  const goBack = () => {
    const prev = back[back.length - 1];
    if (prev === undefined) return;
    setBack((b) => b.slice(0, -1));
    setFwd((f) => [path, ...f]);
    setPath(prev);
    setSelected(new Set());
  };
  const goFwd = () => {
    const next = fwd[0];
    if (next === undefined) return;
    setFwd((f) => f.slice(1));
    setBack((b) => [...b, path]);
    setPath(next);
    setSelected(new Set());
  };
  const goUp = () => path !== '/' && navigate(parentPath(path));

  const setView = (v: View) => {
    setViewState(v);
    try {
      localStorage.setItem('alfa.files.view', v);
    } catch {
      /* ignore */
    }
  };

  // ---------- actions ----------

  const selPaths = () => [...selected].map((n) => joinPath(path, n));

  const open = (e: FileEntry) => {
    const p = joinPath(path, e.name);
    if (e.dir) {
      navigate(p);
      return;
    }
    openApp('viewer', { title: e.name, props: { root, path: p }, key: `viewer:${root}:${p}` });
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
        await reload();
        setSelected(new Set([name]));
      },
    });

  const rename = (name: string) => {
    const dot = name.lastIndexOf('.');
    const entry = entries.find((e) => e.name === name);
    setDialog({
      kind: 'prompt',
      title: 'Rename',
      label: 'New name',
      initial: name,
      selectTo: !entry?.dir && dot > 0 ? dot : undefined,
      confirm: 'Rename',
      onSubmit: async (next) => {
        if (next === name) return;
        const r = await fileApi.rename(root, joinPath(path, name), next);
        if (!r.ok) toast('error', `Could not rename ${name}`, r.error);
        await reload();
        setSelected(new Set([next]));
      },
    });
  };

  const remove = (names: string[], forcePermanent = false) => {
    if (!names.length) return;
    const permanent = forcePermanent || recycle;
    const what = names.length === 1 ? `“${names[0]}”` : `${names.length} items`;
    const run = async () => {
      const r = await fileApi.remove(root, names.map((n) => joinPath(path, n)), permanent);
      if (r.ok) toast('success', permanent ? `Deleted ${what}` : `Moved ${what} to the Recycle Bin`);
      else toast('error', 'Delete failed', r.error);
      setSelected(new Set());
      await reload();
      onRootsChanged();
    };
    if (permanent) {
      setDialog({
        kind: 'confirm',
        title: `Permanently delete ${what}?`,
        body: 'This cannot be undone.',
        confirm: 'Delete forever',
        danger: true,
        onSubmit: run,
      });
    } else {
      void run();
    }
  };

  const emptyBin = () =>
    setDialog({
      kind: 'confirm',
      title: 'Empty the Recycle Bin?',
      body: `Everything in the Recycle Bin of ${rootInfo?.name ?? 'this location'} will be permanently deleted.`,
      confirm: 'Empty Recycle Bin',
      danger: true,
      onSubmit: async () => {
        const r = await fileApi.remove(root, [RECYCLE], true);
        if (r.ok) toast('success', 'Recycle Bin emptied');
        else toast('error', 'Could not empty the Recycle Bin', r.error);
        await reload();
        onRootsChanged();
      },
    });

  const copyOrCut = (move: boolean) => {
    const paths = selPaths();
    if (!paths.length) return;
    setClip({ root, paths, move });
    toast('info', `${paths.length} item${paths.length > 1 ? 's' : ''} ${move ? 'cut' : 'copied'}`, 'Open a folder and paste');
  };

  const paste = async () => {
    if (!clip) return;
    if (clip.root !== root) {
      toast('error', 'Paste across storage locations is not supported yet');
      return;
    }
    const r = await fileApi.transfer(root, clip.paths, path, clip.move);
    if (!r.ok) toast('error', clip.move ? 'Move failed' : 'Copy failed', r.error);
    else if (clip.move) setClip(null);
    await reload();
    if (r.ok) setSelected(new Set(r.data.paths.map(baseName)));
    onRootsChanged();
  };

  const toOther = async (move: boolean) => {
    const paths = selPaths();
    if (!other || !paths.length) return;
    if (other.root !== root) {
      toast('error', 'The other pane is on a different storage location', 'Copy and move work within one location for now.');
      return;
    }
    if (other.path === path) {
      toast('error', 'Both panes show the same folder');
      return;
    }
    const r = await fileApi.transfer(root, paths, other.path, move);
    if (!r.ok) {
      toast('error', move ? 'Move failed' : 'Copy failed', r.error);
      return;
    }
    const n = paths.length;
    toast('success', `${move ? 'Moved' : 'Copied'} ${n} item${n > 1 ? 's' : ''}`, `to ${other.path === '/' ? 'the top folder' : baseName(other.path)}`);
    setSelected(new Set());
    await reload();
    onTransferred();
    onRootsChanged();
  };

  const download = async (names: string[]) => {
    for (const n of names) {
      const e = entries.find((x) => x.name === n);
      if (!e || e.dir) continue;
      if (!(await downloadFile(root, joinPath(path, n)))) toast('error', `Could not download ${n}`);
    }
  };

  const doUpload = async (list: File[]) => {
    if (!list.length || !root) return;
    const total = list.reduce((a, f) => a + f.size, 0);
    const names = list.length === 1 ? list[0].name : `${list.length} files`;
    setUploading({ names, loaded: 0, total });
    const r = await upload(root, path, list, (p) => setUploading({ names, loaded: p.loaded, total: p.total }));
    setUploading(null);
    if (r.ok) toast('success', `Uploaded ${names}`, fmtBytes(total));
    else toast('error', `Upload of ${names} failed`, r.error);
    await reload();
    if (r.ok) setSelected(new Set(r.data.paths.map(baseName)));
    onRootsChanged();
  };

  // ---------- selection ----------

  const clickItem = (e: MouseEvent, name: string) => {
    e.stopPropagation();
    pane.current?.focus();
    if (e.shiftKey && anchor) {
      const names = shown.map((x) => x.name);
      const [a, b] = [names.indexOf(anchor), names.indexOf(name)].sort((x, y) => x - y);
      setSelected(new Set(names.slice(a, b + 1)));
      return;
    }
    if (e.ctrlKey || e.metaKey) {
      setSelected((s) => {
        const n = new Set(s);
        if (n.has(name)) n.delete(name);
        else n.add(name);
        return n;
      });
    } else {
      setSelected(new Set([name]));
    }
    setAnchor(name);
  };

  const onContext = (e: MouseEvent, name: string | null) => {
    e.preventDefault();
    e.stopPropagation();
    if (name && !selected.has(name)) {
      setSelected(new Set([name]));
      setAnchor(name);
    }
    if (!name) setSelected(new Set());
    const box = pane.current?.closest('.files-main')?.getBoundingClientRect();
    setMenu({ x: e.clientX - (box?.left ?? 0), y: e.clientY - (box?.top ?? 0), target: name });
  };

  const onKey = (e: KeyboardEvent) => {
    if ((e.target as HTMLElement).closest('input, textarea') || dialog) return;
    const names = [...selected];
    const mod = e.ctrlKey || e.metaKey;
    if (e.key === 'F5' && other) {
      void toOther(false);
    } else if (e.key === 'F6' && other) {
      void toOther(true);
    } else if (e.key === 'Tab' && dual && !mod) {
      onSwitchPane();
    } else if (e.key === ' ' && !mod) {
      onTogglePreview();
    } else if (e.key === 'Delete') {
      remove(names, e.shiftKey);
    } else if (e.key === 'F2' && names.length === 1) {
      rename(names[0]);
    } else if (e.key === 'Enter' && names.length === 1) {
      const it = entries.find((x) => x.name === names[0]);
      if (it) open(it);
    } else if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
      goUp();
    } else if (e.altKey && e.key === 'ArrowLeft') {
      goBack();
    } else if (e.altKey && e.key === 'ArrowRight') {
      goFwd();
    } else if (mod && e.key.toLowerCase() === 'a') {
      setSelected(new Set(shown.map((x) => x.name)));
    } else if (mod && e.key.toLowerCase() === 'c') {
      copyOrCut(false);
    } else if (mod && e.key.toLowerCase() === 'x') {
      copyOrCut(true);
    } else if (mod && e.key.toLowerCase() === 'v') {
      void paste();
    } else if (e.key === 'Escape') {
      setSelected(new Set());
    } else {
      return;
    }
    e.preventDefault();
  };

  // ---------- drag & drop upload ----------

  const hasFiles = (e: DragEvent) => e.dataTransfer.types.includes('Files');
  const dnd = {
    onDragEnter: (e: DragEvent) => {
      if (!hasFiles(e) || recycle) return;
      e.preventDefault();
      dragDepth.current++;
      setDragOver(true);
    },
    onDragOver: (e: DragEvent) => {
      if (!hasFiles(e) || recycle) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'copy';
    },
    onDragLeave: () => {
      dragDepth.current = Math.max(0, dragDepth.current - 1);
      if (!dragDepth.current) setDragOver(false);
    },
    onDrop: (e: DragEvent) => {
      if (!hasFiles(e) || recycle) return;
      e.preventDefault();
      dragDepth.current = 0;
      setDragOver(false);
      void doUpload([...e.dataTransfer.files]);
    },
  };

  // ---------- menus ----------

  const menuItems = (target: string | null): MenuEntry[] => {
    const names = target ? [...selected] : [];
    const single = names.length === 1 ? entries.find((e) => e.name === names[0]) : undefined;
    if (!target) {
      return recycle
        ? [
            { label: 'Empty Recycle Bin', icon: 'trash', danger: true, onClick: emptyBin, disabled: !entries.length },
            'sep',
            { label: 'Refresh', icon: 'restart', onClick: () => void reload() },
          ]
        : [
            { label: 'New folder', icon: 'folderPlus', onClick: newFolder },
            { label: 'Upload files', icon: 'upload', onClick: () => uploadInput.current?.click() },
            { label: 'Paste', icon: 'clipboard', shortcut: 'Ctrl+V', disabled: !clip, onClick: () => void paste() },
            'sep',
            { label: 'Select all', shortcut: 'Ctrl+A', onClick: () => setSelected(new Set(shown.map((x) => x.name))) },
            { label: 'Refresh', icon: 'restart', onClick: () => void reload() },
          ];
    }
    const items: MenuEntry[] = [];
    if (single) items.push({ label: single.dir ? 'Open' : 'Open / Preview', icon: single.dir ? 'folder' : 'eye', shortcut: 'Enter', onClick: () => open(single) });
    if (names.some((n) => !entries.find((e) => e.name === n)?.dir))
      items.push({ label: 'Download', icon: 'download', onClick: () => void download(names) });
    items.push('sep');
    items.push({ label: 'Cut', icon: 'scissors', shortcut: 'Ctrl+X', onClick: () => copyOrCut(true) });
    if (!recycle) items.push({ label: 'Copy', icon: 'copy', shortcut: 'Ctrl+C', onClick: () => copyOrCut(false) });
    if (single && !recycle) items.push({ label: 'Rename', icon: 'edit', shortcut: 'F2', onClick: () => rename(single.name) });
    if (other && !recycle) {
      items.push('sep');
      items.push({ label: 'Copy to other pane', icon: 'copy', shortcut: 'F5', onClick: () => void toOther(false) });
      items.push({ label: 'Move to other pane', icon: 'swap', shortcut: 'F6', onClick: () => void toOther(true) });
    }
    items.push('sep');
    items.push({
      label: recycle ? 'Delete forever' : 'Move to Recycle Bin',
      icon: 'trash',
      shortcut: 'Del',
      danger: true,
      onClick: () => remove(names),
    });
    if (!recycle) items.push({ label: 'Delete forever', shortcut: 'Shift+Del', danger: true, onClick: () => remove(names, true) });
    return items;
  };

  // ---------- render ----------

  const crumbs = path === '/' ? [] : path.slice(1).split('/');
  const selEntries = entries.filter((e) => selected.has(e.name));
  const selSize = selEntries.reduce((a, e) => a + (e.dir ? 0 : e.size), 0);

  return (
    <div
      className={`files-main ${dual ? 'in-dual' : ''} ${dual && active ? 'active' : ''}`}
      onPointerDownCapture={onActivate}
      onFocusCapture={onActivate}
    >
        <div className="toolbar files-toolbar">
          <div className="btn-group">
            <button type="button" className="ghost icon-btn" aria-label="Back" disabled={!back.length} onClick={goBack}>
              <Icon name="chevronLeft" size={16} />
            </button>
            <button type="button" className="ghost icon-btn" aria-label="Forward" disabled={!fwd.length} onClick={goFwd}>
              <Icon name="chevronRight" size={16} />
            </button>
            <button type="button" className="ghost icon-btn" aria-label="Up" disabled={path === '/'} onClick={goUp}>
              <Icon name="chevronUp" size={16} />
            </button>
          </div>

          <div className="crumbs">
            <button type="button" className="crumb" onClick={() => navigate('/')}>
              {rootInfo?.name ?? 'Files'}
            </button>
            {crumbs.map((c, i) => (
              <span key={i} className="crumb-wrap">
                <Icon name="chevronRight" size={12} />
                <button
                  type="button"
                  className="crumb"
                  onClick={() => navigate('/' + crumbs.slice(0, i + 1).join('/'))}
                >
                  {i === 0 && c === '.recycle' ? 'Recycle Bin' : c}
                </button>
              </span>
            ))}
          </div>

          <label className="search compact files-search">
            <Icon name="search" size={14} />
            <input placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </label>

          <div className="btn-group">
            {recycle ? (
              <button type="button" className="ghost danger small" disabled={!entries.length} onClick={emptyBin}>
                <Icon name="trash" size={14} /> Empty
              </button>
            ) : (
              <>
                <button type="button" className="ghost icon-btn" title="New folder" onClick={newFolder}>
                  <Icon name="folderPlus" size={16} />
                </button>
                <button type="button" className="ghost icon-btn" title="Upload" onClick={() => uploadInput.current?.click()}>
                  <Icon name="upload" size={16} />
                </button>
              </>
            )}
            <button type="button" className={`ghost icon-btn ${dual ? 'on' : ''}`} title={dual ? 'Single pane' : 'Dual pane (F5 copy · F6 move · Tab switch)'} onClick={onToggleDual}>
              <Icon name="columns" size={16} />
            </button>
            <button type="button" className={`ghost icon-btn ${preview ? 'on' : ''}`} title="Preview (Space)" onClick={onTogglePreview}>
              <Icon name="sidebarRight" size={16} />
            </button>
            <button
              type="button"
              className="ghost icon-btn"
              title={view === 'grid' ? 'List view' : 'Grid view'}
              onClick={() => setView(view === 'grid' ? 'list' : 'grid')}
            >
              <Icon name={view === 'grid' ? 'list' : 'grid'} size={16} />
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
            <Icon name="info" size={14} /> Deleted items stay here until you empty the bin. To restore, cut an item and paste it
            into a folder.
          </div>
        )}

        <div
          ref={pane}
          className={`files-pane ${dragOver ? 'drag-over' : ''}`}
          tabIndex={0}
          onKeyDown={onKey}
          onClick={() => setSelected(new Set())}
          onContextMenu={(e) => onContext(e, null)}
          {...dnd}
        >
          {listError ? (
            <div className="empty">
              <Icon name="alert" size={28} />
              <p className="muted">{listError}</p>
              <button type="button" className="ghost" onClick={() => navigate('/')}>
                Go to top
              </button>
            </div>
          ) : !shown.length && !loading ? (
            <div className="empty">
              <Icon name={recycle ? 'trash' : 'folder'} size={36} />
              <p className="muted">
                {filter ? 'No items match the filter.' : recycle ? 'The Recycle Bin is empty.' : 'This folder is empty. Drop files here to upload.'}
              </p>
            </div>
          ) : view === 'grid' ? (
            <div className="file-grid">
              {shown.map((e) => {
                const kind = fileKind(e.name, e.dir);
                const sel = selected.has(e.name);
                const isCut = clip?.move && clip.root === root && clip.paths.includes(joinPath(path, e.name));
                return (
                  <div
                    key={e.name}
                    className={`file-tile ${sel ? 'selected' : ''} ${isCut ? 'cut' : ''}`}
                    onClick={(ev) => clickItem(ev, e.name)}
                    onDoubleClick={() => open(e)}
                    onContextMenu={(ev) => onContext(ev, e.name)}
                    title={e.name}
                  >
                    <div className={`file-thumb kind-${kind}`}>
                      {kind === 'image' && thumbTicket ? (
                        <img src={rawUrl(root, joinPath(path, e.name), thumbTicket)} alt="" loading="lazy" draggable={false} />
                      ) : (
                        <Icon name={KIND_ICON[kind]} size={kind === 'folder' ? 46 : 38} />
                      )}
                    </div>
                    <span className="file-name">{e.name}</span>
                  </div>
                );
              })}
            </div>
          ) : (
            <table className="table files-table">
              <thead>
                <tr>
                  {(
                    [
                      ['name', 'Name'],
                      ['mod', 'Modified'],
                      ['size', 'Size'],
                    ] as [SortKey, string][]
                  ).map(([k, label]) => (
                    <th key={k} className={k === 'size' ? 'num' : ''}>
                      <button
                        type="button"
                        className="th-sort"
                        onClick={(ev) => {
                          ev.stopPropagation();
                          setSort((s) => ({ key: k, asc: s.key === k ? !s.asc : true }));
                        }}
                      >
                        {label} {sort.key === k && <Icon name={sort.asc ? 'chevronUp' : 'arrowDown'} size={12} />}
                      </button>
                    </th>
                  ))}
                  <th className="perm-col">Permissions</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((e) => {
                  const kind = fileKind(e.name, e.dir);
                  const isCut = clip?.move && clip.root === root && clip.paths.includes(joinPath(path, e.name));
                  return (
                    <tr
                      key={e.name}
                      className={`${selected.has(e.name) ? 'selected' : ''} ${isCut ? 'cut' : ''}`}
                      onClick={(ev) => clickItem(ev, e.name)}
                      onDoubleClick={() => open(e)}
                      onContextMenu={(ev) => onContext(ev, e.name)}
                    >
                      <td>
                        <span className={`row-icon kind-${kind}`}>
                          <Icon name={KIND_ICON[kind]} size={17} />
                        </span>
                        {e.name}
                        {e.symlink && <span className="chip tiny">link</span>}
                      </td>
                      <td className="muted">{new Date(e.mod_time).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}</td>
                      <td className="num muted">{e.dir ? '—' : fmtBytes(e.size)}</td>
                      <td className="perm-col">
                        <PermBadges entry={e} compact unix={unixPerms} />
                        {unixPerms && e.owner && <span className="perm-owner">{e.owner}</span>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
          {dragOver && (
            <div className="drop-hint">
              <Icon name="upload" size={32} />
              Drop to upload to {path === '/' ? rootInfo?.name : baseName(path)}
            </div>
          )}
        </div>

        <div className="statusbar small">
          {uploading ? (
            <span className="upload-status">
              <Icon name="upload" size={13} /> Uploading {uploading.names} — {fmtBytes(uploading.loaded)} of {fmtBytes(uploading.total)}
              <span className="upload-bar">
                <Bar value={uploading.total ? (uploading.loaded / uploading.total) * 100 : 0} />
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
          items={menuItems(menu.target)}
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
            // Return keyboard focus to the file pane so shortcuts keep working.
            requestAnimationFrame(() => pane.current?.focus());
          }}
        />
      )}
    </div>
  );
}
