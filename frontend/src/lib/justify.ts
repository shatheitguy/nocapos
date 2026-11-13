// Justified rows for the photo timeline: every row fills the width exactly and
// keeps each picture's shape, with rows as close to a target height as they
// can be. Pure (no DOM), so it is tested on its own.

export interface JustifiedRow {
  /** First item (index into the ratios). */
  start: number;
  /** One past the last item. */
  end: number;
  height: number;
  /** Left edge of each item in the row, then its width. */
  lefts: number[];
  widths: number[];
}

/** Very wide or very tall pictures are cropped to this range of shapes. */
export const MIN_RATIO = 0.5;
export const MAX_RATIO = 2.6;

export const clampRatio = (r: number) => (Number.isFinite(r) && r > 0 ? Math.min(MAX_RATIO, Math.max(MIN_RATIO, r)) : 1);

/**
 * Lays out items with the given width/height ratios in rows of `width`
 * pixels separated by `gap`. A row ends where adding the next item would
 * take it further from `target` than stopping. The last row is not stretched
 * taller than the target.
 */
export function justify(ratios: number[], width: number, target: number, gap: number): JustifiedRow[] {
  const rows: JustifiedRow[] = [];
  if (width <= 0 || target <= 0 || !ratios.length) return rows;
  const heightOf = (sum: number, n: number) => (width - gap * (n - 1)) / sum;

  let start = 0;
  let sum = 0;
  for (let i = 0; i < ratios.length; i++) {
    const r = clampRatio(ratios[i]);
    const n = i - start + 1;
    const withIt = heightOf(sum + r, n);
    if (withIt <= target) {
      const without = n > 1 ? heightOf(sum, n - 1) : Infinity;
      // Closer to the target without this item (and not absurdly tall): break before it.
      if (n > 1 && Math.abs(without - target) < Math.abs(withIt - target) && without <= target * 1.5) {
        rows.push(place(ratios, start, i, without, gap));
        start = i;
        sum = r;
        continue;
      }
      rows.push(place(ratios, start, i + 1, withIt, gap));
      start = i + 1;
      sum = 0;
      continue;
    }
    sum += r;
  }
  if (start < ratios.length) {
    const n = ratios.length - start;
    rows.push(place(ratios, start, ratios.length, Math.min(target, heightOf(sum, n)), gap));
  }
  return rows;
}

function place(ratios: number[], start: number, end: number, height: number, gap: number): JustifiedRow {
  const lefts: number[] = [];
  const widths: number[] = [];
  let x = 0;
  for (let i = start; i < end; i++) {
    const w = clampRatio(ratios[i]) * height;
    lefts.push(x);
    widths.push(w);
    x += w + gap;
  }
  return { start, end, height, lefts, widths };
}
