import { useEffect, useRef, useState, type ReactNode } from 'react';
import { visibleRange } from '../lib/containers';

// A scrolling list that only draws the rows on screen (plus a few), so a
// server with hundreds of containers, images or volumes stays fast. Every
// row has the same height. Short lists are drawn whole.

export function VirtualList({ count, rowHeight, render, className = '', label, threshold = 80 }: {
  count: number;
  rowHeight: number;
  render: (i: number) => ReactNode;
  className?: string;
  label?: string;
  /** Up to this many rows, everything is drawn. */
  threshold?: number;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [view, setView] = useState({ top: 0, height: 600 });

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => setView({ top: el.scrollTop, height: el.clientHeight || 600 });
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const virtual = count > threshold;
  const [first, last] = virtual ? visibleRange(count, rowHeight, view.top, view.height) : [0, count];
  const rows: ReactNode[] = [];
  for (let i = first; i < last; i++) rows.push(render(i));

  return (
    <div
      ref={box}
      className={`vlist ${className}`}
      role="list"
      aria-label={label}
      onScroll={virtual ? (e) => setView({ top: e.currentTarget.scrollTop, height: e.currentTarget.clientHeight }) : undefined}
    >
      {virtual ? (
        <div style={{ height: count * rowHeight, position: 'relative' }}>
          <div style={{ position: 'absolute', top: first * rowHeight, left: 0, right: 0 }}>{rows}</div>
        </div>
      ) : (
        rows
      )}
    </div>
  );
}
