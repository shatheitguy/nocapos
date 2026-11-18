import { create } from 'zustand';
import { paletteFromPixels } from '../lib/wallAccent';

// The three main colors of each wallpaper, sampled from the picture once and
// remembered (in memory and in this browser's storage), so the accent can
// match whatever wallpaper someone picks, their own pictures included.

const KEY = 'alfa.wallpalette.';

interface PaletteStore {
  byId: Record<string, string[]>;
}

export const useWallPalette = create<PaletteStore>(() => ({ byId: {} }));

const pending = new Set<string>();

function readCached(id: string): string[] | undefined {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY + id) ?? 'null') as unknown;
    if (Array.isArray(raw) && raw.every((c) => typeof c === 'string' && /^#[0-9a-f]{6}$/i.test(c))) return raw as string[];
  } catch {
    /* unreadable or unavailable */
  }
  return undefined;
}

/** The wallpaper's colors if already known (sampling starts with ensurePalette). */
export function paletteFor(id: string): string[] | undefined {
  const known = useWallPalette.getState().byId[id];
  if (known) return known;
  const cached = readCached(id);
  if (cached) useWallPalette.setState((s) => ({ byId: { ...s.byId, [id]: cached } }));
  return cached;
}

/** Samples a picture into a 64×64 canvas and returns its palette. */
async function sample(src: string): Promise<string[]> {
  const img = new Image();
  img.decoding = 'async';
  img.src = src;
  await img.decode();
  const c = document.createElement('canvas');
  c.width = c.height = 64;
  const ctx = c.getContext('2d', { willReadFrequently: true });
  if (!ctx) return [];
  ctx.drawImage(img, 0, 0, 64, 64);
  return paletteFromPixels(ctx.getImageData(0, 0, 64, 64).data, 3);
}

/**
 * Makes sure the colors of wallpaper `id` (picture at `src`) are known;
 * calls `done` once when they've just been worked out.
 */
export function ensurePalette(id: string, src: string, done?: () => void) {
  if (paletteFor(id) || pending.has(id) || typeof document === 'undefined') return;
  pending.add(id);
  sample(src)
    .then((colors) => {
      try {
        localStorage.setItem(KEY + id, JSON.stringify(colors));
      } catch {
        /* storage full or blocked: keep it in memory only */
      }
      useWallPalette.setState((s) => ({ byId: { ...s.byId, [id]: colors } }));
      done?.();
    })
    .catch(() => {
      /* picture unreadable: the theme color stays */
    })
    .finally(() => pending.delete(id));
}

/** Forget a deleted wallpaper's colors. */
export function forgetPalette(id: string) {
  try {
    localStorage.removeItem(KEY + id);
  } catch {
    /* storage unavailable */
  }
  useWallPalette.setState((s) => {
    const byId = { ...s.byId };
    delete byId[id];
    return { byId };
  });
}

/**
 * The three accent choices for a wallpaper: a built-in wallpaper's own
 * accent first, then the picture's colors, without near-duplicates.
 */
export function wallChoices(builtInAccent: string | undefined, colors: string[] | undefined): string[] {
  const out: string[] = [];
  for (const c of [builtInAccent, ...(colors ?? [])]) {
    if (c && !out.some((o) => hueGap(o, c) < 30)) out.push(c);
  }
  return out.slice(0, 3);
}

function hueOf(hex: string): number {
  const r = parseInt(hex.slice(1, 3), 16) / 255, g = parseInt(hex.slice(3, 5), 16) / 255, b = parseInt(hex.slice(5, 7), 16) / 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min;
  if (!d) return 0;
  const h = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
  return h * 60;
}

const hueGap = (a: string, b: string) => {
  const d = Math.abs(hueOf(a) - hueOf(b));
  return Math.min(d, 360 - d);
};
