import { useState } from 'react';
// The mark is bundled with the shell (the shell build has no public/ folder).
import mark64 from '../../public/brand/mark-64.png';
import mark256 from '../../public/brand/mark-256.png';
import { hueOf, initials } from './logic';

/** The NoCapOS mark. */
export function LogoMark({ size = 24, className = '' }: { size?: number; className?: string }) {
  return (
    <img
      src={size > 64 ? mark256 : mark64}
      width={size}
      height={size}
      alt="NoCapOS"
      draggable={false}
      className={`logo-mark ${className}`}
      style={{ width: size, height: size }}
    />
  );
}

/** "NO CAP OS" under the mark, like the web OS's lock-up. */
export function Wordmark() {
  return (
    <div className="logo-word">
      NO CAP <b>OS</b>
    </div>
  );
}

/** An app's icon from the host (a nocap-shell://icon URL), or a letter tile when there is none. */
export function AppIcon({ icon, name, size = 44, nocap = false }: { icon: string; name: string; size?: number; nocap?: boolean }) {
  const [broken, setBroken] = useState(false);
  const radius = Math.round(size * 0.234);
  const badge = nocap ? <LogoMark size={Math.max(12, Math.round(size * 0.42))} className="nsh-badge" /> : null;
  if (icon && !broken) {
    return (
      <span className="app-icon logo" style={{ width: size, height: size, borderRadius: radius }}>
        <img src={icon} alt="" width={size} height={size} draggable={false} onError={() => setBroken(true)} />
        {badge}
      </span>
    );
  }
  return (
    <span
      className="app-icon nsh-letter"
      style={{ width: size, height: size, borderRadius: radius, fontSize: Math.round(size * 0.44), ['--h' as string]: hueOf(name) }}
    >
      {initials(name).slice(0, 1)}
      {badge}
    </span>
  );
}

/** A round avatar with initials. */
export function Avatar({ name, size = 72 }: { name: string; size?: number }) {
  return (
    <span className="nsh-avatar" style={{ width: size, height: size, fontSize: Math.round(size * 0.4), ['--h' as string]: hueOf(name) }}>
      {initials(name)}
    </span>
  );
}
