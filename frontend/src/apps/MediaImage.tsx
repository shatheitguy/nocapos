import { useEffect, useMemo, useState } from 'react';
import { baseName, downloadFile, fileApi, fileKind, fileTicket, joinPath, parentPath, rawUrl } from '../api/files';
import type { Photo } from '../api/photos';
import { useT } from '../i18n';
import { toast } from '../state/toasts';
import { useWM, type WinState } from '../state/windows';
import { PhotoViewer, type ViewerLinks } from './PhotosViewer';

/**
 * The photo viewer for any picture in Files: the same full-screen viewer as
 * Photos (zoom, pan, rotate, slideshow, info), stepping through the pictures
 * in the file's folder.
 */
export function ImageViewerApp({ win }: { win: WinState }) {
  const t = useT();
  const root = win.props?.root ?? '';
  const path = win.props?.path ?? '';
  const dir = parentPath(path);
  const [list, setList] = useState<Photo[] | null>(null);
  const [index, setIndex] = useState(0);
  const [ticket, setTicket] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [roots, setRoots] = useState<Record<string, string>>({});

  useEffect(() => {
    void fileApi.roots().then((r) => r.ok && setRoots(Object.fromEntries(r.data.map((x) => [x.id, x.name]))));
  }, []);

  useEffect(() => {
    let live = true;
    void Promise.all([fileApi.list(root, dir), fileTicket(root, dir)]).then(([r, tk]) => {
      if (!live) return;
      setTicket(tk);
      if (!r.ok || !tk) {
        setError(r.error ?? t('media.openFail'));
        return;
      }
      const pics = r.data.entries
        .filter((e) => !e.dir && fileKind(e.name) === 'image')
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' }))
        .map((e): Photo => ({ root, path: joinPath(dir, e.name), name: e.name, size: e.size, mod_time: e.mod_time, taken: e.mod_time }));
      setList(pics);
      setIndex(Math.max(0, pics.findIndex((p) => p.path === path)));
    });
    return () => {
      live = false;
    };
  }, [root, dir, path, t]);

  const links = useMemo<ViewerLinks>(
    () => ({
      full: (p) => (ticket ? rawUrl(p.root, p.path, ticket) : null),
      thumb: (p) => (ticket ? rawUrl(p.root, p.path, ticket) : null),
      download: (p) => (ticket ? rawUrl(p.root, p.path, ticket, true) : null),
    }),
    [ticket],
  );

  // The window is named after the picture on show.
  const current = list?.[index];
  useEffect(() => {
    if (!current || win.title === current.name) return;
    useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === win.id ? { ...w, title: current.name } : w)) }));
  }, [current, win.id, win.title]);

  if (error) {
    return (
      <div className="media-empty">
        <p>{error}</p>
        <button type="button" onClick={() => void downloadFile(root, path).then((ok) => !ok && toast('error', t('media.openFail')))}>
          {t('photos.download')}
        </button>
      </div>
    );
  }
  if (!list) return <div className="media-empty"><span className="spinner" /></div>;
  if (!list.length) return <div className="media-empty"><p>{t('media.noPictures', { name: baseName(path) })}</p></div>;

  return (
    <div className="pg-app media-image">
      <PhotoViewer
        photos={list}
        index={Math.min(index, list.length - 1)}
        links={links}
        rootName={(id) => roots[id] ?? id}
        onIndex={setIndex}
        onClose={() => useWM.getState().close(win.id)}
        strip={false}
      />
    </div>
  );
}
