// The NoCap-style QR code still scans: the shapes are drawn into pixels the
// way the SVG draws them (rounded modules, red eyes, the logo over the
// middle) and read back with a QR decoder. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import jsQR from 'jsqr';
import { logoModules, QUIET, qrLayout, SHAPE } from '../src/lib/styledQr.ts';

const INK = [20, 22, 28];
const RED = [232, 35, 43];

/** Whether (px, py) is inside a rounded rectangle. */
function inRR(px: number, py: number, x: number, y: number, w: number, h: number, r: number) {
  if (px < x || px > x + w || py < y || py > y + h) return false;
  const dx = Math.max(x + r - px, 0, px - (x + w - r));
  const dy = Math.max(y + r - py, 0, py - (y + h - r));
  return dx * dx + dy * dy <= r * r;
}

/** The card as RGBA pixels, `scale` pixels per module. */
function render(text: string, scale: number) {
  const L = qrLayout(text);
  const px = L.total * scale;
  const rgba = new Uint8ClampedArray(px * px * 4).fill(255);
  const cells = new Set(L.dots.map((d) => `${d.x},${d.y}`));
  const pad = (1 - SHAPE.dot) / 2;
  const lc = L.logo.x + L.logo.size / 2;
  const lr = L.logo.size * 0.36;
  for (let j = 0; j < px; j++) {
    for (let i = 0; i < px; i++) {
      const x = (i + 0.5) / scale;
      const y = (j + 0.5) / scale;
      const cx = Math.floor(x);
      const cy = Math.floor(y);
      let color: number[] | null = null;
      if (cells.has(`${cx},${cy}`) && inRR(x, y, cx + pad, cy + pad, SHAPE.dot, SHAPE.dot, SHAPE.dotRadius)) color = INK;
      for (const e of L.eyes) {
        if (inRR(x, y, e.x + 2, e.y + 2, 3, 3, SHAPE.pupilRadius)) color = INK;
        else if (inRR(x, y, e.x, e.y, 7, 7, SHAPE.eyeRadius) && !inRR(x, y, e.x + 1, e.y + 1, 5, 5, SHAPE.eyeInnerRadius)) color = RED;
      }
      // The mark: a solid red disc over the middle (darker than the real one, so a harder case).
      if ((x - lc) ** 2 + (y - lc) ** 2 <= lr * lr) color = RED;
      if (color) {
        const o = (j * px + i) * 4;
        rgba[o] = color[0];
        rgba[o + 1] = color[1];
        rgba[o + 2] = color[2];
      }
    }
  }
  return { rgba, px, layout: L };
}

const urls = [
  'https://nocap.local:8443/link?code=482913',
  'nocap://pair?server=https%3A%2F%2F192.168.0.170%3A8443&code=482913&id=a3f9c2e1b5d47a6f0c1e2d3b4a596877',
  'nocap://pair?server=https%3A%2F%2Fnocap-living-room.local%3A8443&code=000731&id=0123456789abcdef0123456789abcdef',
  'https://192.168.0.170:8443/link?code=104857&server=7f3a9c2e1b5d4a6f&fp=SHA256:3b:7a:91:0c:de:44:12:9f:aa:51:6e:08:c3:d2:77:19',
];

for (const url of urls) {
  test(`the styled code scans: ${url.slice(0, 40)}…`, () => {
    for (const scale of [3, 4, 5, 6, 8]) {
      const { rgba, px } = render(url, scale);
      const hit = jsQR(rgba, px, px);
      assert.ok(hit, `no code found at ${scale} px per module`);
      assert.equal(hit.data, url);
    }
  });
}

test('the card keeps the quiet zone, and the logo stays small', () => {
  const L = qrLayout(urls[1]);
  assert.equal(L.total, L.size + 2 * QUIET);
  // Nothing dark in the quiet zone.
  for (const d of L.dots) {
    assert.ok(d.x >= QUIET && d.y >= QUIET && d.x < QUIET + L.size && d.y < QUIET + L.size);
  }
  // The logo hides well under the 30% error correction H recovers.
  assert.ok((L.logo.size * L.logo.size) / (L.size * L.size) < 0.08);
  assert.equal(L.logo.size % 2, 1);
  assert.equal(logoModules(21), 5);
});
