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

const LOADERS: Record<string, () => Promise<AppBody>> = {
  assistant: () => import('./assistant/Assistant').then((m) => m.Assistant),
  nativeapp: () => import('./NativeAppWindow').then((m) => m.NativeAppWindow),
  backups: () => import('./Backups').then((m) => m.Backups),
  remotedesktop: () => import('./RemoteDesktop').then((m) => m.RemoteDesktop),
  nocapdesktop: () => import('./NocapDesktop').then((m) => m.NocapDesktop),
  files: () => import('./Files').then((m) => m.Files),
  terminal: () => import('./Terminal').then((m) => m.Terminal),
  monitor: () => import('./Monitor').then((m) => m.Monitor),
  photos: () => import('./Photos').then((m) => m.Photos),
  containers: () => import('./Containers').then((m) => m.Containers),
  appcenter: () => import('./AppCenter').then((m) => m.AppCenter),
  settings: () => import('./Settings').then((m) => m.Settings),
  shield: () => import('./Shield').then((m) => m.Shield),
  storage: () => import('./Storage').then((m) => m.Storage),
  virtualdesk: () => import('./VirtualDesk').then((m) => m.VirtualDesk),
  vmscreen: () => import('./VirtualDeskScreen').then((m) => m.VmScreen),
  scripts: () => import('./Scripts').then((m) => m.Scripts),
  viewer: () => import('./Viewer').then((m) => m.Viewer),
  imageviewer: () => import('./MediaImage').then((m) => m.ImageViewerApp),
  videoplayer: () => import('./MediaVideo').then((m) => m.VideoPlayerApp),
  musicplayer: () => import('./MediaMusic').then((m) => m.MusicPlayerApp),
  logs: () => import('./Logs').then((m) => m.Logs),
  phone: () => import('./Phone').then((m) => m.Phone),
  notes: () => import('./notes/Notes').then((m) => m.Notes),
  note: () => import('./notes/Notes').then((m) => m.NoteWindow),
};

export const APP_COMPONENTS: Record<string, AppBody> = Object.fromEntries(Object.entries(LOADERS).map(([id, load]) => [id, app(load)]));

/**
 * Starts fetching an app's code before its window needs it (NoCap Desktop's
 * app windows do this while they sign in), so it's ready sooner. The module
 * loader keeps the result: the window's own load gets the same module.
 */
export function preloadApp(id: string) {
  void LOADERS[id]?.().catch(() => undefined);
}
