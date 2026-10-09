import type { ComponentType } from 'react';
import type { WinState } from '../state/windows';
import { AppCenter } from './AppCenter';
import { Assistant } from './assistant/Assistant';
import { Browser } from './Browser';
import { Containers } from './Containers';
import { Files } from './Files';
import { Logs } from './Logs';
import { Monitor } from './Monitor';
import { Photos } from './Photos';
import { RemoteDesktop } from './RemoteDesktop';
import { Scripts } from './Scripts';
import { Settings } from './Settings';
import { Terminal } from './Terminal';
import { Viewer } from './Viewer';

export const APP_COMPONENTS: Record<string, ComponentType<{ win: WinState }>> = {
  assistant: Assistant,
  browser: Browser,
  remotedesktop: RemoteDesktop,
  files: Files,
  terminal: Terminal,
  monitor: Monitor,
  photos: Photos,
  containers: Containers,
  appcenter: AppCenter,
  settings: Settings,
  scripts: Scripts,
  viewer: Viewer,
  logs: Logs,
};
