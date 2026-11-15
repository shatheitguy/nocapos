// Notification banners (top right, 380 px wide): up to three, newest on top.
// They hide after 5 s (urgent ones stay until dismissed; a pointer over the
// stack holds them). The surface's height follows the stack: after every
// change the page tells the host with surface.size (0 = nothing to show).
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { send } from '../bridge';
import { useEvent, useShellInfo } from '../hooks';
import { pushBanner } from '../logic';
import { Note } from '../Note';
import type { NotifyItem } from '../protocol';

const SHOW_MS = 5000;
const GAP = 8; // the stack's margin inside the surface

export function Banners() {
  useShellInfo();
  const [items, setItems] = useState<NotifyItem[]>([]);
  const [held, setHeld] = useState(false);
  const stack = useRef<HTMLDivElement>(null);
  const lastHeight = useRef(-1);
  const timers = useRef(new Map<NotifyItem['id'], number>());

  const drop = (id: NotifyItem['id']) => setItems((list) => list.filter((n) => n.id !== id));

  useEvent('notify.added', (p) => {
    const n = p as NotifyItem;
    if (!n || n.id == null) return;
    setItems((list) => pushBanner(list, n, 3));
  });
  useEvent('notify.closed', (p) => drop((p as { id: NotifyItem['id'] })?.id));

  // One timer per banner; none while the pointer is over the stack.
  useEffect(() => {
    const map = timers.current;
    for (const [id, timer] of map) {
      if (held || !items.some((n) => n.id === id)) {
        window.clearTimeout(timer);
        map.delete(id);
      }
    }
    if (held) return;
    for (const n of items) {
      if (n.urgency === 2 || map.has(n.id)) continue;
      map.set(
        n.id,
        window.setTimeout(() => {
          map.delete(n.id);
          drop(n.id);
        }, SHOW_MS),
      );
    }
  }, [items, held]);
  useEffect(() => () => timers.current.forEach((t) => window.clearTimeout(t)), []);
  // An empty stack hides the surface, so no pointerleave may follow.
  useEffect(() => {
    if (!items.length) setHeld(false);
  }, [items.length]);

  // Tell the host how tall the stack is, after every layout change.
  useLayoutEffect(() => {
    const el = stack.current;
    if (!el) return;
    const report = () => {
      const h = items.length ? Math.min(2000, Math.ceil(el.getBoundingClientRect().height) + GAP * 2) : 0;
      if (h === lastHeight.current) return;
      lastHeight.current = h;
      send('surface.size', { height: h });
    };
    report();
    const ro = new ResizeObserver(report);
    ro.observe(el);
    return () => ro.disconnect();
  }, [items]);

  const open = (n: NotifyItem) => {
    drop(n.id);
    if (n.actions.some((a) => a.key === 'default')) send('notify.invoke', { id: n.id, key: 'default' });
    else send('surface.show', { name: 'center' });
  };

  return (
    <div
      ref={stack}
      className="nsh-banners"
      aria-live="polite"
      onPointerEnter={() => setHeld(true)}
      onPointerLeave={() => setHeld(false)}
    >
      {items.map((n) => (
        <Note
          key={n.id}
          item={n}
          now={Date.now()}
          banner
          onOpen={() => open(n)}
          onDismiss={() => drop(n.id)}
          onAction={(key) => {
            drop(n.id);
            send('notify.invoke', { id: n.id, key });
          }}
        />
      ))}
    </div>
  );
}
