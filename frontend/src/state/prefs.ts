import { create } from 'zustand';

export type ThemeMode = 'auto' | 'light' | 'dark';

/** Photo-style wallpapers (in /wallpapers) bring their own accent for the Glass theme. */
export const WALLPAPERS = [
  { id: 'tide', name: 'Tide', image: true, accent: '#2dd4bf' },
  { id: 'nebula', name: 'Nebula', image: true, accent: '#a78bfa' },
  { id: 'blaze', name: 'Blaze', image: true, accent: '#fb923c' },
  { id: 'lights', name: 'Lights', image: true, accent: '#34d399' },
  { id: 'glacier', name: 'Glacier', image: true, accent: '#60a5fa' },
  { id: 'rose', name: 'Rose', image: true, accent: '#f472b6' },
  { id: 'meadow', name: 'Meadow', image: true, accent: '#84cc16' },
  { id: 'dune', name: 'Dune', image: true, accent: '#f59e0b' },
  { id: 'midnight', name: 'Midnight', image: true, accent: '#818cf8' },
  { id: 'signal', name: 'Signal', image: true, accent: '#ef4444' },
  { id: 'nocap', name: 'NoCap', image: false, accent: '#e8232b' },
  { id: 'aurora', name: 'Aurora', image: false, accent: '#8b7bff' },
  { id: 'dusk', name: 'Dusk', image: false, accent: '#ff6a88' },
  { id: 'ocean', name: 'Ocean', image: false, accent: '#3b9cff' },
  { id: 'forest', name: 'Forest', image: false, accent: '#2fbf87' },
  { id: 'ember', name: 'Ember', image: false, accent: '#f0772b' },
  { id: 'graphite', name: 'Graphite', image: false, accent: '#8a94a8' },
] as const;
export type WallpaperId = (typeof WALLPAPERS)[number]['id'] | 'custom';

// Idle before auto-lock, in minutes. 0 = never.
export const LOCK_TIMEOUTS = [
  { label: 'Never', value: 0 },
  { label: '1 minute', value: 1 },
  { label: '5 minutes', value: 5 },
  { label: '15 minutes', value: 15 },
  { label: '30 minutes', value: 30 },
  { label: '1 hour', value: 60 },
] as const;

export type Screensaver = 'off' | 'clock' | 'starfield';
export const SCREENSAVERS: { id: Screensaver; name: string }[] = [
  { id: 'off', name: 'Off' },
  { id: 'clock', name: 'Clock' },
  { id: 'starfield', name: 'Starfield' },
];
// Idle before the screensaver starts, in minutes.
export const SAVER_DELAYS = [1, 2, 5, 10] as const;

export const LANGUAGES = [{ id: 'en', name: 'English' }] as const;
export type LanguageId = (typeof LANGUAGES)[number]['id'];

export type DockPosition = 'bottom' | 'left' | 'right';
export type Magnify = 'off' | 'small' | 'large';
export type IconSize = 'small' | 'medium' | 'large';
export type TitleButtons = 'right' | 'left';
export type TitleDoubleClick = 'maximize' | 'minimize' | 'none';
export type WeekStart = 'sun' | 'mon';
/** NoCapOS app icons: frosted glass, tinted with the wallpaper's colour, or each app's own colours. */
export type IconStyle = 'glass' | 'tinted' | 'colorful';
export const ICON_STYLES: { id: IconStyle; label: string }[] = [
  { id: 'glass', label: 'Glass' },
  { id: 'tinted', label: 'Wallpaper colour' },
  { id: 'colorful', label: 'Colourful' },
];

/** How system notifications look: like macOS banners or Windows toasts. 'auto' follows this device. */
export type NotifyStyle = 'auto' | 'mac' | 'windows';
export const NOTIFY_STYLES: { id: NotifyStyle; label: string }[] = [
  { id: 'auto', label: 'Automatic' },
  { id: 'mac', label: 'macOS' },
  { id: 'windows', label: 'Windows' },
];

/** Whole-OS visual themes: frosted Glass (default), the original look, and the Cyber-Deck FUI theme. */
export type UiTheme = 'glass' | 'classic' | 'cyberdeck';
export const UI_THEMES: { id: UiTheme; name: string; blurb: string; accent: string; swatch: [string, string, string] }[] = [
  { id: 'glass', name: 'Glass', blurb: 'Frosted glass over your wallpaper · the accent follows the wallpaper unless you pick one', accent: '#2dd4bf', swatch: ['#2dd4bf', '#ffffff', '#0b1a24'] },
  { id: 'classic', name: 'NoCap', blurb: 'Steel and signal red, like the logo · follows light or dark mode', accent: '#e8232b', swatch: ['#e8232b', '#9ca1ab', '#16181d'] },
  { id: 'cyberdeck', name: 'Cyber-Deck', blurb: 'Frosted glass, sharp edges, cyan / purple / amber neon', accent: '#00f3ff', swatch: ['#00f3ff', '#d300ff', '#ffaa00'] },
];

export interface PrefValues {
  theme: ThemeMode;
  uiTheme: UiTheme;
  uiSounds: boolean; // master switch for sound effects
  soundVolume: number; // 0–100
  soundClicks: boolean;
  soundWindows: boolean; // windows opening / closing
  soundAlerts: boolean; // notifications
  soundLock: boolean; // lock, unlock, sign-in
  focusMode: boolean; // do not disturb: hide notifications and their sounds
  notifyStyle: NotifyStyle; // banner look: macOS, Windows, or by device
  brightness: number; // 30–100 %, a software dimmer over NoCapOS
  fxGrid: boolean; // data-grid backdrop on FUI themes
  wallpaper: WallpaperId;
  wallpaperDim: number; // 0–60 %
  accent: string;
  reduceTransparency: boolean;
  iconStyle: IconStyle; // how NoCapOS's own app icons look
  reduceMotion: boolean;
  squareCorners: boolean;
  clock24: boolean;
  clockSeconds: boolean;
  clockDate: boolean; // show the date next to the time in the dock
  timeZone: string; // '' = this device's time zone
  weekStart: WeekStart;

  dockPosition: DockPosition;
  dockSize: number; // icon px, 32–64
  dockMagnify: Magnify;
  dockAutoHide: boolean;
  dockRecents: boolean;
  dockIndicators: boolean;

  widgets: boolean;
  iconSize: IconSize;
  iconLabels: boolean;
  iconSnap: boolean;

  titleButtons: TitleButtons;
  titleDoubleClick: TitleDoubleClick;

  termFontSize: number; // px
  termCrt: boolean; // CRT scanline overlay

  lockTimeout: number; // minutes, 0 = never
  screensaver: Screensaver;
  saverDelay: number; // minutes
  language: LanguageId;
}

export const DEFAULT_PREFS: PrefValues = {
  theme: 'dark',
  uiTheme: 'glass',
  uiSounds: false,
  soundVolume: 50,
  soundClicks: true,
  soundWindows: true,
  soundAlerts: true,
  soundLock: true,
  focusMode: false,
  notifyStyle: 'auto',
  brightness: 100,
  fxGrid: true,
  wallpaper: 'tide',
  wallpaperDim: 0,
  accent: '', // '' = the theme's own accent
  reduceTransparency: false,
  iconStyle: 'glass',
  reduceMotion: false,
  squareCorners: false,
  clock24: false,
  clockSeconds: false,
  clockDate: false,
  timeZone: '',
  weekStart: 'sun',

  dockPosition: 'bottom',
  dockSize: 44,
  dockMagnify: 'small',
  dockAutoHide: false,
  dockRecents: true,
  dockIndicators: true,

  widgets: true,
  iconSize: 'medium',
  iconLabels: true,
  iconSnap: false,

  titleButtons: 'right',
  titleDoubleClick: 'maximize',

  termFontSize: 13,
  termCrt: false,

  lockTimeout: 0,
  screensaver: 'off',
  saverDelay: 5,
  language: 'en',
};

/** The look-and-feel subset that "Reset personalization" restores. */
export const PERSONALIZATION_KEYS: (keyof PrefValues)[] = [
  'theme', 'uiTheme', 'fxGrid', 'wallpaper', 'wallpaperDim', 'accent', 'iconStyle', 'reduceTransparency', 'reduceMotion', 'squareCorners',
  'dockPosition', 'dockSize', 'dockMagnify', 'dockAutoHide', 'dockRecents',
  'dockIndicators', 'widgets', 'iconSize', 'iconLabels', 'iconSnap', 'titleButtons', 'titleDoubleClick', 'notifyStyle',
];

interface Prefs extends PrefValues {
  set: (p: Partial<PrefValues>) => void;
}

const KEY = 'alfa.prefs';

// Browser storage is a per-viewer convenience; it may be unavailable.
function load(): Partial<PrefValues> {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Record<string, unknown>;
    const out: Partial<PrefValues> = {};
    for (const k of Object.keys(DEFAULT_PREFS) as (keyof PrefValues)[]) {
      if (k in raw && typeof raw[k] === typeof DEFAULT_PREFS[k]) (out as Record<string, unknown>)[k] = raw[k];
    }
    // Wallpapers that no longer exist fall back to the default.
    if (out.wallpaper && out.wallpaper !== 'custom' && !WALLPAPERS.some((w) => w.id === out.wallpaper)) delete out.wallpaper;
    // One-time move to the Glass look (people can switch back in Appearance).
    if (!raw.glass1) {
      if (out.uiTheme === 'classic') delete out.uiTheme;
      if (out.wallpaper === 'nocap') delete out.wallpaper;
    }
    if (out.iconStyle && !ICON_STYLES.some((x) => x.id === out.iconStyle)) delete out.iconStyle;
    if (out.notifyStyle && !NOTIFY_STYLES.some((x) => x.id === out.notifyStyle)) delete out.notifyStyle;
    // Themes that no longer exist fall back to the default.
    if (out.uiTheme && !UI_THEMES.some((t) => t.id === out.uiTheme)) delete out.uiTheme;
    // One-time move to the NoCap brand look for people still on the old defaults.
    if (!raw.brand1) {
      if (out.wallpaper === 'aurora') delete out.wallpaper;
      if (out.theme === 'auto') delete out.theme;
    }
    return out;
  } catch {
    return {};
  }
}

export const usePrefs = create<Prefs>((set, get) => ({
  ...DEFAULT_PREFS,
  ...load(),
  set: (p) => {
    set(p);
    const s = get();
    const values = Object.fromEntries((Object.keys(DEFAULT_PREFS) as (keyof PrefValues)[]).map((k) => [k, s[k]]));
    try {
      localStorage.setItem(KEY, JSON.stringify({ ...values, brand1: true, glass1: true }));
    } catch {
      /* ignore */
    }
  },
}));

// ---- custom wallpaper (kept apart from prefs: it's a large image) ----

const WALL_KEY = 'alfa.wallpaper.custom';

export function loadCustomWallpaper(): string | null {
  try {
    return localStorage.getItem(WALL_KEY);
  } catch {
    return null;
  }
}

/** Downscale an image file to at most 2560px and store it as a JPEG data URL. */
export async function saveCustomWallpaper(file: File): Promise<string> {
  const bitmap = await createImageBitmap(file);
  const scale = Math.min(1, 2560 / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement('canvas');
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  canvas.getContext('2d')!.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  for (const q of [0.86, 0.75, 0.6]) {
    const url = canvas.toDataURL('image/jpeg', q);
    try {
      localStorage.setItem(WALL_KEY, url);
      return url;
    } catch {
      /* too big for storage — try a smaller encoding */
    }
  }
  throw new Error('That image is too large to save. Try a smaller one.');
}

// ---- applying prefs to the page ----

/** Dock thickness (icon + padding) — the strip it reserves at a screen edge. */
export const dockThickness = (p: Pick<PrefValues, 'dockSize'>) => p.dockSize + 32;

/** Phones always get a bottom dock that never hides. */
export const isCompact = () => (window.innerWidth || 1280) < 640;

export function effectiveDock(p: PrefValues): { position: DockPosition; autoHide: boolean } {
  return isCompact() ? { position: 'bottom', autoHide: false } : { position: p.dockPosition, autoHide: p.dockAutoHide };
}

export const MENUBAR_H = 30;

/** macOS style (buttons on the left) adds a menu bar along the top. */
export const hasMenuBar = (p: Pick<PrefValues, 'titleButtons'>) => p.titleButtons === 'left' && !isCompact();

/** Space the dock (and menu bar) reserve on each edge (the dock: 0 when it auto-hides). */
export function reserved(p: PrefValues = usePrefs.getState()) {
  const d = effectiveDock(p);
  const t = d.autoHide ? 0 : dockThickness(p);
  return {
    top: hasMenuBar(p) ? MENUBAR_H : 0,
    left: d.position === 'left' ? t : 0,
    right: d.position === 'right' ? t : 0,
    bottom: d.position === 'bottom' ? t : 0,
  };
}

/** Size of the area windows, widgets and desktop icons live in. */
export function workArea(p: PrefValues = usePrefs.getState()) {
  const r = reserved(p);
  return { w: (window.innerWidth || 1280) - r.left - r.right, h: (window.innerHeight || 800) - r.bottom - r.top };
}

const MAGNIFY: Record<Magnify, number> = { off: 1, small: 1.12, large: 1.38 };

/** Applies every look-and-feel pref to <html>/<body>. */
export function applyPrefs(p: PrefValues) {
  const root = document.documentElement;
  const ui = UI_THEMES.find((t) => t.id === p.uiTheme) ?? UI_THEMES[0];
  root.dataset.ui = ui.id;
  // Glass and FUI themes are dark by design; Glass takes its accent from the wallpaper.
  if (ui.id !== 'classic') root.dataset.theme = 'dark';
  else if (p.theme === 'auto') delete root.dataset.theme;
  else root.dataset.theme = p.theme;
  const wall = WALLPAPERS.find((w) => w.id === p.wallpaper);
  root.style.setProperty('--accent', p.accent || (ui.id === 'glass' && wall ? wall.accent : ui.accent));
  root.toggleAttribute('data-grid', ui.id === 'cyberdeck' && p.fxGrid);

  const d = effectiveDock(p);
  const r = reserved(p);
  root.dataset.dock = d.position;
  root.toggleAttribute('data-autohide', d.autoHide);
  root.style.setProperty('--res-top', `${r.top}px`);
  root.toggleAttribute('data-menubar', r.top > 0);
  root.style.setProperty('--res-left', `${r.left}px`);
  root.style.setProperty('--res-right', `${r.right}px`);
  root.style.setProperty('--res-bottom', `${r.bottom}px`);
  root.style.setProperty('--dock-thick', `${dockThickness(p)}px`);
  root.style.setProperty('--dock-icon', `${p.dockSize}px`);
  root.style.setProperty('--dock-mag', String(MAGNIFY[p.dockMagnify]));
  root.toggleAttribute('data-no-indicators', !p.dockIndicators);

  root.toggleAttribute('data-solid', p.reduceTransparency);
  root.dataset.icons = p.iconStyle;
  root.toggleAttribute('data-reduce-motion', p.reduceMotion);
  root.toggleAttribute('data-square', p.squareCorners);
  root.dataset.titleButtons = p.titleButtons;
  root.dataset.iconSize = p.iconSize;
  root.toggleAttribute('data-no-labels', !p.iconLabels);

  const body = document.body;
  body.dataset.wallpaper = p.wallpaper;
  const custom = p.wallpaper === 'custom' ? loadCustomWallpaper() : null;
  body.style.backgroundImage = custom ? `url("${custom}")` : '';
  root.style.setProperty('--wall-dim', String(p.wallpaperDim / 100));
  // Brightness: a dark veil over everything (browsers can't drive the backlight).
  const b = Math.min(100, Math.max(30, p.brightness));
  root.style.setProperty('--veil', String(((100 - b) / 100) * 0.9));
}
