import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { photoKey, type Photo } from '../api/photos';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { justify, type JustifiedRow } from '../lib/justify';
import {
  countText,
  dayKey,
  dayLabel,
  itemsText,
  monthFmt,
  monthKey,
  monthOnlyFmt,
  monthShortFmt,
  ratioOf,
  Thumb,
  ticketFor,
  Tile,
  yearKey,
  type Tickets,
} from './PhotosCommon';

export type Grouping = 'day' | 'month' | 'none';

interface Group {
  key: string;
  date: Date;
  start: number;
  count: number;
}

type Row = { top: number; h: number; g: number } & ({ kind: 'head' } | { kind: 'tiles'; jr: JustifiedRow });

const GAP = 4;
const HEAD = 52;
const OVERSCAN = 900;

export const takenOf = (p: Photo) => p.taken;
export const addedOf = (p: Photo) => p.mod_time;
export const deletedOf = (p: Photo) => p.trash?.deleted_at ?? p.mod_time;

/**
 * The photo timeline: date-grouped, justified rows (each picture keeps its
 * shape), drawn only near the visible part so libraries with many thousands
 * of items scroll smoothly. The current date floats at the top and a date
 * rail on the right jumps through the years.
 */
export function PhotoGrid({
  items,
  grouping,
  dateOf = takenOf,
  target,
  tickets,
  selected,
  selecting,
  onToggle,
  onToggleMany,
  onOpen,
  onLongPress,
  top,
  jump,
  empty,
}: {
  items: Photo[];
  grouping: Grouping;
  dateOf?: (p: Photo) => string;
  /** Row height to aim for, in pixels (smaller on phones). */
  target: number;
  tickets: Tickets;
  selected: Set<string>;
  selecting: boolean;
  onToggle: (i: number, range: boolean) => void;
  onToggleMany: (from: number, to: number, on: boolean) => void;
  onOpen: (i: number) => void;
  onLongPress?: (i: number) => void;
  /** Shown above the grid (scrolls with it). */
  top?: ReactNode;
  /** Scroll to the first group whose key starts with this ("2024", "2024-03"). */
  jump?: { key: string; seq: number };
  empty?: ReactNode;
}) {
  const t = useT();
  const scroller = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [vh, setVh] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [canvasTop, setCanvasTop] = useState(0);
  const [scrolling, setScrolling] = useState(false);

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
  }, [!!top, !!items.length]);

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

  // Phones (and narrow windows) get shorter rows.
  const rowH = width && width < 560 ? Math.round(target * 0.62) : target;
  const head = grouping === 'none' ? 0 : HEAD;

  const { rows, total } = useMemo(() => {
    const rows: Row[] = [];
    let y = 0;
    if (!width) return { rows, total: 0 };
    groups.forEach((g, gi) => {
      if (head) {
        rows.push({ kind: 'head', top: y, h: head, g: gi });
        y += head;
      }
      const ratios = items.slice(g.start, g.start + g.count).map(ratioOf);
      for (const jr of justify(ratios, width, rowH, GAP)) {
        rows.push({ kind: 'tiles', top: y, h: jr.height, g: gi, jr: { ...jr, start: jr.start + g.start, end: jr.end + g.start } });
        y += jr.height + GAP;
      }
      if (head) y += 14;
    });
    return { rows, total: y };
  }, [groups, items, width, rowH, head]);

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
  if (head && st > 4 && heads.length) {
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
  const idle = useRef(0);
  const onScroll = () => {
    cancelAnimationFrame(raf.current);
    raf.current = requestAnimationFrame(() => setScrollTop(scroller.current?.scrollTop ?? 0));
    setScrolling(true);
    window.clearTimeout(idle.current);
    idle.current = window.setTimeout(() => setScrolling(false), 1200);
  };
  useEffect(() => () => window.clearTimeout(idle.current), []);

  const groupSelected = (g: Group) => {
    for (let i = g.start; i < g.start + g.count; i++) if (!selected.has(photoKey(items[i]))) return false;
    return true;
  };

  const label = (g: Group) => (grouping === 'month' ? monthFmt.format(g.date) : dayLabel(t, g.date));
  const labels = useMemo(() => ({ select: t('photos.select'), deselect: t('photos.deselect') }), [t]);

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
                  <strong>{label(g)}</strong>
                  <span className="pg-head-count">{itemsText(t, g.count)}</span>
                  <button type="button" className={`pg-head-sel${selecting ? ' on' : ''}`} onClick={() => onToggleMany(g.start, g.start + g.count, !all)}>
                    <Icon name="check" size={13} />
                    {all ? t('photos.deselectAll') : t('photos.selectAll')}
                  </button>
                </div>
              );
            }
            const jr = r.jr;
            return (
              <div key={`r${jr.start}`} className="pg-row" style={{ top: r.top, height: r.h }}>
                {items.slice(jr.start, jr.end).map((p, j) => (
                  <Tile
                    key={photoKey(p)}
                    photo={p}
                    index={jr.start + j}
                    ticket={ticketFor(tickets, p)}
                    selected={selected.has(photoKey(p))}
                    selecting={selecting}
                    style={{ left: jr.lefts[j], width: jr.widths[j], height: jr.height }}
                    labels={labels}
                    onOpen={onOpen}
                    onToggle={onToggle}
                    onLongPress={onLongPress}
                  />
                ))}
              </div>
            );
          })}
        </div>
      </div>
      {sticky && (
        <div className="pg-sticky" style={{ transform: `translateY(${sticky.offset}px)` }}>
          <strong>{label(groups[sticky.g])}</strong>
        </div>
      )}
      {head > 0 && total > vh * 2 && (
        <Scrubber
          groups={groups}
          rows={rows}
          total={total}
          offset={canvasTop}
          scrollTop={scrollTop}
          vh={vh}
          grouping={grouping}
          active={scrolling}
          onScrollTo={(y) => scroller.current && (scroller.current.scrollTop = y)}
        />
      )}
    </div>
  );
}

/** The date rail on the right: years marked along it; drag or click to jump. */
function Scrubber({
  groups,
  rows,
  total,
  offset,
  scrollTop,
  vh,
  grouping,
  active,
  onScrollTo,
}: {
  groups: Group[];
  rows: Row[];
  total: number;
  offset: number;
  scrollTop: number;
  vh: number;
  grouping: Grouping;
  active: boolean;
  onScrollTo: (y: number) => void;
}) {
  const t = useT();
  const rail = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<{ y: number; label: string } | null>(null);
  const [dragging, setDragging] = useState(false);
  const full = offset + total;
  const railH = Math.max(1, vh - 28);

  const groupTop = useMemo(() => {
    const out: [number, number][] = [];
    let last = -1;
    for (const r of rows) {
      if (r.g === last) continue;
      last = r.g;
      out.push([r.g, r.top]);
    }
    return out;
  }, [rows]);

  // A label for each year (skipping ones that would overlap).
  const marks = useMemo(() => {
    const out: { y: number; label: string }[] = [];
    let lastYear = '';
    let lastY = -100;
    for (const [gi, top] of groupTop) {
      const year = yearKey(groups[gi].date);
      if (year === lastYear) continue;
      lastYear = year;
      const y = ((offset + top) / full) * railH;
      if (y - lastY < 18) continue;
      lastY = y;
      out.push({ y, label: year });
    }
    return out;
  }, [groups, groupTop, offset, full, railH]);

  const at = (clientY: number) => {
    const r = rail.current!.getBoundingClientRect();
    const f = Math.max(0, Math.min(1, (clientY - r.top) / r.height));
    const target = f * Math.max(0, full - vh);
    // The group under that point, for the bubble.
    let lo = 0;
    let hi = groupTop.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (offset + groupTop[mid][1] <= target + 1) lo = mid;
      else hi = mid - 1;
    }
    const d = groups[groupTop[lo]?.[0] ?? 0]?.date;
    const label = d ? (grouping === 'day' ? monthShortFmt.format(d) : monthFmt.format(d)) : '';
    return { y: f * r.height, target, label };
  };

  const thumbY = (scrollTop / Math.max(1, full - vh)) * railH;

  return (
    <div
      ref={rail}
      className={`pg-scrubber${dragging ? ' dragging' : ''}${active ? ' active' : ''}`}
      style={{ height: railH }}
      role="scrollbar"
      aria-label={t('photos.scrubber')}
      aria-orientation="vertical"
      aria-valuenow={Math.round((scrollTop / Math.max(1, full - vh)) * 100)}
      onPointerDown={(e) => {
        e.currentTarget.setPointerCapture(e.pointerId);
        setDragging(true);
        const a = at(e.clientY);
        onScrollTo(a.target);
        setHover({ y: a.y, label: a.label });
      }}
      onPointerMove={(e) => {
        if (e.pointerType === 'touch' && !dragging) return;
        const a = at(e.clientY);
        setHover({ y: a.y, label: a.label });
        if (dragging) onScrollTo(a.target);
      }}
      onPointerUp={() => {
        setDragging(false);
        setHover(null);
      }}
      onPointerLeave={() => !dragging && setHover(null)}
    >
      <span className="pg-rail-line" />
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
  const t = useT();
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
          // Favorites make the best covers; otherwise pictures spread across the period.
          const pics = per.photos.filter((p) => !p.video);
          const pool = pics.length ? pics : per.photos;
          const favs = pool.filter((p) => p.favorite);
          const spread = [0.5, 0.15, 0.85].map((f) => pool[Math.floor(f * (pool.length - 1))]);
          const covers = [...new Set([...favs, ...spread])].slice(0, unit === 'year' ? 3 : 1);
          return (
            <button key={per.key} data-key={per.key} type="button" className={`pg-card n${covers.length}`} onClick={() => onPick(per.key)}>
              <span className="pg-card-art">
                {covers.map((c) => (
                  <Thumb key={photoKey(c)} photo={c} ticket={ticketFor(tickets, c)} />
                ))}
              </span>
              <span className="pg-card-shade" />
              <span className="pg-card-text">
                <strong>{unit === 'year' ? per.key : monthOnlyFmt.format(per.date)}</strong>
                <span>
                  {unit === 'month' && `${per.date.getFullYear()} · `}
                  {countText(t, per.photos)}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

interface Memory {
  id: string;
  title: string;
  sub: string;
  list: Photo[];
}

/**
 * Memories: "On this day" in earlier years, else the same week in earlier
 * years, plus last month's highlights (favorites) when there are any.
 */
export function Memories({ items, tickets, onOpen }: { items: Photo[]; tickets: Tickets; onOpen: (list: Photo[]) => void }) {
  const t = useT();
  const memories = useMemo(() => {
    const now = new Date();
    const out: Memory[] = [];
    const sameDay = new Map<number, Photo[]>();
    const sameWeek = new Map<number, Photo[]>();
    const dayOfYear = (d: Date) => Math.floor((Date.UTC(2001, d.getMonth(), d.getDate()) - Date.UTC(2001, 0, 1)) / 86_400_000);
    const today = dayOfYear(now);
    for (const p of items) {
      const d = new Date(p.taken);
      const y = d.getFullYear();
      if (y >= now.getFullYear() || y < 1971) continue;
      const diff = Math.abs(dayOfYear(d) - today);
      if (diff === 0) sameDay.set(y, [...(sameDay.get(y) ?? []), p]);
      else if (diff <= 3) sameWeek.set(y, [...(sameWeek.get(y) ?? []), p]);
    }
    const ago = (y: number) => (now.getFullYear() - y === 1 ? t('photos.mem.yearAgo') : t('photos.mem.yearsAgo', { n: now.getFullYear() - y }));
    const byDay = [...sameDay.entries()].sort((a, b) => b[0] - a[0]);
    for (const [y, list] of byDay) out.push({ id: `d${y}`, title: ago(y), sub: t('photos.mem.onThisDay', { year: y, items: itemsText(t, list.length) }), list });
    if (!byDay.length) {
      for (const [y, list] of [...sameWeek.entries()].sort((a, b) => b[0] - a[0])) {
        out.push({ id: `w${y}`, title: ago(y), sub: t('photos.mem.thisWeek', { year: y, items: itemsText(t, list.length) }), list });
      }
    }
    // Last month's favorites.
    const lm = new Date(now.getFullYear(), now.getMonth() - 1, 1);
    const favs = items.filter((p) => {
      const d = new Date(p.taken);
      return p.favorite && d.getFullYear() === lm.getFullYear() && d.getMonth() === lm.getMonth();
    });
    if (favs.length >= 2) out.push({ id: 'lm', title: monthOnlyFmt.format(lm), sub: t('photos.mem.bestOf', { items: itemsText(t, favs.length) }), list: favs });
    return out.slice(0, 12);
  }, [items, t]);

  if (!memories.length) return null;
  return (
    <section className="pg-memories" aria-label={t('photos.mem.title')}>
      <h3 className="pg-section">
        <Icon name="sparkles" size={15} /> {t('photos.mem.title')}
      </h3>
      <div className="pg-mem-strip">
        {memories.map((m) => {
          const pics = m.list.filter((p) => !p.video);
          const cover = pics.find((p) => p.favorite) ?? pics[0] ?? m.list[0];
          return (
            <button key={m.id} type="button" className="pg-mem" onClick={() => onOpen(m.list)}>
              <Thumb photo={cover} ticket={ticketFor(tickets, cover)} />
              <span className="pg-card-shade" />
              <span className="pg-card-text">
                <strong>{m.title}</strong>
                <span>{m.sub}</span>
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
