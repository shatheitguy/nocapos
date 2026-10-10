import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { photoKey, thumbUrl, type Photo } from '../api/photos';
import { dayKey, dayLabel, monthFmt, monthKey, monthOnlyFmt, ticketFor, Tile, yearKey, type Tickets } from './PhotosCommon';

export type Grouping = 'day' | 'month' | 'none';

interface Group {
  key: string;
  date: Date;
  start: number;
  count: number;
}

type Row = { top: number; h: number; g: number } & ({ kind: 'head' } | { kind: 'tiles'; start: number; end: number });

const GAP = 3;
const HEAD = 48;
const OVERSCAN = 700;

export const takenOf = (p: Photo) => p.taken;
export const addedOf = (p: Photo) => p.mod_time;
export const deletedOf = (p: Photo) => p.trash?.deleted_at ?? p.mod_time;

const groupLabel = (g: Group, grouping: Grouping) => (grouping === 'month' ? monthFmt.format(g.date) : dayLabel(g.date));

/**
 * The photo timeline: date-grouped rows of square tiles, rendered only near
 * the visible part so libraries with many thousands of items stay fast. A
 * floating date header follows the scroll and a rail on the right jumps
 * through the years.
 */
export function PhotoGrid({
  items,
  grouping,
  dateOf = takenOf,
  tileMin,
  tickets,
  selected,
  selecting,
  onToggle,
  onToggleMany,
  onOpen,
  top,
  jump,
  empty,
}: {
  items: Photo[];
  grouping: Grouping;
  dateOf?: (p: Photo) => string;
  tileMin: number;
  tickets: Tickets;
  selected: Set<string>;
  selecting: boolean;
  onToggle: (i: number, range: boolean) => void;
  onToggleMany: (from: number, to: number, on: boolean) => void;
  onOpen: (i: number) => void;
  /** Shown above the grid (scrolls with it). */
  top?: ReactNode;
  /** Scroll to the first group whose key starts with this ("2024", "2024-03"). */
  jump?: { key: string; seq: number };
  empty?: ReactNode;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [vh, setVh] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [canvasTop, setCanvasTop] = useState(0);

  useLayoutEffect(() => {
    const el = scroller.current;
    const c = canvas.current;
    if (!el || !c) return;
    const measure = () => {
      setWidth(c.clientWidth);
      setVh(el.clientHeight);
      setCanvasTop(c.offsetTop);
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    ro.observe(c);
    if (c.previousElementSibling) ro.observe(c.previousElementSibling);
    return () => ro.disconnect();
  }, [!!top]);

  const groups = useMemo(() => {
    const out: Group[] = [];
    items.forEach((p, i) => {
      const d = new Date(dateOf(p));
      const key = grouping === 'month' ? monthKey(d) : grouping === 'day' ? dayKey(d) : '';
      const last = out[out.length - 1];
      if (last && last.key === key) last.count++;
      else out.push({ key, date: d, start: i, count: 1 });
    });
    return out;
  }, [items, grouping, dateOf]);

  // Small windows (phones) get smaller tiles.
  const min = width && width < 520 ? Math.min(tileMin, 96) : tileMin;
  const cols = Math.max(2, Math.floor((width + GAP) / (min + GAP)));
  const size = width ? (width - GAP * (cols - 1)) / cols : 0;
  const head = grouping === 'none' ? 0 : HEAD;

  const { rows, total } = useMemo(() => {
    const rows: Row[] = [];
    let y = 0;
    if (!size) return { rows, total: 0 };
    groups.forEach((g, gi) => {
      if (head) {
        rows.push({ kind: 'head', top: y, h: head, g: gi });
        y += head;
      }
      for (let i = g.start; i < g.start + g.count; i += cols) {
        rows.push({ kind: 'tiles', top: y, h: size, g: gi, start: i, end: Math.min(i + cols, g.start + g.count) });
        y += size + GAP;
      }
      if (head) y += 10;
    });
    return { rows, total: y };
  }, [groups, cols, size, head]);

  // Rows near the view.
  const st = scrollTop - canvasTop;
  const visible = useMemo(() => {
    let lo = 0;
    let hi = rows.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (rows[mid].top + rows[mid].h < st - OVERSCAN) lo = mid + 1;
      else hi = mid;
    }
    const out: Row[] = [];
    for (let i = lo; i < rows.length && rows[i].top < st + vh + OVERSCAN; i++) out.push(rows[i]);
    return out;
  }, [rows, st, vh]);

  // The group at the top of the view, for the floating header (pushed up by the next one).
  const heads = useMemo(() => rows.filter((r) => r.kind === 'head'), [rows]);
  let sticky: { g: number; offset: number } | null = null;
  if (head && st > 0 && heads.length) {
    let lo = 0;
    let hi = heads.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (heads[mid].top <= st) lo = mid;
      else hi = mid - 1;
    }
    const next = heads[lo + 1];
    sticky = { g: heads[lo].g, offset: next ? Math.min(0, next.top - st - head) : 0 };
  }

  // Jump to a date when asked (after a layout exists).
  const jumped = useRef(0);
  useEffect(() => {
    if (!jump || jump.seq === jumped.current || !rows.length) return;
    jumped.current = jump.seq;
    const g = groups.findIndex((x) => x.key.startsWith(jump.key));
    const row = rows.find((r) => r.g === g);
    if (row && scroller.current) scroller.current.scrollTop = canvasTop + row.top + 1;
  }, [jump, rows, groups, canvasTop]);

  const raf = useRef(0);
  const onScroll = () => {
    cancelAnimationFrame(raf.current);
    raf.current = requestAnimationFrame(() => setScrollTop(scroller.current?.scrollTop ?? 0));
  };

  const groupSelected = (g: Group) => {
    for (let i = g.start; i < g.start + g.count; i++) if (!selected.has(photoKey(items[i]))) return false;
    return true;
  };

  return (
    <div className="pg-gridwrap">
      <div ref={scroller} className="pg-scroll" onScroll={onScroll}>
        {top ? <div className="pg-top">{top}</div> : null}
        {!items.length && empty}
        <div ref={canvas} className="pg-canvas" style={{ height: total }}>
          {visible.map((r) => {
            const g = groups[r.g];
            if (r.kind === 'head') {
              const all = selecting && groupSelected(g);
              return (
                <div key={`h${g.key}${g.start}`} className="pg-head" style={{ top: r.top, height: r.h }}>
                  <strong>{groupLabel(g, grouping)}</strong>
                  <span className="pg-head-count">{g.count}</span>
                  <button type="button" className={`pg-head-sel${selecting ? ' on' : ''}`} onClick={() => onToggleMany(g.start, g.start + g.count, !all)}>
                    {all ? 'Deselect' : 'Select'}
                  </button>
                </div>
              );
            }
            return (
              <div
                key={`r${r.start}`}
                className="pg-row"
                style={{ top: r.top, height: r.h, gridTemplateColumns: `repeat(${cols}, ${size}px)`, gap: GAP }}
              >
                {items.slice(r.start, r.end).map((p, j) => (
                  <Tile
                    key={photoKey(p)}
                    photo={p}
                    index={r.start + j}
                    ticket={ticketFor(tickets, p)}
                    selected={selected.has(photoKey(p))}
                    selecting={selecting}
                    onOpen={onOpen}
                    onToggle={onToggle}
                  />
                ))}
              </div>
            );
          })}
        </div>
      </div>
      {sticky && (
        <div className="pg-sticky" style={{ transform: `translateY(${sticky.offset}px)` }}>
          <strong>{groupLabel(groups[sticky.g], grouping)}</strong>
        </div>
      )}
      {head > 0 && total > vh * 2.5 && (
        <Scrubber
          groups={groups}
          rows={rows}
          total={total}
          offset={canvasTop}
          scrollTop={scrollTop}
          vh={vh}
          grouping={grouping}
          onScrollTo={(y) => scroller.current && (scroller.current.scrollTop = y)}
        />
      )}
    </div>
  );
}

/** The rail on the right: years marked along it; drag or click to jump. */
function Scrubber({
  groups,
  rows,
  total,
  offset,
  scrollTop,
  vh,
  grouping,
  onScrollTo,
}: {
  groups: Group[];
  rows: Row[];
  total: number;
  offset: number;
  scrollTop: number;
  vh: number;
  grouping: Grouping;
  onScrollTo: (y: number) => void;
}) {
  const rail = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ y: number; label: string } | null>(null);
  const [dragging, setDragging] = useState(false);
  const full = offset + total;
  const railH = Math.max(1, vh - 24);

  const groupTop = useMemo(() => {
    const out = new Map<number, number>();
    for (const r of rows) if (!out.has(r.g)) out.set(r.g, r.top);
    return out;
  }, [rows]);

  // A label for each year (skipping ones that would overlap).
  const marks = useMemo(() => {
    const out: { y: number; label: string }[] = [];
    let lastYear = '';
    let lastY = -100;
    groups.forEach((g, i) => {
      const year = yearKey(g.date);
      if (year === lastYear) return;
      lastYear = year;
      const y = ((offset + (groupTop.get(i) ?? 0)) / full) * railH;
      if (y - lastY < 16) return;
      lastY = y;
      out.push({ y, label: year });
    });
    return out;
  }, [groups, groupTop, offset, full, railH]);

  const at = (clientY: number) => {
    const r = rail.current!.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (clientY - r.top) / r.height));
    const target = f * full;
    // The group under that point, for the bubble.
    let g = 0;
    for (const [gi, t] of groupTop) if (offset + t <= target) g = gi;
    const d = groups[g]?.date;
    const label = d ? (grouping === 'day' ? `${monthOnlyFmt.format(d)} ${d.getFullYear()}` : monthFmt.format(d)) : '';
    return { y: f * r.height, target, label };
  };

  const thumbY = (scrollTop / Math.max(1, full - vh)) * railH;

  return (
    <div
      ref={rail}
      className={`pg-scrubber${dragging ? ' dragging' : ''}`}
      style={{ height: railH }}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        setDragging(true);
        const a = at(e.clientY);
        onScrollTo(a.target);
        setHover({ y: a.y, label: a.label });
      }}
      onPointerMove={(e) => {
        const a = at(e.clientY);
        setHover({ y: a.y, label: a.label });
        if (dragging) onScrollTo(a.target);
      }}
      onPointerUp={() => setDragging(false)}
      onPointerLeave={() => !dragging && setHover(null)}
      aria-hidden
    >
      {marks.map((m) => (
        <span key={m.label} className="pg-mark" style={{ top: m.y }}>
          {m.label}
        </span>
      ))}
      <span className="pg-thumb" style={{ top: Math.min(railH - 2, Math.max(0, thumbY)) }} />
      {hover && (
        <span className="pg-bubble" style={{ top: hover.y }}>
          {hover.label}
        </span>
      )}
    </div>
  );
}

/** Years or Months: one big card per period, newest first. */
export function PeriodCards({
  items,
  unit,
  tickets,
  onPick,
  jump,
}: {
  items: Photo[];
  unit: 'year' | 'month';
  tickets: Tickets;
  onPick: (key: string) => void;
  jump?: { key: string; seq: number };
}) {
  const box = useRef<HTMLDivElement>(null);
  const jumped = useRef(0);
  useEffect(() => {
    if (!jump || jump.seq === jumped.current || !box.current) return;
    jumped.current = jump.seq;
    const el = [...box.current.querySelectorAll<HTMLElement>('[data-key]')].find((x) => x.dataset.key!.startsWith(jump.key));
    if (el) box.current.scrollTop = el.offsetTop - 4;
  }, [jump]);
  const periods = useMemo(() => {
    const out: { key: string; date: Date; photos: Photo[] }[] = [];
    for (const p of items) {
      const d = new Date(p.taken);
      const key = unit === 'year' ? yearKey(d) : monthKey(d);
      const last = out[out.length - 1];
      if (last?.key === key) last.photos.push(p);
      else out.push({ key, date: d, photos: [p] });
    }
    return out;
  }, [items, unit]);

  return (
    <div ref={box} className="pg-scroll">
      <div className={`pg-cards ${unit}`}>
        {periods.map((per) => {
          // A favorite makes the best cover; otherwise a picture from the middle.
          const pics = per.photos.filter((p) => !p.video);
          const cover = pics.find((p) => p.favorite) ?? pics[Math.floor(pics.length / 2)] ?? per.photos[0];
          const t = ticketFor(tickets, cover);
          const videos = per.photos.length - pics.length;
          return (
            <button key={per.key} data-key={per.key} type="button" className="pg-card" onClick={() => onPick(per.key)}>
              {t ? <img src={thumbUrl(cover, t)} alt="" loading="lazy" decoding="async" draggable={false} /> : <span className="pg-ph" />}
              <span className="pg-card-shade" />
              <span className="pg-card-text">
                <strong>{unit === 'year' ? per.key : monthOnlyFmt.format(per.date)}</strong>
                <span>
                  {unit === 'month' && `${per.date.getFullYear()} · `}
                  {pics.length} {pics.length === 1 ? 'photo' : 'photos'}
                  {videos ? `, ${videos} ${videos === 1 ? 'video' : 'videos'}` : ''}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

/** "On this day": photos taken on today's date in earlier years. */
export function Memories({ items, tickets, onOpen }: { items: Photo[]; tickets: Tickets; onOpen: (list: Photo[]) => void }) {
  const years = useMemo(() => {
    const now = new Date();
    const byYear = new Map<number, Photo[]>();
    for (const p of items) {
      const d = new Date(p.taken);
      if (d.getMonth() === now.getMonth() && d.getDate() === now.getDate() && d.getFullYear() < now.getFullYear()) {
        const list = byYear.get(d.getFullYear()) ?? [];
        list.push(p);
        byYear.set(d.getFullYear(), list);
      }
    }
    return [...byYear.entries()].sort((a, b) => b[0] - a[0]).map(([y, list]) => ({ ago: now.getFullYear() - y, year: y, list }));
  }, [items]);
  if (!years.length) return null;
  return (
    <section className="pg-memories" aria-label="On this day">
      <h3 className="section-label">On this day</h3>
      <div className="pg-mem-strip">
        {years.map((m) => {
          const cover = m.list.find((p) => !p.video) ?? m.list[0];
          const t = ticketFor(tickets, cover);
          return (
            <button key={m.year} type="button" className="pg-mem" onClick={() => onOpen(m.list)}>
              {t ? <img src={thumbUrl(cover, t)} alt="" loading="lazy" decoding="async" draggable={false} /> : <span className="pg-ph" />}
              <span className="pg-card-shade" />
              <span className="pg-card-text">
                <strong>{m.ago === 1 ? '1 year ago' : `${m.ago} years ago`}</strong>
                <span>
                  {m.year} · {m.list.length} {m.list.length === 1 ? 'item' : 'items'}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}

