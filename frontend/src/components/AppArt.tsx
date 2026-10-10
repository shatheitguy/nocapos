import { useId, type ReactNode } from 'react';

// NoCapOS's own app icons: illustrated tiles with depth (lit from the top,
// soft shadows), drawn on a 64×64 grid. With "match wallpaper" on, the --icon-*
// variables recolour every tile in the theme's accent. `g` turns a local gradient
// name into one unique to this icon, so many icons can share a page.

type Draw = (g: (name: string) => string) => ReactNode;

const lin = (id: string, from: string, to: string, x2 = 0, y2 = 1) => (
  <linearGradient id={id} x1="0" y1="0" x2={x2} y2={y2}>
    <stop offset="0" stopColor={from} />
    <stop offset="1" stopColor={to} />
  </linearGradient>
);

/** A tile background: the app's own colours, or the theme's when icons match the wallpaper. */
const tile = (id: string, from: string, to: string) => (
  <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
    <stop offset="0" style={{ stopColor: `var(--icon-a, ${from})` }} />
    <stop offset="1" style={{ stopColor: `var(--icon-b, ${to})` }} />
  </linearGradient>
);
/** Detail colours that follow the theme too: ink (on white parts) and hi (bright lines). */
const ink = (c: string) => `var(--icon-ink, ${c})`;
/** A gradient whose two colours can be swapped by an icon style through CSS variables. */
const vgrad = (id: string, va: string, from: string, vb: string, to: string) => (
  <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
    <stop offset="0" style={{ stopColor: `var(${va}, ${from})` }} />
    <stop offset="1" style={{ stopColor: `var(${vb}, ${to})` }} />
  </linearGradient>
);
const hi = (c: string) => `var(--icon-hi, ${c})`;

const ICONS: Record<string, Draw> = {
  files: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#ff7ab6', '#a855f7')}
        {vgrad(g('back'), '--icon-soft-a', '#ffd6ec', '--icon-soft-b', '#f9a8d4')}
        {lin(g('front'), '#ffffff', '#f3e8ff')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M13 21a4 4 0 0 1 4-4h9.5c1.2 0 2.3.5 3 1.4L32 21h15a4 4 0 0 1 4 4v3H13Z" fill={`url(#${g('back')})`} />
      <rect x="13" y="25" width="38" height="24" rx="4.5" fill="#000" opacity=".16" transform="translate(0 2)" />
      <rect x="13" y="25" width="38" height="24" rx="4.5" fill={`url(#${g('front')})`} />
      <rect x="18" y="31" width="12" height="3" rx="1.5" fill={ink('#d8b4fe')} />
    </>
  ),
  photos: (g) => (
    <>
      <defs>
        {tile(g('sky'), '#bae6fd', '#38bdf8')}
        {lin(g('m1'), '#6ee7b7', '#10b981')}
        {lin(g('m2'), '#34d399', '#047857')}
        <clipPath id={g('clip')}>
          <rect width="64" height="64" rx="15" />
        </clipPath>
      </defs>
      <g clipPath={`url(#${g('clip')})`}>
        <rect width="64" height="64" fill={`url(#${g('sky')})`} />
        <circle cx="45" cy="19" r="11" fill="#fef3c7" opacity=".35" />
        <circle cx="45" cy="19" r="7" fill={hi('#fde68a')} />
        <path d="M-2 50 20 26l14 15 8-8 24 21v14H-2Z" fill={`url(#${g('m1')})`} />
        <path d="M-2 56 24 36l20 16 22-6v22H-2Z" fill={`url(#${g('m2')})`} />
        <path d="M0 58c12-4 26-4 40-1s20 2 24 0v8H0Z" fill="#0ea5e9" opacity=".55" />
      </g>
    </>
  ),
  appcenter: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#60a5fa', '#2563eb')}
        {lin(g('bag'), '#ffffff', '#dbeafe')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M24 24v-3a8 8 0 0 1 16 0v3" stroke={hi('#e0f2fe')} strokeWidth="3.5" strokeLinecap="round" fill="none" />
      <path d="M16 24h32l-2.4 23.5A4 4 0 0 1 41.6 51H22.4a4 4 0 0 1-4-3.5Z" fill="#000" opacity=".18" transform="translate(0 2)" />
      <path d="M16 24h32l-2.4 23.5A4 4 0 0 1 41.6 51H22.4a4 4 0 0 1-4-3.5Z" fill={`url(#${g('bag')})`} />
      <path d="M26 33v4a6 6 0 0 0 12 0v-4" stroke={ink('#3b82f6')} strokeWidth="3.4" strokeLinecap="round" fill="none" />
    </>
  ),
  settings: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#f1f5f9', '#94a3b8')}
        {vgrad(g('gear'), '--icon-gear-a', '#64748b', '--icon-gear-b', '#1e293b')}
        {vgrad(g('hub'), '--icon-hub-a', '#f8fafc', '--icon-hub-b', '#cbd5e1')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <g transform="translate(32 33)">
        <g fill="#000" opacity=".15" transform="translate(0 2)">
          {[0, 45, 90, 135, 180, 225, 270, 315].map((a) => (
            <rect key={a} x="-4" y="-21" width="8" height="10" rx="2" transform={`rotate(${a})`} />
          ))}
          <circle r="15" />
        </g>
        <g fill={`url(#${g('gear')})`}>
          {[0, 45, 90, 135, 180, 225, 270, 315].map((a) => (
            <rect key={a} x="-4" y="-21" width="8" height="10" rx="2" transform={`rotate(${a})`} />
          ))}
          <circle r="15" />
        </g>
        <circle r="7" fill={`url(#${g('hub')})`} />
        <circle r="3" fill={ink('#475569')} />
      </g>
    </>
  ),
  monitor: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#1f2937', '#030712')}
        <linearGradient id={g('area')} x1="0" y1="0" x2="0" y2="1"><stop offset="0" style={{ stopColor: hi('#4ade80') }} /><stop offset="1" style={{ stopColor: hi('#4ade80'), stopOpacity: 0 }} /></linearGradient>
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      {[22, 32, 42].map((y) => (
        <line key={y} x1="12" x2="52" y1={y} y2={y} stroke="#fff" strokeOpacity=".08" />
      ))}
      <path d="M12 40 20 36l7 4 8-14 7 8 10-10v28H12Z" fill={`url(#${g('area')})`} opacity=".45" />
      <path d="M12 40 20 36l7 4 8-14 7 8 10-10" stroke={hi('#4ade80')} strokeWidth="3.4" strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <circle cx="52" cy="24" r="3.4" fill={hi('#bbf7d0')} />
    </>
  ),
  terminal: (g) => (
    <>
      <defs>{tile(g('bg'), '#374151', '#0b0f17')}</defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <circle cx="16" cy="15" r="2.6" fill="#f87171" />
      <circle cx="24" cy="15" r="2.6" fill="#fbbf24" />
      <circle cx="32" cy="15" r="2.6" fill={hi('#4ade80')} />
      <path d="m16 30 8 6-8 6" stroke={hi('#4ade80')} strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <rect x="28" y="40" width="16" height="4" rx="2" fill="#e5e7eb" />
    </>
  ),
  assistant: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#a78bfa', '#4f46e5')}
        {lin(g('star'), '#ffffff', '#e0e7ff')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M28 12c1.3 10.2 6 15 16 16.3-10 1.3-14.7 6-16 16.3-1.3-10.2-6-15-16-16.3 10-1.3 14.7-6 16-16.3Z" fill={`url(#${g('star')})`} />
      <path d="M46 36c.7 5 3 7.4 8 8-5 .7-7.3 3-8 8-.7-5-3-7.3-8-8 5-.6 7.3-3 8-8Z" fill={hi('#fbcfe8')} />
      <circle cx="47" cy="17" r="3" fill={hi('#c7d2fe')} />
    </>
  ),
  backups: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#34d399', '#0f766e')}
        {lin(g('face'), '#ffffff', '#d1fae5')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M32 11a21 21 0 1 1-20.2 26.6" stroke={hi('#a7f3d0')} strokeWidth="4" strokeLinecap="round" fill="none" />
      <path d="M8.5 31.5 11 41.6l9-5.8Z" fill={hi('#a7f3d0')} />
      <circle cx="32" cy="33" r="14" fill="#000" opacity=".15" />
      <circle cx="32" cy="32" r="14" fill={`url(#${g('face')})`} />
      <path d="M32 24v8.5l6 3.5" stroke={ink('#0f766e')} strokeWidth="3.4" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </>
  ),
  storage: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#93c5fd', '#2563eb')}
        {lin(g('box'), '#ffffff', '#e2e8f0')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      {[14, 36].map((y) => (
        <g key={y}>
          <rect x="12" y={y + 2} width="40" height="15" rx="4" fill="#000" opacity=".16" />
          <rect x="12" y={y} width="40" height="15" rx="4" fill={`url(#${g('box')})`} />
          <rect x="17" y={y + 6} width="16" height="3" rx="1.5" fill={ink('#cbd5e1')} />
          <circle cx="45" cy={y + 7.5} r="2.6" fill={y === 36 ? hi('#22c55e') : ink('#60a5fa')} />
        </g>
      ))}
    </>
  ),
  containers: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#5eead4', '#0e7490')}
        {lin(g('top'), '#ffffff', '#ccfbf1')}
        {lin(g('left'), '#99f6e4', '#5eead4')}
        {lin(g('right'), '#2dd4bf', '#0d9488')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M32 12 50 22v20L32 52 14 42V22Z" fill="#000" opacity=".15" transform="translate(0 2)" />
      <path d="M32 12 50 22 32 32 14 22Z" fill={`url(#${g('top')})`} />
      <path d="M14 22 32 32v20L14 42Z" fill={`url(#${g('left')})`} />
      <path d="M50 22 32 32v20l18-10Z" fill={`url(#${g('right')})`} />
    </>
  ),
  browser: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#fb923c', '#dc2626')}
        {lin(g('shield'), '#ffffff', '#ffedd5')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M32 10 50 16l-1.4 18c-.9 9-7.4 15.5-16.6 19.5C22.8 49.5 16.3 43 15.4 34L14 16Z" fill="#000" opacity=".15" transform="translate(0 2)" />
      <path d="M32 10 50 16l-1.4 18c-.9 9-7.4 15.5-16.6 19.5C22.8 49.5 16.3 43 15.4 34L14 16Z" fill={`url(#${g('shield')})`} />
      <path d="M32 19 41 22.2 40.2 32c-.5 4.7-3.6 8.3-8.2 10.6Z" fill={ink('#fb923c')} />
      <path d="M32 19 23 22.2 23.8 32c.5 4.7 3.6 8.3 8.2 10.6Z" fill={ink('#fdba74')} />
    </>
  ),
  remotedesktop: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#818cf8', '#3730a3')}
        {lin(g('screen'), '#38bdf8', '#1d4ed8')}
        {lin(g('frame'), '#ffffff', '#e2e8f0')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <rect x="10" y="14" width="44" height="30" rx="4" fill="#000" opacity=".16" transform="translate(0 2)" />
      <rect x="10" y="14" width="44" height="30" rx="4" fill={`url(#${g('frame')})`} />
      <rect x="13.5" y="17.5" width="37" height="23" rx="2" fill={`url(#${g('screen')})`} />
      <path d="M28 44h8l2 6H26Z" fill={ink('#cbd5e1')} />
      <rect x="22" y="50" width="20" height="3.4" rx="1.7" fill="#e2e8f0" />
      <path d="m29 23 10 6-4.4 1.2 2.6 5-2.2 1.1-2.6-5L29 34Z" fill="#fff" />
    </>
  ),
  scripts: (g) => (
    <>
      <defs>
        {tile(g('bg'), '#86efac', '#15803d')}
        {lin(g('page'), '#ffffff', '#f0fdf4')}
      </defs>
      <rect width="64" height="64" rx="15" fill={`url(#${g('bg')})`} />
      <path d="M18 10h20l10 10v30a4 4 0 0 1-4 4H18a4 4 0 0 1-4-4V14a4 4 0 0 1 4-4Z" fill="#000" opacity=".15" transform="translate(0 2)" />
      <path d="M18 10h20l10 10v30a4 4 0 0 1-4 4H18a4 4 0 0 1-4-4V14a4 4 0 0 1 4-4Z" fill={`url(#${g('page')})`} />
      <path d="M38 10v7a3 3 0 0 0 3 3h7Z" fill={hi('#bbf7d0')} />
      <path d="m26 30-5 5 5 5M36 30l5 5-5 5" stroke={ink('#16a34a')} strokeWidth="3.2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
    </>
  ),
};

/** A NoCapOS app's illustrated icon, or null when it has none. */
export function AppArt({ id, size }: { id?: string; size: number }) {
  const uid = useId().replace(/:/g, '');
  const draw = id ? ICONS[id] : undefined;
  if (!draw) return null;
  return (
    <svg className="app-art3d" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      {draw((name) => `${uid}-${name}`)}
      <rect width="64" height="32" rx="15" fill="#fff" opacity=".08" />
    </svg>
  );
}

export const hasArt = (id?: string) => !!id && id in ICONS;
