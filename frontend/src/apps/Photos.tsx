import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent as RMouseEvent, type ReactNode } from 'react';
import { fileApi } from '../api/files';
import { fromTrash, originalUrl, photoKey, photosApi, type Album, type Photo, type PhotoKey } from '../api/photos';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon, type IconName } from '../components/Icon';
import { tr, useT } from '../i18n';
import { linkPhones, sendFilesToPhone } from '../shell/linkActions';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { startUpload, useTransfers } from '../state/transfers';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { countText, itemsText, Thumb, ticketFor, type Tickets } from './PhotosCommon';
import { addedOf, deletedOf, Memories, PeriodCards, PhotoGrid, type Grouping } from './PhotosGrid';
import { PhotoViewer } from './PhotosViewer';

type View =
  | { kind: 'library' }
  | { kind: 'favorites' }
  | { kind: 'videos' }
  | { kind: 'recent' }
  | { kind: 'albums' }
  | { kind: 'album'; id: number }
  | { kind: 'trash' };

type Zoom = 'years' | 'months' | 'days' | 'all';

const MEDIA = /\.(jpe?g|png|gif|webp|bmp|heic|heif|mp4|mov|m4v|webm)$/i;
const RECENT_DAYS = 30;
const SHARE_MAX = 10;

function loadZoom(): Zoom {
  try {
    const z = localStorage.getItem('photos.zoom');
    if (z === 'years' || z === 'months' || z === 'days' || z === 'all') return z;
  } catch {
    /* default */
  }
  return 'days';
}

const sameView = (a: View, b: View) => a.kind === b.kind && (a.kind !== 'album' || (b.kind === 'album' && a.id === b.id));

/** Photos: the pictures and videos in the Photos folder of each drive. */
export function Photos(_: { win: WinState }) {
  const t = useT();
  const [items, setItems] = useState<Photo[] | null>(null);
  const [trash, setTrash] = useState<Photo[] | null>(null);
  const [albums, setAlbums] = useState<Album[]>([]);
  const [scanning, setScanning] = useState(false);
  const [uploadTo, setUploadTo] = useState<PhotoKey | null>(null);
  const [tickets, setTickets] = useState<Tickets>({ photos: {}, trash: {} });
  const [roots, setRoots] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [view, setView] = useState<View>({ kind: 'library' });
  const [zoom, setZoomState] = useState<Zoom>(loadZoom);
  const [jump, setJump] = useState<{ key: string; seq: number } | undefined>();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [selectMode, setSelectMode] = useState(false);
  const [viewer, setViewer] = useState<{ keys: string[]; index: number } | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const [renaming, setRenaming] = useState<number | null>(null);
  const [dropping, setDropping] = useState(false);
  const [picker, setPicker] = useState<Album | null>(null);
  const [query, setQuery] = useState('');
  const [searching, setSearching] = useState(false);
  const lastClicked = useRef<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const setZoom = (z: Zoom) => {
    setZoomState(z);
    try {
      localStorage.setItem('photos.zoom', z);
    } catch {
      /* not saved */
    }
  };

  const loadTrash = useCallback(async () => {
    const r = await photosApi.trash();
    if (r.ok) setTrash(r.data.items.map(fromTrash));
  }, []);

  const load = useCallback(async () => {
    const [l, a] = await Promise.all([photosApi.list(), photosApi.albums()]);
    if (!l.ok) {
      setError(l.error ?? tr('photos.loadFail'));
      setItems((v) => v ?? []);
      return;
    }
    setError('');
    setItems(l.data.items);
    setScanning(l.data.scanning);
    setUploadTo(l.data.upload);
    if (a.ok) setAlbums(a.data.albums);
  }, []);

  useEffect(() => {
    void load();
    void loadTrash();
    void fileApi.roots().then((r) => r.ok && setRoots(Object.fromEntries(r.data.map((x) => [x.id, x.name]))));
  }, [load, loadTrash]);

  // Links for thumbnails and originals; renewed well before they expire.
  useEffect(() => {
    let timer = 0;
    const get = async () => {
      const r = await photosApi.tickets();
      if (r.ok) setTickets({ photos: r.data.tickets, trash: r.data.trash ?? {} });
      timer = window.setTimeout(get, r.ok ? 45 * 60_000 : 30_000);
    };
    void get();
    return () => window.clearTimeout(timer);
  }, []);

  // While new photos are being dated, check back.
  useEffect(() => {
    if (!scanning) return;
    const timer = window.setTimeout(() => void load(), 2500);
    return () => window.clearTimeout(timer);
  }, [scanning, items, load]);

  // An upload finished: the library index catches up a few seconds later.
  const transfersVersion = useTransfers((s) => s.version);
  useEffect(() => {
    if (!transfersVersion) return;
    void load();
    const a = window.setTimeout(() => void load(), 5500);
    const b = window.setTimeout(() => void load(), 11000);
    return () => {
      window.clearTimeout(a);
      window.clearTimeout(b);
    };
  }, [transfersVersion, load]);

  const transfers = useTransfers((s) => s.list);
  const uploads = transfers.filter((x) => x.kind === 'upload' && x.status === 'running' && uploadTo && x.root === uploadTo.root && x.dest === uploadTo.path);
  const upDone = uploads.reduce((a, x) => a + x.done, 0);
  const upTotal = uploads.reduce((a, x) => a + x.total, 0);

  const byKey = useMemo(() => {
    const m = new Map<string, Photo>();
    for (const p of items ?? []) m.set(photoKey(p), p);
    for (const p of trash ?? []) m.set(photoKey(p), p);
    return m;
  }, [items, trash]);

  const albumById = view.kind === 'album' ? albums.find((a) => a.id === view.id) : undefined;

  // An album's pictures, newest first: the chosen cover leads.
  const albumPhotos = useCallback(
    (a: Album): Photo[] => {
      const list = a.items.map((k) => byKey.get(photoKey(k))).filter((p): p is Photo => !!p && !p.trash);
      list.sort((x, y) => (x.taken < y.taken ? 1 : -1));
      const chosen = a.cover && byKey.get(photoKey(a.cover));
      return chosen ? [chosen, ...list.filter((p) => p !== chosen)] : list;
    },
    [byKey],
  );

  const shown = useMemo(() => {
    const all = items ?? [];
    let list: Photo[];
    switch (view.kind) {
      case 'favorites':
        list = all.filter((p) => p.favorite);
        break;
      case 'videos':
        list = all.filter((p) => p.video);
        break;
      case 'recent': {
        const since = Date.now() - RECENT_DAYS * 86_400_000;
        list = all.filter((p) => new Date(p.mod_time).getTime() >= since).sort((a, b) => (a.mod_time < b.mod_time ? 1 : -1));
        break;
      }
      case 'album': {
        const keys = new Set((albumById?.items ?? []).map(photoKey));
        list = all.filter((p) => keys.has(photoKey(p)));
        break;
      }
      case 'trash':
        list = trash ?? [];
        break;
      case 'albums':
        list = [];
        break;
      default:
        list = all;
    }
    const q = query.trim().toLowerCase();
    return q ? list.filter((p) => p.path.toLowerCase().includes(q)) : list;
  }, [items, trash, view, albumById, query]);

  // A new view starts with nothing selected.
  useEffect(() => {
    setSelected(new Set());
    setSelectMode(false);
    lastClicked.current = null;
    setJump(undefined);
    if (view.kind === 'trash') void loadTrash();
  }, [view.kind, view.kind === 'album' ? view.id : 0, loadTrash]);

  const selectedPhotos = shown.filter((p) => selected.has(photoKey(p)));
  const selecting = selectMode || selected.size > 0;

  const shownRef = useRef(shown);
  shownRef.current = shown;
  const onToggle = useCallback((i: number, range: boolean) => {
    const list = shownRef.current;
    const from = lastClicked.current;
    setSelected((prev) => {
      const next = new Set(prev);
      if (range && from !== null) {
        const [a, b] = [Math.min(from, i), Math.max(from, i)];
        for (let j = a; j <= b && j < list.length; j++) next.add(photoKey(list[j]));
      } else {
        const k = photoKey(list[i]);
        if (next.has(k)) next.delete(k);
        else next.add(k);
      }
      return next;
    });
    lastClicked.current = i;
  }, []);
  const onToggleMany = useCallback((from: number, to: number, on: boolean) => {
    const list = shownRef.current;
    setSelected((prev) => {
      const next = new Set(prev);
      for (let j = from; j < to; j++) {
        if (on) next.add(photoKey(list[j]));
        else next.delete(photoKey(list[j]));
      }
      return next;
    });
  }, []);
  const onOpen = useCallback((i: number) => setViewer({ keys: shownRef.current.map(photoKey), index: i }), []);
  const onLongPress = useCallback(
    (i: number) => {
      setSelectMode(true);
      onToggle(i, false);
    },
    [onToggle],
  );

  const clearSelection = () => {
    setSelected(new Set());
    setSelectMode(false);
  };

  // ---------- actions ----------
  const setFavorite = async (list: Photo[], on: boolean) => {
    const keys = new Set(list.map(photoKey));
    setItems((v) => v?.map((p) => (keys.has(photoKey(p)) ? { ...p, favorite: on } : p)) ?? v);
    const r = await photosApi.favorite(list, on);
    if (!r.ok) {
      toast('error', tr('photos.toast.favFail'), r.error);
      void load();
    }
  };

  const remove = async (list: Photo[]) => {
    const one = list.length === 1;
    const ok = await confirmDialog({
      title: one ? tr('photos.confirm.deleteOne', { name: list[0].name }) : tr('photos.confirm.deleteMany', { n: list.length }),
      message: tr('photos.confirm.deleteBody'),
      confirmLabel: tr('photos.delete'),
      danger: true,
    });
    if (!ok) return false;
    const r = await photosApi.remove(list);
    if (!r.ok) {
      toast('error', tr('photos.toast.deleteFail'), r.error);
      return false;
    }
    const keys = new Set(list.map(photoKey));
    setItems((v) => v?.filter((p) => !keys.has(photoKey(p))) ?? v);
    clearSelection();
    void load();
    void loadTrash();
    return true;
  };

  const restore = async (list: Photo[]) => {
    const ids = list.flatMap((p) => (p.trash ? [p.trash.id] : []));
    if (!ids.length) return false;
    const r = await photosApi.restore(ids);
    if (!r.ok) {
      toast('error', tr('photos.toast.restoreFail'), r.error);
      return false;
    }
    toast(
      'success',
      list.length === 1 ? tr('photos.toast.restoredOne', { name: list[0].name }) : tr('photos.toast.restoredMany', { n: list.length }),
      tr('photos.toast.restoredBody'),
    );
    clearSelection();
    void loadTrash();
    void load();
    window.setTimeout(() => void load(), 2500);
    return true;
  };

  const purge = async (list: Photo[]) => {
    const ok = await confirmDialog({
      title: list.length === 1 ? tr('photos.confirm.purgeOne', { name: list[0].name }) : tr('photos.confirm.purgeMany', { n: list.length }),
      message: tr('photos.confirm.purgeBody'),
      confirmLabel: tr('photos.deleteForever'),
      danger: true,
    });
    if (!ok) return false;
    const r = await photosApi.purge(list.flatMap((p) => (p.trash ? [p.trash.id] : [])));
    if (!r.ok) {
      toast('error', tr('photos.toast.deleteFail'), r.error);
      return false;
    }
    clearSelection();
    void loadTrash();
    return true;
  };

  const newAlbum = async (list: Photo[] = []) => {
    const r = await photosApi.createAlbum(tr('photos.newAlbumName'), list);
    if (!r.ok) return toast('error', tr('photos.toast.albumFail'), r.error);
    clearSelection();
    await load();
    setView({ kind: 'album', id: r.data.id });
    setRenaming(r.data.id);
  };

  const addToAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, true);
    if (!r.ok) return toast('error', tr('photos.toast.addFail'), r.error);
    toast('success', tr('photos.toast.added', { album: a.name }), list.length === 1 ? list[0].name : itemsText(tr, list.length));
    clearSelection();
    void load();
  };

  const removeFromAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, false);
    if (!r.ok) return toast('error', tr('photos.toast.removeFail'), r.error);
    clearSelection();
    void load();
  };

  const setCover = async (a: Album, p: Photo) => {
    const r = await photosApi.setCover(a.id, p);
    if (!r.ok) return toast('error', tr('photos.toast.coverFail'), r.error);
    toast('success', tr('photos.toast.cover'), p.name);
    clearSelection();
    void load();
  };

  const deleteAlbum = async (a: Album) => {
    const ok = await confirmDialog({
      title: tr('photos.confirm.deleteAlbum', { name: a.name }),
      message: tr('photos.confirm.deleteAlbumBody'),
      confirmLabel: tr('photos.deleteAlbum'),
      danger: true,
    });
    if (!ok) return;
    const r = await photosApi.deleteAlbum(a.id);
    if (!r.ok) return toast('error', tr('photos.toast.deleteAlbumFail'), r.error);
    setView({ kind: 'albums' });
    void load();
  };

  const download = (list: Photo[]) => {
    const max = 30;
    if (list.length > max) toast('info', tr('photos.toast.downloadFirst', { n: max }), tr('photos.toast.downloadMore'));
    list.slice(0, max).forEach((p, i) => {
      const tk = ticketFor(tickets, p);
      if (!tk) return;
      window.setTimeout(() => {
        const a = document.createElement('a');
        a.href = originalUrl(p, tk, true);
        a.download = p.name;
        document.body.appendChild(a);
        a.click();
        a.remove();
      }, i * 300);
    });
  };

  /** Where a menu opened from a button goes: below it, or above it near the bottom. */
  const menuAt = (e: RMouseEvent, count: number) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const height = count * 36 + 14;
    return { x: r.left, y: r.bottom > window.innerHeight * 0.6 ? Math.max(8, r.top - 8 - height) : r.bottom + 6 };
  };

  const albumMenu = (e: RMouseEvent, list: Photo[]) => {
    const entries: MenuItem[] = [
      ...albums.map((a): MenuItem => ({ label: a.name, icon: 'albums', onClick: () => void addToAlbum(a, list) })),
      { label: tr('photos.newAlbumDots'), icon: 'plus', divider: albums.length > 0, onClick: () => void newAlbum(list) },
    ];
    setMenu({ ...menuAt(e, entries.length), items: entries });
  };

  // Share: the device's own share sheet (phones), or straight to a linked phone.
  const systemShare = async (list: Photo[]) => {
    try {
      const files = await Promise.all(
        list.slice(0, SHARE_MAX).map(async (p) => {
          const tk = ticketFor(tickets, p);
          const res = await fetch(originalUrl(p, tk ?? ''));
          if (!res.ok) throw new Error(String(res.status));
          const blob = await res.blob();
          return new File([blob], p.name, { type: blob.type });
        }),
      );
      if (!navigator.canShare?.({ files })) throw new Error('unsupported');
      await navigator.share({ files });
    } catch (err) {
      if ((err as Error)?.name !== 'AbortError') toast('error', tr('photos.toast.shareFail'));
    }
  };
  const shareMenu = (e: RMouseEvent, list: Photo[]) => {
    const entries: MenuItem[] = [];
    if (typeof navigator.canShare === 'function' && navigator.canShare({ files: [new File([], 'x.jpg', { type: 'image/jpeg' })] })) {
      entries.push({
        label: list.length > SHARE_MAX ? tr('photos.shareFirst', { n: SHARE_MAX }) : tr('photos.shareSheet'),
        icon: 'share',
        onClick: () => void systemShare(list),
      });
    }
    const phones = linkPhones();
    const byRoot = new Map<string, string[]>();
    for (const p of list) byRoot.set(p.root, [...(byRoot.get(p.root) ?? []), p.path]);
    for (const ph of phones) {
      entries.push({
        label: phones.length > 1 ? tr('link.sendToNamed', { device: ph.name }) : tr('link.sendToPhone'),
        icon: 'phone',
        onClick: () => {
          for (const [root, paths] of byRoot) void sendFilesToPhone(ph, root, paths);
        },
      });
    }
    entries.push({ label: tr('photos.download'), icon: 'download', divider: entries.length > 0, onClick: () => download(list) });
    setMenu({ ...menuAt(e, entries.length), items: entries });
  };

  const upload = (files: File[]) => {
    const media = files.filter((f) => f.type.startsWith('image/') || f.type.startsWith('video/') || MEDIA.test(f.name));
    if (!uploadTo) return;
    if (media.length < files.length) toast('info', tr('photos.toast.skipped', { n: files.length - media.length }), tr('photos.toast.skippedBody'));
    if (media.length) void startUpload(uploadTo.root, uploadTo.path, media);
  };

  const onDrop = (e: DragEvent) => {
    if (!e.dataTransfer.types.includes('Files')) return;
    e.preventDefault();
    setDropping(false);
    upload([...e.dataTransfer.files]);
  };

  const openPeriod = (key: string) => {
    setZoom(zoom === 'years' ? 'months' : 'days');
    setJump({ key, seq: Date.now() });
  };

  // ---------- render ----------
  const rootName = (id: string) => roots[id] ?? id;
  const favCount = useMemo(() => (items ?? []).filter((p) => p.favorite).length, [items]);
  const videoCount = useMemo(() => (items ?? []).filter((p) => p.video).length, [items]);

  const nav = (v: View, icon: IconName, label: string, count?: number) => (
    <button type="button" className={`pg-nav${sameView(v, view) ? ' on' : ''}`} aria-current={sameView(v, view) ? 'page' : undefined} onClick={() => setView(v)}>
      <Icon name={icon} size={17} />
      <span className="pg-nav-label">{label}</span>
      {count ? <span className="pg-count">{count.toLocaleString()}</span> : null}
    </button>
  );

  const title = {
    library: t('photos.library'),
    favorites: t('photos.favorites'),
    videos: t('photos.videos'),
    recent: t('photos.recent'),
    albums: t('photos.albums'),
    album: albumById?.name ?? '',
    trash: t('photos.trash'),
  }[view.kind];

  const subtitle = (() => {
    if (items === null) return '';
    if (view.kind === 'albums') return albums.length === 1 ? t('photos.oneAlbum') : t('photos.nAlbums', { n: albums.length });
    if (view.kind === 'trash') return shown.length ? t('photos.trashSub', { counts: countText(t, shown) }) : t('photos.trashSubEmpty');
    if (view.kind === 'recent') return shown.length ? t('photos.recentSub', { counts: countText(t, shown), days: RECENT_DAYS }) : t('photos.recentSubEmpty', { days: RECENT_DAYS });
    return countText(t, shown);
  })();

  const library = view.kind === 'library';
  const grouping: Grouping = view.kind === 'album' ? 'none' : library && zoom === 'all' ? 'month' : 'day';
  const target = library && zoom === 'all' ? 118 : library ? 196 : 176;
  const dateOf = view.kind === 'recent' ? addedOf : view.kind === 'trash' ? deletedOf : undefined;
  const periods = library && (zoom === 'years' || zoom === 'months') && !query;

  const viewerList = viewer ? viewer.keys.map((k) => byKey.get(k)).filter((p): p is Photo => !!p) : [];
  const viewerIndex = viewer ? Math.min(viewer.index, viewerList.length - 1) : -1;

  const emptyView = () => {
    if (query.trim()) return <EmptyNote icon="search" title={t('photos.empty.search')} text={t('photos.empty.searchText', { q: query.trim() })} />;
    switch (view.kind) {
      case 'favorites':
        return <EmptyNote icon="heart" title={t('photos.empty.favorites')} text={t('photos.empty.favoritesText')} />;
      case 'videos':
        return <EmptyNote icon="fileVideo" title={t('photos.empty.videos')} text={t('photos.empty.videosText')} />;
      case 'recent':
        return <EmptyNote icon="clock" title={t('photos.empty.recent')} text={t('photos.empty.recentText', { days: RECENT_DAYS })} />;
      case 'trash':
        return <EmptyNote icon="trash" title={t('photos.empty.trash')} text={t('photos.empty.trashText')} />;
      case 'album':
        return (
          <EmptyNote icon="albums" title={t('photos.empty.album')} text={t('photos.empty.albumText')}>
            {albumById && (
              <button type="button" className="pg-btn primary" onClick={() => setPicker(albumById)}>
                <Icon name="plus" size={16} /> {t('photos.addPhotos')}
              </button>
            )}
          </EmptyNote>
        );
      default:
        return (
          <div className="pg-empty">
            <span className="pg-empty-art" aria-hidden>
              <span className="a" />
              <span className="b" />
              <span className="c">
                <Icon name="image" size={30} />
              </span>
            </span>
            <h3>{t('photos.empty.library')}</h3>
            <p>
              {t(Object.keys(tickets.photos).length > 1 ? 'photos.empty.libraryTextMany' : 'photos.empty.libraryText')}
              {uploadTo ? <span className="pg-folder"> {t('photos.empty.folder', { drive: rootName(uploadTo.root) })}</span> : null}
            </p>
            <div className="pg-empty-actions">
              <button type="button" className="pg-btn primary" disabled={!uploadTo} onClick={() => fileInput.current?.click()}>
                <Icon name="upload" size={16} /> {t('photos.upload')}
              </button>
              <button type="button" className="pg-btn" disabled={!uploadTo} onClick={() => uploadTo && openApp('files', { props: { root: uploadTo.root, path: uploadTo.path } })}>
                <Icon name="folder" size={16} /> {t('photos.openInFiles')}
              </button>
            </div>
          </div>
        );
    }
  };

  const isTrash = view.kind === 'trash';
  const allFav = selectedPhotos.length > 0 && selectedPhotos.every((p) => p.favorite);
  const canSelect = view.kind !== 'albums' && !!shown.length && !periods;

  return (
    <div className="pg-app">
      <div
        className={`pg${dropping ? ' dropping' : ''}`}
        onDragOver={(e) => {
          if (!e.dataTransfer.types.includes('Files') || isTrash) return;
          e.preventDefault();
          setDropping(true);
        }}
        onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDropping(false)}
        onDrop={onDrop}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && selecting && !viewer) {
            clearSelection();
            e.stopPropagation();
          }
          if ((e.ctrlKey || e.metaKey) && e.key === 'a' && !viewer && shown.length && !(e.target instanceof HTMLInputElement)) {
            e.preventDefault();
            setSelected(new Set(shown.map(photoKey)));
          }
        }}
      >
        <nav className="pg-side" aria-label={t('photos.title')}>
          <div className="pg-brand">
            <span className="pg-brand-mark" aria-hidden>
              <Icon name="image" size={16} />
            </span>
            <span>{t('photos.title')}</span>
          </div>
          <div className="pg-navgroup">
            {nav({ kind: 'library' }, 'image', t('photos.library'), items?.length)}
            {nav({ kind: 'favorites' }, 'heart', t('photos.favorites'), favCount)}
            {nav({ kind: 'videos' }, 'fileVideo', t('photos.videos'), videoCount)}
            {nav({ kind: 'recent' }, 'clock', t('photos.recent'))}
            <span className="pg-only-narrow">{nav({ kind: 'albums' }, 'albums', t('photos.albums'))}</span>
          </div>
          <div className="pg-navgroup pg-albumlist">
            <div className="pg-side-head">
              <button type="button" className={`pg-side-link${view.kind === 'albums' ? ' on' : ''}`} onClick={() => setView({ kind: 'albums' })}>
                {t('photos.albums')}
              </button>
              <button type="button" className="pg-add" title={t('photos.newAlbum')} aria-label={t('photos.newAlbum')} onClick={() => void newAlbum()}>
                <Icon name="plus" size={15} />
              </button>
            </div>
            {albums.map((a) => {
              const c = albumPhotos(a)[0];
              return (
                <button key={a.id} type="button" className={`pg-nav pg-nav-album${sameView({ kind: 'album', id: a.id }, view) ? ' on' : ''}`} onClick={() => setView({ kind: 'album', id: a.id })}>
                  <span className="pg-mini">{c ? <Thumb photo={c} ticket={ticketFor(tickets, c)} /> : <Icon name="albums" size={13} />}</span>
                  <span className="pg-nav-label">{a.name}</span>
                  <span className="pg-count">{a.items.length || ''}</span>
                </button>
              );
            })}
            {!albums.length && <p className="pg-hint">{t('photos.albumsHint')}</p>}
          </div>
          <div className="pg-navgroup pg-side-end">{nav({ kind: 'trash' }, 'trash', t('photos.trash'), trash?.length)}</div>
        </nav>

        <div className={`pg-main${selecting ? ' selecting' : ''}`}>
          <header className="pg-header">
            <div className="pg-titles">
              {view.kind === 'album' && albumById ? (
                <>
                  <button type="button" className="pg-back" onClick={() => setView({ kind: 'albums' })}>
                    <Icon name="chevronLeft" size={14} /> {t('photos.albums')}
                  </button>
                  <AlbumTitle
                    key={albumById.id}
                    album={albumById}
                    editing={renaming === albumById.id}
                    onDone={async (name) => {
                      setRenaming(null);
                      if (name && name !== albumById.name) {
                        const r = await photosApi.renameAlbum(albumById.id, name);
                        if (!r.ok) toast('error', tr('photos.toast.renameFail'), r.error);
                        void load();
                      }
                    }}
                    onEdit={() => setRenaming(albumById.id)}
                  />
                </>
              ) : (
                <h2 className="pg-title">{title}</h2>
              )}
              <p className="pg-subtitle">
                {subtitle}
                {scanning && <span className="pg-updating"> · {t('photos.updating')}</span>}
              </p>
            </div>
            <div className="pg-tools">
              {uploads.length > 0 && (
                <span className="pg-uploading" title={t('photos.uploading')}>
                  <span className="spinner" />
                  {upTotal ? `${Math.round((upDone / upTotal) * 100)}%` : '…'}
                  <span className="pg-upbar">
                    <span style={{ width: `${upTotal ? (upDone / upTotal) * 100 : 5}%` }} />
                  </span>
                </span>
              )}
              {view.kind !== 'albums' && !!(items?.length || query) && (
                <label className={`pg-search${searching || query ? ' open' : ''}`}>
                  <Icon name="search" size={15} />
                  <input
                    type="search"
                    value={query}
                    placeholder={t('photos.search')}
                    aria-label={t('photos.search')}
                    onFocus={() => setSearching(true)}
                    onBlur={() => setSearching(false)}
                    onChange={(e) => setQuery(e.target.value)}
                    onKeyDown={(e) => e.key === 'Escape' && (setQuery(''), e.currentTarget.blur())}
                  />
                </label>
              )}
              {view.kind === 'album' && albumById && (
                <>
                  <button type="button" className="pg-btn" onClick={() => setPicker(albumById)}>
                    <Icon name="plus" size={15} /> <span className="pg-btn-label">{t('photos.addPhotos')}</span>
                  </button>
                  {albumById.items.length > 0 && (
                    <button type="button" className="pg-btn icon" aria-label={t('photos.v.slideshow')} title={t('photos.v.slideshow')} onClick={() => onOpen(0)}>
                      <Icon name="play" size={15} />
                    </button>
                  )}
                  <button
                    type="button"
                    className="pg-btn icon"
                    aria-label={t('photos.albumOptions')}
                    title={t('photos.albumOptions')}
                    onClick={(e) => {
                      const r = e.currentTarget.getBoundingClientRect();
                      setMenu({
                        x: r.right - 200,
                        y: r.bottom + 6,
                        items: [
                          { label: t('photos.rename'), icon: 'pencil', onClick: () => setRenaming(albumById.id) },
                          ...(albumById.cover ? [{ label: t('photos.newestCover'), icon: 'image' as IconName, onClick: () => void photosApi.setCover(albumById.id, null).then(() => load()) }] : []),
                          { label: t('photos.deleteAlbum'), icon: 'trash', danger: true, divider: true, onClick: () => void deleteAlbum(albumById) },
                        ],
                      });
                    }}
                  >
                    <Icon name="more" size={17} />
                  </button>
                </>
              )}
              {canSelect && (
                <button type="button" className={`pg-btn${selecting ? ' on' : ''}`} aria-pressed={selecting} onClick={() => (selecting ? clearSelection() : setSelectMode(true))}>
                  {selecting ? t('photos.done') : t('photos.select')}
                </button>
              )}
              {!isTrash && (
                <button type="button" className="pg-btn primary" disabled={!uploadTo} onClick={() => fileInput.current?.click()}>
                  <Icon name="upload" size={16} /> <span className="pg-btn-label">{t('photos.uploadShort')}</span>
                </button>
              )}
              <input
                ref={fileInput}
                type="file"
                accept="image/*,video/*,.heic,.heif"
                multiple
                hidden
                onChange={(e) => {
                  upload([...(e.target.files ?? [])]);
                  e.target.value = '';
                }}
              />
            </div>
            {library && !!items?.length && !query && (
              <div className="pg-zoom" role="tablist" aria-label={t('photos.zoom')}>
                {(['years', 'months', 'days', 'all'] as Zoom[]).map((z) => (
                  <button
                    key={z}
                    type="button"
                    role="tab"
                    aria-selected={zoom === z}
                    className={zoom === z ? 'on' : ''}
                    onClick={() => {
                      setZoom(z);
                      setJump(undefined);
                    }}
                  >
                    {t(`photos.zoom.${z}`)}
                  </button>
                ))}
              </div>
            )}
          </header>

          {error && <p className="pg-error">{error}</p>}

          {items === null ? (
            <div className="pg-empty">
              <span className="spinner" />
            </div>
          ) : view.kind === 'albums' ? (
            <AlbumsPage albums={albums} photosOf={albumPhotos} tickets={tickets} onOpen={(a) => setView({ kind: 'album', id: a.id })} onNew={() => void newAlbum()} />
          ) : periods && items.length ? (
            <PeriodCards key={zoom} items={items} unit={zoom === 'years' ? 'year' : 'month'} tickets={tickets} onPick={openPeriod} jump={jump} />
          ) : (
            <PhotoGrid
              key={view.kind === 'album' ? `album${view.id}` : `${view.kind}-${zoom}`}
              items={shown}
              grouping={grouping}
              dateOf={dateOf}
              target={target}
              tickets={tickets}
              selected={selected}
              selecting={selecting}
              onToggle={onToggle}
              onToggleMany={onToggleMany}
              onOpen={onOpen}
              onLongPress={onLongPress}
              jump={jump}
              top={library && !query ? <Memories items={items} tickets={tickets} onOpen={(list) => setViewer({ keys: list.map(photoKey), index: 0 })} /> : undefined}
              empty={emptyView()}
            />
          )}

          {selecting && (
            <div className="pg-actionbar" role="toolbar" aria-label={t('photos.selection')}>
              <span className="pg-selcount">{selected.size ? t('photos.nSelected', { n: selected.size }) : t('photos.selectItems')}</span>
              {isTrash ? (
                <>
                  <ActBtn icon="rewind" label={t('photos.restore')} disabled={!selected.size} onClick={() => void restore(selectedPhotos)} />
                  <ActBtn icon="trash" label={t('photos.deleteForever')} danger disabled={!selected.size} onClick={() => void purge(selectedPhotos)} />
                </>
              ) : (
                <>
                  <ActBtn icon="share" label={t('photos.share')} disabled={!selected.size} onClick={(e) => shareMenu(e, selectedPhotos)} />
                  <ActBtn icon="heart" label={allFav ? t('photos.unfavorite') : t('photos.favorite')} on={allFav} disabled={!selected.size} onClick={() => void setFavorite(selectedPhotos, !allFav)} />
                  <ActBtn icon="albums" label={t('photos.addToAlbum')} disabled={!selected.size} onClick={(e) => albumMenu(e, selectedPhotos)} />
                  {albumById && selected.size === 1 && <ActBtn icon="image" label={t('photos.makeCover')} onClick={() => void setCover(albumById, selectedPhotos[0])} />}
                  {albumById && <ActBtn icon="close" label={t('photos.removeFromAlbum')} disabled={!selected.size} onClick={() => void removeFromAlbum(albumById, selectedPhotos)} />}
                  <ActBtn icon="download" label={t('photos.download')} disabled={!selected.size} onClick={() => download(selectedPhotos)} />
                  <ActBtn icon="trash" label={t('photos.delete')} danger disabled={!selected.size} onClick={() => void remove(selectedPhotos)} />
                </>
              )}
              <button type="button" className="pg-act icon" aria-label={t('photos.cancelSelection')} title={t('photos.cancelSelectionKey')} onClick={clearSelection}>
                <Icon name="close" size={16} />
              </button>
            </div>
          )}

          {dropping && (
            <div className="pg-drop">
              <span className="pg-drop-icon">
                <Icon name="upload" size={30} />
              </span>
              <strong>{t('photos.drop')}</strong>
              <span>{uploadTo ? t('photos.dropTo', { drive: rootName(uploadTo.root) }) : ''}</span>
            </div>
          )}
        </div>

        {viewer && viewerList.length > 0 && viewerIndex >= 0 && (
          <PhotoViewer
            photos={viewerList}
            index={viewerIndex}
            tickets={tickets}
            rootName={rootName}
            onIndex={(i) => setViewer((v) => (v ? { ...v, index: i } : v))}
            onClose={() => setViewer(null)}
            onFavorite={(p) => void setFavorite([p], !p.favorite)}
            onAlbum={(e, p) => albumMenu(e, [p])}
            onShare={(e, p) => shareMenu(e, [p])}
            onDelete={async (p) => {
              const done = p.trash ? await purge([p]) : await remove([p]);
              if (done && viewerList.length <= 1) setViewer(null);
            }}
            onRestore={async (p) => {
              if ((await restore([p])) && viewerList.length <= 1) setViewer(null);
            }}
          />
        )}
        {picker && (
          <AlbumPicker
            album={picker}
            items={items ?? []}
            tickets={tickets}
            onClose={() => setPicker(null)}
            onAdd={async (list) => {
              setPicker(null);
              await addToAlbum(picker, list);
            }}
          />
        )}
        {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
      </div>
    </div>
  );
}

function ActBtn({ icon, label, onClick, disabled, danger, on }: { icon: IconName; label: string; onClick: (e: RMouseEvent) => void; disabled?: boolean; danger?: boolean; on?: boolean }) {
  return (
    <button type="button" className={`pg-act${danger ? ' danger' : ''}${on ? ' on' : ''}`} disabled={disabled} title={label} aria-label={label} onClick={onClick}>
      <Icon name={icon} size={17} />
      <span className="pg-act-label">{label}</span>
    </button>
  );
}

function AlbumTitle({ album, editing, onEdit, onDone }: { album: Album; editing: boolean; onEdit: () => void; onDone: (name: string) => void }) {
  const t = useT();
  const [name, setName] = useState(album.name);
  if (!editing) {
    return (
      <h2 className="pg-title pg-editable" title={t('photos.clickRename')} onClick={onEdit}>
        {album.name}
      </h2>
    );
  }
  return (
    <input
      className="pg-title-input"
      value={name}
      autoFocus
      maxLength={80}
      aria-label={t('photos.albumName')}
      onFocus={(e) => e.currentTarget.select()}
      onChange={(e) => setName(e.target.value)}
      onBlur={() => onDone(name.trim())}
      onKeyDown={(e) => {
        if (e.key === 'Enter') e.currentTarget.blur();
        if (e.key === 'Escape') {
          setName(album.name);
          onDone(album.name);
        }
      }}
    />
  );
}

/** Albums: a cover mosaic per album (the cover large, then the next three). */
function AlbumsPage({
  albums,
  photosOf,
  tickets,
  onOpen,
  onNew,
}: {
  albums: Album[];
  photosOf: (a: Album) => Photo[];
  tickets: Tickets;
  onOpen: (a: Album) => void;
  onNew: () => void;
}) {
  const t = useT();
  return (
    <div className="pg-scroll">
      <div className="pg-albums">
        {albums.map((a) => {
          const pics = photosOf(a).slice(0, 4);
          return (
            <button key={a.id} type="button" className="pg-album" onClick={() => onOpen(a)}>
              <span className={`pg-album-cover n${pics.length}`}>
                {pics.length ? pics.map((p) => <Thumb key={photoKey(p)} photo={p} ticket={ticketFor(tickets, p)} />) : <Icon name="albums" size={34} />}
              </span>
              <strong>{a.name}</strong>
              <span>{itemsText(t, a.items.length)}</span>
            </button>
          );
        })}
        <button type="button" className="pg-album new" onClick={onNew}>
          <span className="pg-album-cover">
            <Icon name="plus" size={30} />
          </span>
          <strong>{t('photos.newAlbum')}</strong>
          <span>{t('photos.newAlbumHint')}</span>
        </button>
      </div>
    </div>
  );
}

/** Choose photos from the library to add to an album. */
function AlbumPicker({
  album,
  items,
  tickets,
  onClose,
  onAdd,
}: {
  album: Album;
  items: Photo[];
  tickets: Tickets;
  onClose: () => void;
  onAdd: (list: Photo[]) => void;
}) {
  const t = useT();
  const list = useMemo(() => {
    const inside = new Set(album.items.map(photoKey));
    return items.filter((p) => !inside.has(photoKey(p)));
  }, [album, items]);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const last = useRef<number | null>(null);
  const listRef = useRef(list);
  listRef.current = list;
  const toggle = useCallback((i: number, range: boolean) => {
    const l = listRef.current;
    const from = last.current;
    setSel((prev) => {
      const next = new Set(prev);
      if (range && from !== null) {
        for (let j = Math.min(from, i); j <= Math.max(from, i); j++) next.add(photoKey(l[j]));
      } else if (next.has(photoKey(l[i]))) next.delete(photoKey(l[i]));
      else next.add(photoKey(l[i]));
      return next;
    });
    last.current = i;
  }, []);
  const many = useCallback((from: number, to: number, on: boolean) => {
    const l = listRef.current;
    setSel((prev) => {
      const next = new Set(prev);
      for (let j = from; j < to; j++) {
        if (on) next.add(photoKey(l[j]));
        else next.delete(photoKey(l[j]));
      }
      return next;
    });
  }, []);
  return (
    <div className="pg-picker" role="dialog" aria-label={t('photos.addTo', { name: album.name })} onKeyDown={(e) => e.key === 'Escape' && onClose()}>
      <div className="pg-picker-sheet">
        <header className="pg-picker-head">
          <div>
            <h2>{t('photos.addTo', { name: album.name })}</h2>
            <p className="pg-subtitle">{sel.size ? t('photos.nSelected', { n: sel.size }) : t('photos.pickerHint')}</p>
          </div>
          <button type="button" className="pg-btn" onClick={onClose}>
            {t('photos.cancel')}
          </button>
          <button type="button" className="pg-btn primary" disabled={!sel.size} onClick={() => onAdd(list.filter((p) => sel.has(photoKey(p))))}>
            {sel.size ? t('photos.addN', { n: sel.size }) : t('photos.add')}
          </button>
        </header>
        <PhotoGrid
          items={list}
          grouping="month"
          target={120}
          tickets={tickets}
          selected={sel}
          selecting
          onToggle={toggle}
          onToggleMany={many}
          onOpen={(i) => toggle(i, false)}
          empty={<EmptyNote icon="image" title={t('photos.empty.picker')} text={t('photos.empty.pickerText')} />}
        />
      </div>
    </div>
  );
}

function EmptyNote({ icon, title, text, children }: { icon: IconName; title: string; text: string; children?: ReactNode }) {
  return (
    <div className="pg-empty">
      <span className="pg-empty-icon">
        <Icon name={icon} size={28} />
      </span>
      <h3>{title}</h3>
      <p>{text}</p>
      {children && <div className="pg-empty-actions">{children}</div>}
    </div>
  );
}
