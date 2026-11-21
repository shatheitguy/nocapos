import { api } from './client';

/** What the server's own monitor shows: the native NoCap Desktop, or the console. */
export type DesktopMode = 'full' | 'off';

/** Whether the screen really shows NoCap Desktop. */
export interface DesktopHealth {
  status: 'ok' | 'starting' | 'warning' | 'broken' | 'off' | 'unavailable';
  problems?: { code: string; detail?: string }[];
  /** The fallback in use when the normal setup didn't work here. */
  fallback?: '' | 'software';
  /** Someone is still signed in on the earlier desktop: NoCap Desktop starts after a restart. */
  restart_needed?: boolean;
  old_session_users?: string[];
}

/** How long NoCap Desktop has worked here (the earlier desktop's packages can go once it's confirmed). */
export interface DesktopConfirm {
  runs: number;
  minutes: number;
  first?: string;
  confirmed?: string;
}

/** The earlier desktop's packages: the one-time notice once NoCap Desktop is confirmed working. */
export interface OldDesktop {
  notice: boolean;
  confirmed: boolean;
  progress: DesktopConfirm;
  /** NoCapOS installed these: they can go. */
  remove: string[];
  /** Installed by someone else (or unsure): they stay. */
  keep: string[];
  choice?: '' | 'keep' | 'removed';
  removed?: string[];
}

/** A package task (GET /api/v1/packages/jobs/{id}). */
export interface PkgJob {
  id: string;
  running: boolean;
  ok: boolean;
  output: string;
  error?: string;
}

/** NoCap Desktop on the server's own monitor. */
export interface DesktopState {
  available: boolean;
  reason?: string;
  /** Some mode other than off is on. */
  enabled: boolean;
  active: boolean;
  /** A monitor is plugged into the server. */
  display: boolean;
  mode: DesktopMode;
  /** The modes this server has: full and off once NoCap Desktop is set up. */
  modes: DesktopMode[] | null;
  /** Signed in on the full desktop right now. */
  session_user?: string;
  /** The screen shows the sign-in screen (nobody signed in). */
  sign_in_screen?: boolean;
  autologin: boolean;
  autologin_user?: string;
  autologin_supported: boolean;
  /** The screen can be opened in a NoCap Desktop window. */
  remote: boolean;
  health?: DesktopHealth;
}

export interface HostAccess {
  native: boolean;
  /** This browser runs on the server itself. */
  client_local: boolean;
  remote: { supported: boolean; enabled: boolean; reason?: string };
  desktop: DesktopState;
}

export const hostAccessApi = {
  get: () => api<HostAccess>('/api/v1/system/host-access'),
  setRemote: (enabled: boolean, confirm = false) =>
    api<HostAccess>('/api/v1/system/remote-access', { method: 'PUT', body: { enabled, confirm } }),
  setDesktop: (enabled: boolean) => api<HostAccess>('/api/v1/system/desktop', { method: 'PUT', body: { enabled } }),
  setDesktopMode: (mode: DesktopMode) => api<HostAccess>('/api/v1/system/desktop', { method: 'PUT', body: { mode } }),
  setAutologin: (enabled: boolean) => api<HostAccess>('/api/v1/system/desktop/autologin', { method: 'PUT', body: { enabled } }),
  /** Puts NoCap Desktop back on the screen (restarts the sign-in screen). */
  repairDesktop: () =>
    api<{ actions: string[]; health: DesktopHealth; state: HostAccess; restart_needed?: boolean; old_session_users?: string[] }>(
      '/api/v1/system/desktop/repair', { method: 'POST', body: {} }),
  oldDesktop: () => api<OldDesktop>('/api/v1/system/desktop/old-desktop'),
  /** keep: leave them (the notice goes); remove: a package task. */
  oldDesktopKeep: () => api<OldDesktop>('/api/v1/system/desktop/old-desktop', { method: 'POST', body: { action: 'keep' } }),
  oldDesktopRemove: () => api<{ job: PkgJob }>('/api/v1/system/desktop/old-desktop', { method: 'POST', body: { action: 'remove' } }),
  pkgJob: (id: string) => api<PkgJob>(`/api/v1/packages/jobs/${encodeURIComponent(id)}`),
  /** The text bundle for a black screen: journal, sessions, GPU, screens. */
  desktopDiagnostics: () => api<{ text: string }>('/api/v1/system/desktop/diagnostics'),
  /** A single-use ticket for whatever the server's own screen shows (the NoCap Desktop window); sign_in: it's the sign-in screen. */
  view: (width: number, height: number, dpi: number) =>
    api<{ ticket: string; protocol: 'rdp' | 'vnc'; sign_in?: boolean }>('/api/v1/system/desktop/view', { method: 'POST', body: { width, height, dpi } }),
};
