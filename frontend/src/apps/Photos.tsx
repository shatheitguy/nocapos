import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent as RMouseEvent } from 'react';
import { browserCanShow, originalUrl, photoKey, photosApi, thumbUrl, type Album, type Photo, type PhotoKey } from '../api/photos';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon, type IconName } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { startUpload, useTransfers } from '../state/transfers';
import type { WinState } from '../state/windows';
import { openApp } from './meta';

type View = { kind: 'library' } | { kind: 'favorites' } | { kind: 'videos' } | { kind: 'album'; id: number };

const PAGE = 240;
const MEDIA = /\.(jpe?g|png|gif|webp|bmp|heic|heif|mp4|mov|m4v|webm)$/i;
const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

function fmtBytes(n: number) {
  if (n < 1024) return `${n} B`;
  const u = ['KB', 'MB', 'GB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
}

/** Photos: the pictures and videos in the Photos folder of each drive, newest first. */
export function Photos(_: { win: WinState }) {
  const [items, setItems] = useState<Photo[] | null>(null);
  const [albums, setAlbums] = useState<Album[]>([]);
  const [scanning, setScanning] = useState(false);
  const [uploadTo, setUploadTo] = useState<PhotoKey | null>(null);
  const [tickets, setTickets] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [view, setView] = useState<View>({ kind: 'library' });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<number | null>(null);
  const [limit, setLimit] = useState(PAGE);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const [renaming, setRenaming] = useState<number | null>(null);
  const [dropping, setDropping] = useState(false);
  const lastClicked = useRef<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const sentinel = useRef<HTMLDivElement>(null);

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
  }, [load]);

  // Links for thumbnails and originals; renewed well before they expire.
  useEffect(() => {
    let timer = 0;
    const get = async () => {
      const r = await photosApi.tickets();
      if (r.ok) setTickets(r.data.tickets);
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

  const albumById = view.kind === 'album' ? albums.find((a) => a.id === view.id) : undefined;
  const shown = useMemo(() => {
    const all = items ?? [];
    switch (view.kind) {
      case 'favorites':
        return all.filter((p) => p.favorite);
      case 'videos':
        return all.filter((p) => p.video);
      case 'album': {
        const keys = new Set((albumById?.items ?? []).map(photoKey));
        return all.filter((p) => keys.has(photoKey(p)));
      }
      default:
        return all;
    }
  }, [items, view, albumById]);

  // A new view starts at the top with nothing selected.
  useEffect(() => {
    setSelected(new Set());
    setLimit(PAGE);
    lastClicked.current = null;
  }, [view.kind, view.kind === 'album' ? view.id : 0]);

  // Render more as the end of the grid scrolls into view.
  useEffect(() => {
    const el = sentinel.current;
    if (!el) return;
    const io = new IntersectionObserver((e) => e[0]?.isIntersecting && setLimit((l) => l + PAGE), { rootMargin: '600px' });
    io.observe(el);
    return () => io.disconnect();
  }, [shown.length, limit]);

  const groups = useMemo(() => {
    const out: { label: string; start: number; photos: Photo[] }[] = [];
    shown.slice(0, limit).forEach((p, i) => {
      const label = monthFmt.format(new Date(p.taken));
      const last = out[out.length - 1];
      if (last?.label === label) last.photos.push(p);
      else out.push({ label, start: i, photos: [p] });
    });
    return out;
  }, [shown, limit]);

  const selectedPhotos = shown.filter((p) => selected.has(photoKey(p)));
  const selecting = selected.size > 0;

  const toggle = (i: number, range: boolean) => {
    const next = new Set(selected);
    if (range && lastClicked.current !== null) {
      const [a, b] = [Math.min(lastClicked.current, i), Math.max(lastClicked.current, i)];
      for (let j = a; j <= b; j++) next.add(photoKey(shown[j]));
    } else {
      const k = photoKey(shown[i]);
      if (next.has(k)) next.delete(k);
      else next.add(k);
    }
    lastClicked.current = i;
    setSelected(next);
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
      message: 'They move to the Recycle Bin of their drive, where you can restore them from Files.',
      confirmLabel: 'Move to Recycle Bin',
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
    setSelected(new Set());
    void load();
    return true;
  };

  const newAlbum = async (list: Photo[] = []) => {
    const r = await photosApi.createAlbum('New Album', list);
    if (!r.ok) return toast('error', 'Could not create the album', r.error);
    await load();
    setView({ kind: 'album', id: r.data.id });
    setRenaming(r.data.id);
  };

  const addToAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, true);
    if (!r.ok) return toast('error', 'Could not add to the album', r.error);
    toast('success', `Added to “${a.name}”`, list.length === 1 ? list[0].name : `${list.length} items`);
    setSelected(new Set());
    void load();
  };

  const removeFromAlbum = async (a: Album, list: Photo[]) => {
    const r = await photosApi.albumItems(a.id, list, false);
    if (!r.ok) return toast('error', 'Could not remove from the album', r.error);
    setSelected(new Set());
    void load();
  };

  const albumMenu = (e: RMouseEvent, list: Photo[]) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setMenu({
      x: r.left,
      y: r.bottom + 4,
      items: [
        ...albums.map((a): MenuItem => ({ label: a.name, icon: 'albums', onClick: () => void addToAlbum(a, list) })),
        { label: 'New Album', icon: 'plus', onClick: () => void newAlbum(list) },
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

  // ---------- render ----------
  const title =
    view.kind === 'favorites' ? 'Favorites' : view.kind === 'videos' ? 'Videos' : view.kind === 'album' ? (albumById?.name ?? 'Album') : 'Library';

  const nav = (v: View, icon: IconName, label: string, count?: number) => {
    const on = v.kind === view.kind && (v.kind !== 'album' || (view.kind === 'album' && v.id === view.id));
    return (
      <button type="button" className={on ? 'on' : ''} onClick={() => setView(v)}>
        <Icon name={icon} size={16} />
        <span className="ph-nav-label">{label}</span>
        {count !== undefined && <span className="ph-count">{count}</span>}
      </button>
    );
  };

  return (
    <div
      className={`app-split photos ${dropping ? 'dropping' : ''}`}
      onDragOver={(e) => {
        if (!e.dataTransfer.types.includes('Files')) return;
        e.preventDefault();
        setDropping(true);
      }}
      onDragLeave={(e) => e.currentTarget === e.target && setDropping(false)}
      onDrop={onDrop}
    >
      <nav className="sidebar ph-sidebar">
        <div className="sidebar-group">
          {nav({ kind: 'library' }, 'image', 'Library', items?.length)}
          {nav({ kind: 'favorites' }, 'heart', 'Favorites', items?.filter((p) => p.favorite).length)}
          {nav({ kind: 'videos' }, 'fileVideo', 'Videos', items?.filter((p) => p.video).length)}
        </div>
        <div className="sidebar-group">
          <span className="sidebar-heading ph-albums-head">
            Albums
            <button type="button" className="icon-btn ph-add" title="New album" aria-label="New album" onClick={() => void newAlbum()}>
              <Icon name="plus" size={14} />
            </button>
          </span>
          {albums.map((a) => nav({ kind: 'album', id: a.id }, 'albums', a.name, a.items.length))}
          {!albums.length && <p className="ph-hint">Select photos and choose Add to Album, or press +.</p>}
        </div>
      </nav>

      <div className="ph-main">
        <header className="ph-toolbar">
          {view.kind === 'album' && albumById ? (
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
          ) : (
            <h2 className="ph-title">{title}</h2>
          )}
          <span className="ph-sub">
            {items === null ? '' : `${shown.length} ${shown.length === 1 ? 'item' : 'items'}`}
            {scanning && ' · updating…'}
          </span>
          <div className="ph-actions">
            {selecting ? (
              <>
                <span className="ph-selcount">{selected.size} selected</span>
                <button
                  type="button"
                  className="ghost"
                  title="Favorite"
                  onClick={() => void setFavorite(selectedPhotos, !selectedPhotos.every((p) => p.favorite))}
                >
                  <Icon name="heart" size={16} /> {selectedPhotos.every((p) => p.favorite) ? 'Unfavorite' : 'Favorite'}
                </button>
                <button type="button" className="ghost" onClick={(e) => albumMenu(e, selectedPhotos)}>
                  <Icon name="albums" size={16} /> Add to Album
                </button>
                {albumById && (
                  <button type="button" className="ghost" onClick={() => void removeFromAlbum(albumById, selectedPhotos)}>
                    Remove from Album
                  </button>
                )}
                <button type="button" className="ghost danger" title="Delete" aria-label="Delete" onClick={() => void remove(selectedPhotos)}>
                  <Icon name="trash" size={16} />
                </button>
                <button type="button" className="ghost" title="Clear selection" aria-label="Clear selection" onClick={() => setSelected(new Set())}>
                  <Icon name="close" size={16} />
                </button>
              </>
            ) : (
              <>
                {view.kind === 'album' && albumById && (
                  <button
                    type="button"
                    className="ghost"
                    title="Delete album"
                    aria-label="Delete album"
                    onClick={async () => {
                      const ok = await confirmDialog({
                        title: `Delete the album “${albumById.name}”?`,
                        message: 'Only the album goes; the photos stay in your library.',
                        confirmLabel: 'Delete Album',
                        danger: true,
                      });
                      if (!ok) return;
                      const r = await photosApi.deleteAlbum(albumById.id);
                      if (!r.ok) return toast('error', 'Could not delete the album', r.error);
                      setView({ kind: 'library' });
                      void load();
                    }}
                  >
                    <Icon name="trash" size={16} />
                  </button>
                )}
                <button type="button" className="primary" disabled={!uploadTo} onClick={() => fileInput.current?.click()}>
                  <Icon name="upload" size={16} /> Upload
                </button>
              </>
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

        <div className="ph-scroll">
          {error && <p className="error">{error}</p>}
          {items === null ? (
            <div className="ph-empty">
              <span className="spinner" />
            </div>
          ) : !shown.length ? (
            <Empty
              view={view}
              uploadTo={uploadTo}
              onUpload={() => fileInput.current?.click()}
              onBrowse={() => uploadTo && openApp('files', { props: { root: uploadTo.root, path: uploadTo.path } })}
            />
          ) : (
            <>
              {groups.map((g) => (
                <section key={g.label + g.start} className="ph-group">
                  <h3>{g.label}</h3>
                  <div className="ph-grid">
                    {g.photos.map((p, j) => {
                      const i = g.start + j;
                      return (
                        <Tile
                          key={photoKey(p)}
                          photo={p}
                          ticket={tickets[p.root]}
                          selected={selected.has(photoKey(p))}
                          selecting={selecting}
                          onOpen={() => setOpen(i)}
                          onToggle={(range) => toggle(i, range)}
                        />
                      );
                    })}
                  </div>
                </section>
              ))}
              {limit < shown.length && <div ref={sentinel} className="ph-more" />}
            </>
          )}
        </div>
        {dropping && (
          <div className="ph-drop">
            <Icon name="upload" size={28} />
            Drop to add to Photos
          </div>
        )}
      </div>

      {open !== null && shown[open] && (
        <PhotoViewer
          photos={shown}
          index={open}
          tickets={tickets}
          onIndex={setOpen}
          onClose={() => setOpen(null)}
          onFavorite={(p) => void setFavorite([p], !p.favorite)}
          onAlbum={(e, p) => albumMenu(e, [p])}
          onDelete={async (p) => {
            const at = open;
            if (await remove([p])) setOpen(shown.length > 1 ? Math.min(at, shown.length - 2) : null);
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
      <h2 className="ph-title editable" title="Click to rename" onClick={onEdit}>
        {album.name}
      </h2>
    );
  }
  return (
    <input
      className="ph-title-input"
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

function Empty({ view, uploadTo, onUpload, onBrowse }: { view: View; uploadTo: PhotoKey | null; onUpload: () => void; onBrowse: () => void }) {
  if (view.kind === 'favorites') return <EmptyNote icon="heart" title="No favorites yet" text="Tap the heart on a photo to keep it here." />;
  if (view.kind === 'videos') return <EmptyNote icon="fileVideo" title="No videos yet" text="Videos you add to Photos show up here." />;
  if (view.kind === 'album') return <EmptyNote icon="albums" title="This album is empty" text="Select photos in your library and choose Add to Album." />;
  return (
    <div className="ph-empty">
      <span className="ph-empty-icon">
        <Icon name="image" size={34} />
      </span>
      <h3>No photos yet</h3>
      <p>Upload from this device or drop pictures here. Anything you copy into the Photos folder of a drive shows up too.</p>
      <div className="ph-empty-actions">
        <button type="button" className="primary" disabled={!uploadTo} onClick={onUpload}>
          <Icon name="upload" size={16} /> Upload Photos
        </button>
        <button type="button" disabled={!uploadTo} onClick={onBrowse}>
          <Icon name="folder" size={16} /> Open Photos Folder
        </button>
      </div>
    </div>
  );
}

function EmptyNote({ icon, title, text }: { icon: IconName; title: string; text: string }) {
  return (
    <div className="ph-empty">
      <span className="ph-empty-icon">
        <Icon name={icon} size={30} />
      </span>
      <h3>{title}</h3>
      <p>{text}</p>
    </div>
  );
}

function Tile({
  photo,
  ticket,
  selected,
  selecting,
  onOpen,
  onToggle,
}: {
  photo: Photo;
  ticket?: string;
  selected: boolean;
  selecting: boolean;
  onOpen: () => void;
  onToggle: (range: boolean) => void;
}) {
  const [failed, setFailed] = useState(false);
  return (
    <div
      role="button"
      tabIndex={0}
      className={`ph-tile ${selected ? 'sel' : ''} ${selecting ? 'selecting' : ''}`}
      title={photo.name}
      onClick={(e) => (selecting || e.shiftKey || e.ctrlKey || e.metaKey ? onToggle(e.shiftKey) : onOpen())}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onOpen();
        if (e.key === ' ') {
          e.preventDefault();
          onToggle(false);
        }
      }}
    >
      {ticket && !failed ? (
        <img src={thumbUrl(photo, ticket)} alt="" loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} />
      ) : (
        <span className="ph-ph">
          <Icon name={photo.video ? 'fileVideo' : 'image'} size={26} />
        </span>
      )}
      {photo.video && (
        <span className="ph-badge">
          <Icon name="play" size={11} />
        </span>
      )}
      {photo.favorite && (
        <span className="ph-fav">
          <Icon name="heart" size={14} />
        </span>
      )}
      <button
        type="button"
        className="ph-check"
        aria-label={selected ? 'Deselect' : 'Select'}
        onClick={(e) => {
          e.stopPropagation();
          onToggle(e.shiftKey);
        }}
      >
        {selected && <Icon name="check" size={12} />}
      </button>
    </div>
  );
}

function PhotoViewer({
  photos,
  index,
  tickets,
  onIndex,
  onClose,
  onFavorite,
  onAlbum,
  onDelete,
}: {
  photos: Photo[];
  index: number;
  tickets: Record<string, string>;
  onIndex: (i: number) => void;
  onClose: () => void;
  onFavorite: (p: Photo) => void;
  onAlbum: (e: RMouseEvent, p: Photo) => void;
  onDelete: (p: Photo) => void;
}) {
  const p = photos[index];
  const ticket = tickets[p.root];
  const [loaded, setLoaded] = useState(false);
  const [info, setInfo] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const go = useCallback((d: number) => onIndex(Math.max(0, Math.min(photos.length - 1, index + d))), [index, photos.length, onIndex]);

  useEffect(() => setLoaded(false), [p]);
  useEffect(() => box.current?.focus(), []);

  return (
    <div
      ref={box}
      className="ph-viewer"
      tabIndex={-1}
      role="dialog"
      aria-label={p.name}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose();
        else if (e.key === 'ArrowLeft') go(-1);
        else if (e.key === 'ArrowRight') go(1);
        else if (e.key === 'Delete') onDelete(p);
        else return;
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      <header className="ph-vbar">
        <button type="button" className="ghost" aria-label="Back" title="Back (Esc)" onClick={onClose}>
          <Icon name="chevronLeft" size={18} />
        </button>
        <div className="ph-vtitle">
          <strong>{p.name}</strong>
          <span>{dateFmt.format(new Date(p.taken))}</span>
        </div>
        <button type="button" className={`ghost ${p.favorite ? 'faved' : ''}`} aria-label="Favorite" title="Favorite" onClick={() => onFavorite(p)}>
          <Icon name="heart" size={18} />
        </button>
        <button type="button" className="ghost" aria-label="Add to album" title="Add to Album" onClick={(e) => onAlbum(e, p)}>
          <Icon name="albums" size={18} />
        </button>
        <button type="button" className={`ghost ${info ? 'on' : ''}`} aria-label="Info" title="Info" onClick={() => setInfo((v) => !v)}>
          <Icon name="info" size={18} />
        </button>
        {ticket && (
          <a className="ghost btn-like" href={originalUrl(p, ticket, true)} aria-label="Download" title="Download">
            <Icon name="download" size={18} />
          </a>
        )}
        <button type="button" className="ghost" aria-label="Delete" title="Delete" onClick={() => onDelete(p)}>
          <Icon name="trash" size={18} />
        </button>
      </header>

      <div className="ph-stage" onClick={(e) => e.target === e.currentTarget && onClose()}>
        {!ticket ? (
          <span className="spinner" />
        ) : p.video ? (
          <video key={photoKey(p)} src={originalUrl(p, ticket)} controls autoPlay playsInline />
        ) : (
          <>
            {!loaded && <img className="ph-preview" src={thumbUrl(p, ticket)} alt="" draggable={false} />}
            <img
              key={photoKey(p)}
              className={`ph-full ${loaded ? 'ready' : ''}`}
              src={browserCanShow(p) ? originalUrl(p, ticket) : thumbUrl(p, ticket, 'l')}
              alt={p.name}
              draggable={false}
              onLoad={() => setLoaded(true)}
            />
          </>
        )}
        {index > 0 && (
          <button type="button" className="ph-nav prev" aria-label="Previous" onClick={() => go(-1)}>
            <Icon name="chevronLeft" size={22} />
          </button>
        )}
        {index < photos.length - 1 && (
          <button type="button" className="ph-nav next" aria-label="Next" onClick={() => go(1)}>
            <Icon name="chevronRight" size={22} />
          </button>
        )}
        {info && (
          <dl className="ph-info">
            <dt>Taken</dt>
            <dd>{dateFmt.format(new Date(p.taken))}</dd>
            {p.width ? (
              <>
                <dt>Size</dt>
                <dd>
                  {p.width} × {p.height}
                </dd>
              </>
            ) : null}
            <dt>File</dt>
            <dd>{fmtBytes(p.size)}</dd>
            <dt>Where</dt>
            <dd className="ph-path">{p.path}</dd>
          </dl>
        )}
      </div>
    </div>
  );
}
