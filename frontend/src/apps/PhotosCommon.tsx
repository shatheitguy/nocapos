import { memo, useState } from 'react';
import { thumbUrl, type Photo } from '../api/photos';
import { Icon } from '../components/Icon';

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
export const monthOnlyFmt = new Intl.DateTimeFormat(undefined, { month: 'long' });
export const longDateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'long' });
export const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });
export const dateTimeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

/** "Today", "Yesterday", "Tuesday" (this week), "Sat, 12 March" or with the year when it isn't this year. */
export function dayLabel(d: Date, now = new Date()) {
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(now) - start(d)) / 86_400_000);
  if (days === 0) return 'Today';
  if (days === 1) return 'Yesterday';
  if (days > 1 && days < 7) return weekdayFmt.format(d);
  return d.getFullYear() === now.getFullYear() ? dayFmt.format(d) : dayYearFmt.format(d);
}

/** One photo or video in a grid. */
export const Tile = memo(function Tile({
  photo,
  index,
  ticket,
  selected,
  selecting,
  onOpen,
  onToggle,
}: {
  photo: Photo;
  index: number;
  ticket?: string;
  selected: boolean;
  selecting: boolean;
  onOpen: (i: number) => void;
  onToggle: (i: number, range: boolean) => void;
}) {
  const [failed, setFailed] = useState(false);
  return (
    <div
      role="button"
      tabIndex={0}
      aria-pressed={selecting ? selected : undefined}
      className={`pg-tile${selected ? ' sel' : ''}${selecting ? ' selecting' : ''}`}
      title={photo.name}
      onClick={(e) => (selecting || e.shiftKey || e.ctrlKey || e.metaKey ? onToggle(index, e.shiftKey) : onOpen(index))}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onOpen(index);
        if (e.key === ' ') {
          e.preventDefault();
          onToggle(index, e.shiftKey);
        }
      }}
    >
      {ticket && !failed ? (
        <img src={thumbUrl(photo, ticket)} alt="" loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} />
      ) : (
        <span className="pg-ph">
          <Icon name={photo.video ? 'fileVideo' : 'image'} size={26} />
        </span>
      )}
      {photo.video && (
        <span className="pg-dur">
          {photo.duration ? fmtDuration(photo.duration) : null}
          <Icon name="play" size={10} />
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
        aria-label={selected ? 'Deselect' : 'Select'}
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
