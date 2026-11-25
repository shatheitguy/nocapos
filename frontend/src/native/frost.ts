// Frosted glass on NoCap Desktop. Each app window, the dock and the panels
// are separate windows on the screen, and nothing can blur what another
// window shows: see-through glass let the text of the windows behind read
// through, sharp. So on the screen the glass is opaque, and what it is
// frosted with is the wallpaper itself: a tiny, colour-rich copy of it that
// the window stretches over its whole area (a 32-pixel picture scaled up is
// as soft as a heavy blur, and costs nothing to draw, even without a GPU).
// The window's tint lies over it at the Transparency the owner picked.
import { shownWallpaper, usePrefs, wallpaperSrc, type PrefValues } from '../state/prefs';

const W = 32;
const H = 18;
const made = new Map<string, string>();
let want = '';

/** Raises the saturation of RGBA pixels in place, like the glass's saturate(). */
export function saturate(px: Uint8ClampedArray, amount: number) {
  for (let i = 0; i < px.length; i += 4) {
    const r = px[i];
    const g = px[i + 1];
    const b = px[i + 2];
    const grey = 0.2126 * r + 0.7152 * g + 0.0722 * b;
    px[i] = grey + (r - grey) * amount;
    px[i + 1] = grey + (g - grey) * amount;
    px[i + 2] = grey + (b - grey) * amount;
  }
}

async function frostOf(src: string): Promise<string> {
  const img = new Image();
  img.decoding = 'async';
  img.src = src;
  await img.decode();
  const c = document.createElement('canvas');
  c.width = W;
  c.height = H;
  const ctx = c.getContext('2d', { willReadFrequently: true });
  if (!ctx) return '';
  // Cover, like the wallpaper: crop the picture to the screen's shape.
  const s = Math.max(W / img.naturalWidth, H / img.naturalHeight);
  const w = W / s;
  const h = H / s;
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(img, (img.naturalWidth - w) / 2, (img.naturalHeight - h) / 2, w, h, 0, 0, W, H);
  const data = ctx.getImageData(0, 0, W, H);
  saturate(data.data, 1.6);
  ctx.putImageData(data, 0, 0);
  return c.toDataURL('image/png');
}

function apply(p: PrefValues) {
  const id = shownWallpaper(p.wallpaper);
  const src = wallpaperSrc(id);
  const root = document.documentElement;
  const key = `${id}:${src.length}`;
  want = key;
  const done = made.get(key);
  if (done !== undefined) {
    if (done) root.style.setProperty('--frost', `url("${done}")`);
    else root.style.removeProperty('--frost');
    return;
  }
  if (!src) return;
  frostOf(src)
    .catch(() => '')
    .then((url) => {
      made.set(key, url);
      if (want === key) apply(usePrefs.getState());
    });
}

/** Keeps --frost (the frosted wallpaper behind native glass) in step with the wallpaper. */
export function startFrost() {
  apply(usePrefs.getState());
  usePrefs.subscribe((p, prev) => {
    if (p.wallpaper !== prev.wallpaper) apply(p);
  });
}
