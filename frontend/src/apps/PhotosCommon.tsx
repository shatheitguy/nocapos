import { memo, useEffect, useRef, useState, type CSSProperties } from 'react';
import { photoKey, thumbUrl, type Photo } from '../api/photos';
import { Icon } from '../components/Icon';
import type { T } from '../i18n';

/** Links for thumbnails and originals: one per drive for the Photos folder, one for its recycle bin. */
export interface Tickets {
  photos: Record<string, string>;
  trash: Record<string, string>;
}

export const ticketFor = (t: Tickets, p: Photo): string | undefined => (p.trash ? t.trash[p.root] : t.photos[p.root]);

export function fmtBytes(n: number) {
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

/** 75.4 → "1:15", 3725 → "1:02:05". */
export function fmtDuration(s: number) {
  const t = Math.max(0, Math.round(s));
  const h = Math.floor(t / 3600);
  const m = Math.floor((t % 3600) / 60);
  const sec = String(t % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

const pad = (n: number) => String(n).padStart(2, '0');
export const yearKey = (d: Date) => String(d.getFullYear());
export const monthKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
export const dayKey = (d: Date) => `${monthKey(d)}-${pad(d.getDate())}`;

const weekdayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'long' });
const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'long' });
const dayYearFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'long', year: 'numeric' });
export const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });
export const monthShortFmt = new Intl.DateTimeFormat(undefined, { month: 'short', year: 'numeric' });
export const monthOnlyFmt = new Intl.DateTimeFormat(undefined, { month: 'long' });
export const longDateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'long' });
export const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });
export const dateTimeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

/** "Today", "Yesterday", "Tuesday" (this week), "Sat, 12 March" or with the year when it isn't this year. */
export function dayLabel(t: T, d: Date, now = new Date()) {
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(now) - start(d)) / 86_400_000);
  if (days === 0) return t('photos.today');
  if (days === 1) return t('photos.yesterday');
  if (days > 1 && days < 7) return weekdayFmt.format(d);
  return d.getFullYear() === now.getFullYear() ? dayFmt.format(d) : dayYearFmt.format(d);
}

/** "12 photos, 3 videos" (or one of them). */
export function countText(t: T, list: Photo[]) {
  const v = list.reduce((a, p) => a + (p.video ? 1 : 0), 0);
  const ph = list.length - v;
  const parts: string[] = [];
  if (ph || !v) parts.push(ph === 1 ? t('photos.onePhoto') : t('photos.nPhotos', { n: ph.toLocaleString() }));
  if (v) parts.push(v === 1 ? t('photos.oneVideo') : t('photos.nVideos', { n: v.toLocaleString() }));
  return parts.join(', ');
}

/** "1 item" / "5 items". */
export const itemsText = (t: T, n: number) => (n === 1 ? t('photos.oneItem') : t('photos.nItems', { n: n.toLocaleString() }));

/** The width/height of a photo (square when unknown). */
export const ratioOf = (p: Photo) => (p.width && p.height ? p.width / p.height : 1);

/** Whether the person asked for less motion. */
export function useReducedMotion() {
  const q = '(prefers-reduced-motion: reduce)';
  const [on, setOn] = useState(() => typeof matchMedia === 'function' && matchMedia(q).matches);
  useEffect(() => {
    if (typeof matchMedia !== 'function') return;
    const m = matchMedia(q);
    const f = () => setOn(m.matches);
    m.addEventListener('change', f);
    return () => m.removeEventListener('change', f);
  }, []);
  return on;
}

/** A soft colour for a photo's placeholder, the same every time for the same file. */
export function placeholderStyle(p: Photo): CSSProperties {
  const k = photoKey(p);
  let h = 0;
  for (let i = 0; i < k.length; i++) h = (h * 31 + k.charCodeAt(i)) | 0;
  const hue = Math.abs(h) % 360;
  return { background: `linear-gradient(135deg, hsl(${hue} 22% 20%), hsl(${(hue + 40) % 360} 26% 13%))` };
}

/**
 * A thumbnail that fades in from a blurred, tinted placeholder once it has
 * loaded (and stays a tidy icon tile if it can't be made).
 */
export const Thumb = memo(function Thumb({
  photo,
  ticket,
  src,
  size = 's',
  className = '',
}: {
  photo: Photo;
  ticket?: string;
  /** A ready link instead of a Photos ticket. */
  src?: string | null;
  size?: 's' | 'l';
  className?: string;
}) {
  const [state, setState] = useState<'loading' | 'ready' | 'failed'>('loading');
  const url = src ?? (ticket ? thumbUrl(photo, ticket, size) : null);
  return (
    <span className={`pg-thumbimg ${state} ${className}`} style={placeholderStyle(photo)}>
      {url && state !== 'failed' ? (
        <img
          src={url}
          alt=""
          loading="lazy"
          decoding="async"
          draggable={false}
          onLoad={() => setState('ready')}
          onError={() => setState('failed')}
        />
      ) : null}
      {state === 'failed' || !url ? (
        <span className="pg-ph">
          <Icon name={photo.video ? 'fileVideo' : 'image'} size={24} />
        </span>
      ) : null}
    </span>
  );
});

/** One photo or video in the timeline. */
export const Tile = memo(function Tile({
  photo,
  index,
  ticket,
  selected,
  selecting,
  style,
  labels,
  onOpen,
  onToggle,
  onLongPress,
}: {
  photo: Photo;
  index: number;
  ticket?: string;
  selected: boolean;
  selecting: boolean;
  style?: CSSProperties;
  labels: { select: string; deselect: string };
  onOpen: (i: number) => void;
  onToggle: (i: number, range: boolean) => void;
  onLongPress?: (i: number) => void;
}) {
  // A long press on a touch screen starts selecting (like a right-click elsewhere).
  const timer = useRef(0);
  const pressed = useRef(false);
  const cancel = () => window.clearTimeout(timer.current);
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selecting ? selected : undefined}
      aria-label={photo.name}
      className={`pg-tile${selected ? ' sel' : ''}${selecting ? ' selecting' : ''}${photo.video ? ' video' : ''}`}
      style={style}
      title={photo.name}
      onPointerDown={(e) => {
        if (e.pointerType !== 'touch' || !onLongPress) return;
        pressed.current = false;
        timer.current = window.setTimeout(() => {
          pressed.current = true;
          onLongPress(index);
          navigator.vibrate?.(12);
        }, 450);
      }}
      onPointerUp={cancel}
      onPointerCancel={cancel}
      onPointerLeave={cancel}
      onContextMenu={(e) => {
        if (onLongPress && (e.nativeEvent as PointerEvent).pointerType === 'touch') e.preventDefault();
      }}
      onClick={(e) => {
        if (pressed.current) {
          pressed.current = false;
          return;
        }
        if (selecting || e.shiftKey || e.ctrlKey || e.metaKey) onToggle(index, e.shiftKey);
        else onOpen(index);
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onOpen(index);
        if (e.key === ' ') {
          e.preventDefault();
          onToggle(index, e.shiftKey);
        }
      }}
    >
      <Thumb photo={photo} ticket={ticket} />
      {photo.video && (
        <span className="pg-dur">
          <Icon name="play" size={10} />
          {photo.duration ? fmtDuration(photo.duration) : null}
        </span>
      )}
      {photo.favorite && (
        <span className="pg-fav">
          <Icon name="heart" size={13} />
        </span>
      )}
      <button
        type="button"
        className="pg-check"
        tabIndex={-1}
        aria-label={selected ? labels.deselect : labels.select}
        onClick={(e) => {
          e.stopPropagation();
          onToggle(index, e.shiftKey);
        }}
      >
        <Icon name="check" size={12} />
      </button>
    </div>
  );
});
