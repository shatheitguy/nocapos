// Served straight from public/brand (Vite leaves public/ files alone, so these
// are referenced by their root-relative path, the same way app-icons/*.svg are).
const MARK = '/brand/mark-512.png';

/** The NoCapOS mark: the studio's own artwork, used exactly as supplied. */
export function LogoMark({ size = 24, className }: { size?: number; className?: string }) {
  return (
    <img
      src={MARK}
      width={size}
      height={size}
      alt="NoCapOS"
      draggable={false}
      className={`logo-mark ${className ?? ''}`}
      style={{ width: size, height: size }}
    />
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
