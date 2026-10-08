import { useId } from 'react';

/**
 * The NoCapOS "NC" mark: a steel N whose diagonal sweeps into the C, with a
 * red pinstripe and a red inner C.
 */
export function LogoMark({ size = 24, className }: { size?: number; className?: string }) {
  const id = useId().replace(/:/g, '');
  const steel = `nc-steel-${id}`;
  const red = `nc-red-${id}`;
  return (
    <svg className={`logo-mark ${className ?? ''}`} width={size} height={size} viewBox="0 0 100 100" role="img" aria-label="NoCapOS">
      <defs>
        <linearGradient id={steel} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="var(--logo-steel-1, #d9dde4)" />
          <stop offset="0.5" stopColor="var(--logo-steel-2, #8d929c)" />
          <stop offset="1" stopColor="var(--logo-steel-3, #5a5f69)" />
        </linearGradient>
        <linearGradient id={red} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#ff4040" />
          <stop offset="1" stopColor="#d70000" />
        </linearGradient>
      </defs>
      <path d="M40.5 43 A33 33 0 0 1 95 27 L88 33 A24 24 0 0 0 51 44 Z" fill={`url(#${steel})`} />
      <path d="M6 10 L21 25 L21 92 L6 80 Z" fill={`url(#${steel})`} />
      <path d="M6 10 L19 10 L59 66 Q68 78 88 69 L95 74 Q66 96 50 76 Z" fill={`url(#${steel})`} />
      <path d="M12 6 L57 64 Q66 76 87 67" fill="none" stroke={`url(#${red})`} strokeWidth="2.6" />
      <path d="M85 38 A17 17 0 1 0 85 62 L79 57 A9.5 9.5 0 1 1 79 43 Z" fill={`url(#${red})`} />
      <path d="M6 64 L6 80 L21 92 L21 88 Z" fill="#ff2a2a" />
    </svg>
  );
}

/** Mark + "NO CAP OS" wordmark, optionally with the tagline. */
export function Logo({ size = 72, tagline = true }: { size?: number; tagline?: boolean }) {
  return (
    <div className="logo-lockup">
      <LogoMark size={size} />
      <div className="logo-word">
        NO CAP <b>OS</b>
      </div>
      {tagline && <div className="logo-tag">Freedom to do more</div>}
    </div>
  );
}
