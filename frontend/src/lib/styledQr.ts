// The NoCap-style QR code: rounded modules in ink, the three finder eyes in
// NoCap red, a white card with the full quiet zone, and a clear spot in the
// middle for the NoCap mark. The logo covers modules, so the code uses the
// highest error correction (H, about 30% can be lost; the logo takes under
// 6%). Pure geometry in module units (no DOM): drawn as SVG by
// components/StyledQr.tsx and decoded in scripts/styledQr.test.ts.
import QRCode from 'qrcode';

/** Modules of white around the code, as the standard asks. */
export const QUIET = 4;

export interface QrLayout {
  /** Modules across the code itself. */
  size: number;
  /** Modules across the whole card (the code plus the quiet zone on both sides). */
  total: number;
  /** Top-left corners of the dark data modules, in card units. */
  dots: { x: number; y: number }[];
  /** Top-left corners of the three 7×7 finder eyes, in card units. */
  eyes: { x: number; y: number }[];
  /** The clear square in the middle for the logo, in card units. */
  logo: { x: number; y: number; size: number };
}

/** The clear square's side in modules: odd (centred on a module), about a fifth of the code. */
export function logoModules(size: number): number {
  let n = Math.round(size * 0.22);
  if (n % 2 === 0) n -= 1;
  return Math.max(5, n);
}

export function qrLayout(text: string): QrLayout {
  const qr = QRCode.create(text, { errorCorrectionLevel: 'H' });
  const size = qr.modules.size;
  const dark = (r: number, c: number) => !!qr.modules.get(r, c);
  const inEye = (r: number, c: number) => (r < 7 && c < 7) || (r < 7 && c >= size - 7) || (r >= size - 7 && c < 7);
  const n = logoModules(size);
  const from = (size - n) / 2;
  const inLogo = (r: number, c: number) => r >= from && r < from + n && c >= from && c < from + n;
  const dots: { x: number; y: number }[] = [];
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size; c++) {
      if (dark(r, c) && !inEye(r, c) && !inLogo(r, c)) dots.push({ x: c + QUIET, y: r + QUIET });
    }
  }
  return {
    size,
    total: size + QUIET * 2,
    dots,
    eyes: [
      { x: QUIET, y: QUIET },
      { x: QUIET + size - 7, y: QUIET },
      { x: QUIET, y: QUIET + size - 7 },
    ],
    logo: { x: QUIET + from, y: QUIET + from, size: n },
  };
}

/** How the shapes are drawn (shared by the SVG and the test's rasterizer). */
export const SHAPE = {
  /** A data module: a rounded square this wide, centred in its cell. */
  dot: 0.95,
  dotRadius: 0.32,
  /** An eye: a 7×7 ring one module thick, and a 3×3 centre. */
  eyeRadius: 2.2,
  eyeInnerRadius: 1.3,
  pupilRadius: 1,
};

export const QR_INK = '#14161c';
export const QR_RED = '#e8232b';
