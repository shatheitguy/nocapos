import { useLayoutEffect, useRef, type RefObject } from 'react';

const reducedMotion = () =>
  document.documentElement.hasAttribute('data-reduce-motion') || !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;

/**
 * Icons that change place glide there instead of jumping: after each render,
 * every [data-key] inside `root` that moved (relative to `root`) animates from
 * where it was. Only while `on`; nothing moves with reduced motion.
 */
export function useFlip(root: RefObject<HTMLElement | null>, on: boolean) {
  const last = useRef(new Map<string, { x: number; y: number }>());
  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    const base = el.getBoundingClientRect();
    const now = new Map<string, { x: number; y: number }>();
    const animate = on && !reducedMotion();
    for (const item of el.querySelectorAll<HTMLElement>('[data-key]')) {
      const key = item.dataset.key!;
      item.getAnimations?.().forEach((a) => a.id === 'flip' && a.cancel());
      const r = item.getBoundingClientRect();
      const at = { x: r.left - base.left, y: r.top - base.top };
      now.set(key, at);
      const was = last.current.get(key);
      if (!animate || !was || !item.animate) continue;
      const dx = was.x - at.x;
      const dy = was.y - at.y;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
      const a = item.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: 'none' }], {
        duration: 220,
        easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)',
      });
      a.id = 'flip';
    }
    last.current = now;
  });
}
