// Holographic system-monitor widgets: per-core CPU heatmap, RAM & swap rings,
// network up/down waveform and storage "fuel gauges". Charts draw whenever new
// metrics arrive (no animation loop), in the active theme's colors.
import { useEffect, useRef } from 'react';
import { openApp } from '../apps/meta';
import { Icon } from '../components/Icon';
import { clamp, fmtBytes, fmtRate } from '../lib/format';
import { usePrefs } from '../state/prefs';
import { HISTORY, useSystem } from '../state/system';

// ---------- theme colors for canvas ----------

type RGB = [number, number, number];

let probe: CanvasRenderingContext2D | null = null;
/** Any CSS color (hex, rgb(), named) → [r, g, b], via the canvas color parser. */
function toRGB(color: string): RGB {
  probe ??= document.createElement('canvas').getContext('2d');
  if (!probe) return [0, 200, 255];
  probe.fillStyle = '#000';
  probe.fillStyle = color.trim() || '#000';
  const v = probe.fillStyle as string;
  if (v.startsWith('#')) return [parseInt(v.slice(1, 3), 16), parseInt(v.slice(3, 5), 16), parseInt(v.slice(5, 7), 16)];
  const m = v.match(/[\d.]+/g)?.map(Number) ?? [0, 0, 0];
  return [m[0], m[1], m[2]];
}

function palette() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) => cs.getPropertyValue(name).trim() || fallback;
  return {
    accent: v('--accent', '#e8232b'),
    accent2: v('--accent-2', v('--violet', '#8a5cf6')),
    accent3: v('--accent-3', v('--warn', '#d58a00')),
    bad: v('--bad', '#dc4040'),
    muted: v('--muted', '#888'),
  };
}

// Stable empty list for selectors (a fresh [] each call would re-render forever).
const NONE: never[] = [];

const rgba = (c: RGB, a: number) => `rgba(${c[0]},${c[1]},${c[2]},${a})`;
const mix = (a: RGB, b: RGB, t: number): RGB => [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t];

/** Re-render charts when the look changes. */
function useThemeKey() {
  return usePrefs((s) => `${s.uiTheme}|${s.accent}|${s.theme}`);
}

/** A canvas that matches its box (incl. HiDPI) and redraws on resize or when `deps` change. */
function useCanvas(draw: (ctx: CanvasRenderingContext2D, w: number, h: number) => void, deps: unknown[]) {
  const ref = useRef<HTMLCanvasElement>(null);
  const drawRef = useRef(draw);
  drawRef.current = draw;
  useEffect(() => {
    const c = ref.current;
    if (!c) return;
    const paint = () => {
      const w = c.clientWidth;
      const h = c.clientHeight;
      if (!w || !h) return;
      const dpr = window.devicePixelRatio || 1;
      if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
        c.width = Math.round(w * dpr);
        c.height = Math.round(h * dpr);
      }
      const ctx = c.getContext('2d');
      if (!ctx) return;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);
      drawRef.current(ctx, w, h);
    };
    paint();
    const ro = new ResizeObserver(paint);
    ro.observe(c);
    return () => ro.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  return ref;
}

function Empty({ icon, text }: { icon: 'cpu' | 'network' | 'disk' | 'memory'; text: string }) {
  return (
    <div className="holo-empty">
      <Icon name={icon} size={22} />
      <span>{text}</span>
    </div>
  );
}

// ---------- CPU per-core heatmap ----------

export function CoreHeatmapWidget() {
  const history = useSystem((s) => s.history);
  const latest = useSystem((s) => s.latest);
  const theme = useThemeKey();
  const cores = latest?.cpu.per_core ?? [];
  const n = cores.length;

  const canvas = useCanvas(
    (ctx, w, h) => {
      if (!n) return;
      const p = palette();
      const cold = toRGB(p.accent);
      const hot = toRGB(p.accent3);
      const peak = toRGB(p.bad);
      const rows = n;
      const cw = w / HISTORY;
      const rh = h / rows;
      const start = HISTORY - history.length;
      // faint empty grid
      ctx.fillStyle = rgba(cold, 0.05);
      for (let r = 0; r < rows; r++) ctx.fillRect(0, r * rh + 1, w, rh - 2);
      history.forEach((s, i) => {
        for (let r = 0; r < rows; r++) {
          const v = clamp((s.cores[r] ?? 0) / 100, 0, 1);
          if (v < 0.01) continue;
          const col = v < 0.7 ? cold : v < 0.9 ? mix(cold, hot, (v - 0.7) / 0.2) : mix(hot, peak, (v - 0.9) / 0.1);
          ctx.fillStyle = rgba(col, 0.18 + v * 0.82);
          ctx.fillRect((start + i) * cw, r * rh + 1, Math.max(1, cw - 0.6), rh - 2);
        }
      });
      // "now" cursor
      ctx.fillStyle = rgba(cold, 0.9);
      ctx.shadowColor = rgba(cold, 0.9);
      ctx.shadowBlur = 6;
      ctx.fillRect(w - 1.5, 0, 1.5, h);
      ctx.shadowBlur = 0;
    },
    [history, n, theme],
  );

  const avg = n ? cores.reduce((a, b) => a + b, 0) / n : latest?.cpu.percent ?? 0;
  const hottest = n ? cores.reduce((best, v, i) => (v > cores[best] ? i : best), 0) : -1;

  return (
    <button type="button" className="widget glass holo" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span>
          <Icon name="cpu" size={15} /> CPU cores
        </span>
        <span className="holo-stat">{avg.toFixed(0)}%</span>
      </div>
      {n ? (
        <>
          <div className="holo-heat">
            <span className="holo-axis">{Array.from({ length: Math.min(n, 16) }, (_, i) => <i key={i}>{n <= 16 ? i : ''}</i>)}</span>
            <canvas ref={canvas} className="holo-canvas" />
          </div>
          <div className="holo-foot">
            <span>{n} cores · last {Math.round((HISTORY * 2) / 60)} min</span>
            {hottest >= 0 && (
              <span>
                hottest <b>C{hottest}</b> {cores[hottest].toFixed(0)}%
              </span>
            )}
          </div>
        </>
      ) : (
        <Empty icon="cpu" text="This host doesn't report per-core CPU." />
      )}
    </button>
  );
}

// ---------- RAM & swap rings ----------

function RingArc({ r, value, className }: { r: number; value: number; className: string }) {
  const c = 2 * Math.PI * r;
  const v = clamp(value, 0, 100);
  return (
    <>
      <circle cx="60" cy="60" r={r} className="holo-track" />
      <circle cx="60" cy="60" r={r} className={className} strokeDasharray={`${(v / 100) * c} ${c}`} transform="rotate(-90 60 60)" />
    </>
  );
}

export function MemoryRingsWidget() {
  const m = useSystem((s) => s.latest?.memory);
  if (!m) return <div className="widget glass skeleton" />;
  const swapPct = m.swap_total ? (m.swap_used / m.swap_total) * 100 : 0;
  return (
    <button type="button" className="widget glass holo" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span>
          <Icon name="memory" size={15} /> Memory
        </span>
        <span className="holo-stat">{m.percent.toFixed(0)}%</span>
      </div>
      <div className="holo-rings">
        <svg viewBox="0 0 120 120" className="holo-ring-svg">
          <RingArc r={50} value={m.percent} className="holo-arc ram" />
          <RingArc r={37} value={swapPct} className="holo-arc swap" />
          <text x="60" y="58" className="holo-ring-num">{m.percent.toFixed(0)}%</text>
          <text x="60" y="73" className="holo-ring-cap">RAM</text>
        </svg>
        <div className="holo-legend">
          <div>
            <i className="holo-dot ram" /> RAM
            <b>{fmtBytes(m.used, 1)}</b>
            <span>of {fmtBytes(m.total, 0)}</span>
          </div>
          <div>
            <i className="holo-dot swap" /> Swap
            {m.swap_total ? (
              <>
                <b>{fmtBytes(m.swap_used, 1)}</b>
                <span>of {fmtBytes(m.swap_total, 0)}</span>
              </>
            ) : (
              <span>not in use</span>
            )}
          </div>
        </div>
      </div>
    </button>
  );
}

// ---------- network waveform ----------

export function NetWaveWidget() {
  const history = useSystem((s) => s.history);
  const nics = useSystem((s) => s.latest?.network ?? NONE);
  const theme = useThemeKey();
  const last = history[history.length - 1];
  const peak = Math.max(1024, ...history.map((s) => Math.max(s.rx, s.tx)));

  const canvas = useCanvas(
    (ctx, w, h) => {
      if (!history.length) return;
      const p = palette();
      const down = toRGB(p.accent);
      const up = toRGB(p.accent2);
      const mid = h / 2;
      const scale = (mid - 4) / (peak * 1.1);
      const step = w / (HISTORY - 1);
      const x0 = (HISTORY - history.length) * step;

      // axis + faint grid
      ctx.strokeStyle = rgba(down, 0.18);
      ctx.lineWidth = 1;
      ctx.setLineDash([3, 4]);
      for (const y of [mid / 2, mid + mid / 2]) {
        ctx.beginPath();
        ctx.moveTo(0, y);
        ctx.lineTo(w, y);
        ctx.stroke();
      }
      ctx.setLineDash([]);
      ctx.strokeStyle = rgba(down, 0.35);
      ctx.beginPath();
      ctx.moveTo(0, mid);
      ctx.lineTo(w, mid);
      ctx.stroke();

      const wave = (color: RGB, pick: (i: number) => number, dir: 1 | -1) => {
        const pts = history.map((_, i) => [x0 + i * step, mid - dir * pick(i) * scale] as const);
        const grad = ctx.createLinearGradient(0, mid, 0, dir === 1 ? 0 : h);
        grad.addColorStop(0, rgba(color, 0.05));
        grad.addColorStop(1, rgba(color, 0.45));
        ctx.beginPath();
        ctx.moveTo(pts[0][0], mid);
        pts.forEach(([x, y]) => ctx.lineTo(x, y));
        ctx.lineTo(pts[pts.length - 1][0], mid);
        ctx.closePath();
        ctx.fillStyle = grad;
        ctx.fill();
        ctx.beginPath();
        pts.forEach(([x, y], i) => (i ? ctx.lineTo(x, y) : ctx.moveTo(x, y)));
        ctx.strokeStyle = rgba(color, 0.95);
        ctx.lineWidth = 1.6;
        ctx.shadowColor = rgba(color, 0.9);
        ctx.shadowBlur = 8;
        ctx.stroke();
        ctx.shadowBlur = 0;
      };
      wave(down, (i) => history[i].rx, 1);
      wave(up, (i) => history[i].tx, -1);
    },
    [history, peak, theme],
  );

  return (
    <button type="button" className="widget glass holo" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span>
          <Icon name="network" size={15} /> Network
        </span>
        <span className="muted small holo-ifname">{nics.map((n) => n.interface).join(', ') || '—'}</span>
      </div>
      {nics.length ? (
        <>
          <canvas ref={canvas} className="holo-canvas wave" />
          <div className="holo-foot">
            <span className="holo-down">▼ {fmtRate(last?.rx)}</span>
            <span className="holo-up">▲ {fmtRate(last?.tx)}</span>
            <span>peak {fmtRate(peak)}</span>
          </div>
        </>
      ) : (
        <Empty icon="network" text="No network counters reported yet." />
      )}
    </button>
  );
}

// ---------- storage fuel gauges ----------

function Gauge({ pct }: { pct: number }) {
  const v = clamp(pct, 0, 100);
  const r = 40;
  const half = Math.PI * r;
  const level = v >= 90 ? 'crit' : v >= 75 ? 'warn' : 'ok';
  const ticks = Array.from({ length: 11 }, (_, i) => {
    const a = Math.PI - (i / 10) * Math.PI;
    const r1 = 50;
    const r2 = i % 5 === 0 ? 44 : 47;
    return <line key={i} x1={55 + r1 * Math.cos(a)} y1={55 - r1 * Math.sin(a)} x2={55 + r2 * Math.cos(a)} y2={55 - r2 * Math.sin(a)} className="holo-tick" />;
  });
  return (
    <svg viewBox="0 0 110 64" className="holo-gauge">
      {ticks}
      <path d={`M ${55 - r} 55 A ${r} ${r} 0 0 1 ${55 + r} 55`} className="holo-track" />
      <path d={`M ${55 - r} 55 A ${r} ${r} 0 0 1 ${55 + r} 55`} className={`holo-arc gauge ${level}`} strokeDasharray={`${(v / 100) * half} ${half}`} />
      <text x="55" y="52" className="holo-gauge-num">{v.toFixed(0)}%</text>
    </svg>
  );
}

export function StorageGaugesWidget() {
  const disks = useSystem((s) => s.latest?.disks ?? NONE);
  return (
    <button type="button" className="widget glass holo" onClick={() => openApp('monitor')}>
      <div className="widget-head">
        <span>
          <Icon name="disk" size={15} /> Storage
        </span>
        <span className="muted small">{disks.length} volume{disks.length === 1 ? '' : 's'}</span>
      </div>
      {disks.length ? (
        <div className="holo-gauges">
          {disks.map((d) => (
            <div key={d.path} className="holo-gauge-cell">
              <Gauge pct={d.percent} />
              <b title={d.path}>{d.path}</b>
              <span>{fmtBytes(d.free, 0)} free</span>
            </div>
          ))}
        </div>
      ) : (
        <Empty icon="disk" text="No volumes reported." />
      )}
    </button>
  );
}
