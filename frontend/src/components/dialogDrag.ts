import type { PointerEvent as ReactPointerEvent } from 'react';

// Dialogs and sheets move by their title row, like windows do by their title
// bar, but only inside the window (or page) they belong to. The offset is
// kept in the CSS `translate` property, so it adds to any transform the
// dialog's own open animation uses.

export interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** How far a dialog at `home` (its place with no offset) may move by (dx, dy) and still be inside `area`. */
export function clampOffset(home: Box, area: Box, dx: number, dy: number): { x: number; y: number } {
  const clamp = (v: number, lo: number, hi: number) => (lo > hi ? (lo + hi) / 2 : Math.min(hi, Math.max(lo, v)));
  return {
    x: Math.round(clamp(dx, area.left - home.left, area.right - home.right)),
    y: Math.round(clamp(dy, area.top - home.top, area.bottom - home.bottom)),
  };
}

const MOVABLE = '.dialog, .st-sheet, .ug-sheet';
const NOT_A_HANDLE = 'button, a, input, select, textarea, [contenteditable], [role="button"], [role="menu"]';

function offsetOf(el: HTMLElement): { x: number; y: number } {
  const [x = '0', y = '0'] = (el.style.translate || '').split(/\s+/);
  return { x: parseFloat(x) || 0, y: parseFloat(y) || 0 };
}

/** pointerdown on a dialog's title row: drag the dialog it heads. */
export function startDialogDrag(e: ReactPointerEvent<HTMLElement>) {
  if (e.button !== 0 || !e.isPrimary) return;
  // On phones dialogs are bottom sheets: they don't move.
  if (document.documentElement.hasAttribute('data-compact')) return;
  const target = e.target as Element;
  if (target.closest(NOT_A_HANDLE)) return;
  const dialog = e.currentTarget.closest(MOVABLE) as HTMLElement | null;
  const area = dialog?.parentElement;
  if (!dialog || !area) return;
  e.preventDefault(); // no text selection, no drag-and-drop of the title
  const start = offsetOf(dialog);
  const now = dialog.getBoundingClientRect();
  const home: Box = { left: now.left - start.x, top: now.top - start.y, right: now.right - start.x, bottom: now.bottom - start.y };
  const a = area.getBoundingClientRect();
  const bounds: Box = { left: a.left, top: a.top, right: a.right, bottom: a.bottom };
  const x0 = e.clientX;
  const y0 = e.clientY;
  const head = e.currentTarget;
  const id = e.pointerId;
  try {
    head.setPointerCapture(id);
  } catch {
    /* the pointer is gone already */
  }
  dialog.classList.add('dragging');
  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== id) return;
    const o = clampOffset(home, bounds, start.x + ev.clientX - x0, start.y + ev.clientY - y0);
    dialog.style.translate = `${o.x}px ${o.y}px`;
  };
  const end = (ev: PointerEvent) => {
    if (ev.pointerId !== id) return;
    dialog.classList.remove('dragging');
    head.removeEventListener('pointermove', move);
    head.removeEventListener('pointerup', end);
    head.removeEventListener('pointercancel', end);
  };
  head.addEventListener('pointermove', move);
  head.addEventListener('pointerup', end);
  head.addEventListener('pointercancel', end);
}
