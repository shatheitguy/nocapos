import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { baseName, downloadFile, fileApi, fileKind, fileTicket, joinPath, parentPath, rawUrl } from '../api/files';
import { browserCanShow, thumbUrl, type Photo } from '../api/photos';
import { useT } from '../i18n';
import { toast } from '../state/toasts';
import { useWM, type WinState } from '../state/windows';
import { pictureMenu, saveEdited, saveTurns, trashWithUndo } from './photoActions';
import { PhotoViewer, type ViewerLinks } from './PhotosViewer';

/**
 * The photo viewer for any picture in Files, in a window of its own: the
 * same viewer as Photos (zoom, pan, rotate, slideshow, info, edit), with a
 * filmstrip of the pictures in the file's folder.
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
  // Bumped to read the folder again (after a save, a turn, a delete or its undo).
  const [rev, setRev] = useState(0);
  // The picture to land on when the folder is read again.
  const want = useRef(path);

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
      setIndex((i) => {
        const at = pics.findIndex((p) => p.path === want.current);
        return at >= 0 ? at : Math.max(0, Math.min(i, pics.length - 1));
      });
    });
    return () => {
      live = false;
    };
  }, [root, dir, rev, t]);
  const reload = useCallback((show?: string) => {
    if (show) want.current = show;
    setRev((n) => n + 1);
  }, []);

  const links = useMemo<ViewerLinks>(
    () => ({
      // The version in the link, so a saved turn or edit shows at once.
      full: (p) => (ticket ? (browserCanShow(p) ? `${rawUrl(p.root, p.path, ticket)}&v=${p.size}-${encodeURIComponent(p.mod_time)}` : thumbUrl(p, ticket, 'l')) : null),
      thumb: (p, size = 's') => (ticket ? thumbUrl(p, ticket, size) : null),
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
        onIndex={(i) => {
          setIndex(i);
          if (list[i]) want.current = list[i].path;
        }}
        onClose={() => useWM.getState().close(win.id)}
        windowed
        onRotate={async (p, turns) => {
          const ok = await saveTurns(p, links.full(p), turns);
          if (ok) reload();
          return ok;
        }}
        onSaveEdit={async (p, file, replace) => {
          const saved = await saveEdited(p, file, replace);
          if (saved) reload(saved);
          return !!saved;
        }}
        onDelete={(p) => {
          const next = list[index + 1] ?? list[index - 1];
          void trashWithUndo(p, (back) => {
            if (back) reload(p.path);
            else if (next) reload(next.path);
            else useWM.getState().close(win.id);
          });
        }}
        moreItems={(p) => pictureMenu(p, links.full(p), { inFiles: false })}
      />
    </div>
  );
}
