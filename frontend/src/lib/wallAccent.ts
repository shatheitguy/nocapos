// Picks an accent color from a wallpaper picture: the most vivid color that
// covers a good part of it, tuned so it stays readable on the dark glass.
// Pure (no DOM) so it can be unit tested; the caller samples the pixels.

const BINS = 36; // 10° hue buckets

function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const l = (max + min) / 2;
  if (max === min) return [0, 0, l];
  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
  let h: number;
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
  else if (max === g) h = (b - r) / d + 2;
  else h = (r - g) / d + 4;
  return [h * 60, s, l];
}

function hslToHex(h: number, s: number, l: number): string {
  const k = (n: number) => (n + h / 30) % 12;
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)));
  const hex = (x: number) => Math.round(x * 255).toString(16).padStart(2, '0');
  return `#${hex(f(0))}${hex(f(8))}${hex(f(4))}`;
}

interface Group { score: number; x: number; y: number; s: number }

/** Per hue bucket: weighted score, hue vector and saturation, plus pixel count. */
function buckets(data: Uint8ClampedArray | number[]): { groups: Group[]; total: number } {
  const groups: Group[] = Array.from({ length: BINS }, () => ({ score: 0, x: 0, y: 0, s: 0 }));
  let total = 0;
  for (let i = 0; i + 3 < data.length; i += 4) {
    if (data[i + 3] < 128) continue; // transparent
    total++;
    const [h, s, l] = rgbToHsl(data[i], data[i + 1], data[i + 2]);
    // Skip greys, near-black and near-white: they make dull accents.
    if (s < 0.25 || l < 0.12 || l > 0.9) continue;
    // Vivid, mid-bright pixels count most.
    const w = s * (1 - Math.abs(l - 0.5) * 1.6);
    if (w <= 0) continue;
    const g = groups[Math.floor(h / (360 / BINS)) % BINS];
    g.score += w;
    g.x += Math.cos((h * Math.PI) / 180) * w;
    g.y += Math.sin((h * Math.PI) / 180) * w;
    g.s += s * w;
  }
  return { groups, total };
}

/**
 * Up to n accent colors from a picture, strongest first, at least 40° of hue
 * apart so they're really different choices. Empty when the picture has no
 * real color.
 */
export function paletteFromPixels(data: Uint8ClampedArray | number[], n = 3): string[] {
  const { groups, total } = buckets(data);
  if (!total) return [];
  const near = (b: number, d: number) => groups[(b + d + BINS) % BINS];
  const window = (b: number) => near(b, 0).score + 0.5 * (near(b, -1).score + near(b, 1).score);
  const out: string[] = [];
  const used: number[] = [];
  while (out.length < n) {
    let best = -1, bestScore = 0;
    for (let b = 0; b < BINS; b++) {
      if (used.some((u) => Math.min(Math.abs(u - b), BINS - Math.abs(u - b)) < 4)) continue;
      const v = window(b);
      if (v > bestScore) { bestScore = v; best = b; }
    }
    // Needs to cover a real part of the picture (weighted), not a few pixels.
    if (best < 0 || bestScore / total < 0.03) break;
    used.push(best);
    let x = 0, y = 0, sw = 0, ws = 0;
    for (const d of [-1, 0, 1]) {
      const g = near(best, d);
      x += g.x; y += g.y; sw += g.s; ws += g.score;
    }
    const hue = ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360;
    const sat = Math.min(0.9, Math.max(0.6, sw / ws));
    // Bright enough to read on dark glass, not so bright it glares.
    out.push(hslToHex(hue, sat, 0.58));
  }
  return out;
}

/**
 * The accent for a picture given its RGBA pixels (any size; a 64×64 sample
 * is plenty), or null when the picture has no real color (black and white,
 * nearly grey), so the theme's own color is used instead.
 */
export function accentFromPixels(data: Uint8ClampedArray | number[]): string | null {
  return paletteFromPixels(data, 1)[0] ?? null;
}
