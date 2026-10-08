import { useId } from 'react';
import { clamp, levelClass } from '../lib/format';

/** Area sparkline. `max` fixes the Y scale (e.g. 100 for percentages). */
export function Sparkline({
  values,
  max,
  height = 56,
  className = '',
}: {
  values: number[];
  max?: number;
  height?: number;
  className?: string;
}) {
  const gid = useId();
  const w = 300;
  const n = Math.max(values.length, 2);
  const top = max ?? Math.max(1, ...values) * 1.15;
  const pts = values.map((v, i) => [(i / (n - 1)) * w, height - (clamp(v, 0, top) / top) * (height - 2) - 1] as const);
  const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`).join('');
  const area = pts.length ? `${line}L${pts[pts.length - 1][0].toFixed(1)},${height}L0,${height}Z` : '';
  return (
    <svg className={`spark ${className}`} viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" height={height}>
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="currentColor" stopOpacity="0.35" />
          <stop offset="1" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      {pts.length > 1 && (
        <>
          <path d={area} fill={`url(#${gid})`} />
          <path d={line} fill="none" stroke="currentColor" strokeWidth="1.8" vectorEffect="non-scaling-stroke" />
        </>
      )}
    </svg>
  );
}

/** Circular gauge. */
export function Ring({ value, size = 84, label, sub }: { value: number; size?: number; label: string; sub?: string }) {
  const r = 34;
  const c = 2 * Math.PI * r;
  const v = clamp(value, 0, 100);
  return (
    <div className="ring" style={{ width: size }}>
      <svg viewBox="0 0 80 80" width={size} height={size}>
        <circle cx="40" cy="40" r={r} className="ring-track" />
        <circle
          cx="40"
          cy="40"
          r={r}
          className={`ring-fill ${levelClass(v)}`}
          strokeDasharray={`${(v / 100) * c} ${c}`}
          transform="rotate(-90 40 40)"
        />
        <text x="40" y="44" textAnchor="middle" className="ring-value">
          {Math.round(v)}%
        </text>
      </svg>
      <div className="ring-label">{label}</div>
      {sub && <div className="ring-sub">{sub}</div>}
    </div>
  );
}

export function Bar({ value }: { value: number }) {
  const v = clamp(value, 0, 100);
  return (
    <div className="bar">
      <i className={levelClass(v)} style={{ width: `${v}%` }} />
    </div>
  );
}
