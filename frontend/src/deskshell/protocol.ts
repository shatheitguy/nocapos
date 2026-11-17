// The NoCap Shell bridge contract, as types (shell/PROTOCOL.md is the source
// of truth). Types only: pure helpers import these with `import type`, so they
// still run under node for the unit tests.

export type SurfaceName = 'desktop' | 'chrome' | 'app' | 'polkit' | 'greeter';

export type Voice = 'genz' | 'standard';
export type Theme = 'dark' | 'light';

export interface ShellInfo {
  surface: string;
  user: string;
  realName: string;
  voice: Voice;
  theme: Theme;
  version: string;
}

export interface AppInfo {
  id: string;
  name: string;
  comment: string;
  icon: string;
  categories: string[];
  keywords: string[];
}

export interface WindowInfo {
  id: string | number;
  appId: string;
  title: string;
  active: boolean;
  minimized: boolean;
  maximized: boolean;
}

export interface AudioState {
  available: boolean;
  volume: number;
  muted: boolean;
}

export interface BrightnessState {
  available: boolean;
  percent: number;
}

export interface NetworkInfo {
  state: 'online' | 'connecting' | 'offline';
  kind: 'wifi' | 'ethernet' | 'other' | 'none';
  name: string;
  strength: number;
}

export interface NotifyAction {
  key: string;
  label: string;
}

export interface NotifyItem {
  id: string | number;
  source: 'app' | 'nocapos';
  app: string;
  icon: string;
  title: string;
  body: string;
  /** Unix time: seconds or milliseconds (both are accepted). */
  time: number;
  urgency: 0 | 1 | 2;
  actions: NotifyAction[];
  read: boolean;
}

export interface OsdEvent {
  kind: 'volume' | 'brightness';
  value: number;
  muted?: boolean;
}

export interface PolkitRequest {
  cookie: string;
  message: string;
  icon: string;
  user: string;
}

export interface GreeterUser {
  name: string;
  realName: string;
}

export type GreeterState = 'secret' | 'visible' | 'info' | 'error' | 'done' | 'failed';
export interface GreeterReply {
  state: GreeterState;
  message: string;
}

export type PowerAction = 'lock' | 'logout' | 'suspend' | 'reboot' | 'poweroff';
