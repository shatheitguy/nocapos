import type { PointerEvent as RPointerEvent } from 'react';
import { create } from 'zustand';
import { iconCell, useDesktopIcons } from './desktopIcons';
import { useDock } from './dock';

// One drag-and-drop system for app icons in the dock, Launchpad and desktop.
//
// Drop targets are found by hit-testing the DOM for [data-drop]:
//   data-drop="dock"     the dock (pinned items carry data-dock-pin=<appId>)
//   data-drop="trash"    the Recycle Bin in the dock
//   data-drop="desktop"  the empty desktop and its icons

export type DragSource = 'dock' | 'launchpad' | 'desktop';

export type DropTarget = { kind: 'dock'; before: string | null } | { kind: 'trash' } | { kind: 'desktop' } | null;

export interface ActiveDrag {
  appId: string;
  source: DragSource;
  x: number;
  y: number;
}

export const useAppDrag = create<{ drag: ActiveDrag | null; over: DropTarget }>(() => ({ drag: null, over: null }));

const THRESHOLD = 6;

function hitTest(x: number, y: number): DropTarget {
  const el = document.elementFromPoint(x, y) as HTMLElement | null;
  const zone = el?.closest<HTMLElement>('[data-drop]');
  switch (zone?.dataset.drop) {
    case 'trash':
      return { kind: 'trash' };
    case 'dock':
      return { kind: 'dock', before: el?.closest<HTMLElement>('[data-dock-pin]')?.dataset.dockPin ?? null };
    case 'desktop':
      return { kind: 'desktop' };
    default:
      return null;
  }
}

/** The click that ends a drag must not also open the app. */
function swallowNextClick() {
  const stop = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };
  window.addEventListener('click', stop, { capture: true, once: true });
  window.setTimeout(() => window.removeEventListener('click', stop, { capture: true }), 0);
}

function drop(appId: string, source: DragSource, over: DropTarget, at: { x: number; y: number }) {
  const dock = useDock.getState();
  const desk = useDesktopIcons.getState();
  if (!over) return;
  switch (over.kind) {
    case 'dock':
      // Dock icons were already reordered live while hovering.
      if (!(source === 'dock' && dock.has(appId))) dock.place(appId, over.before);
      break;
    case 'trash':
      if (source === 'desktop') desk.remove(appId);
      else if (source === 'dock') dock.remove(appId);
      break;
    case 'desktop':
      desk.place(appId, at);
      break;
  }
}

/**
 * Call from an app icon's onPointerDown. Nothing happens until the pointer
 * moves past a small threshold, so a plain click still opens the app.
 */
export function pressApp(e: RPointerEvent<HTMLElement>, appId: string, source: DragSource, opts: { onStart?: () => void } = {}) {
  if (e.button !== 0 || !e.isPrimary) return;
  const startX = e.clientX;
  const startY = e.clientY;
  // Where inside the icon it was grabbed — desktop icons keep that offset.
  const r = e.currentTarget.getBoundingClientRect();
  const grab = source === 'desktop' ? { x: startX - r.left, y: startY - r.top } : { x: iconCell().w / 2, y: iconCell().icon / 2 + 8 };
  let started = false;

  const onMove = (ev: PointerEvent) => {
    if (!started) {
      if (Math.hypot(ev.clientX - startX, ev.clientY - startY) < THRESHOLD) return;
      started = true;
      document.body.classList.add('app-dragging');
      opts.onStart?.();
    }
    ev.preventDefault();
    const over = hitTest(ev.clientX, ev.clientY);
    const dock = useDock.getState();
    if (source === 'dock' && over?.kind === 'dock' && over.before && over.before !== appId && dock.has(appId)) {
      dock.moveTo(appId, over.before);
    }
    useAppDrag.setState({ drag: { appId, source, x: ev.clientX, y: ev.clientY }, over });
  };

  const finish = (ev: PointerEvent, commit: boolean) => {
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', onCancel);
    if (!started) return;
    document.body.classList.remove('app-dragging');
    useAppDrag.setState({ drag: null, over: null });
    swallowNextClick();
    if (commit) drop(appId, source, hitTest(ev.clientX, ev.clientY), { x: ev.clientX - grab.x, y: ev.clientY - grab.y });
  };
  const onUp = (ev: PointerEvent) => finish(ev, true);
  const onCancel = (ev: PointerEvent) => finish(ev, false);

  window.addEventListener('pointermove', onMove, { passive: false });
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onCancel);
}
