import { useId, type ReactNode } from 'react';
import { usePrefs } from '../state/prefs';

// NoCapOS's own app icons, drawn on a 64×64 grid: one glossy, colourful
// object per app, shown on a dark glass tile (soft shine at the top, hairline
// edge) or on its own; or, in the "Shapes" style, abstract shapes on colour.
// `g` turns a local gradient name into one unique to this icon, so many
// icons can share a page.

type G = (name: string) => string;
type Draw = (g: G) => ReactNode;

const lin = (id: string, from: string, to: string, x2 = 0, y2 = 1) => (
  <linearGradient id={id} x1="0" y1="0" x2={x2} y2={y2}>
    <stop offset="0" stopColor={from} />
    <stop offset="1" stopColor={to} />
  </linearGradient>
);
const url = (id: string) => `url(#${id})`;

/** The shared dark glass tile every icon sits on. */
const Tile = ({ g }: { g: G }) => (
  <>
    <defs>
      {lin(g('tile'), '#3b3e49', '#1a1c22')}
      <linearGradient id={g('shine')} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#fff" stopOpacity=".2" />
        <stop offset=".5" stopColor="#fff" stopOpacity="0" />
      </linearGradient>
    </defs>
    <rect x="1" y="1" width="62" height="62" rx="15" fill={url(g('tile'))} />
    <rect x="1" y="1" width="62" height="62" rx="15" fill={url(g('shine'))} />
    <rect x="1.5" y="1.5" width="61" height="61" rx="14.5" fill="none" stroke="#fff" strokeOpacity=".14" />
  </>
);

/** A soft white gloss band across the top of an object. */
const gloss = (d: string) => <path d={d} fill="#fff" opacity=".28" />;

/** A gear with eight teeth. */
const gear = (cx: number, cy: number, r: number, fill: string) => (
  <>
    {Array.from({ length: 8 }, (_, i) => (
      <rect key={i} x={cx - 3.5} y={cy - r - 5} width="7" height="9" rx="2" fill={fill} transform={`rotate(${i * 45} ${cx} ${cy})`} />
    ))}
    <circle cx={cx} cy={cy} r={r} fill={fill} />
  </>
);

const ICONS: Record<string, Draw> = {
  launchpad: (g) => (
    <>
      <defs>
        {(
          [
            ['a', '#ff8a8a', '#ef4444'],
            ['b', '#ffd36b', '#f59e0b'],
            ['c', '#86efac', '#16a34a'],
            ['d', '#7dd3fc', '#0284c7'],
            ['e', '#c4b5fd', '#7c3aed'],
            ['f', '#f9a8d4', '#db2777'],
          ] as const
        ).map(([k, a, b]) => lin(g(k), a, b, 0, 1))}
      </defs>
      {['a', 'b', 'c', 'd', 'e', 'f', 'c', 'd', 'a'].map((k, i) => {
        const x = 13 + (i % 3) * 14;
        const y = 13 + Math.floor(i / 3) * 14;
        return (
          <g key={i}>
            <rect x={x} y={y} width="10.5" height="10.5" rx="3" fill={url(g(k))} />
            <rect x={x} y={y} width="10.5" height="3.5" rx="1.75" fill="#fff" opacity=".3" />
          </g>
        );
      })}
    </>
  ),
  files: (g) => (
    <>
      <defs>
        {lin(g('back'), '#3f8cf0', '#1e40af')}
        {lin(g('front'), '#6cb8ff', '#2563eb')}
      </defs>
      <path d="M11 19a4 4 0 0 1 4-4h11l4 4h19a4 4 0 0 1 4 4v3H11Z" fill={url(g('back'))} />
      <rect x="16" y="20" width="32" height="10" rx="2" fill="#f8fafc" />
      <rect x="19" y="17.5" width="26" height="8" rx="2" fill="#dbe7f7" />
      <path d="M11 25h42v20a4 4 0 0 1-4 4H15a4 4 0 0 1-4-4Z" fill={url(g('front'))} />
      {gloss('M11 25h42v4H11Z')}
    </>
  ),
  photos: (g) => (
    <>
      <defs>{lin(g('disc'), '#ffd36b', '#ff5f6d', 1, 1)}</defs>
      <circle cx="32" cy="32" r="19" fill={url(g('disc'))} />
      <circle cx="25.5" cy="26" r="5" fill="#fff7d6" />
      <path d="M14.5 40 24 30.5l7.5 7.5 5-4.5 12.5 10A19 19 0 0 1 14.5 40Z" fill="#fff" opacity=".88" />
      {gloss('M17 24a17 17 0 0 1 30 0 19 9 0 0 0-30 0Z')}
    </>
  ),
  appcenter: (g) => (
    <>
      <defs>{lin(g('bag'), '#a78bfa', '#6d28d9')}</defs>
      <path d="M24 23v-3a8 8 0 0 1 16 0v3" fill="none" stroke="#c4b5fd" strokeWidth="3.2" strokeLinecap="round" />
      <path d="M15 23h34l-2.5 25a3 3 0 0 1-3 2.7h-23a3 3 0 0 1-3-2.7Z" fill={url(g('bag'))} />
      {gloss('M15 23h34l-.4 4H15.4Z')}
      <path d="m32 30.5 2.2 4.6 5 .6-3.7 3.4 1 5-4.5-2.6-4.5 2.6 1-5-3.7-3.4 5-.6Z" fill="#fff" />
    </>
  ),
  settings: (g) => (
    <>
      <defs>{lin(g('metal'), '#f8fafc', '#94a3b8', 1, 1)}</defs>
      {gear(32, 32, 14, url(g('metal')))}
      <circle cx="32" cy="32" r="6" fill="#1f2937" />
      <circle cx="32" cy="32" r="2.5" fill="#64748b" />
    </>
  ),
  monitor: (g) => (
    <>
      <defs>{lin(g('bar'), '#fb7185', '#e11d48')}</defs>
      <rect x="12" y="38" width="7" height="12" rx="2" fill={url(g('bar'))} opacity=".55" />
      <rect x="23" y="28" width="7" height="22" rx="2" fill={url(g('bar'))} opacity=".75" />
      <rect x="34" y="18" width="7" height="32" rx="2" fill={url(g('bar'))} />
      <rect x="45" y="32" width="7" height="18" rx="2" fill={url(g('bar'))} opacity=".7" />
      <path d="M12 30 24 22l11 5 16-12" fill="none" stroke="#fff" strokeOpacity=".85" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
    </>
  ),
  terminal: (g) => (
    <>
      <defs>{lin(g('ink'), '#34d399', '#059669')}</defs>
      <rect x="11" y="15" width="42" height="34" rx="6" fill="#0b0f15" stroke="#fff" strokeOpacity=".12" />
      <circle cx="17" cy="20.5" r="1.6" fill="#ef4444" />
      <circle cx="22" cy="20.5" r="1.6" fill="#f59e0b" />
      <circle cx="27" cy="20.5" r="1.6" fill="#22c55e" />
      <path d="m19 29 7 5-7 5" fill="none" stroke={url(g('ink'))} strokeWidth="3.4" strokeLinecap="round" strokeLinejoin="round" />
      <rect x="29" y="38" width="14" height="3.4" rx="1.7" fill={url(g('ink'))} />
    </>
  ),
  assistant: (g) => (
    <>
      <defs>{lin(g('spark'), '#f0abfc', '#7c3aed', 1, 1)}</defs>
      <path d="M30 12c1.6 9.5 5.5 13.4 15 15-9.5 1.6-13.4 5.5-15 15-1.6-9.5-5.5-13.4-15-15 9.5-1.6 13.4-5.5 15-15Z" fill={url(g('spark'))} />
      <path d="M45 36c.8 4.5 2.5 6.2 7 7-4.5.8-6.2 2.5-7 7-.8-4.5-2.5-6.2-7-7 4.5-.8 6.2-2.5 7-7Z" fill="#fde68a" />
      {gloss('M30 12c.9 5 2.4 8.4 4.8 10.6-2 .4-3.7 1.2-4.8 2.4-1.1-1.2-2.8-2-4.8-2.4C27.6 20.4 29.1 17 30 12Z')}
    </>
  ),
  backups: (g) => (
    <>
      <defs>{lin(g('ring'), '#fcd34d', '#f59e0b')}</defs>
      <circle cx="32" cy="33" r="17" fill="none" stroke={url(g('ring'))} strokeWidth="5" />
      <path d="M15 20v9.5h9.5" fill="none" stroke={url(g('ring'))} strokeWidth="4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M32 24v10l6.5 4" fill="none" stroke="#fff" strokeWidth="3.4" strokeLinecap="round" strokeLinejoin="round" />
    </>
  ),
  storage: (g) => (
    <>
      <defs>{lin(g('drive'), '#5eead4', '#0d9488')}</defs>
      {[14, 27, 40].map((y) => (
        <g key={y}>
          <rect x="12" y={y} width="40" height="11" rx="3.5" fill={url(g('drive'))} />
          <rect x="12" y={y} width="40" height="3.5" rx="1.75" fill="#fff" opacity=".25" />
          <circle cx="45" cy={y + 6} r="1.8" fill="#ecfeff" />
          <rect x="17" y={y + 5} width="14" height="2.2" rx="1.1" fill="#134e4a" opacity=".55" />
        </g>
      ))}
    </>
  ),
  containers: (g) => (
    <>
      <defs>
        {lin(g('top'), '#7dd3fc', '#38bdf8')}
        {lin(g('left'), '#2563eb', '#1d4ed8')}
        {lin(g('right'), '#0ea5e9', '#0369a1')}
      </defs>
      <path d="M32 12 50 21.5 32 31 14 21.5Z" fill={url(g('top'))} />
      <path d="M14 21.5 32 31v21L14 42.5Z" fill={url(g('left'))} />
      <path d="M50 21.5 32 31v21l18-9.5Z" fill={url(g('right'))} />
      <path d="M23 26.2v10M41 26.2v10" stroke="#fff" strokeOpacity=".35" strokeWidth="2" strokeLinecap="round" />
    </>
  ),
  browser: (g) => (
    <>
      <defs>{lin(g('globe'), '#fdba74', '#ea580c', 1, 1)}</defs>
      <circle cx="32" cy="32" r="19" fill={url(g('globe'))} />
      <g fill="none" stroke="#fff" strokeOpacity=".8" strokeWidth="2">
        <ellipse cx="32" cy="32" rx="8" ry="19" />
        <path d="M13 32h38M16 22h32M16 42h32" />
      </g>
      {gloss('M15 26a18 18 0 0 1 34 0 19 8 0 0 0-34 0Z')}
    </>
  ),
  remotedesktop: (g) => (
    <>
      <defs>{lin(g('screen'), '#93c5fd', '#2563eb')}</defs>
      <rect x="9" y="13" width="46" height="31" rx="4" fill="#0f172a" />
      <rect x="11.5" y="15.5" width="41" height="26" rx="2.5" fill={url(g('screen'))} />
      <path d="M26 52h12l-1.5-8h-9Z" fill="#475569" />
      <path d="m30 22 13 6.5-5.6 1.6 3.4 5.6-3 1.7-3.4-5.6-4.1 4Z" fill="#fff" />
    </>
  ),
  virtualdesk: (g) => (
    <>
      <defs>
        {lin(g('front'), '#67e8f9', '#0284c7')}
        {lin(g('back'), '#a5f3fc', '#22d3ee')}
      </defs>
      <rect x="22" y="12" width="31" height="21" rx="3.5" fill={url(g('back'))} opacity=".75" />
      <rect x="11" y="21" width="33" height="23" rx="3.5" fill={url(g('front'))} />
      {gloss('M11 24.5a3.5 3.5 0 0 1 3.5-3.5h26a3.5 3.5 0 0 1 3.5 3.5V27H11Z')}
      <rect x="15" y="29" width="9" height="6" rx="1" fill="#fff" opacity=".9" />
      <rect x="27" y="29" width="13" height="2.4" rx="1.2" fill="#fff" opacity=".7" />
      <rect x="27" y="33" width="9" height="2.4" rx="1.2" fill="#fff" opacity=".5" />
      <path d="M21 52h13M27.5 44v8" stroke="#94a3b8" strokeWidth="3" strokeLinecap="round" />
    </>
  ),
  scripts: (g) => (
    <>
      <defs>{lin(g('page'), '#bef264', '#65a30d')}</defs>
      <path d="M16 12h22l10 10v28a3 3 0 0 1-3 3H16a3 3 0 0 1-3-3V15a3 3 0 0 1 3-3Z" fill={url(g('page'))} />
      <path d="M38 12v7a3 3 0 0 0 3 3h7Z" fill="#ecfccb" />
      <path d="M26 30v14l11-7Z" fill="#fff" />
      {gloss('M13 15a3 3 0 0 1 3-3h22v4H13Z')}
    </>
  ),
  viewer: (g) => (
    <>
      <defs>{lin(g('page'), '#f8fafc', '#cbd5e1')}</defs>
      <path d="M17 11h20l10 10v29a3 3 0 0 1-3 3H17a3 3 0 0 1-3-3V14a3 3 0 0 1 3-3Z" fill={url(g('page'))} />
      <path d="M37 11v7a3 3 0 0 0 3 3h7Z" fill="#94a3b8" />
      {[27, 33, 39, 45].map((y, i) => (
        <rect key={y} x="20" y={y} width={i === 3 ? 12 : 20} height="2.6" rx="1.3" fill={i === 0 ? '#6366f1' : '#64748b'} />
      ))}
    </>
  ),
  logs: (g) => (
    <>
      <defs>{lin(g('panel'), '#cbd5e1', '#64748b')}</defs>
      <rect x="11" y="13" width="42" height="38" rx="5" fill="#0b0f15" stroke="#fff" strokeOpacity=".12" />
      {[20, 27, 34, 41].map((y, i) => (
        <g key={y}>
          <circle cx="18" cy={y + 1.3} r="1.7" fill={['#22c55e', '#38bdf8', '#f59e0b', '#ef4444'][i]} />
          <rect x="23" y={y} width={[24, 18, 22, 14][i]} height="2.6" rx="1.3" fill={url(g('panel'))} />
        </g>
      ))}
    </>
  ),
};

// A virtual machine's screen window wears Virtual Desk's icon.
ICONS.vmscreen = ICONS.virtualdesk;

// The "Shapes" style: each app as a simple composition of shapes on its colour.
const bg = (c: string) => <rect width="64" height="64" rx="15" fill={c} />;
const SHAPES: Record<string, () => ReactNode> = {
  launchpad: () => (<>{bg('#b91c1c')}{Array.from({ length: 9 }, (_, i) => <rect key={i} x={14 + (i % 3) * 13} y={14 + Math.floor(i / 3) * 13} width="10" height="10" rx="3" fill="#fff" opacity={i % 2 ? 0.6 : 0.95} />)}</>),
  files: () => (<>{bg('#1d4ed8')}<rect x="12" y="20" width="40" height="28" rx="4" fill="#60a5fa" /><rect x="12" y="16" width="18" height="8" rx="3" fill="#93c5fd" /></>),
  photos: () => (<>{bg('#fff7ed')}<circle cx="24" cy="26" r="11" fill="#f97316" /><circle cx="40" cy="26" r="11" fill="#ec4899" opacity=".85" /><circle cx="32" cy="40" r="11" fill="#8b5cf6" opacity=".85" /></>),
  appcenter: () => (<>{bg('#6d28d9')}<circle cx="32" cy="22" r="9" fill="none" stroke="#ede9fe" strokeWidth="4" /><rect x="14" y="22" width="36" height="28" rx="6" fill="#a78bfa" /></>),
  settings: () => (<>{bg('#334155')}<circle cx="32" cy="32" r="17" fill="none" stroke="#cbd5e1" strokeWidth="6" strokeDasharray="7 4" /><circle cx="32" cy="32" r="6" fill="#f8fafc" /></>),
  monitor: () => (<>{bg('#9f1239')}{[14, 26, 38].map((x, i) => <rect key={x} x={x} y={40 - i * 10} width="9" height={12 + i * 10} rx="3" fill="#fda4af" opacity={0.6 + i * 0.2} />)}</>),
  terminal: () => (<>{bg('#020617')}<rect x="14" y="20" width="12" height="12" rx="2" fill="#22c55e" /><rect x="28" y="34" width="22" height="6" rx="3" fill="#22c55e" opacity=".6" /><rect x="14" y="44" width="30" height="4" rx="2" fill="#22c55e" opacity=".3" /></>),
  assistant: () => (<>{bg('#7c3aed')}<path d="M29 13c1.5 9 5 12.5 14 14-9 1.5-12.5 5-14 14-1.5-9-5-12.5-14-14 9-1.5 12.5-5 14-14Z" fill="#f5d0fe" /><circle cx="46" cy="44" r="5" fill="#fde68a" /></>),
  backups: () => (<>{bg('#b45309')}<circle cx="32" cy="32" r="17" fill="none" stroke="#fde68a" strokeWidth="6" opacity=".45" /><path d="M32 15a17 17 0 0 1 17 17" fill="none" stroke="#fde68a" strokeWidth="6" strokeLinecap="round" /><circle cx="32" cy="32" r="5" fill="#fff7ed" /></>),
  storage: () => (<>{bg('#0f766e')}{[14, 27, 40].map((y, i) => <rect key={y} x="13" y={y} width="38" height="10" rx="5" fill="#5eead4" opacity={1 - i * 0.25} />)}</>),
  containers: () => (<>{bg('#1e3a8a')}<rect x="13" y="13" width="17" height="17" rx="3" fill="#60a5fa" /><rect x="34" y="13" width="17" height="17" rx="3" fill="#93c5fd" /><rect x="13" y="34" width="17" height="17" rx="3" fill="#93c5fd" /><rect x="34" y="34" width="17" height="17" rx="3" fill="#dbeafe" /></>),
  browser: () => (<>{bg('#c2410c')}<circle cx="32" cy="32" r="18" fill="#fdba74" /><path d="M32 14a18 18 0 0 1 0 36Z" fill="#fff7ed" opacity=".7" /></>),
  remotedesktop: () => (<>{bg('#3730a3')}<rect x="11" y="15" width="42" height="28" rx="4" fill="#a5b4fc" /><path d="m34 30 14 6-6 2 3 6-3 1.5-3-6-5 4Z" fill="#fff" /><rect x="26" y="47" width="12" height="4" rx="2" fill="#a5b4fc" /></>),
  virtualdesk: () => (<>{bg('#0e7490')}<rect x="22" y="14" width="28" height="20" rx="4" fill="#67e8f9" opacity=".6" /><rect x="14" y="24" width="28" height="20" rx="4" fill="#cffafe" /><rect x="24" y="48" width="8" height="4" rx="2" fill="#cffafe" /></>),
  scripts: () => (<>{bg('#3f6212')}<path d="M22 16v32l26-16Z" fill="#bef264" /><rect x="14" y="44" width="16" height="5" rx="2.5" fill="#ecfccb" opacity=".6" /></>),
  viewer: () => (<>{bg('#475569')}<rect x="16" y="11" width="32" height="42" rx="5" fill="#e2e8f0" />{[21, 28, 35, 42].map((y, i) => <rect key={y} x="22" y={y} width={i === 3 ? 12 : 20} height="3" rx="1.5" fill="#64748b" />)}</>),
  logs: () => (<>{bg('#111827')}{[16, 25, 34, 43].map((y, i) => <rect key={y} x="14" y={y} width={[36, 26, 32, 18][i]} height="5" rx="2.5" fill={['#22c55e', '#38bdf8', '#f59e0b', '#ef4444'][i]} />)}</>),
};
SHAPES.vmscreen = SHAPES.virtualdesk;

/**
 * A NoCapOS app's icon in the chosen style, or null when it has none:
 * "tiles" puts the object on a dark glass tile, "objects" shows it on its
 * own, slightly larger, and "shapes" draws the app as shapes on its colour.
 */
export function AppArt({ id, size }: { id?: string; size: number }) {
  const uid = useId().replace(/:/g, '');
  const style = usePrefs((s) => s.iconStyle);
  const draw = id ? ICONS[id] : undefined;
  if (!draw || !id) return null;
  const g: G = (name) => `${uid}-${name}`;
  const shape = style === 'shapes' ? SHAPES[id] : undefined;
  return (
    <svg className="app-art3d" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      {shape ? (
        shape()
      ) : style === 'objects' ? (
        <g transform="translate(32 32) scale(1.16) translate(-32 -32)">{draw(g)}</g>
      ) : (
        <>
          <Tile g={g} />
          {draw(g)}
        </>
      )}
    </svg>
  );
}

export const hasArt = (id?: string) => !!id && id in ICONS;
