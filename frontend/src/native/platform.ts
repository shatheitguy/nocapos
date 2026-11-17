// NoCap OS running natively on the machine's own screen (NoCap Desktop).
//
// The desktop on the screen is this same NoCap OS build, shown by the NoCap
// Shell host (shell/) from local files at nocap-shell://shell/shell.html in
// a few native surfaces:
//
//   desktop  the wallpaper, widgets, desktop icons and desktop menu
//   chrome   the menu bar, dock, launcher, search, panels and notifications
//            (see-through; windows show below it)
//   app      one NoCap OS app in a real window of its own
//   polkit   the admin password prompt
//   greeter  the sign-in screen
//
// Everything else is the same code as in the browser. This module says
// which surface (if any) the page is, and holds the few things that differ:
// where alfad's live connections are (the page's own HTTP requests go
// through the host to alfad on the loopback address), and the bridge to the
// host.

import { call, on } from '../deskshell/bridge';

export type NativeSurface = 'desktop' | 'chrome' | 'app' | 'polkit' | 'greeter';
const SURFACES: readonly NativeSurface[] = ['desktop', 'chrome', 'app', 'polkit', 'greeter'];

let surface: NativeSurface | null | undefined;

/** The native surface this page is, or null in a browser. */
export function nativeSurface(): NativeSurface | null {
  if (surface !== undefined) return surface;
  surface = null;
  if (typeof location !== 'undefined' && location.protocol === 'nocap-shell:') {
    const q = new URLSearchParams(location.search).get('surface') ?? '';
    if ((SURFACES as readonly string[]).includes(q)) surface = q as NativeSurface;
  }
  return surface;
}

export const isNative = () => nativeSurface() !== null;

/** For tests: pretend to be a surface (or a browser with null). */
export function setNativeSurfaceForTest(s: NativeSurface | null) {
  surface = s;
}

// alfad's loopback listener (the host says where).
let apiOrigin = 'http://127.0.0.1:8780';

const LOOPBACK_ORIGIN = /^https?:\/\/127\.0\.0\.1:\d{1,5}$/;

export function setApiOrigin(o: string) {
  if (LOOPBACK_ORIGIN.test(o)) apiOrigin = o;
}

/** Where WebSockets go: alfad itself (ws://, wss://), never anywhere else. */
export function wsBase(): string {
  if (isNative()) return apiOrigin.replace(/^http/, 'ws');
  return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}`;
}

/** The server's host name, for addresses of apps on their own ports. */
export function serverHost(): string {
  return isNative() ? '127.0.0.1' : location.hostname;
}

/** The server's own address (shown to people, e.g. for WebDAV). */
export function serverOrigin(): string {
  return isNative() ? apiOrigin : location.origin;
}

/** A call to the host (rejects in a browser). */
export function hostCall<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> {
  if (!isNative()) return Promise.reject(new Error('not on NoCap Desktop'));
  return call<T>(method, params);
}

/** Fire and forget. */
export function hostSend(method: string, params?: Record<string, unknown>): void {
  hostCall(method, params).catch((e) => console.warn('nocap:', e instanceof Error ? e.message : e));
}

export function hostOn(name: string, fn: (payload: unknown) => void): () => void {
  if (!isNative()) return () => {};
  return on(name, fn);
}
