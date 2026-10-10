import { useCallback, useEffect, useRef, useState, type MouseEvent as RMouseEvent } from 'react';
import { browserCanShow, originalUrl, photoKey, photosApi, thumbUrl, type Camera, type Photo } from '../api/photos';
import { Icon } from '../components/Icon';
import { dateTimeFmt, fmtBytes, fmtDuration, longDateFmt, ticketFor, timeFmt, type Tickets } from './PhotosCommon';

const cameraCache = new Map<string, Camera | null>();

function exposureText(c: Camera) {
  const parts: string[] = [];
  if (c.f_number) parts.push(`ƒ/${+c.f_number.toFixed(1)}`);
  if (c.exposure) parts.push(c.exposure >= 1 ? `${+c.exposure.toFixed(1)} s` : `1/${Math.round(1 / c.exposure)} s`);
  if (c.iso) parts.push(`ISO ${c.iso}`);
  if (c.focal) parts.push(`${+c.focal.toFixed(1)} mm`);
  return parts.join('  ·  ');
}

/** The full-window viewer: one photo or video at a time over a blurred copy of itself. */
export function PhotoViewer({
  photos,
  index,
  tickets,
  rootName,
  onIndex,
  onClose,
  onFavorite,
  onAlbum,
  onDelete,
  onRestore,
}: {
  photos: Photo[];
  index: number;
  tickets: Tickets;
  rootName: (id: string) => string;
  onIndex: (i: number) => void;
  onClose: () => void;
  onFavorite: (p: Photo) => void;
  onAlbum: (e: RMouseEvent, p: Photo) => void;
  onDelete: (p: Photo) => void;
  onRestore?: (p: Photo) => void;
}) {
  const p = photos[index];
  const ticket = ticketFor(tickets, p);
  const deleted = !!p.trash;
  const [loaded, setLoaded] = useState(false);
  const [info, setInfo] = useState(() => {
    try {
      return localStorage.getItem('photos.info') === '1';
    } catch {
      return false;
    }
  });
  const [camera, setCamera] = useState<Camera | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const go = useCallback((d: number) => onIndex(Math.max(0, Math.min(photos.length - 1, index + d))), [index, photos.length, onIndex]);

  useEffect(() => setLoaded(false), [p]);
  useEffect(() => box.current?.focus(), []);
  useEffect(() => {
    try {
      localStorage.setItem('photos.info', info ? '1' : '0');
    } catch {
      /* not saved */
    }
  }, [info]);

  // Camera details are read when the info panel is open.
  useEffect(() => {
    if (!info || deleted) return;
    const k = photoKey(p);
    if (cameraCache.has(k)) {
      setCamera(cameraCache.get(k) ?? null);
      return;
    }
    setCamera(null);
    let live = true;
    void photosApi.info(p).then((r) => {
      const c = r.ok ? (r.data.camera ?? null) : null;
      cameraCache.set(k, c);
      if (live) setCamera(c);
    });
    return () => {
      live = false;
    };
  }, [info, p, deleted]);

  const taken = new Date(p.taken);
  const where = deleted ? p.trash!.orig_path : p.path;
  const mp = p.width && p.height ? (p.width * p.height) / 1e6 : 0;

  return (
    <div
      ref={box}
      className="pg-viewer"
      tabIndex={-1}
      role="dialog"
      aria-label={p.name}
      onKeyDown={(e) => {
        if (e.key === 'Escape') onClose();
        else if (e.key === 'ArrowLeft') go(-1);
        else if (e.key === 'ArrowRight') go(1);
        else if (e.key === 'Delete') onDelete(p);
        else if (e.key === 'i' && !e.ctrlKey && !e.metaKey) setInfo((v) => !v);
        else if (e.key === 'f' && !deleted && !e.ctrlKey && !e.metaKey) onFavorite(p);
        else return;
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {ticket && <div className="pg-vback" style={{ backgroundImage: `url("${thumbUrl(p, ticket)}")` }} />}
      <header className="pg-vbar">
        <button type="button" className="pg-vbtn" aria-label="Back" title="Back (Esc)" onClick={onClose}>
          <Icon name="chevronLeft" size={20} />
        </button>
        <div className="pg-vtitle">
          <strong>{longDateFmt.format(taken)}</strong>
          <span>
            {timeFmt.format(taken)} · {p.name}
            {photos.length > 1 && ` · ${index + 1} of ${photos.length}`}
          </span>
        </div>
        {deleted ? (
          onRestore && (
            <button type="button" className="pg-vbtn text" onClick={() => onRestore(p)}>
              <Icon name="rewind" size={16} /> Restore
            </button>
          )
        ) : (
          <>
            <button
              type="button"
              className={`pg-vbtn${p.favorite ? ' faved' : ''}`}
              aria-label={p.favorite ? 'Unfavorite' : 'Favorite'}
              title={p.favorite ? 'Unfavorite (F)' : 'Favorite (F)'}
              onClick={() => onFavorite(p)}
            >
              <Icon name="heart" size={18} />
            </button>
            <button type="button" className="pg-vbtn" aria-label="Add to album" title="Add to Album" onClick={(e) => onAlbum(e, p)}>
              <Icon name="albums" size={18} />
            </button>
          </>
        )}
        {ticket && (
          <a className="pg-vbtn" href={originalUrl(p, ticket, true)} aria-label="Download" title="Download">
            <Icon name="download" size={18} />
          </a>
        )}
        <button type="button" className={`pg-vbtn${info ? ' on' : ''}`} aria-label="Info" title="Info (I)" onClick={() => setInfo((v) => !v)}>
          <Icon name="info" size={18} />
        </button>
        <button type="button" className="pg-vbtn" aria-label={deleted ? 'Delete forever' : 'Delete'} title={deleted ? 'Delete forever' : 'Delete'} onClick={() => onDelete(p)}>
          <Icon name="trash" size={18} />
        </button>
      </header>

      <div className="pg-vbody">
        <div className="pg-stage" onClick={(e) => e.target === e.currentTarget && onClose()}>
          {!ticket ? (
            <span className="spinner" />
          ) : p.video ? (
            <video key={photoKey(p)} src={originalUrl(p, ticket)} poster={thumbUrl(p, ticket, 'l')} controls autoPlay playsInline />
          ) : (
            <>
              {!loaded && <img className="pg-preview" src={thumbUrl(p, ticket)} alt="" draggable={false} />}
              <img
                key={photoKey(p)}
                className={`pg-full${loaded ? ' ready' : ''}`}
                src={browserCanShow(p) ? originalUrl(p, ticket) : thumbUrl(p, ticket, 'l')}
                alt={p.name}
                draggable={false}
                onLoad={() => setLoaded(true)}
              />
            </>
          )}
          {index > 0 && (
            <button type="button" className="pg-arrow prev" aria-label="Previous" onClick={() => go(-1)}>
              <Icon name="chevronLeft" size={22} />
            </button>
          )}
          {index < photos.length - 1 && (
            <button type="button" className="pg-arrow next" aria-label="Next" onClick={() => go(1)}>
              <Icon name="chevronRight" size={22} />
            </button>
          )}
        </div>

        {info && (
          <aside className="pg-info" aria-label="Info">
            <h3>{p.name}</h3>
            <dl>
              <dt>{p.video ? 'Recorded' : 'Taken'}</dt>
              <dd>{dateTimeFmt.format(taken)}</dd>
              {deleted && (
                <>
                  <dt>Deleted</dt>
                  <dd>{dateTimeFmt.format(new Date(p.trash!.deleted_at))}</dd>
                </>
              )}
              {p.video && p.duration ? (
                <>
                  <dt>Length</dt>
                  <dd>{fmtDuration(p.duration)}</dd>
                </>
              ) : null}
              {p.width ? (
                <>
                  <dt>Dimensions</dt>
                  <dd>
                    {p.width} × {p.height}
                    {mp >= 0.5 && <span className="pg-muted"> · {mp.toFixed(1)} MP</span>}
                  </dd>
                </>
              ) : null}
              <dt>File size</dt>
              <dd>{fmtBytes(p.size)}</dd>
            </dl>
            {camera && (camera.make || camera.model || exposureText(camera)) && (
              <div className="pg-camera">
                <div className="pg-camera-head">
                  <Icon name="image" size={15} />
                  <span>
                    <strong>{[camera.make, camera.model].filter(Boolean).join(' ') || 'Camera'}</strong>
                    {camera.lens && <small>{camera.lens}</small>}
                  </span>
                </div>
                {exposureText(camera) && <div className="pg-exposure">{exposureText(camera)}</div>}
                {camera.focal_35 ? <div className="pg-muted">{camera.focal_35} mm in 35 mm terms</div> : null}
              </div>
            )}
            <dl>
              <dt>{deleted ? 'Was in' : 'Location'}</dt>
              <dd className="pg-path">
                {rootName(p.root)} › {where.replace(/^\//, '').split('/').slice(0, -1).join(' › ') || 'top folder'}
              </dd>
            </dl>
          </aside>
        )}
      </div>
    </div>
  );
}
