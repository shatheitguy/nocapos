import { createContext, useContext, useEffect, useRef, useState, useSyncExternalStore, type RefObject } from 'react';
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

// ---- pausing background work ----

/** False inside a minimized window. Provided by each app window. */
export const WindowShownContext = createContext(true);

function onVisibility(cb: () => void) {
  document.addEventListener('visibilitychange', cb);
  return () => document.removeEventListener('visibilitychange', cb);
}

/** True while the browser tab is visible. */
export function useDocumentVisible() {
  return useSyncExternalStore(onVisibility, () => !document.hidden);
}

/** True while the tab is visible and the hosting window (if any) isn't minimized. */
export function usePageActive() {
  const shown = useContext(WindowShownContext);
  return useDocumentVisible() && shown;
}

/**
 * Calls `fn` every `ms` while the page is active (see usePageActive). A tick
 * is skipped while the previous call is still running, and after a pause the
 * next call happens right away so the data catches up. The first load is the
 * caller's job.
 */
export function usePoll(fn: () => unknown, ms: number, enabled = true) {
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  });
  const busy = useRef(false);
  const paused = useRef(false);
  const active = usePageActive() && enabled;
  useEffect(() => {
    if (!active) {
      paused.current = true;
      return;
    }
    const tick = async () => {
      if (busy.current) return;
      busy.current = true;
      try {
        await fnRef.current();
      } catch {
        /* the next tick tries again */
      } finally {
        busy.current = false;
      }
    };
    if (paused.current) {
      paused.current = false;
      void tick();
    }
    const t = window.setInterval(() => void tick(), ms);
    return () => window.clearInterval(t);
  }, [active, ms]);
}
