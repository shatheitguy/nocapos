import { useEffect, useRef, useState, type RefObject } from 'react';
import { subscribe } from '../api/socket';

/** Subscribes to a WebSocket topic for the component's lifetime (null = off). */
export function useTopic<T>(topic: string | null, onData: (d: T) => void, onEnd?: (err?: string) => void) {
  const dataRef = useRef(onData);
  const endRef = useRef(onEnd);
  useEffect(() => {
    dataRef.current = onData;
    endRef.current = onEnd;
  });
  useEffect(() => {
    if (!topic) return;
    return subscribe(
      topic,
      (d) => dataRef.current(d as T),
      (e) => endRef.current?.(e),
    );
  }, [topic]);
}

export function useViewport() {
  const [vp, setVp] = useState({ w: window.innerWidth, h: window.innerHeight });
  useEffect(() => {
    const on = () => setVp({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener('resize', on);
    return () => window.removeEventListener('resize', on);
  }, []);
  return vp;
}

export function useClock(intervalMs = 1000) {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), intervalMs);
    return () => window.clearInterval(t);
  }, [intervalMs]);
  return now;
}

/** Closes a popover on outside click / Escape. */
export function useDismiss(open: boolean, onClose: () => void, ...refs: RefObject<HTMLElement | null>[]) {
  const closeRef = useRef(onClose);
  useEffect(() => {
    closeRef.current = onClose;
  });
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (refs.some((r) => r.current?.contains(e.target as Node))) return;
      closeRef.current();
    };
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && closeRef.current();
    window.addEventListener('pointerdown', onDown);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown);
      window.removeEventListener('keydown', onKey);
    };
    // refs are stable objects, so only `open` matters
  }, [open]);
}
