import { useMemo } from 'react';
import { QR_INK, QR_RED, qrLayout, SHAPE } from '../lib/styledQr';

/** A rounded rectangle as an SVG path (drawn clockwise). */
function rr(x: number, y: number, w: number, h: number, r: number) {
  return `M${x + r} ${y}h${w - 2 * r}a${r} ${r} 0 0 1 ${r} ${r}v${h - 2 * r}a${r} ${r} 0 0 1 ${-r} ${r}h${-(w - 2 * r)}a${r} ${r} 0 0 1 ${-r} ${-r}v${-(h - 2 * r)}a${r} ${r} 0 0 1 ${r} ${-r}z`;
}

/**
 * A QR code in NoCap style: rounded ink modules, red finder eyes, the NoCap
 * mark in the middle, on a white card (scanners need the contrast and the
 * quiet zone).
 */
export function StyledQr({ text, label, className }: { text: string; label: string; className?: string }) {
  const layout = useMemo(() => {
    try {
      return qrLayout(text);
    } catch {
      return null;
    }
  }, [text]);
  if (!layout) return null;
  const { total, dots, eyes, logo } = layout;
  const pad = (1 - SHAPE.dot) / 2;
  const dotPath = dots.map((d) => rr(d.x + pad, d.y + pad, SHAPE.dot, SHAPE.dot, SHAPE.dotRadius)).join('');
  const eyePath = eyes.map((e) => rr(e.x, e.y, 7, 7, SHAPE.eyeRadius) + rr(e.x + 1, e.y + 1, 5, 5, SHAPE.eyeInnerRadius)).join('');
  const pupilPath = eyes.map((e) => rr(e.x + 2, e.y + 2, 3, 3, SHAPE.pupilRadius)).join('');
  const m = logo.size * 0.14;
  return (
    <svg className={className} viewBox={`0 0 ${total} ${total}`} role="img" aria-label={label} shapeRendering="geometricPrecision">
      <rect width={total} height={total} rx={total * 0.06} fill="#fff" />
      <path d={dotPath} fill={QR_INK} />
      <path d={eyePath} fill={QR_RED} fillRule="evenodd" />
      <path d={pupilPath} fill={QR_INK} />
      <rect x={logo.x + 0.3} y={logo.y + 0.3} width={logo.size - 0.6} height={logo.size - 0.6} rx={logo.size * 0.26} fill="#fff" stroke={QR_RED} strokeOpacity={0.25} strokeWidth={0.2} />
      <image href="/brand/mark-128.png" x={logo.x + m} y={logo.y + m} width={logo.size - 2 * m} height={logo.size - 2 * m} preserveAspectRatio="xMidYMid meet" />
    </svg>
  );
}
