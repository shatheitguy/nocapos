import { lazy, type ComponentType } from 'react';
import type { WinState } from '../state/windows';

type AppBody = ComponentType<{ win: WinState }>;

// Each app is its own chunk, fetched the first time its window opens, so the
// desktop doesn't download the terminal, photo library, etc. up front.
const app = (load: () => Promise<AppBody>) =>
  lazy(async () => {
    try {
      return { default: await load() };
    } catch {
      // Usually a chunk from before an update that the server no longer has.
      throw new Error('This app could not be loaded. NoCapOS may have been updated: reload the page.');
    }
  });

export const APP_COMPONENTS: Record<string, AppBody> = {
  assistant: app(() => import('./assistant/Assistant').then((m) => m.Assistant)),
  browser: app(() => import('./Browser').then((m) => m.Browser)),
  backups: app(() => import('./Backups').then((m) => m.Backups)),
  remotedesktop: app(() => import('./RemoteDesktop').then((m) => m.RemoteDesktop)),
  files: app(() => import('./Files').then((m) => m.Files)),
  terminal: app(() => import('./Terminal').then((m) => m.Terminal)),
  monitor: app(() => import('./Monitor').then((m) => m.Monitor)),
  photos: app(() => import('./Photos').then((m) => m.Photos)),
  containers: app(() => import('./Containers').then((m) => m.Containers)),
  appcenter: app(() => import('./AppCenter').then((m) => m.AppCenter)),
  settings: app(() => import('./Settings').then((m) => m.Settings)),
  storage: app(() => import('./Storage').then((m) => m.Storage)),
  virtualdesk: app(() => import('./VirtualDesk').then((m) => m.VirtualDesk)),
  vmscreen: app(() => import('./VirtualDeskScreen').then((m) => m.VmScreen)),
  scripts: app(() => import('./Scripts').then((m) => m.Scripts)),
  viewer: app(() => import('./Viewer').then((m) => m.Viewer)),
  logs: app(() => import('./Logs').then((m) => m.Logs)),
};
