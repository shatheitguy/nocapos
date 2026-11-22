// The photo editor's maths: colour adjustments as one colour matrix (the
// same one drives the live preview and the saved file), crop shapes,
// straightening, auto-enhance and the undo history. Pure (no DOM), so it is
// tested on its own (scripts/photoEdit.test.ts).

/** -100..100 each; 0 leaves the picture as it is. */
export interface Adjust {
  brightness: number;
  contrast: number;
  saturation: number;
  warmth: number;
}

export type FilterId = 'none' | 'vivid' | 'ember' | 'frost' | 'fade' | 'noir' | 'mono';

export type Aspect = 'free' | '1:1' | '4:3' | '16:9';

/** A crop in the picture's own (turned) frame, as fractions 0..1. */
export interface Crop {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Black and white points found by auto-enhance (0..255). */
export interface Levels {
  lo: number;
  hi: number;
}

export interface Edit {
  adjust: Adjust;
  filter: FilterId;
  levels: Levels | null;
  /** Quarter turns clockwise. */
  turns: number;
  /** Degrees, -45..45. */
  straighten: number;
  aspect: Aspect;
  crop: Crop;
}

export const NO_ADJUST: Adjust = { brightness: 0, contrast: 0, saturation: 0, warmth: 0 };
export const FULL: Crop = { x: 0, y: 0, w: 1, h: 1 };
export const NEW_EDIT: Edit = { adjust: NO_ADJUST, filter: 'none', levels: null, turns: 0, straighten: 0, aspect: 'free', crop: FULL };

/** Whether an edit changes anything. */
export function isUnchanged(e: Edit): boolean {
  const a = e.adjust;
  return (
    !a.brightness && !a.contrast && !a.saturation && !a.warmth && e.filter === 'none' && !e.levels &&
    ((e.turns % 4) + 4) % 4 === 0 && !e.straighten && e.crop.x === 0 && e.crop.y === 0 && e.crop.w === 1 && e.crop.h === 1
  );
}

// ---------- colour ----------

/** A 4×5 colour matrix, row by row (r, g, b, a), the fifth column an offset in 0..1, as SVG feColorMatrix takes it. */
export type Matrix = number[];

export const IDENTITY: Matrix = [1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0];

/** b after a: (b · a). */
export function compose(a: Matrix, b: Matrix): Matrix {
  const out = new Array<number>(20).fill(0);
  for (let r = 0; r < 4; r++) {
    for (let c = 0; c < 5; c++) {
      let v = c === 4 ? b[r * 5 + 4] : 0;
      for (let k = 0; k < 4; k++) v += b[r * 5 + k] * a[k * 5 + c];
      out[r * 5 + c] = v;
    }
  }
  return out;
}

const LUMA = [0.2126, 0.7152, 0.0722];

function saturationMatrix(s: number): Matrix {
  const [lr, lg, lb] = LUMA.map((l) => l * (1 - s));
  return [lr + s, lg, lb, 0, 0, lr, lg + s, lb, 0, 0, lr, lg, lb + s, 0, 0, 0, 0, 0, 1, 0];
}

const scaleOffset = (k: number, o: number): Matrix => [k, 0, 0, 0, o, 0, k, 0, 0, o, 0, 0, k, 0, o, 0, 0, 0, 1, 0];

/** Preset looks, as matrices of their own. */
export const FILTERS: Record<FilterId, Matrix> = {
  none: IDENTITY,
  vivid: compose(saturationMatrix(1.35), scaleOffset(1.08, -0.04)),
  // NoCap red: warm, deep shadows.
  ember: [1.12, 0.04, 0, 0, 0.02, 0.02, 0.96, 0, 0, -0.01, 0, 0, 0.86, 0, -0.02, 0, 0, 0, 1, 0],
  frost: [0.92, 0, 0.04, 0, 0, 0, 1, 0.04, 0, 0.01, 0.04, 0.06, 1.08, 0, 0.03, 0, 0, 0, 1, 0],
  fade: compose(saturationMatrix(0.7), scaleOffset(0.82, 0.1)),
  noir: compose(saturationMatrix(0), scaleOffset(1.25, -0.12)),
  mono: saturationMatrix(0),
};

export const FILTER_IDS = Object.keys(FILTERS) as FilterId[];

/** The matrix for an edit's colours: levels, then the sliders, then the filter. */
export function colorMatrix(e: Pick<Edit, 'adjust' | 'filter' | 'levels'>): Matrix {
  let m = IDENTITY;
  if (e.levels && e.levels.hi > e.levels.lo) {
    const k = 255 / (e.levels.hi - e.levels.lo);
    m = compose(m, scaleOffset(k, -e.levels.lo / (e.levels.hi - e.levels.lo)));
  }
  const a = e.adjust;
  if (a.brightness) m = compose(m, scaleOffset(1, (a.brightness / 100) * 0.35));
  if (a.contrast) {
    const k = a.contrast > 0 ? 1 + (a.contrast / 100) * 1.2 : 1 + a.contrast / 125;
    m = compose(m, scaleOffset(k, 0.5 * (1 - k)));
  }
  if (a.saturation) m = compose(m, saturationMatrix(1 + a.saturation / 100));
  if (a.warmth) {
    const w = (a.warmth / 100) * 0.12;
    m = compose(m, [1 + w, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1 - w, 0, 0, 0, 0, 0, 1, 0]);
  }
  if (e.filter !== 'none') m = compose(m, FILTERS[e.filter]);
  return m;
}

/** Applies a colour matrix to RGBA pixels, in place. */
export function applyMatrix(px: Uint8ClampedArray | number[], m: Matrix): void {
  const o0 = m[4] * 255;
  const o1 = m[9] * 255;
  const o2 = m[14] * 255;
  for (let i = 0; i < px.length; i += 4) {
    const r = px[i];
    const g = px[i + 1];
    const b = px[i + 2];
    px[i] = m[0] * r + m[1] * g + m[2] * b + o0;
    px[i + 1] = m[5] * r + m[6] * g + m[7] * b + o1;
    px[i + 2] = m[10] * r + m[11] * g + m[12] * b + o2;
    if (!(px instanceof Uint8ClampedArray)) for (let k = 0; k < 3; k++) px[i + k] = Math.max(0, Math.min(255, Math.round(px[i + k])));
  }
}

/** Auto-enhance: stretch the darkest and lightest 0.5 % to black and white, from a luminance histogram (256 bins). */
export function autoLevels(hist: ArrayLike<number>): Levels | null {
  let total = 0;
  for (let i = 0; i < 256; i++) total += hist[i];
  if (!total) return null;
  const cut = total * 0.005;
  let lo = 0;
  for (let acc = 0; lo < 255 && acc + hist[lo] <= cut; lo++) acc += hist[lo];
  let hi = 255;
  for (let acc = 0; hi > 0 && acc + hist[hi] <= cut; hi--) acc += hist[hi];
  // Already spans the range (or nearly flat): nothing worth stretching.
  if (hi - lo < 16 || (lo <= 4 && hi >= 251)) return null;
  return { lo, hi };
}

/** The luminance histogram of RGBA pixels (every `step`th pixel). */
export function histogram(px: ArrayLike<number>, step = 1): Uint32Array {
  const h = new Uint32Array(256);
  for (let i = 0; i < px.length; i += 4 * step) h[Math.round(LUMA[0] * px[i] + LUMA[1] * px[i + 1] + LUMA[2] * px[i + 2])]++;
  return h;
}

// ---------- shape ----------

const RATIO: Record<Exclude<Aspect, 'free'>, number> = { '1:1': 1, '4:3': 4 / 3, '16:9': 16 / 9 };

/** The width ÷ height an aspect asks for, turned to match the picture (a tall picture gets 3:4). */
export function aspectRatio(aspect: Aspect, w: number, h: number): number | null {
  if (aspect === 'free') return null;
  const r = RATIO[aspect];
  return h > w ? 1 / r : r;
}

/** The largest centred crop with that ratio. */
export function aspectCrop(aspect: Aspect, w: number, h: number): Crop {
  const r = aspectRatio(aspect, w, h);
  if (!r) return FULL;
  const cw = Math.min(1, (h * r) / w);
  const ch = Math.min(1, w / r / h);
  return { x: (1 - cw) / 2, y: (1 - ch) / 2, w: cw, h: ch };
}

/** Keeps a crop inside the picture, at least `min` big, and in its ratio (if any) by trimming its height. */
export function clampCrop(c: Crop, aspect: Aspect, w: number, h: number, min = 0.05): Crop {
  let cw = Math.max(min, Math.min(1, c.w));
  let ch = Math.max(min, Math.min(1, c.h));
  const r = aspectRatio(aspect, w, h);
  if (r) {
    ch = (cw * w) / r / h;
    if (ch > 1) {
      ch = 1;
      cw = (h * r) / w;
    }
  }
  const x = Math.max(0, Math.min(1 - cw, c.x));
  const y = Math.max(0, Math.min(1 - ch, c.y));
  return { x, y, w: cw, h: ch };
}

/** How much a w×h picture turned by `deg` must grow so it still fills its own frame (no empty corners). */
export function straightenScale(w: number, h: number, deg: number): number {
  const t = (Math.abs(deg) * Math.PI) / 180;
  const c = Math.cos(t);
  const s = Math.sin(t);
  return Math.max((w * c + h * s) / w, (w * s + h * c) / h);
}

/** Width and height after quarter turns. */
export function turnedSize(w: number, h: number, turns: number): { w: number; h: number } {
  return ((turns % 4) + 4) % 2 ? { w: h, h: w } : { w, h };
}

/** The saved picture's size in pixels. */
export function outputSize(w: number, h: number, e: Pick<Edit, 'turns' | 'crop'>): { w: number; h: number } {
  const t = turnedSize(w, h, e.turns);
  return { w: Math.max(1, Math.round(t.w * e.crop.w)), h: Math.max(1, Math.round(t.h * e.crop.h)) };
}

// ---------- history ----------

export interface History<T> {
  past: T[];
  present: T;
  future: T[];
}

export const startHistory = <T>(present: T): History<T> => ({ past: [], present, future: [] });

/** A new step (dropping anything that was undone); at most `limit` steps back. */
export function record<T>(h: History<T>, next: T, limit = 50): History<T> {
  if (next === h.present) return h;
  return { past: [...h.past, h.present].slice(-limit), present: next, future: [] };
}

export function undo<T>(h: History<T>): History<T> {
  if (!h.past.length) return h;
  return { past: h.past.slice(0, -1), present: h.past[h.past.length - 1], future: [h.present, ...h.future] };
}

export function redo<T>(h: History<T>): History<T> {
  if (!h.future.length) return h;
  return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1) };
}

// ---------- saving ----------

/** "beach.JPG" → "beach (edited).JPG"; "beach (edited).jpg" stays as it is (the server numbers copies). */
export function editedName(name: string): string {
  const dot = name.lastIndexOf('.');
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : '.jpg';
  const fixedExt = /^\.(jpe?g|png|webp)$/i.test(ext) ? ext : '.jpg';
  return /\(edited\)$/.test(stem) ? `${stem}${fixedExt}` : `${stem} (edited)${fixedExt}`;
}

/** The type to save a picture as (what it was, when the browser can write that). */
export function saveType(name: string): 'image/jpeg' | 'image/png' | 'image/webp' {
  const n = name.toLowerCase();
  if (n.endsWith('.png')) return 'image/png';
  if (n.endsWith('.webp')) return 'image/webp';
  return 'image/jpeg';
}

/** Whether replacing the original keeps its name (else the edit is saved under a .jpg name). */
export const keepsType = (name: string) => /\.(jpe?g|png|webp)$/i.test(name);
