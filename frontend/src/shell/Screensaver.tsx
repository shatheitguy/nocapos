import { useEffect, useRef } from 'react';
import { fmtDate, fmtTime } from '../lib/time';
import { useClock } from '../lib/hooks';
import { usePrefs, type Screensaver as SaverKind } from '../state/prefs';

/** Full-screen idle screensaver. Any pointer/key activity dismisses it (handled by the parent). */
export function Screensaver({ kind, onWake }: { kind: SaverKind; onWake: () => void }) {
  useEffect(() => {
    const wake = () => onWake();
    const opts = { passive: true } as AddEventListenerOptions;
    window.addEventListener('pointermove', wake, opts);
    window.addEventListener('pointerdown', wake, opts);
    window.addEventListener('keydown', wake);
    window.addEventListener('wheel', wake, opts);
    return () => {
      window.removeEventListener('pointermove', wake);
      window.removeEventListener('pointerdown', wake);
      window.removeEventListener('keydown', wake);
      window.removeEventListener('wheel', wake);
    };
  }, [onWake]);

  return (
    <div className="screensaver">
      {kind === 'starfield' ? <Starfield /> : <SaverClock />}
    </div>
  );
}

function SaverClock() {
  const now = useClock(1000);
  return (
    <div className="saver-clock">
      <div className="saver-time">{fmtTime(now, usePrefs.getState(), false)}</div>
      <div className="saver-date">{fmtDate(now, usePrefs.getState())}</div>
    </div>
  );
}

function Starfield() {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    let raf = 0;
    let w = 0;
    let h = 0;
    const stars = Array.from({ length: 220 }, () => ({ x: 0, y: 0, z: 0 }));
    const reset = (s: { x: number; y: number; z: number }) => {
      s.x = (Math.random() - 0.5) * 2;
      s.y = (Math.random() - 0.5) * 2;
      s.z = Math.random();
    };
    stars.forEach(reset);
    const size = () => {
      w = canvas.width = canvas.offsetWidth * devicePixelRatio;
      h = canvas.height = canvas.offsetHeight * devicePixelRatio;
    };
    size();
    const ro = new ResizeObserver(size);
    ro.observe(canvas);
    const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#8a5cf6';
    const draw = () => {
      ctx.fillStyle = 'rgba(6,8,14,0.4)';
      ctx.fillRect(0, 0, w, h);
      for (const s of stars) {
        s.z -= 0.004;
        if (s.z <= 0) reset(s);
        const px = (s.x / s.z) * (w / 2) + w / 2;
        const py = (s.y / s.z) * (h / 2) + h / 2;
        if (px < 0 || px > w || py < 0 || py > h) continue;
        const r = (1 - s.z) * 2.2 * devicePixelRatio;
        ctx.globalAlpha = 1 - s.z;
        ctx.fillStyle = s.z < 0.3 ? accent : '#dfe6f5';
        ctx.beginPath();
        ctx.arc(px, py, r, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.globalAlpha = 1;
      raf = requestAnimationFrame(draw);
    };
    draw();
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, []);
  return <canvas ref={ref} className="starfield" />;
}
