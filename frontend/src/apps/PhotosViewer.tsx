import { useCallback, useEffect, useLayoutEffect, useRef, useState, type MouseEvent as RMouseEvent, type PointerEvent as RPointerEvent } from 'react';
import { browserCanShow, originalUrl, photoKey, photosApi, thumbUrl, type Camera, type Photo } from '../api/photos';
import { Icon } from '../components/Icon';
import { WinClose } from '../components/WinClose';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Modal } from '../components/Modal';
import { tr, useT } from '../i18n';
import { keepsType } from '../lib/photoEdit';
import { toast } from '../state/toasts';
import { PhotoEditor } from './PhotoEditor';
import { dateTimeFmt, fmtBytes, fmtDuration, longDateFmt, Thumb, ticketFor, timeFmt, useReducedMotion, type Tickets } from './PhotosCommon';

const cameraCache = new Map<string, Camera | null>();
const MAX_ZOOM = 8;
const SLIDE_STEPS = [3000, 5000, 10000];
const readSlideMs = () => {
  try {
    const v = Number(localStorage.getItem('photos.slideMs'));
    return SLIDE_STEPS.includes(v) ? v : 5000;
  } catch {
    return 5000;
  }
};

function exposureText(c: Camera) {
  const parts: string[] = [];
  if (c.f_number) parts.push(`ƒ/${+c.f_number.toFixed(1)}`);
  if (c.exposure) parts.push(c.exposure >= 1 ? `${+c.exposure.toFixed(1)} s` : `1/${Math.round(1 / c.exposure)} s`);
  if (c.iso) parts.push(`ISO ${c.iso}`);
  if (c.focal) parts.push(`${+c.focal.toFixed(1)} mm`);
  return parts;
}

/** 48.8582° N, 2.2945° E */
export const coordText = (lat: number, lon: number) =>
  `${Math.abs(lat).toFixed(5)}° ${lat >= 0 ? 'N' : 'S'}, ${Math.abs(lon).toFixed(5)}° ${lon >= 0 ? 'E' : 'W'}`;

/** Where the viewer gets each picture (null while a link is still being made). */
export interface ViewerLinks {
  /** The picture or video to show. */
  full: (p: Photo) => string | null;
  /** A small preview ("l": a large one). */
  thumb: (p: Photo, size?: 's' | 'l') => string | null;
  download: (p: Photo) => string | null;
}

/** Links for the Photos library (and its recycle bin), from its tickets. */
export function ticketLinks(tickets: Tickets): ViewerLinks {
  return {
    full: (p) => {
      const tk = ticketFor(tickets, p);
      // The version in the link, so a saved turn or edit shows at once.
      return tk ? (browserCanShow(p) ? `${originalUrl(p, tk)}&v=${p.size}-${encodeURIComponent(p.mod_time)}` : thumbUrl(p, tk, 'l')) : null;
    },
    thumb: (p, size = 's') => {
      const tk = ticketFor(tickets, p);
      return tk ? thumbUrl(p, tk, size) : null;
    },
    download: (p) => {
      const tk = ticketFor(tickets, p);
      return tk ? originalUrl(p, tk, true) : null;
    },
  };
}

interface View {
  s: number;
  x: number;
  y: number;
}
const FIT: View = { s: 1, x: 0, y: 0 };

/**
 * The full-screen viewer: one photo or video at a time over a blurred copy
 * of itself. Swipe or use the arrows to move, pinch, scroll or double-click
 * to zoom, drag to pan; rotate, favorite, slideshow and an info panel with
 * what the camera recorded.
 */
export function PhotoViewer({
  photos,
  index,
  links,
  rootName,
  onIndex,
  onClose,
  onFavorite,
  onAlbum,
  onShare,
  onDelete,
  onRestore,
  strip: showStrip = true,
  windowed = false,
  onRotate,
  onSaveEdit,
  moreItems,
}: {
  photos: Photo[];
  index: number;
  links: ViewerLinks;
  rootName: (id: string) => string;
  onIndex: (i: number) => void;
  onClose: () => void;
  onFavorite?: (p: Photo) => void;
  onAlbum?: (e: RMouseEvent, p: Photo) => void;
  onShare?: (e: RMouseEvent, p: Photo) => void;
  onDelete?: (p: Photo) => void;
  onRestore?: (p: Photo) => void;
  /** The filmstrip of neighbours along the bottom. */
  strip?: boolean;
  /** In a window of its own: the window's title bar closes it (and, full screen, the same close button on the bar). */
  windowed?: boolean;
  /** Save a turn (quarter turns clockwise); resolves true once saved, and the picture reloads upright. */
  onRotate?: (p: Photo, turns: number) => Promise<boolean>;
  /** Save an edit from the editor (a copy, or in place of the original); resolves true once saved. */
  onSaveEdit?: (p: Photo, file: Blob, replace: boolean) => Promise<boolean>;
  /** More actions for the "…" menu (wallpaper, open in Files, send to a phone...). */
  moreItems?: (p: Photo) => MenuItem[];
}) {
  const t = useT();
  const reduced = useReducedMotion();
  const p = photos[index];
  const src = links.full(p);
  const thumb = links.thumb(p);
  const download = links.download(p);
  const deleted = !!p.trash;
  // Keyed by photo, so a picture that loads straight from the cache can't be marked unloaded afterwards.
  const [loadedKey, setLoadedKey] = useState('');
  const [measured, setMeasured] = useState<{ key: string; w: number; h: number } | null>(null);
  const pk = photoKey(p);
  const loaded = loadedKey === pk;
  const natural = p.width && p.height ? { w: p.width, h: p.height } : measured?.key === pk ? measured : null;
  const [info, setInfo] = useState(() => {
    try {
      return localStorage.getItem('photos.info') === '1';
    } catch {
      return false;
    }
  });
  const [camera, setCamera] = useState<Camera | null>(null);
  const [rot, setRot] = useState(0);
  const [view, setView] = useState<View>(FIT);
  const [drag, setDrag] = useState<{ dx: number; dy: number } | null>(null);
  const [gesturing, setGesturing] = useState(false);
  const [slideshow, setSlideshow] = useState(false);
  const [slideMs, setSlideMs] = useState(readSlideMs);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [keysHelp, setKeysHelp] = useState(false);
  const [idle, setIdle] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [full, setFull] = useState(false);
  const [editing, setEditing] = useState(false);
  const [overBar, setOverBar] = useState(false);
  const [stage, setStage] = useState({ w: 0, h: 0 });
  const box = useRef<HTMLDivElement>(null);
  // Turns waiting to be saved, and whether the saved picture still has to load.
  const savedTurns = useRef(0);
  const turnTimer = useRef(0);
  const uprightOnLoad = useRef(false);
  const stageRef = useRef<HTMLDivElement>(null);
  const strip = useRef<HTMLDivElement>(null);

  // Which way the last step went (the next picture slides in from that side).
  const nav = useRef({ idx: index, dir: 0 });
  if (nav.current.idx !== index) nav.current = { idx: index, dir: index > nav.current.idx ? 1 : -1 };

  const go = useCallback(
    (d: number, wrap = false) => {
      let i = index + d;
      if (wrap) i = (i + photos.length) % photos.length;
      onIndex(Math.max(0, Math.min(photos.length - 1, i)));
    },
    [index, photos.length, onIndex],
  );

  // A new photo starts upright, fitted and unzoomed.
  useEffect(() => {
    setRot(0);
    setView(FIT);
    setDrag(null);
    setEditing(false);
    savedTurns.current = 0;
    uprightOnLoad.current = false;
  }, [pk]);
  useEffect(() => box.current?.focus(), []);
  useEffect(() => {
    try {
      localStorage.setItem('photos.info', info ? '1' : '0');
    } catch {
      /* not saved */
    }
  }, [info]);

  // The stage size, for fitting and zoom limits.
  useLayoutEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStage({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setStage({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

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

  // The neighbours load in the background so moving on is instant.
  useEffect(() => {
    for (const q of [photos[index + 1], photos[index - 1], photos[index + 2], photos[index - 2]]) {
      const u = q && !q.video ? links.full(q) : null;
      if (u) {
        const im = new Image();
        im.src = u;
        // Decoded ahead (off the main thread where the browser can), so the next one shows at once.
        void im.decode?.().catch(() => undefined);
      }
    }
  }, [index, photos, links]);

  // Slideshow: pictures move on by themselves; a video plays to its end first.
  useEffect(() => {
    if (!slideshow || p.video || gesturing || view.s > 1) return;
    const timer = window.setTimeout(() => go(1, true), slideMs);
    return () => window.clearTimeout(timer);
  }, [slideshow, p, go, gesturing, view.s, slideMs]);
  const nextSlideMs = () =>
    setSlideMs((ms) => {
      const n = SLIDE_STEPS[(SLIDE_STEPS.indexOf(ms) + 1) % SLIDE_STEPS.length];
      try {
        localStorage.setItem('photos.slideMs', String(n));
      } catch {
        /* not saved */
      }
      return n;
    });

  // Controls fade away while nothing moves (slideshow and full screen).
  const wake = useRef(0);
  const poke = useCallback(() => {
    setIdle(false);
    window.clearTimeout(wake.current);
    wake.current = window.setTimeout(() => setIdle(true), 2600);
  }, []);
  useEffect(() => {
    poke();
    return () => window.clearTimeout(wake.current);
  }, [poke]);

  useEffect(() => {
    const f = () => setFull(document.fullscreenElement === box.current);
    document.addEventListener('fullscreenchange', f);
    return () => document.removeEventListener('fullscreenchange', f);
  }, []);
  const toggleFull = () => {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void box.current?.requestFullscreen?.().catch(() => undefined);
  };

  // Keep the current photo in the middle of the filmstrip.
  useEffect(() => {
    const el = strip.current?.querySelector<HTMLElement>('.cur');
    el?.scrollIntoView({ inline: 'center', block: 'nearest', behavior: reduced ? 'auto' : 'smooth' });
  }, [index, reduced]);

  // ---------- fit, zoom and pan ----------
  const quarter = ((rot % 180) + 180) % 180 === 90;
  const nat = natural ?? { w: 4, h: 3 };
  const boxW = quarter ? stage.h : stage.w;
  const boxH = quarter ? stage.w : stage.h;
  const pad = stage.w < 560 ? 0 : 20;
  const fit = stage.w && stage.h ? Math.max(0.01, Math.min((boxW - pad * 2) / nat.w, (boxH - pad * 2) / nat.h, 3)) : 0;
  // Far enough in for 400 % of the real pixels, however big the picture.
  const maxZoom = fit > 0 ? Math.max(MAX_ZOOM, 4 / fit) : MAX_ZOOM;
  const imgW = nat.w * fit;
  const imgH = nat.h * fit;
  // On screen (after turning), for pan limits.
  const shownW = quarter ? imgH : imgW;
  const shownH = quarter ? imgW : imgH;

  const clampView = useCallback(
    (v: View): View => {
      const s = Math.max(1, Math.min(maxZoom, v.s));
      const mx = Math.max(0, (shownW * s - stage.w) / 2);
      const my = Math.max(0, (shownH * s - stage.h) / 2);
      return { s, x: Math.max(-mx, Math.min(mx, v.x)), y: Math.max(-my, Math.min(my, v.y)) };
    },
    [shownW, shownH, stage.w, stage.h, maxZoom],
  );

  /** Zoom to scale s keeping the stage point (px, py from its centre) still. */
  const zoomAt = useCallback(
    (s: number, px: number, py: number, from: View = view) => {
      const ns = Math.max(1, Math.min(maxZoom, s));
      const k = ns / from.s;
      setView(clampView({ s: ns, x: px - (px - from.x) * k, y: py - (py - from.y) * k }));
    },
    [view, clampView, maxZoom],
  );

  const rel = (clientX: number, clientY: number) => {
    const r = stageRef.current!.getBoundingClientRect();
    return { x: clientX - r.left - r.width / 2, y: clientY - r.top - r.height / 2 };
  };

  // Wheel zoom (needs a non-passive listener to stop the page scrolling).
  useEffect(() => {
    const el = stageRef.current;
    if (!el || p.video) return;
    let stepAt = 0;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      // A sideways swipe on a touchpad steps through the pictures (unless zoomed in).
      if (!e.ctrlKey && Math.abs(e.deltaX) > Math.abs(e.deltaY) * 1.5 && Math.abs(e.deltaX) > 12 && viewRef.current.s <= 1) {
        if (Date.now() - stepAt > 450) {
          stepAt = Date.now();
          goRef.current(e.deltaX > 0 ? 1 : -1);
        }
        return;
      }
      const pt = rel(e.clientX, e.clientY);
      setView((v) => {
        const ns = Math.max(1, Math.min(maxZoom, v.s * Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0018))));
        const k = ns / v.s;
        return clampView({ s: ns, x: pt.x - (pt.x - v.x) * k, y: pt.y - (pt.y - v.y) * k });
      });
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  }, [clampView, p.video, maxZoom]);

  const calmRef = useRef(false);
  const viewRef = useRef(view);
  viewRef.current = view;
  const goRef = useRef(go);
  goRef.current = go;

  // Touch and mouse: swipe between photos, pinch to zoom, drag to pan, double-tap to zoom.
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const gesture = useRef<{
    kind: 'swipe' | 'pan' | 'pinch';
    sx: number;
    sy: number;
    t0: number;
    from: View;
    dist?: number;
    mid?: { x: number; y: number };
  } | null>(null);
  const lastTap = useRef({ at: 0, x: 0, y: 0 });
  const tapTimer = useRef(0);

  const onPointerDown = (e: RPointerEvent) => {
    if (p.video || e.button > 0) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    setGesturing(true);
    const pts = [...pointers.current.values()];
    if (pts.length === 2) {
      const mid = rel((pts[0].x + pts[1].x) / 2, (pts[0].y + pts[1].y) / 2);
      gesture.current = { kind: 'pinch', sx: 0, sy: 0, t0: Date.now(), from: view, dist: Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y), mid };
      setDrag(null);
    } else if (pts.length === 1) {
      gesture.current = { kind: view.s > 1 ? 'pan' : 'swipe', sx: e.clientX, sy: e.clientY, t0: Date.now(), from: view };
    }
  };

  const onPointerMove = (e: RPointerEvent) => {
    poke();
    if (!pointers.current.has(e.pointerId)) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    const g = gesture.current;
    if (!g) return;
    const pts = [...pointers.current.values()];
    if (g.kind === 'pinch' && pts.length >= 2 && g.dist && g.mid) {
      const dist = Math.hypot(pts[0].x - pts[1].x, pts[0].y - pts[1].y);
      const mid = rel((pts[0].x + pts[1].x) / 2, (pts[0].y + pts[1].y) / 2);
      const ns = Math.max(1, Math.min(maxZoom, g.from.s * (dist / g.dist)));
      // The picture point under the starting midpoint follows the fingers.
      const qx = (g.mid.x - g.from.x) / g.from.s;
      const qy = (g.mid.y - g.from.y) / g.from.s;
      setView(clampView({ s: ns, x: mid.x - qx * ns, y: mid.y - qy * ns }));
    } else if (g.kind === 'pan') {
      setView(clampView({ s: g.from.s, x: g.from.x + e.clientX - g.sx, y: g.from.y + e.clientY - g.sy }));
    } else if (g.kind === 'swipe') {
      setDrag({ dx: e.clientX - g.sx, dy: e.clientY - g.sy });
    }
  };

  const onPointerUp = (e: RPointerEvent) => {
    pointers.current.delete(e.pointerId);
    const g = gesture.current;
    if (pointers.current.size > 0) {
      // One finger lifted from a pinch: carry on as a pan.
      const [rest] = [...pointers.current.values()];
      gesture.current = { kind: 'pan', sx: rest.x, sy: rest.y, t0: Date.now(), from: view };
      return;
    }
    gesture.current = null;
    setGesturing(false);
    if (!g) return;
    const dx = e.clientX - g.sx;
    const dy = e.clientY - g.sy;
    const moved = Math.hypot(dx, dy);
    if (g.kind === 'swipe') {
      setDrag(null);
      const fast = Date.now() - g.t0 < 260;
      if (Math.abs(dx) > Math.abs(dy) && (Math.abs(dx) > 80 || (fast && Math.abs(dx) > 30))) {
        go(dx < 0 ? 1 : -1);
        return;
      }
      if (dy > 120 && dy > Math.abs(dx) * 1.5) {
        onClose();
        return;
      }
    }
    // A tap: double-tap zooms in or back out; a single tap on touch shows or hides the controls.
    if (moved < 8 && Date.now() - g.t0 < 300) {
      const now = Date.now();
      const near = Math.hypot(e.clientX - lastTap.current.x, e.clientY - lastTap.current.y) < 30;
      if (now - lastTap.current.at < 320 && near) {
        window.clearTimeout(tapTimer.current);
        lastTap.current.at = 0;
        const pt = rel(e.clientX, e.clientY);
        // Fit ⇄ 100 % (actual pixels; twice the size for pictures smaller than the window).
        if (view.s > 1) setView(FIT);
        else zoomAt(fit > 0 && 1 / fit > 1.05 ? 1 / fit : 2, pt.x, pt.y, FIT);
        return;
      }
      lastTap.current = { at: now, x: e.clientX, y: e.clientY };
      if (e.pointerType === 'touch') {
        tapTimer.current = window.setTimeout(() => {
          // A tap brings the bars back, or puts them away.
          if (calmRef.current) {
            setHidden(false);
            poke();
          } else setHidden(true);
        }, 330);
      }
    }
  };
  useEffect(() => () => window.clearTimeout(tapTimer.current), []);

  const zoomBy = (f: number) => zoomAt(view.s * f, 0, 0);
  const rotate = (d = 1) => {
    setRot((r) => r + 90 * d);
    setView(FIT);
    if (!onRotate || deleted) return;
    // Saved once the turning stops, as one change.
    savedTurns.current += d;
    window.clearTimeout(turnTimer.current);
    const photo = p;
    turnTimer.current = window.setTimeout(() => {
      const turns = savedTurns.current;
      savedTurns.current = 0;
      if (!(((turns % 4) + 4) % 4)) return;
      void onRotate(photo, turns).then((ok) => {
        if (ok) uprightOnLoad.current = true;
      });
    }, 900);
  };
  useEffect(() => () => window.clearTimeout(turnTimer.current), []);

  const taken = new Date(p.taken);
  const where = deleted ? p.trash!.orig_path : p.path;
  const mp = p.width && p.height ? (p.width * p.height) / 1e6 : 0;
  const exposure = camera ? exposureText(camera) : [];
  const hasLoc = !!camera && !!(camera.lat || camera.lon);
  // The bars fade away while nothing moves (not while the pointer rests on them, or a panel is open).
  const chromeOff = !editing && (hidden || (idle && !overBar && !info && !menu && !keysHelp));
  calmRef.current = chromeOff;
  const canEdit = !!onSaveEdit && !p.video && !deleted && browserCanShow(p) && !!src;
  const moving = !gesturing && !reduced;
  const imgStyle = {
    width: imgW,
    height: imgH,
    transform: `translate(-50%, -50%) translate(${view.x + (drag?.dx ?? 0)}px, ${view.y + (drag && view.s === 1 ? Math.max(0, drag.dy) : 0)}px) rotate(${rot}deg) scale(${view.s})`,
    transition: moving ? 'transform 0.28s cubic-bezier(.2,.8,.2,1)' : 'none',
    opacity: drag && drag.dy > 0 && Math.abs(drag.dy) > Math.abs(drag.dx) ? Math.max(0.4, 1 - drag.dy / 500) : undefined,
  };
  const lo = Math.max(0, index - 40);
  const hi = Math.min(photos.length, index + 41);

  return (
    <div
      ref={box}
      className={`pg-viewer${chromeOff ? ' calm' : ''}${info ? ' with-info' : ''}`}
      tabIndex={-1}
      role="dialog"
      aria-label={p.name}
      onPointerMove={poke}
      onKeyDown={(e) => {
        if (e.ctrlKey || e.metaKey || e.altKey || editing) return;
        if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
        const k = e.key;
        if (k === 'Escape') {
          if (document.fullscreenElement) return;
          if (view.s > 1) setView(FIT);
          else onClose();
        } else if (k === 'ArrowLeft') go(-1);
        else if (k === 'ArrowRight') go(1);
        else if (k === 'Delete') onDelete?.(p);
        else if (k === 'i') setInfo((v) => !v);
        else if (k === 'f' && !deleted && onFavorite) onFavorite(p);
        else if (k === 'r' && !p.video) rotate(1);
        else if (k === 'R' && !p.video) rotate(-1);
        else if (k === 'e' && canEdit) setEditing(true);
        else if (k === '?') setKeysHelp(true);
        else if (k === 'Home') onIndex(0);
        else if (k === 'End') onIndex(photos.length - 1);
        else if (k === 's') setSlideshow((v) => !v);
        else if ((k === '+' || k === '=') && !p.video) zoomBy(1.5);
        else if (k === '-' && !p.video) zoomBy(1 / 1.5);
        else if (k === '0') setView(FIT);
        else if (k === 'Enter' && !p.video) toggleFull();
        else return;
        e.preventDefault();
        e.stopPropagation();
      }}
    >
      {thumb && <div className="pg-vback" style={{ backgroundImage: `url("${thumb}")` }} />}
      <header className="pg-vbar" onPointerEnter={() => setOverBar(true)} onPointerLeave={() => setOverBar(false)}>
        {!windowed && (
          <button type="button" className="pg-vbtn" aria-label={t('photos.v.back')} title={t('photos.v.backKey')} onClick={onClose}>
            <Icon name="chevronLeft" size={20} />
          </button>
        )}
        <div className="pg-vtitle">
          <strong>{longDateFmt.format(taken)}</strong>
          <span>
            {timeFmt.format(taken)}
            {photos.length > 1 && ` · ${t('photos.v.position', { n: index + 1, total: photos.length })}`}
          </span>
        </div>
        <div className="pg-vtools">
          {deleted ? (
            onRestore && (
              <button type="button" className="pg-vbtn text" onClick={() => onRestore(p)}>
                <Icon name="rewind" size={16} /> {t('photos.restore')}
              </button>
            )
          ) : (
            <>
              {onFavorite && (
                <button
                  type="button"
                  className={`pg-vbtn${p.favorite ? ' faved' : ''}`}
                  aria-pressed={!!p.favorite}
                  aria-label={p.favorite ? t('photos.unfavorite') : t('photos.favorite')}
                  title={`${p.favorite ? t('photos.unfavorite') : t('photos.favorite')} (F)`}
                  onClick={() => onFavorite(p)}
                >
                  <Icon name="heart" size={18} />
                </button>
              )}
              {onShare && (
                <button type="button" className="pg-vbtn" aria-label={t('photos.share')} title={t('photos.share')} onClick={(e) => onShare(e, p)}>
                  <Icon name="share" size={18} />
                </button>
              )}
              {onAlbum && (
                <button type="button" className="pg-vbtn hide-narrow" aria-label={t('photos.addToAlbum')} title={t('photos.addToAlbum')} onClick={(e) => onAlbum(e, p)}>
                  <Icon name="albums" size={18} />
                </button>
              )}
            </>
          )}
          {!p.video && (
            <>
              <button type="button" className="pg-vbtn hide-narrow" aria-label={t('pv.rotateLeft')} title={`${t('pv.rotateLeft')} (Shift+R)`} onClick={() => rotate(-1)}>
                <Icon name="rotate" size={18} className="flip-x" />
              </button>
              <button type="button" className="pg-vbtn hide-narrow" aria-label={t('pv.rotateRight')} title={`${t('pv.rotateRight')} (R)`} onClick={() => rotate(1)}>
                <Icon name="rotate" size={18} />
              </button>
            </>
          )}
          {canEdit && (
            <button type="button" className="pg-vbtn" aria-label={t('pv.edit.title')} title={`${t('pv.edit.title')} (E)`} onClick={() => setEditing(true)}>
              <Icon name="sliders" size={18} />
            </button>
          )}
          {photos.length > 1 && (
            <button
              type="button"
              className={`pg-vbtn${slideshow ? ' on' : ''}`}
              aria-pressed={slideshow}
              aria-label={slideshow ? t('photos.v.stopShow') : t('photos.v.slideshow')}
              title={`${slideshow ? t('photos.v.stopShow') : t('photos.v.slideshow')} (S)`}
              onClick={() => setSlideshow((v) => !v)}
            >
              <Icon name={slideshow ? 'pause' : 'play'} size={17} />
            </button>
          )}
          {slideshow && (
            <button type="button" className="pg-vbtn text" title={t('pv.slideEvery')} aria-label={t('pv.slideEvery')} onClick={nextSlideMs}>
              {slideMs / 1000} s
            </button>
          )}
          {download && (
            <a className="pg-vbtn hide-narrow" href={download} download={p.name} aria-label={t('photos.download')} title={t('photos.download')}>
              <Icon name="download" size={18} />
            </a>
          )}
          <button type="button" className={`pg-vbtn${info ? ' on' : ''}`} aria-pressed={info} aria-label={t('photos.v.info')} title={`${t('photos.v.info')} (I)`} onClick={() => setInfo((v) => !v)}>
            <Icon name="info" size={18} />
          </button>
          <button type="button" className="pg-vbtn hide-narrow" aria-label={full ? t('photos.v.exitFull') : t('photos.v.full')} title={full ? t('photos.v.exitFull') : t('photos.v.full')} onClick={toggleFull}>
            <Icon name={full ? 'fullscreenExit' : 'fullscreen'} size={18} />
          </button>
          {moreItems && (
            <button
              type="button"
              className={`pg-vbtn${menu ? ' on' : ''}`}
              aria-label={t('pv.more')}
              title={t('pv.more')}
              aria-haspopup="menu"
              onClick={(e) => {
                const r = e.currentTarget.getBoundingClientRect();
                setMenu(menu ? null : { x: r.right - 220, y: r.bottom + 6 });
              }}
            >
              <Icon name="more" size={18} />
            </button>
          )}
          {onDelete && (
            <button
              type="button"
              className="pg-vbtn danger"
              aria-label={deleted ? t('photos.deleteForever') : t('photos.delete')}
              title={deleted ? t('photos.deleteForever') : t('photos.delete')}
              onClick={() => onDelete(p)}
            >
              <Icon name="trash" size={18} />
            </button>
          )}
        </div>
        {windowed && full && (
          <WinClose
            className="pg-vclose"
            label={tr('common.close')}
            onClick={() => {
              if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
              onClose();
            }}
          />
        )}
      </header>

      <div className="pg-vbody">
        <div
          ref={stageRef}
          className={`pg-stage${view.s > 1 ? ' zoomed' : ''}${p.video ? ' is-video' : ''}`}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
        >
          {!src ? (
            <span className="spinner" />
          ) : p.video ? (
            <div key={photoKey(p)} className="pg-video">
              <video src={src} poster={links.thumb(p, 'l') ?? undefined} controls autoPlay playsInline onEnded={slideshow ? () => go(1, true) : undefined} />
            </div>
          ) : (
            <div key={photoKey(p)} className={`pg-frame${reduced ? '' : slideshow ? ' fade slow' : nav.current.dir > 0 ? ' from-next' : nav.current.dir < 0 ? ' from-prev' : ' fade'}`}>
              {!loaded && thumb && thumb !== src && <img className="pg-img pg-preview" src={thumb} alt="" draggable={false} style={imgStyle} />}
              <img
                className={`pg-img pg-full${loaded ? ' ready' : ''}`}
                src={src}
                alt={p.name}
                draggable={false}
                style={imgStyle}
                onLoad={(e) => {
                  setLoadedKey(pk);
                  // The saved turn is in the picture now.
                  if (uprightOnLoad.current) {
                    uprightOnLoad.current = false;
                    setRot(0);
                  }
                  if (!p.width || !p.height) setMeasured({ key: pk, w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight });
                }}
                onError={() => toast('error', t('photos.v.loadFail'), p.name)}
              />
            </div>
          )}
          {index > 0 && (
            <button type="button" className="pg-arrow prev" aria-label={t('photos.v.prev')} onPointerDown={(e) => e.stopPropagation()} onClick={() => go(-1)}>
              <Icon name="chevronLeft" size={22} />
            </button>
          )}
          {index < photos.length - 1 && (
            <button type="button" className="pg-arrow next" aria-label={t('photos.v.next')} onPointerDown={(e) => e.stopPropagation()} onClick={() => go(1)}>
              <Icon name="chevronRight" size={22} />
            </button>
          )}
          {!p.video && view.s > 1.01 && thumb && shownW > 0 && (
            <Navigator thumb={thumb} rot={rot} view={view} shown={{ w: shownW, h: shownH }} stage={stage} onPan={(v) => setView(clampView({ ...view, ...v }))} />
          )}
          {!p.video && (
            <div className="pg-zoombar" onPointerDown={(e) => e.stopPropagation()}>
              <button type="button" aria-label={t('photos.v.zoomOut')} title={`${t('photos.v.zoomOut')} (−)`} disabled={view.s <= 1} onClick={() => zoomBy(1 / 1.5)}>
                <Icon name="zoomOut" size={16} />
              </button>
              <button type="button" className="pg-zoomval" title={t('photos.v.fitKey')} onClick={() => setView(FIT)}>
                {Math.round(view.s * fit * 100) || 100}%
              </button>
              <button type="button" aria-label={t('photos.v.zoomIn')} title={`${t('photos.v.zoomIn')} (+)`} disabled={view.s >= maxZoom} onClick={() => zoomBy(1.5)}>
                <Icon name="zoomIn" size={16} />
              </button>
            </div>
          )}
        </div>

        {info && (
          <aside className="pg-info" aria-label={t('photos.v.info')}>
            <div className="pg-info-head">
              <h3>{p.name}</h3>
              <WinClose label={t('photos.v.closeInfo')} onClick={() => setInfo(false)} />
            </div>
            <dl>
              <dt>{p.video ? t('photos.v.recorded') : t('photos.v.taken')}</dt>
              <dd>{dateTimeFmt.format(taken)}</dd>
              {deleted && (
                <>
                  <dt>{t('photos.v.deleted')}</dt>
                  <dd>{dateTimeFmt.format(new Date(p.trash!.deleted_at))}</dd>
                </>
              )}
              {p.video && p.duration ? (
                <>
                  <dt>{t('photos.v.length')}</dt>
                  <dd>{fmtDuration(p.duration)}</dd>
                </>
              ) : null}
              {p.width ? (
                <>
                  <dt>{t('photos.v.size')}</dt>
                  <dd>
                    {p.width} × {p.height}
                    {mp >= 0.5 && <span className="pg-muted"> · {mp.toFixed(1)} MP</span>}
                  </dd>
                </>
              ) : null}
              <dt>{t('photos.v.fileSize')}</dt>
              <dd>{fmtBytes(p.size)}</dd>
            </dl>
            {camera && (camera.make || camera.model || exposure.length > 0) && (
              <div className="pg-camera">
                <div className="pg-camera-head">
                  <Icon name="camera" size={16} />
                  <span>
                    <strong>{[camera.make, camera.model].filter(Boolean).join(' ') || t('photos.v.camera')}</strong>
                    {camera.lens && <small>{camera.lens}</small>}
                  </span>
                </div>
                {exposure.length > 0 && (
                  <div className="pg-exposure">
                    {exposure.map((x) => (
                      <span key={x}>{x}</span>
                    ))}
                  </div>
                )}
                {camera.focal_35 ? <div className="pg-muted">{t('photos.v.focal35', { mm: camera.focal_35 })}</div> : null}
              </div>
            )}
            {hasLoc && camera && (
              <div className="pg-place">
                <Icon name="mapPin" size={16} />
                <span>
                  <strong>{t('photos.v.place')}</strong>
                  <small>{coordText(camera.lat ?? 0, camera.lon ?? 0)}</small>
                </span>
                <button
                  type="button"
                  className="pg-vbtn small"
                  aria-label={t('photos.v.copyPlace')}
                  title={t('photos.v.copyPlace')}
                  onClick={() =>
                    void navigator.clipboard
                      ?.writeText(`${(camera.lat ?? 0).toFixed(6)}, ${(camera.lon ?? 0).toFixed(6)}`)
                      .then(() => toast('success', t('photos.v.copied')))
                      .catch(() => undefined)
                  }
                >
                  <Icon name="copy" size={14} />
                </button>
              </div>
            )}
            <dl>
              <dt>{deleted ? t('photos.v.wasIn') : t('photos.v.folder')}</dt>
              <dd className="pg-path">
                {rootName(p.root)} › {where.replace(/^\//, '').split('/').slice(0, -1).join(' › ') || t('photos.v.topFolder')}
              </dd>
            </dl>
          </aside>
        )}
      </div>

      {showStrip && photos.length > 1 && (
        <div
          ref={strip}
          className="pg-strip"
          aria-hidden
          onPointerEnter={() => setOverBar(true)}
          onPointerLeave={() => setOverBar(false)}
          onWheel={(e) => {
            if (Math.abs(e.deltaY) < 4 && Math.abs(e.deltaX) < 4) return;
            go((Math.abs(e.deltaY) > Math.abs(e.deltaX) ? e.deltaY : e.deltaX) > 0 ? 1 : -1);
          }}
        >
          {lo > 0 && <span className="pg-strip-more" />}
          {photos.slice(lo, hi).map((q, j) => (
            <button key={photoKey(q)} type="button" tabIndex={-1} className={`pg-strip-item${lo + j === index ? ' cur' : ''}`} onClick={() => onIndex(lo + j)}>
              <Thumb photo={q} src={links.thumb(q)} />
            </button>
          ))}
          {hi < photos.length && <span className="pg-strip-more" />}
        </div>
      )}
      {editing && canEdit && src && (
        <PhotoEditor
          key={pk}
          src={src}
          name={p.name}
          canReplace={keepsType(p.name)}
          onCancel={() => {
            setEditing(false);
            box.current?.focus();
          }}
          onSave={async (file, replace) => {
            const ok = await onSaveEdit!(p, file, replace);
            if (ok) {
              setEditing(false);
              box.current?.focus();
            }
            return ok;
          }}
        />
      )}
      {menu && moreItems && <ContextMenu x={menu.x} y={menu.y} items={moreItems(p)} onClose={() => setMenu(null)} />}
      {keysHelp && (
        <Modal title={t('pv.keys.title')} icon="grid" onClose={() => setKeysHelp(false)} className="pv-keys">
          <dl className="pv-keys-list">
            {KEYS.filter(([, , need]) => !need || (need === 'edit' ? !!onSaveEdit : true)).map(([k, label]) => (
              <div key={k}>
                <dt>
                  {k.split(' ').map((x) => (
                    <kbd key={x}>{x}</kbd>
                  ))}
                </dt>
                <dd>{t(label)}</dd>
              </div>
            ))}
          </dl>
        </Modal>
      )}
    </div>
  );
}

/** The keys the "?" sheet lists: key(s), what they do, and what they need. */
const KEYS: [string, string, ('edit' | '')?][] = [
  ['← →', 'pv.keys.step'],
  ['Home End', 'pv.keys.ends'],
  ['+ −', 'pv.keys.zoom'],
  ['0', 'pv.keys.fit'],
  ['R', 'pv.keys.right'],
  ['Shift+R', 'pv.keys.left'],
  ['E', 'pv.keys.edit', 'edit'],
  ['S', 'pv.keys.show'],
  ['I', 'pv.keys.info'],
  ['Enter', 'pv.keys.full'],
  ['Del', 'pv.keys.delete'],
  ['Esc', 'pv.keys.close'],
  ['?', 'pv.keys.help'],
];

/** The small map in the corner while zoomed in: the whole picture, with the part on show outlined (drag it to move). */
function Navigator({ thumb, rot, view, shown, stage, onPan }: {
  thumb: string; rot: number; view: { s: number; x: number; y: number }; shown: { w: number; h: number }; stage: { w: number; h: number };
  onPan: (v: { x: number; y: number }) => void;
}) {
  const W = 150;
  const k = W / Math.max(shown.w, shown.h);
  const nw = shown.w * k;
  const nh = shown.h * k;
  // The stage in picture fractions (centre-relative pan, scaled picture).
  const fw = Math.min(1, stage.w / (shown.w * view.s));
  const fh = Math.min(1, stage.h / (shown.h * view.s));
  const cx = 0.5 - view.x / (shown.w * view.s);
  const cy = 0.5 - view.y / (shown.h * view.s);
  const quarter = ((rot % 180) + 180) % 180 === 90;
  const move = (e: RPointerEvent<HTMLDivElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const fx = (e.clientX - r.left) / r.width;
    const fy = (e.clientY - r.top) / r.height;
    onPan({ x: (0.5 - fx) * shown.w * view.s, y: (0.5 - fy) * shown.h * view.s });
  };
  return (
    <div
      className="pv-nav"
      style={{ width: nw, height: nh }}
      onPointerDown={(e) => {
        e.stopPropagation();
        e.currentTarget.setPointerCapture(e.pointerId);
        move(e);
      }}
      onPointerMove={(e) => e.buttons && move(e)}
    >
      <img src={thumb} alt="" draggable={false} style={{ width: quarter ? nh : nw, height: quarter ? nw : nh, transform: `translate(-50%, -50%) rotate(${rot}deg)` }} />
      <span style={{ left: `${(cx - fw / 2) * 100}%`, top: `${(cy - fh / 2) * 100}%`, width: `${fw * 100}%`, height: `${fh * 100}%` }} />
    </div>
  );
}
