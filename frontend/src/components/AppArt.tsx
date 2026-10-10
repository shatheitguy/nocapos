import type { ReactNode } from 'react';

// Artwork for NoCapOS's own app icons: solid, layered shapes (white at
// different strengths) drawn on a 24×24 grid, so each icon has depth.

const W = '#fff';

const ART: Record<string, ReactNode> = {
  assistant: (
    <>
      <path d="M10 3.2c.5 3.9 2.3 5.8 6.3 6.3-4 .5-5.8 2.4-6.3 6.3-.5-3.9-2.4-5.8-6.3-6.3 3.9-.5 5.8-2.4 6.3-6.3Z" fill={W} />
      <path d="M17.5 13.5c.3 2 1.2 2.9 3.2 3.2-2 .3-2.9 1.2-3.2 3.2-.3-2-1.2-2.9-3.2-3.2 2-.3 2.9-1.2 3.2-3.2Z" fill={W} opacity=".7" />
      <circle cx="18" cy="5.5" r="1.4" fill={W} opacity=".55" />
    </>
  ),
  files: (
    <>
      <path d="M3 6.5A2.5 2.5 0 0 1 5.5 4h3.8c.7 0 1.3.3 1.8.8L12.5 6.3H18.5A2.5 2.5 0 0 1 21 8.8V10H3Z" fill={W} opacity=".6" />
      <rect x="3" y="8.6" width="18" height="11.4" rx="2.4" fill={W} />
      <rect x="6" y="11.4" width="5.5" height="1.4" rx=".7" fill="#000" opacity=".12" />
    </>
  ),
  photos: (
    <>
      {[0, 45, 90, 135, 180, 225, 270, 315].map((a, i) => (
        <ellipse key={a} cx="12" cy="7.2" rx="2.6" ry="4.6" fill={W} opacity={i % 2 ? 0.55 : 0.85} transform={`rotate(${a} 12 12)`} />
      ))}
      <circle cx="12" cy="12" r="2.2" fill={W} />
    </>
  ),
  backups: (
    <>
      <circle cx="12" cy="12" r="8.5" fill={W} opacity=".3" />
      <path d="M12 3.5a8.5 8.5 0 1 1-8.1 11.1" stroke={W} strokeWidth="2.4" strokeLinecap="round" fill="none" />
      <path d="M3 9.2 4 14.8l5-2.6Z" fill={W} />
      <path d="M12 7.5V12l3.2 2" stroke={W} strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </>
  ),
  storage: (
    <>
      <rect x="3.5" y="4" width="17" height="6.8" rx="2" fill={W} opacity=".6" />
      <rect x="3.5" y="13.2" width="17" height="6.8" rx="2" fill={W} />
      <circle cx="17" cy="7.4" r="1.1" fill="#000" opacity=".22" />
      <circle cx="17" cy="16.6" r="1.1" fill="#34d399" />
      <rect x="6.3" y="15.9" width="6" height="1.4" rx=".7" fill="#000" opacity=".15" />
    </>
  ),
  monitor: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="3" fill={W} opacity=".28" />
      <path d="M5.5 13.2h3l2-4.6 3 8.4 2.2-5.4 1.2 1.6h1.6" stroke={W} strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </>
  ),
  containers: (
    <>
      <path d="M12 3 20 7.4 12 11.8 4 7.4Z" fill={W} />
      <path d="M4 7.4v9.2L12 21v-9.2Z" fill={W} opacity=".72" />
      <path d="M20 7.4v9.2L12 21v-9.2Z" fill={W} opacity=".45" />
    </>
  ),
  appcenter: (
    <>
      <path d="M8.5 8V6.8a3.5 3.5 0 0 1 7 0V8" stroke={W} strokeWidth="2" strokeLinecap="round" fill="none" opacity=".8" />
      <path d="M5.2 8h13.6l-1 10.4a2.2 2.2 0 0 1-2.2 2H8.4a2.2 2.2 0 0 1-2.2-2Z" fill={W} />
      <path d="M12 11.2l.9 1.9 2.1.3-1.5 1.5.4 2.1-1.9-1-1.9 1 .4-2.1-1.5-1.5 2.1-.3Z" fill="#000" opacity=".18" />
    </>
  ),
  browser: (
    <>
      <path d="M12 2.8 19.6 5.4 19 13c-.4 3.6-3.2 6.4-7 8.2-3.8-1.8-6.6-4.6-7-8.2L4.4 5.4Z" fill={W} />
      <path d="M12 6.2 16.6 7.8 16.2 12.6c-.3 2.2-2 4-4.2 5.2Z" fill="#000" opacity=".14" />
    </>
  ),
  remotedesktop: (
    <>
      <rect x="2.8" y="4" width="18.4" height="12.4" rx="2.2" fill={W} />
      <rect x="4.8" y="6" width="14.4" height="8.4" rx="1" fill="#000" opacity=".16" />
      <path d="M9 20h6M12 16.4V20" stroke={W} strokeWidth="2.2" strokeLinecap="round" opacity=".75" />
      <path d="m10.5 8.6 3.4 1.6-3.4 1.6Z" fill={W} />
    </>
  ),
  settings: (
    <>
      <path
        d="M10.3 2.8h3.4l.5 2.4c.6.2 1.2.5 1.7.9l2.3-.8 1.7 2.9-1.8 1.6c.1.6.1 1.3 0 1.9l1.8 1.6-1.7 2.9-2.3-.8c-.5.4-1.1.7-1.7.9l-.5 2.4h-3.4l-.5-2.4c-.6-.2-1.2-.5-1.7-.9l-2.3.8-1.7-2.9 1.8-1.6a6 6 0 0 1 0-1.9L4.1 8.2l1.7-2.9 2.3.8c.5-.4 1.1-.7 1.7-.9Z"
        fill={W}
        transform="translate(12 12.4) scale(1.14) translate(-12 -11.2)"
      />
      <circle cx="12" cy="12.4" r="3.2" fill="#000" opacity=".22" />
    </>
  ),
  terminal: (
    <>
      <rect x="2.8" y="4" width="18.4" height="16" rx="3" fill={W} opacity=".14" />
      <path d="m6.5 9 3.6 3-3.6 3" stroke={W} strokeWidth="2.3" strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <path d="M12.5 16h5" stroke={W} strokeWidth="2.3" strokeLinecap="round" opacity=".7" />
    </>
  ),
  scripts: (
    <>
      <path d="M6.5 2.8h7.3l4.7 4.7v11.7a2 2 0 0 1-2 2h-10a2 2 0 0 1-2-2V4.8a2 2 0 0 1 2-2Z" fill={W} />
      <path d="M13.8 2.8v3.7a1 1 0 0 0 1 1h3.7Z" fill="#000" opacity=".16" />
      <path d="m9.6 11.6-2 2 2 2M14.4 11.6l2 2-2 2" stroke="#000" strokeOpacity=".32" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </>
  ),
};

/** Artwork for a NoCapOS app, or null when it has none. */
export function appArt(id: string | undefined, size: number): ReactNode {
  const art = id ? ART[id] : undefined;
  if (!art) return null;
  return (
    <svg className="app-art" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      {art}
    </svg>
  );
}
