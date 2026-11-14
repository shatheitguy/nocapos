// Icon order for the roster (the app launcher) and its folders: plain list
// helpers with no React or storage, so they can be unit tested.
//
// An order is a list of keys: app ids, "folder:<id>", "store:<id>", "native:<id>".
// `before` names the key a moved icon lands in front of; null means the end.

/** The saved order applied to what is there now: gone keys dropped, new keys at the end. */
export function arrange(saved: readonly string[], present: readonly string[]): string[] {
  const here = new Set(present);
  const out: string[] = [];
  const seen = new Set<string>();
  for (const k of saved) {
    if (here.has(k) && !seen.has(k)) {
      out.push(k);
      seen.add(k);
    }
  }
  for (const k of present) {
    if (!seen.has(k)) {
      out.push(k);
      seen.add(k);
    }
  }
  return out;
}

/** The list with key taken out and put in front of `before` (or at the end). Adds key if it was missing. */
export function moveBefore(list: readonly string[], key: string, before: string | null): string[] {
  const out = list.filter((k) => k !== key);
  const at = before == null || before === key ? -1 : out.indexOf(before);
  if (at < 0) out.push(key);
  else out.splice(at, 0, key);
  return out;
}

/** The list with one key swapped for others, in its place (a folder for its apps, an app for a new folder). */
export function replaceKey(list: readonly string[], key: string, with_: readonly string[]): string[] {
  const at = list.indexOf(key);
  const rest = list.filter((k) => k !== key && !with_.includes(k));
  if (at < 0) return [...rest, ...with_];
  // Where `key` sat, counted among the keys that stay.
  const pos = list.slice(0, at).filter((k) => !with_.includes(k)).length;
  rest.splice(pos, 0, ...with_);
  return rest;
}

/**
 * Where a drop at a grid cell would put `dragged`, given what is on screen.
 * `index` is the cell under the pointer in `shown` (which already has the
 * dragged icon at its preview spot); `after` is true on the right half of it.
 * Returns the key to land before, null for the end, or undefined for "no change"
 * (the pointer is over the dragged icon's own spot).
 */
export function slotAt(shown: readonly string[], dragged: string, index: number, after: boolean): string | null | undefined {
  if (index < 0) return undefined;
  if (index >= shown.length) return null;
  const k = shown[index];
  if (k === dragged) return undefined;
  if (!after) return k;
  for (let i = index + 1; i < shown.length; i++) if (shown[i] !== dragged) return shown[i];
  return null;
}

/** What is on screen while dragging: `base` with the dragged key moved to its slot (if any). */
export function preview(base: readonly string[], dragged: string, before: string | null | undefined): string[] {
  return before === undefined ? [...base] : moveBefore(base, dragged, before);
}

/** The order to save: what is shown, plus saved keys not loaded yet (kept so they keep their place later). */
export function toSave(saved: readonly string[], shown: readonly string[], stillLoading: (k: string) => boolean): string[] {
  const out = [...shown];
  const have = new Set(shown);
  saved.forEach((k, i) => {
    if (have.has(k) || !stillLoading(k)) return;
    // Put it back after the key it followed before, if that is still here.
    let j = i - 1;
    while (j >= 0 && !have.has(saved[j])) j--;
    const at = j < 0 ? 0 : out.indexOf(saved[j]) + 1;
    out.splice(at, 0, k);
    have.add(k);
  });
  return out;
}

/** The grid cell under a point: index across all pages, and whether it is the cell's right half. */
export function cellAt(
  x: number,
  y: number,
  box: { left: number; top: number; width: number; height: number },
  cols: number,
  rows: number,
  page: number,
): { index: number; after: boolean } | null {
  const cw = box.width / cols;
  const ch = box.height / rows;
  const rx = x - box.left;
  const ry = y - box.top;
  if (rx < 0 || ry < 0 || rx >= box.width || ry >= box.height || cw <= 0 || ch <= 0) return null;
  const col = Math.min(cols - 1, Math.floor(rx / cw));
  const row = Math.min(rows - 1, Math.floor(ry / ch));
  return { index: page * cols * rows + row * cols + col, after: rx - col * cw > cw / 2 };
}
