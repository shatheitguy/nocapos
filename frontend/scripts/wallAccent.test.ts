// Wallpaper accent picking: run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { accentFromPixels } from '../src/lib/wallAccent.ts';

/** n pixels of one color. */
const fill = (n: number, r: number, g: number, b: number, a = 255) => Array.from({ length: n }, () => [r, g, b, a]).flat();

const hue = (hex: string) => {
  const r = parseInt(hex.slice(1, 3), 16) / 255, g = parseInt(hex.slice(3, 5), 16) / 255, b = parseInt(hex.slice(5, 7), 16) / 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min;
  if (!d) return 0;
  const h = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
  return h * 60;
};

test('a blue picture gives a blue accent', () => {
  const a = accentFromPixels(fill(400, 30, 90, 220));
  assert.ok(a);
  assert.ok(Math.abs(hue(a!) - 223) < 12, a!);
});

test('the vivid color wins over a large dull background', () => {
  // Mostly dark grey-blue night sky, with an orange sunset band.
  const a = accentFromPixels([...fill(700, 40, 44, 52), ...fill(300, 240, 120, 30)]);
  assert.ok(a);
  assert.ok(Math.abs(hue(a!) - 28) < 12, a!);
});

test('black and white pictures keep the theme color', () => {
  assert.equal(accentFromPixels([...fill(500, 0, 0, 0), ...fill(500, 255, 255, 255), ...fill(500, 128, 128, 128)]), null);
});

test('a few colored pixels are not enough', () => {
  assert.equal(accentFromPixels([...fill(2000, 20, 20, 20), ...fill(10, 255, 0, 0)]), null);
});

test('reds on both sides of 0° average to red, not cyan', () => {
  const a = accentFromPixels([...fill(300, 230, 20, 60), ...fill(300, 230, 60, 20)]);
  assert.ok(a);
  const h = hue(a!);
  assert.ok(h < 15 || h > 345, a!);
});

test('the accent is readable: mid lightness, strong saturation', () => {
  const a = accentFromPixels(fill(400, 120, 200, 140))!; // pale green
  const r = parseInt(a.slice(1, 3), 16), g = parseInt(a.slice(3, 5), 16), b = parseInt(a.slice(5, 7), 16);
  const l = (Math.max(r, g, b) + Math.min(r, g, b)) / 2 / 255;
  assert.ok(l > 0.5 && l < 0.66, a);
  assert.ok(Math.max(r, g, b) - Math.min(r, g, b) > 100, a);
});

test('transparent pixels are ignored', () => {
  assert.equal(accentFromPixels(fill(400, 255, 0, 0, 0)), null);
});

test('palette: up to three clearly different colors, strongest first', async () => {
  const { paletteFromPixels } = await import('../src/lib/wallAccent.ts');
  const p = paletteFromPixels([...fill(500, 30, 90, 220), ...fill(300, 240, 120, 30), ...fill(200, 40, 200, 90), ...fill(150, 35, 100, 230)]);
  assert.equal(p.length, 3);
  assert.ok(Math.abs(hue(p[0]) - 223) < 12, p[0]); // blue (both blues merge)
  assert.ok(Math.abs(hue(p[1]) - 28) < 12, p[1]); // orange
  assert.ok(Math.abs(hue(p[2]) - 140) < 15, p[2]); // green
});

test('palette: a one-color picture gives one color', async () => {
  const { paletteFromPixels } = await import('../src/lib/wallAccent.ts');
  assert.equal(paletteFromPixels(fill(400, 200, 40, 60)).length, 1);
});
