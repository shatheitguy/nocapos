// Remote Desktop's model: connection settings, presets, and plain-word
// errors. No React and no imports from the app, so `npm test` runs it as is.

export type RemoteProtocol = 'rdp' | 'vnc';
export type SizeMode = 'fit' | 'fullscreen' | 'fixed';
export type ClipboardMode = 'both' | 'to-remote' | 'from-remote' | 'off';
export type Speed = 'lan' | 'broadband' | 'low' | 'custom';
export type Security = 'any' | 'nla' | 'tls' | 'rdp';

/** A connection's options, as the server stores and the ticket takes them. */
export interface RemoteOptions {
  security?: Security;
  ignore_cert?: boolean;
  size_mode?: SizeMode;
  fixed_width?: number;
  fixed_height?: number;
  /** Text and icon size, percent (RDP). */
  scale?: number;
  /** 0 = the protocol's default. */
  color_depth?: number;
  clipboard?: ClipboardMode;
  audio?: boolean;
  speed?: Speed;
  wallpaper?: boolean;
  theming?: boolean;
  font_smoothing?: boolean;
  full_window_drag?: boolean;
  desktop_composition?: boolean;
  menu_animations?: boolean;
  gateway_host?: string;
  gateway_port?: number;
  gateway_user?: string;
  gateway_domain?: string;
}

/** What the start card edits: one computer and its options. */
export interface ConnForm {
  id?: string;
  name: string;
  protocol: RemoteProtocol;
  hostname: string;
  port: number;
  username: string;
  domain: string;
  password: string;
  remember: boolean;
  save: boolean;
  gatewayPassword: string;
  options: RemoteOptions;
}

export const DEFAULT_PORT: Record<RemoteProtocol, number> = { rdp: 3389, vnc: 5900 };

export const FIXED_SIZES: [number, number][] = [
  [1024, 768],
  [1280, 720],
  [1280, 800],
  [1366, 768],
  [1600, 900],
  [1920, 1080],
  [2560, 1440],
];

export const SCALES = [100, 125, 150, 175, 200];

/** Colour depths each protocol offers (0 = automatic). */
export const COLOR_DEPTHS: Record<RemoteProtocol, number[]> = { rdp: [0, 32, 24, 16, 8], vnc: [0, 24, 16, 8] };

type ExperienceKey = 'wallpaper' | 'theming' | 'font_smoothing' | 'full_window_drag' | 'desktop_composition' | 'menu_animations';
export const EXPERIENCE: ExperienceKey[] = ['wallpaper', 'theming', 'font_smoothing', 'full_window_drag', 'desktop_composition', 'menu_animations'];

/** What each connection speed turns on. */
export const SPEEDS: Record<Exclude<Speed, 'custom'>, Record<ExperienceKey, boolean>> = {
  lan: { wallpaper: true, theming: true, font_smoothing: true, full_window_drag: true, desktop_composition: true, menu_animations: true },
  broadband: { wallpaper: false, theming: true, font_smoothing: true, full_window_drag: false, desktop_composition: true, menu_animations: false },
  low: { wallpaper: false, theming: false, font_smoothing: false, full_window_drag: false, desktop_composition: false, menu_animations: false },
};

/** Applies a speed preset's switches. */
export function withSpeed(o: RemoteOptions, speed: Speed): RemoteOptions {
  if (speed === 'custom') return { ...o, speed };
  return { ...o, ...SPEEDS[speed], speed };
}

/** The preset the switches match, or custom. */
export function speedOf(o: RemoteOptions): Speed {
  for (const s of ['lan', 'broadband', 'low'] as const) {
    if (EXPERIENCE.every((k) => !!o[k] === SPEEDS[s][k])) return s;
  }
  return 'custom';
}

export function defaultOptions(): RemoteOptions {
  return withSpeed({ security: 'any', ignore_cert: false, size_mode: 'fit', scale: 100, color_depth: 0, clipboard: 'both', audio: true }, 'broadband');
}

export function blankForm(protocol: RemoteProtocol = 'rdp'): ConnForm {
  return {
    name: '',
    protocol,
    hostname: '',
    port: DEFAULT_PORT[protocol],
    username: '',
    domain: '',
    password: '',
    remember: false,
    save: true,
    gatewayPassword: '',
    options: defaultOptions(),
  };
}

/**
 * Splits "host", "host:port", "[v6]:port" or a bare IPv6 address typed in
 * the Computer field. A port in the text wins over the form's.
 */
export function splitHost(text: string): { host: string; port?: number } {
  const t = text.trim();
  const v6 = /^\[([^\]]+)\](?::(\d{1,5}))?$/.exec(t);
  if (v6) return { host: v6[1], port: v6[2] ? Number(v6[2]) : undefined };
  const m = /^([^:\s]+):(\d{1,5})$/.exec(t);
  if (m && Number(m[2]) > 0 && Number(m[2]) <= 65535) return { host: m[1], port: Number(m[2]) };
  return { host: t };
}

/** A host name or IP address the server will take. */
export function validHost(h: string): boolean {
  return h.length > 0 && h.length <= 253 && !/[\s/\\,;]/.test(h);
}

/** "me", "DOMAIN\me" or "me@host" for display. */
export function userLabel(username: string, domain: string): string {
  if (!username) return '';
  return domain ? `${domain}\\${username}` : username;
}

/** Options trimmed to what the protocol has (VNC: no RDP-only settings). */
export function optionsFor(protocol: RemoteProtocol, o: RemoteOptions): RemoteOptions {
  if (protocol === 'rdp') return { ...o };
  const { security: _s, ignore_cert: _i, scale: _sc, audio: _a, gateway_host: _gh, gateway_port: _gp, gateway_user: _gu, gateway_domain: _gd, ...rest } = o;
  const out: RemoteOptions = { ...rest };
  for (const k of EXPERIENCE) delete out[k];
  delete out.speed;
  if (out.color_depth === 32) out.color_depth = 0;
  return out;
}

export type SortKey = 'name' | 'recent' | 'host';

export interface Listable {
  name: string;
  hostname: string;
  username: string;
  last_used?: number;
}

/** Saved computers matching a search, in the chosen order. */
export function sortComputers<T extends Listable>(list: T[], sort: SortKey, query: string): T[] {
  const q = query.trim().toLowerCase();
  const out = q ? list.filter((c) => `${c.name} ${c.hostname} ${c.username}`.toLowerCase().includes(q)) : [...list];
  const byName = (a: T, b: T) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true });
  if (sort === 'recent') out.sort((a, b) => (b.last_used ?? 0) - (a.last_used ?? 0) || byName(a, b));
  else if (sort === 'host') out.sort((a, b) => a.hostname.localeCompare(b.hostname, undefined, { numeric: true }) || byName(a, b));
  else out.sort(byName);
  return out;
}

/** Past computer addresses, newest first, no repeats, at most max. */
export function addHistory(list: string[], host: string, max = 12): string[] {
  const h = host.trim();
  if (!h) return list;
  return [h, ...list.filter((x) => x.toLowerCase() !== h.toLowerCase())].slice(0, max);
}

export type FailKind = 'auth' | 'unreachable' | 'cert' | 'security' | 'engine' | 'closed' | 'other';

/**
 * What went wrong, from a Guacamole status code and message, in terms the
 * person can act on. ignoredCert: the attempt already trusted the
 * certificate (so a TLS failure isn't about it).
 */
export function classifyFailure(code: number | undefined, message: string, protocol: RemoteProtocol, ignoredCert: boolean): FailKind {
  const m = message.toLowerCase();
  if (code === 769 || code === 771 || /auth|credential|password|logon|log on|access denied/.test(m)) return 'auth';
  if (code === 513 && /engine/.test(m)) return 'engine';
  if (/dns|not found|unreachable|no route|timed out|timeout|could not connect|unable to connect|connection refused/.test(m) || code === 519 || code === 514) {
    return 'unreachable';
  }
  if (protocol === 'rdp' && !ignoredCert && (/certificate|tls|security|refused/.test(m) || code === 520)) return 'cert';
  if (protocol === 'rdp' && /security|negotiat/.test(m)) return 'security';
  if (code === 520 || /refused/.test(m)) return 'unreachable';
  if (code === 517 || code === 518 || code === 523 || /ended|closed|disconnected/.test(m)) return 'closed';
  return 'other';
}

/** Round trip in milliseconds → a quality level for the connection bar. */
export function qualityOf(rttMs: number | null, unstable: boolean): 'good' | 'fair' | 'poor' | 'unknown' {
  if (unstable) return 'poor';
  if (rttMs == null) return 'unknown';
  if (rttMs < 90) return 'good';
  if (rttMs < 220) return 'fair';
  return 'poor';
}

/** The X11 keysym for a typed character (for the on-screen keyboard). */
export function keysymFor(ch: string): number | null {
  const cp = ch.codePointAt(0);
  if (cp == null) return null;
  if (ch === '\n') return 0xff0d; // Return
  if (cp >= 0x20 && cp <= 0x7e) return cp;
  if (cp >= 0xa0 && cp <= 0xff) return cp;
  return 0x01000000 | cp;
}

/** Special keys the connection bar can send: keysyms pressed together. */
export const SPECIAL_KEYS: { id: string; keys: number[] }[] = [
  { id: 'ctrlAltDel', keys: [0xffe3, 0xffe9, 0xffff] },
  { id: 'ctrlShiftEsc', keys: [0xffe3, 0xffe1, 0xff1b] },
  { id: 'altTab', keys: [0xffe9, 0xff09] },
  { id: 'altF4', keys: [0xffe9, 0xffc1] },
  { id: 'win', keys: [0xffeb] },
  { id: 'winL', keys: [0xffeb, 0x6c] },
  { id: 'printScreen', keys: [0xff61] },
  { id: 'esc', keys: [0xff1b] },
  { id: 'tab', keys: [0xff09] },
];
