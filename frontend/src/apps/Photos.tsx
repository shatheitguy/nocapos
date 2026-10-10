import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent as RMouseEvent, type ReactNode } from 'react';
import { fileApi } from '../api/files';
import { fromTrash, originalUrl, photoKey, photosApi, thumbUrl, type Album, type Photo, type PhotoKey } from '../api/photos';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon, type IconName } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { startUpload, useTransfers } from '../state/transfers';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { ticketFor, type Tickets } from './PhotosCommon';
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
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

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
      setError(l.error ?? 'Could not load your photos');
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
    const t = window.setTimeout(() => void load(), 2500);
    return () => window.clearTimeout(t);
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
  const uploads = transfers.filter((t) => t.kind === 'upload' && t.status === 'running' && uploadTo && t.root === uploadTo.root && t.dest === uploadTo.path);
  const upDone = uploads.reduce((a, t) => a + t.done, 0);
  const upTotal = uploads.reduce((a, t) => a + t.total, 0);

  const byKey = useMemo(() => {
    const m = new Map<string, Photo>();
    for (const p of items ?? []) m.set(photoKey(p), p);
    for (const p of trash ?? []) m.set(photoKey(p), p);
    return m;
  }, [items, trash]);

  const albumById = view.kind === 'album' ? albums.find((a) => a.id === view.id) : undefined;

  // Album cover: the chosen one, else the newest photo in it.
  const coverOf = useCallback(
    (a: Album): Photo | undefined => {
      const chosen = a.cover && byKey.get(photoKey(a.cover));
      if (chosen) return chosen;
      let best: Photo | undefined;
      for (const k of a.items) {
        const p = byKey.get(photoKey(k));
        if (p && (!best || p.taken > best.taken)) best = p;
      }
      return best;
    },
    [byKey],
  );

  const shown = useMemo(() => {
    const all = items ?? [];
    switch (view.kind) {
      case 'favorites':
        return all.filter((p) => p.favorite);
      case 'videos':
        return all.filter((p) => p.video);
      case 'recent': {
        const since = Date.now() - RECENT_DAYS * 86_400_000;
        return all.filter((p) => new Date(p.mod_time).getTime() >= since).sort((a, b) => (a.mod_time < b.mod_time ? 1 : -1));
      }
      case 'album': {
        const keys = new Set((albumById?.items ?? []).map(photoKey));
        return all.filter((p) => keys.has(photoKey(p)));
      }
      case 'trash':
        return trash ?? [];
      case 'albums':
        return [];
      default:
        return all;
    }
  }, [items, trash, view, albumById]);

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
      toast('error', 'Could not update favorites', r.error);
      void load();
    }
  };

  const remove = async (list: Photo[]) => {
    const one = list.length === 1;
    const ok = await confirmDialog({
      title: one ? `Delete “${list[0].name}”?` : `Delete ${list.length} items?`,
      message: 'They move to Recently deleted (the Recycle Bin of their drive), where you can restore them.',
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return false;
    const r = await photosApi.remove(list);
    if (!r.ok) {
      toast('error', 'Could not delete', r.error);
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
      toast('error', 'Could not restore', r.error);
      return false;
    }
    toast('success', list.length === 1 ? `Restored “${list[0].name}”` : `Restored ${list.length} items`, 'They are back in your library.');
    clearSelection();
    void loadTrash();
    void load();
    window.setTimeout(() => void load(), 2500);
    return true;
  };

  const purge = async (list: Photo[]) => {
    const ok = await confirmDialog({
      title: list.length === 1 ? `Delete “${list[0].name}” forever?` : `Delete ${list.length} items forever?`,
      message: 'They are removed from the Recycle Bin for good. This can’t be undone.',
      confirmLabel: 'Delete Forever',
      danger: true,
    });
    if (!ok) return false;
    const r = await photosApi.purge(list.flatMap((p) => (p.trash ? [p.trash.id] : [])));
    if (!r.ok) {
      toast('error', 'Could not delete', r.error);
      return false;
    }
    clearSelection();
    void loadTrash();
    return true;
  };

  const newAlbum = async (list: Photo[] = []) => {
    const r = await photosApi.createAlbum('New Album', list);
    if (!r.ok) return toast('error', 'Could not create the album', r.error);
    clearSelection();
    await load();
    setView({ kind: 'album', id: r.data.id });
    setRenaming(r.data.id);
  };

  const addToAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, true);
    if (!r.ok) return toast('error', 'Could not add to the album', r.error);
    toast('success', `Added to “${a.name}”`, list.length === 1 ? list[0].name : `${list.length} items`);
    clearSelection();
    void load();
  };

  const removeFromAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, false);
    if (!r.ok) return toast('error', 'Could not remove from the album', r.error);
    clearSelection();
    void load();
  };

  const setCover = async (a: Album, p: Photo) => {
    const r = await photosApi.setCover(a.id, p);
    if (!r.ok) return toast('error', 'Could not change the cover', r.error);
    toast('success', 'Album cover changed', p.name);
    clearSelection();
    void load();
  };

  const deleteAlbum = async (a: Album) => {
    const ok = await confirmDialog({
      title: `Delete the album “${a.name}”?`,
      message: 'Only the album goes; the photos stay in your library.',
      confirmLabel: 'Delete Album',
      danger: true,
    });
    if (!ok) return;
    const r = await photosApi.deleteAlbum(a.id);
    if (!r.ok) return toast('error', 'Could not delete the album', r.error);
    setView({ kind: 'albums' });
    void load();
  };

  const download = (list: Photo[]) => {
    const max = 30;
    if (list.length > max) toast('info', `Downloading the first ${max}`, 'To get more at once, open the Photos folder in Files.');
    list.slice(0, max).forEach((p, i) => {
      const t = ticketFor(tickets, p);
      if (!t) return;
      window.setTimeout(() => {
        const a = document.createElement('a');
        a.href = originalUrl(p, t, true);
        a.download = p.name;
        document.body.appendChild(a);
        a.click();
        a.remove();
      }, i * 300);
    });
  };

  const albumMenu = (e: RMouseEvent, list: Photo[]) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    // From the selection bar near the bottom, open upwards.
    const height = (albums.length + 1) * 36 + 14;
    setMenu({
      x: r.left,
      y: r.bottom > window.innerHeight * 0.6 ? Math.max(8, r.top - 8 - height) : r.bottom + 6,
      items: [
        ...albums.map((a): MenuItem => ({ label: a.name, icon: 'albums', onClick: () => void addToAlbum(a, list) })),
        { label: 'New Album…', icon: 'plus', onClick: () => void newAlbum(list) },
      ],
    });
  };

  const upload = (files: File[]) => {
    const media = files.filter((f) => f.type.startsWith('image/') || f.type.startsWith('video/') || MEDIA.test(f.name));
    if (!uploadTo) return;
    if (media.length < files.length) toast('info', `${files.length - media.length} skipped`, 'Only pictures and videos go into Photos.');
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
    <button type="button" className={`pg-nav${sameView(v, view) ? ' on' : ''}`} onClick={() => setView(v)}>
      <Icon name={icon} size={17} />
      <span className="pg-nav-label">{label}</span>
      {count ? <span className="pg-count">{count}</span> : null}
    </button>
  );

  const title =
    view.kind === 'favorites'
      ? 'Favorites'
      : view.kind === 'videos'
        ? 'Videos'
        : view.kind === 'recent'
          ? 'Recently Added'
          : view.kind === 'albums'
            ? 'Albums'
            : view.kind === 'trash'
              ? 'Recently Deleted'
              : 'Library';

  const counts = () => {
    if (items === null) return '';
    if (view.kind === 'albums') return plural(albums.length, 'album');
    const v = shown.filter((p) => p.video).length;
    const ph = shown.length - v;
    const parts = [ph || !v ? plural(ph, 'photo') : '', v ? plural(v, 'video') : ''].filter(Boolean);
    return parts.join(', ');
  };

  const subtitle =
    view.kind === 'trash'
      ? shown.length
        ? `${counts()} · kept in the Recycle Bin of their drive until you delete them`
        : 'Kept in the Recycle Bin of their drive until you delete them'
      : view.kind === 'recent'
        ? shown.length
          ? `${counts()} added in the last ${RECENT_DAYS} days`
          : `Added in the last ${RECENT_DAYS} days`
        : counts();

  const grouping: Grouping = view.kind === 'album' ? 'none' : view.kind === 'library' && zoom === 'all' ? 'month' : 'day';
  const tileMin = view.kind === 'library' && zoom === 'all' ? 104 : view.kind === 'library' ? 168 : 150;
  const dateOf = view.kind === 'recent' ? addedOf : view.kind === 'trash' ? deletedOf : undefined;

  const viewerList = viewer ? viewer.keys.map((k) => byKey.get(k)).filter((p): p is Photo => !!p) : [];
  const viewerIndex = viewer ? Math.min(viewer.index, viewerList.length - 1) : -1;

  const emptyView = () => {
    switch (view.kind) {
      case 'favorites':
        return <EmptyNote icon="heart" title="No favorites yet" text="Tap the heart on a photo to keep it here." />;
      case 'videos':
        return <EmptyNote icon="fileVideo" title="No videos yet" text="Videos you add to Photos show up here." />;
      case 'recent':
        return <EmptyNote icon="clock" title="Nothing added lately" text={`Photos and videos added in the last ${RECENT_DAYS} days show up here.`} />;
      case 'trash':
        return <EmptyNote icon="trash" title="Nothing recently deleted" text="Photos you delete wait here, so you can change your mind." />;
      case 'album':
        return (
          <EmptyNote icon="albums" title="This album is empty" text="Add photos from your library.">
            {albumById && (
              <button type="button" className="primary pill" onClick={() => setPicker(albumById)}>
                <Icon name="plus" size={16} /> Add Photos
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
            <h3>Your photos will appear here</h3>
            <p>
              Photos shows the pictures and videos in the <strong>Photos</strong> folder of {Object.keys(tickets.photos).length > 1 ? 'each drive' : 'your drive'}
              {uploadTo ? (
                <>
                  {' '}
                  (<span className="pg-folder">{rootName(uploadTo.root)} › Photos</span>)
                </>
              ) : null}
              , including subfolders. Upload from this device, drop files here, or copy them into that folder.
            </p>
            <div className="pg-empty-actions">
              <button type="button" className="primary pill" disabled={!uploadTo} onClick={() => fileInput.current?.click()}>
                <Icon name="upload" size={16} /> Upload Photos
              </button>
              <button
                type="button"
                className="pill"
                disabled={!uploadTo}
                onClick={() => uploadTo && openApp('files', { props: { root: uploadTo.root, path: uploadTo.path } })}
              >
                <Icon name="folder" size={16} /> Open in Files
              </button>
            </div>
          </div>
        );
    }
  };

  const isTrash = view.kind === 'trash';
  const allFav = selectedPhotos.length > 0 && selectedPhotos.every((p) => p.favorite);

  return (
    <div
      className={`app-split pg${dropping ? ' dropping' : ''}`}
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
      <nav className="sidebar pg-side">
        <h1 className="app-title pg-brand">Photos</h1>
        <div className="pg-navgroup">
          {nav({ kind: 'library' }, 'image', 'Library', items?.length)}
          {nav({ kind: 'favorites' }, 'heart', 'Favorites', favCount)}
          {nav({ kind: 'videos' }, 'fileVideo', 'Videos', videoCount)}
          {nav({ kind: 'recent' }, 'clock', 'Recently Added')}
        </div>
        <div className="pg-navgroup">
          <div className="pg-side-head">
            <button type="button" className={`pg-side-link${view.kind === 'albums' ? ' on' : ''}`} onClick={() => setView({ kind: 'albums' })}>
              Albums
            </button>
            <button type="button" className="icon-btn ghost pg-add" title="New album" aria-label="New album" onClick={() => void newAlbum()}>
              <Icon name="plus" size={15} />
            </button>
          </div>
          {albums.map((a) => {
            const c = coverOf(a);
            const t = c && ticketFor(tickets, c);
            return (
              <button key={a.id} type="button" className={`pg-nav pg-nav-album${sameView({ kind: 'album', id: a.id }, view) ? ' on' : ''}`} onClick={() => setView({ kind: 'album', id: a.id })}>
                <span className="pg-mini">{c && t ? <img src={thumbUrl(c, t)} alt="" loading="lazy" draggable={false} /> : <Icon name="albums" size={13} />}</span>
                <span className="pg-nav-label">{a.name}</span>
                <span className="pg-count">{a.items.length || ''}</span>
              </button>
            );
          })}
          {!albums.length && <p className="pg-hint">Select photos and choose Add to Album, or press +.</p>}
        </div>
        <div className="pg-navgroup pg-side-end">{nav({ kind: 'trash' }, 'trash', 'Recently Deleted', trash?.length)}</div>
      </nav>

      <div className="pg-main">
        <header className="pg-header">
          <div className="pg-titles">
            {view.kind === 'album' && albumById ? (
              <>
                <button type="button" className="pg-back" onClick={() => setView({ kind: 'albums' })}>
                  <Icon name="chevronLeft" size={14} /> Albums
                </button>
                <AlbumTitle
                  key={albumById.id}
                  album={albumById}
                  editing={renaming === albumById.id}
                  onDone={async (name) => {
                    setRenaming(null);
                    if (name && name !== albumById.name) {
                      const r = await photosApi.renameAlbum(albumById.id, name);
                      if (!r.ok) toast('error', 'Could not rename the album', r.error);
                      void load();
                    }
                  }}
                  onEdit={() => setRenaming(albumById.id)}
                />
              </>
            ) : (
              <h2 className="app-title">{title}</h2>
            )}
            <p className="app-subtitle">
              {view.kind === 'album' ? counts() : subtitle}
              {scanning && ' · updating…'}
            </p>
          </div>
          <div className="pg-tools">
            {uploads.length > 0 && (
              <span className="pg-uploading" title="Uploading">
                <span className="spinner" />
                Uploading {upTotal ? `${Math.round((upDone / upTotal) * 100)}%` : '…'}
                <span className="pg-upbar">
                  <span style={{ width: `${upTotal ? (upDone / upTotal) * 100 : 5}%` }} />
                </span>
              </span>
            )}
            {view.kind === 'library' && !!items?.length && (
              <div className="segmented pg-zoom" role="tablist" aria-label="Zoom">
                {(['years', 'months', 'days', 'all'] as Zoom[]).map((z) => (
                  <button key={z} type="button" role="tab" aria-selected={zoom === z} className={zoom === z ? 'on' : ''}
                    onClick={() => {
                      setZoom(z);
                      setJump(undefined);
                    }}
                  >
                    {z === 'all' ? 'All Photos' : z[0].toUpperCase() + z.slice(1)}
                  </button>
                ))}
              </div>
            )}
            {view.kind === 'album' && albumById && (
              <>
                <button type="button" className="pill pg-soft" onClick={() => setPicker(albumById)}>
                  <Icon name="plus" size={15} /> Add Photos
                </button>
                <button
                  type="button"
                  className="icon-btn ghost"
                  aria-label="Album options"
                  title="Album options"
                  onClick={(e) => {
                    const r = e.currentTarget.getBoundingClientRect();
                    setMenu({
                      x: r.left - 120,
                      y: r.bottom + 6,
                      items: [
                        { label: 'Rename', icon: 'pencil', onClick: () => setRenaming(albumById.id) },
                        ...(albumById.cover ? [{ label: 'Use Newest as Cover', icon: 'image' as IconName, onClick: () => void photosApi.setCover(albumById.id, null).then(() => load()) }] : []),
                        { label: 'Delete Album', icon: 'trash', danger: true, onClick: () => void deleteAlbum(albumById) },
                      ],
                    });
                  }}
                >
                  <Icon name="more" size={17} />
                </button>
              </>
            )}
            {view.kind !== 'albums' && !!shown.length && !(view.kind === 'library' && (zoom === 'years' || zoom === 'months')) && (
              <button type="button" className={`pill pg-soft${selecting ? ' on' : ''}`} onClick={() => (selecting ? clearSelection() : setSelectMode(true))}>
                {selecting ? 'Done' : 'Select'}
              </button>
            )}
            {!isTrash && (
              <button type="button" className="primary pill" disabled={!uploadTo} onClick={() => fileInput.current?.click()}>
                <Icon name="upload" size={16} /> Upload
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
        </header>

        {error && <p className="error pg-error">{error}</p>}

        {items === null ? (
          <div className="pg-empty">
            <span className="spinner" />
          </div>
        ) : view.kind === 'albums' ? (
          <AlbumsPage albums={albums} coverOf={coverOf} tickets={tickets} onOpen={(a) => setView({ kind: 'album', id: a.id })} onNew={() => void newAlbum()} />
        ) : view.kind === 'library' && (zoom === 'years' || zoom === 'months') && items.length ? (
          <PeriodCards key={zoom} items={items} unit={zoom === 'years' ? 'year' : 'month'} tickets={tickets} onPick={openPeriod} jump={jump} />
        ) : (
          <PhotoGrid
            key={view.kind === 'album' ? `album${view.id}` : `${view.kind}-${zoom}`}
            items={shown}
            grouping={grouping}
            dateOf={dateOf}
            tileMin={tileMin}
            tickets={tickets}
            selected={selected}
            selecting={selecting}
            onToggle={onToggle}
            onToggleMany={onToggleMany}
            onOpen={onOpen}
            jump={jump}
            top={
              view.kind === 'library' ? (
                <Memories items={items} tickets={tickets} onOpen={(list) => setViewer({ keys: list.map(photoKey), index: 0 })} />
              ) : undefined
            }
            empty={emptyView()}
          />
        )}

        {selecting && (
          <div className="pg-actionbar" role="toolbar" aria-label="Selection">
            <span className="pg-selcount">{selected.size ? `${selected.size} selected` : 'Select items'}</span>
            {isTrash ? (
              <>
                <button type="button" className="pg-act" disabled={!selected.size} onClick={() => void restore(selectedPhotos)}>
                  <Icon name="rewind" size={16} /> Restore
                </button>
                <button type="button" className="pg-act danger" disabled={!selected.size} onClick={() => void purge(selectedPhotos)}>
                  <Icon name="trash" size={16} /> Delete Forever
                </button>
              </>
            ) : (
              <>
                <button type="button" className="pg-act" disabled={!selected.size} onClick={() => void setFavorite(selectedPhotos, !allFav)} title={allFav ? 'Unfavorite' : 'Favorite'}>
                  <Icon name="heart" size={16} /> <span className="pg-act-label">{allFav ? 'Unfavorite' : 'Favorite'}</span>
                </button>
                <button type="button" className="pg-act" disabled={!selected.size} onClick={(e) => albumMenu(e, selectedPhotos)} title="Add to Album">
                  <Icon name="albums" size={16} /> <span className="pg-act-label">Add to Album</span>
                </button>
                {albumById && (
                  <>
                    {selected.size === 1 && (
                      <button type="button" className="pg-act" onClick={() => void setCover(albumById, selectedPhotos[0])} title="Make Cover">
                        <Icon name="image" size={16} /> <span className="pg-act-label">Make Cover</span>
                      </button>
                    )}
                    <button type="button" className="pg-act" disabled={!selected.size} onClick={() => void removeFromAlbum(albumById, selectedPhotos)} title="Remove from Album">
                      <Icon name="close" size={16} /> <span className="pg-act-label">Remove</span>
                    </button>
                  </>
                )}
                <button type="button" className="pg-act" disabled={!selected.size} onClick={() => download(selectedPhotos)} title="Download">
                  <Icon name="download" size={16} /> <span className="pg-act-label">Download</span>
                </button>
                <button type="button" className="pg-act danger" disabled={!selected.size} onClick={() => void remove(selectedPhotos)} title="Delete">
                  <Icon name="trash" size={16} /> <span className="pg-act-label">Delete</span>
                </button>
              </>
            )}
            <button type="button" className="pg-act icon" aria-label="Cancel selection" title="Cancel (Esc)" onClick={clearSelection}>
              <Icon name="close" size={16} />
            </button>
          </div>
        )}

        {dropping && (
          <div className="pg-drop">
            <span className="pg-drop-icon">
              <Icon name="upload" size={30} />
            </span>
            <strong>Drop to add to Photos</strong>
            <span>{uploadTo ? `Saved to ${rootName(uploadTo.root)} › Photos` : ''}</span>
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
  );
}

function AlbumTitle({ album, editing, onEdit, onDone }: { album: Album; editing: boolean; onEdit: () => void; onDone: (name: string) => void }) {
  const [name, setName] = useState(album.name);
  if (!editing) {
    return (
      <h2 className="app-title pg-editable" title="Click to rename" onClick={onEdit}>
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
      aria-label="Album name"
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

function AlbumsPage({
  albums,
  coverOf,
  tickets,
  onOpen,
  onNew,
}: {
  albums: Album[];
  coverOf: (a: Album) => Photo | undefined;
  tickets: Tickets;
  onOpen: (a: Album) => void;
  onNew: () => void;
}) {
  return (
    <div className="pg-scroll">
      <div className="pg-albums">
        {albums.map((a) => {
          const c = coverOf(a);
          const t = c && ticketFor(tickets, c);
          return (
            <button key={a.id} type="button" className="pg-album" onClick={() => onOpen(a)}>
              <span className="pg-album-cover">
                {c && t ? <img src={thumbUrl(c, t)} alt="" loading="lazy" decoding="async" draggable={false} /> : <Icon name="albums" size={34} />}
              </span>
              <strong>{a.name}</strong>
              <span>{plural(a.items.length, 'item')}</span>
            </button>
          );
        })}
        <button type="button" className="pg-album new" onClick={onNew}>
          <span className="pg-album-cover">
            <Icon name="plus" size={30} />
          </span>
          <strong>New Album</strong>
          <span>Group photos you love</span>
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
    <div className="pg-picker" role="dialog" aria-label={`Add to ${album.name}`} onKeyDown={(e) => e.key === 'Escape' && onClose()}>
      <div className="pg-picker-sheet">
        <header className="pg-picker-head">
          <div>
            <h2>Add to “{album.name}”</h2>
            <p className="app-subtitle">{sel.size ? `${sel.size} selected` : 'Click photos to choose them; Shift-click picks a range.'}</p>
          </div>
          <button type="button" className="pill pg-soft" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="primary pill" disabled={!sel.size} onClick={() => onAdd(list.filter((p) => sel.has(photoKey(p))))}>
            Add {sel.size || ''}
          </button>
        </header>
        <PhotoGrid
          items={list}
          grouping="month"
          tileMin={104}
          tickets={tickets}
          selected={sel}
          selecting
          onToggle={toggle}
          onToggleMany={many}
          onOpen={(i) => toggle(i, false)}
          empty={<EmptyNote icon="image" title="Nothing more to add" text="Everything in your library is already in this album." />}
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
