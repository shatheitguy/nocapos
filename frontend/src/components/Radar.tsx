import { blipSpot } from '../lib/squad';
import { LogoMark } from './Logo';

// The NoCap radar: the mark in the middle, red pulses rolling out from it
// and a slow sweep while it searches; each server found gets a blip that
// pops in. Only transforms and opacity animate (smooth on phones), and with
// reduced motion it's just the rings. The NoCap OS app draws the same thing
// in Compose (sync/Radar.kt).

export interface RadarBlip {
  id: string;
  label: string;
  /** linked servers glow steady; the rest blink once as they're found */
  linked?: boolean;
}

export function NoCapRadar({ scanning, blips, size = 220, label }: { scanning: boolean; blips: RadarBlip[]; size?: number; label: string }) {
  return (
    <div className={`ncr${scanning ? ' scanning' : ''}`} style={{ width: size, height: size }} role="img" aria-label={label}>
      <span className="ncr-ring r1" />
      <span className="ncr-ring r2" />
      <span className="ncr-ring r3" />
      <span className="ncr-cross" />
      {scanning && (
        <>
          <span className="ncr-sweep" />
          <span className="ncr-pulse p1" />
          <span className="ncr-pulse p2" />
          <span className="ncr-pulse p3" />
        </>
      )}
      {blips.map((b, i) => {
        const p = blipSpot(b.id, i);
        return (
          <span
            key={b.id}
            className={`ncr-blip${b.linked ? ' linked' : ''}`}
            style={{ left: `${p.x}%`, top: `${p.y}%`, animationDelay: `${i * 90}ms` }}
            title={b.label}
          />
        );
      })}
      <span className="ncr-core">
        <LogoMark size={Math.round(size * 0.22)} />
      </span>
    </div>
  );
}
