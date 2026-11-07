// The NoCapOS mark with a slow glow, and the brand progress bar: used while
// NoCapOS updates, restarts or shuts down. Both calm down under reduced motion.

export function BrandMark({ size = 96, busy = true }: { size?: number; busy?: boolean }) {
  return (
    <div className={`brand-mark ${busy ? 'busy' : ''}`} style={{ width: size, height: size }} aria-hidden="true">
      <span className="brand-mark-glow" />
      <img src="/brand/mark-512.png" alt="" width={size} height={size} draggable={false} />
    </div>
  );
}

/** percent: a determinate bar; undefined: an endless sweep (waiting on something). */
export function BrandBar({ percent, label, done }: { percent?: number; label: string; done?: boolean }) {
  const indeterminate = percent === undefined;
  return (
    <div
      className={`brand-bar ${indeterminate ? 'indeterminate' : ''} ${done ? 'done' : ''}`}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={indeterminate ? undefined : Math.round(percent)}
    >
      <div className="brand-bar-fill" style={indeterminate ? undefined : { width: `${Math.max(percent, 3)}%` }} />
    </div>
  );
}
