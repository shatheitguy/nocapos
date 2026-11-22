import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as RMouseEvent } from 'react';
import { fileApi } from '../api/files';
import { fromTrash, photoKey, photosApi, type Album, type Photo } from '../api/photos';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { tr, useT } from '../i18n';
import { pickSet, type PhotoSet } from '../lib/photoSet';
import { linkPhones, sendFilesToPhone } from '../shell/linkActions';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { useWM, type WinState } from '../state/windows';
import type { Tickets } from './PhotosCommon';
import { onPhotosChanged, photosChanged, photoTrashWithUndo, pictureMenu, saveEdited, saveTurns } from './photoActions';
import { PhotoViewer, ticketLinks } from './PhotosViewer';

/**
 * A picture from Photos in a window of its own: the same viewer, stepping
 * through the view or memory it was opened from (an album, favorites, a
 * search...). It reads the library itself, and tells Photos when it changes
 * something (a favorite, a delete, a turn).
 */
export function PhotoSetWindow({ win, set }: { win: WinState; set: PhotoSet }) {
  const t = useT();
  const [items, setItems] = useState<Photo[] | null>(null);
  const [trash, setTrash] = useState<Photo[]>([]);
  const [albums, setAlbums] = useState<Album[]>([]);
  const [tickets, setTickets] = useState<Tickets>({ photos: {}, trash: {} });
  const [roots, setRoots] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [index, setIndex] = useState(0);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  // The picture on show, kept across reloads (a delete moves on to the next one).
  const want = useRef(photoKey({ root: win.props?.root ?? '', path: win.props?.path ?? '' }));
  const isTrash = 'view' in set && set.view === 'trash';

  const load = useCallback(async () => {
    const [l, a, b] = await Promise.all([photosApi.list(), photosApi.albums(), isTrash ? photosApi.trash() : Promise.resolve(null)]);
    if (!l.ok) {
      setError(l.error ?? tr('photos.loadFail'));
      setItems((v) => v ?? []);
      return;
    }
    setError('');
    setItems(l.data.items);
    if (a.ok) setAlbums(a.data.albums);
    if (b?.ok) setTrash(b.data.items.map(fromTrash));
  }, [isTrash]);

  useEffect(() => {
    void load();
    void fileApi.roots().then((r) => r.ok && setRoots(Object.fromEntries(r.data.map((x) => [x.id, x.name]))));
    return onPhotosChanged(() => void load());
  }, [load]);

  // Links for the pictures; renewed well before they expire.
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

  const list = useMemo(() => (items ? pickSet(set, items, albums, trash) : []), [items, albums, trash, set]);

  // Land on the wanted picture whenever the list changes.
  useEffect(() => {
    if (!list.length) return;
    const at = list.findIndex((p) => photoKey(p) === want.current);
    setIndex((i) => (at >= 0 ? at : Math.max(0, Math.min(i, list.length - 1))));
  }, [list]);

  const links = useMemo(() => ticketLinks(tickets), [tickets]);
  const current = list[Math.min(index, list.length - 1)];

  // The window is named after the picture on show.
  useEffect(() => {
    if (!current || win.title === current.name) return;
    useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === win.id ? { ...w, title: current.name } : w)) }));
  }, [current, win.id, win.title]);

  const close = () => useWM.getState().close(win.id);
  const refreshSoon = () => {
    photosChanged();
    window.setTimeout(() => photosChanged(), 2000);
  };
  // After a picture leaves the list: show the next one (or the one before), or close at the end.
  const moveOff = (p: Photo) => {
    const i = list.findIndex((x) => photoKey(x) === photoKey(p));
    const next = list[i + 1] ?? list[i - 1];
    if (next) want.current = photoKey(next);
    else close();
  };

  const menuAt = (e: RMouseEvent, entries: MenuItem[]) => {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const height = entries.length * 36 + 14;
    setMenu({ x: r.left, y: r.bottom > window.innerHeight * 0.6 ? Math.max(8, r.top - 8 - height) : r.bottom + 6, items: entries });
  };

  const addToAlbum = async (a: Album | null, p: Photo) => {
    const r = a ? await photosApi.albumItems(a.id, [p], true) : await photosApi.createAlbum(tr('photos.newAlbumName'), [p]);
    if (!r.ok) return toast('error', a ? tr('photos.toast.addFail') : tr('photos.toast.albumFail'), r.error);
    if (a) toast('success', tr('photos.toast.added', { album: a.name }), p.name);
    photosChanged();
  };

  if (error && !items?.length) return <div className="media-empty"><p>{error}</p></div>;
  if (!items) return <div className="media-empty"><span className="spinner" /></div>;
  if (!list.length || !current) return <div className="media-empty"><p>{t('photos.empty.search')}</p></div>;

  return (
    <div className="pg-app media-image">
      <PhotoViewer
        photos={list}
        index={Math.min(index, list.length - 1)}
        links={links}
        rootName={(id) => roots[id] ?? id}
        onIndex={(i) => {
          setIndex(i);
          if (list[i]) want.current = photoKey(list[i]);
        }}
        onClose={close}
        windowed
        onFavorite={
          isTrash
            ? undefined
            : (p) =>
                void (async () => {
                  const on = !p.favorite;
                  setItems((v) => v?.map((x) => (photoKey(x) === photoKey(p) ? { ...x, favorite: on } : x)) ?? v);
                  const r = await photosApi.favorite([p], on);
                  if (!r.ok) toast('error', tr('photos.toast.favFail'), r.error);
                  photosChanged();
                })()
        }
        onAlbum={
          isTrash
            ? undefined
            : (e, p) =>
                menuAt(e, [
                  ...albums.map((a): MenuItem => ({ label: a.name, icon: 'albums', onClick: () => void addToAlbum(a, p) })),
                  { label: tr('photos.newAlbumDots'), icon: 'plus', divider: albums.length > 0, onClick: () => void addToAlbum(null, p) },
                ])
        }
        onShare={
          isTrash
            ? undefined
            : (e, p) => {
                const phones = linkPhones();
                const entries: MenuItem[] = phones.map((ph) => ({
                  label: phones.length > 1 ? tr('link.sendToNamed', { device: ph.name }) : tr('link.sendToPhone'),
                  icon: 'phone',
                  onClick: () => void sendFilesToPhone(ph, p.root, [p.path]),
                }));
                const href = links.download(p);
                if (href) {
                  entries.push({
                    label: tr('photos.download'),
                    icon: 'download',
                    divider: entries.length > 0,
                    onClick: () => {
                      const a = document.createElement('a');
                      a.href = href;
                      a.download = p.name;
                      document.body.appendChild(a);
                      a.click();
                      a.remove();
                    },
                  });
                }
                menuAt(e, entries);
              }
        }
        onDelete={(p) =>
          void (async () => {
            if (!p.trash) {
              await photoTrashWithUndo(p, (back) => {
                if (back) want.current = photoKey(p);
                else moveOff(p);
                void load();
              });
              return;
            }
            const ok = await confirmDialog({
              title: tr('photos.confirm.purgeOne', { name: p.name }),
              message: tr('photos.confirm.purgeBody'),
              confirmLabel: tr('photos.deleteForever'),
              danger: true,
            });
            if (!ok) return;
            const r = await photosApi.purge([p.trash.id]);
            if (!r.ok) return toast('error', tr('photos.toast.deleteFail'), r.error);
            moveOff(p);
            photosChanged();
          })()
        }
        onRestore={
          isTrash
            ? (p) =>
                void (async () => {
                  if (!p.trash) return;
                  const r = await photosApi.restore([p.trash.id]);
                  if (!r.ok) return toast('error', tr('photos.toast.restoreFail'), r.error);
                  toast('success', tr('photos.toast.restoredOne', { name: p.name }), tr('photos.toast.restoredBody'));
                  moveOff(p);
                  photosChanged();
                })()
            : undefined
        }
        onRotate={
          isTrash
            ? undefined
            : async (p, turns) => {
                const ok = await saveTurns(p, links.full(p), turns);
                if (ok) refreshSoon();
                return ok;
              }
        }
        onSaveEdit={
          isTrash
            ? undefined
            : async (p, file, replace) => {
                const saved = await saveEdited(p, file, replace);
                if (saved) refreshSoon();
                return !!saved;
              }
        }
        moreItems={(p) => (p.trash ? [] : pictureMenu(p, links.full(p), { video: p.video }))}
      />
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}
