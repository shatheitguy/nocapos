// App metadata (no component imports, so anything may import this).
import type { IconName } from '../components/Icon';
import { useRecents } from '../state/recents';
import { useWM } from '../state/windows';

export interface AppMeta {
  id: string;
  title: string;
  icon: IconName;
  /** Tile gradient: two CSS colors. */
  tile: [string, string];
  size: { w: number; h: number };
  description: string;
  /** At most one window (e.g. one shared Brave stream). */
  single?: boolean;
  adminOnly?: boolean;
  /** Not listed in the launcher (opened by other apps). */
  hidden?: boolean;
}

export const APPS: Record<string, AppMeta> = {
  assistant: {
    id: 'assistant',
    title: 'AI Assistant',
    icon: 'sparkles',
    tile: ['#8a5cf6', '#5b3bd6'],
    size: { w: 1040, h: 720 },
    description: 'Chat with local or cloud AI models',
  },
  files: {
    id: 'files',
    title: 'Files',
    icon: 'folder',
    tile: ['#ffc53d', '#f08c00'],
    size: { w: 1000, h: 640 },
    description: 'Browse, upload and manage your files',
    adminOnly: true,
  },
  photos: {
    id: 'photos',
    title: 'Photos',
    icon: 'image',
    tile: ['#ff8a5c', '#e2366f'],
    size: { w: 1080, h: 700 },
    description: 'Your photos and videos: timeline, favorites and albums',
    adminOnly: true,
  },
  backups: {
    id: 'backups',
    title: 'Backups',
    icon: 'rewind',
    tile: ['#2fc6a0', '#137a63'],
    size: { w: 1040, h: 700 },
    description: 'Automatic encrypted backups, and Rewind to restore old versions',
    single: true,
    adminOnly: true,
  },
  storage: {
    id: 'storage',
    title: 'Storage',
    icon: 'hdd',
    tile: ['#5b8cff', '#2c46b8'],
    size: { w: 1080, h: 720 },
    description: 'Disks, health, RAID pools and snapshots',
    single: true,
    adminOnly: true,
  },
  monitor: {
    id: 'monitor',
    title: 'Resource Monitor',
    icon: 'monitor',
    tile: ['#2fb4ff', '#3b6bff'],
    size: { w: 920, h: 640 },
    description: 'Live CPU, memory, network, storage and accelerators',
  },
  containers: {
    id: 'containers',
    title: 'Containers',
    icon: 'containers',
    tile: ['#2ec5a6', '#138a8a'],
    size: { w: 1040, h: 660 },
    description: 'Manage Docker containers',
    adminOnly: true,
  },
  appcenter: {
    id: 'appcenter',
    title: 'App Store',
    icon: 'store',
    tile: ['#ff9f43', '#ee5a24'],
    size: { w: 1040, h: 700 },
    description: 'Install, open and update self-hosted apps',
    adminOnly: true,
  },
  browser: {
    id: 'browser',
    title: 'Brave',
    icon: 'brave',
    tile: ['#fb542b', '#a52e12'],
    size: { w: 1200, h: 780 },
    description: 'Real Brave browser, installed on this system',
    single: true,
    adminOnly: true,
  },
  remotedesktop: {
    id: 'remotedesktop',
    title: 'Remote Desktop',
    icon: 'desktop',
    tile: ['#2f6bff', '#1b3f9e'],
    size: { w: 1080, h: 700 },
    description: 'Connect to Windows and Linux PCs over RDP',
    adminOnly: true,
  },
  virtualdesk: {
    id: 'virtualdesk',
    title: 'Virtual Desk',
    icon: 'vm',
    tile: ['#22d3ee', '#1e40af'],
    size: { w: 1100, h: 720 },
    description: 'Run Windows, Linux and Android virtual machines on this server',
    single: true,
    adminOnly: true,
  },
  vmscreen: {
    id: 'vmscreen',
    title: 'Virtual Desk',
    icon: 'vm',
    tile: ['#22d3ee', '#1e40af'],
    size: { w: 1100, h: 740 },
    description: 'A virtual machine’s screen',
    adminOnly: true,
    hidden: true,
  },
  settings: {
    id: 'settings',
    title: 'Settings',
    icon: 'settings',
    tile: ['#8e9bb5', '#556179'],
    size: { w: 940, h: 680 },
    description: 'Appearance, account and system',
    single: true,
  },
  terminal: {
    id: 'terminal',
    title: 'Terminal',
    icon: 'terminal',
    tile: ['#2f3748', '#11151d'],
    size: { w: 860, h: 540 },
    description: 'Shell into containers or the host',
    adminOnly: true,
  },
  scripts: {
    id: 'scripts',
    title: 'Scripts',
    icon: 'fileCode',
    tile: ['#36c26b', '#1d7a43'],
    size: { w: 980, h: 640 },
    description: 'Save shell scripts and run them on the host in one click',
    single: true,
    adminOnly: true,
  },
  viewer: {
    id: 'viewer',
    title: 'Viewer',
    icon: 'eye',
    tile: ['#4f7cff', '#2d4fd6'],
    size: { w: 900, h: 620 },
    description: 'Preview and edit files',
    adminOnly: true,
    hidden: true,
  },
  logs: {
    id: 'logs',
    title: 'Logs',
    icon: 'logs',
    tile: ['#5f6b85', '#2f3748'],
    size: { w: 900, h: 560 },
    description: 'Container log viewer',
    adminOnly: true,
    hidden: true,
  },
};

export const PINNED = ['assistant', 'browser', 'files', 'monitor', 'remotedesktop', 'appcenter', 'settings'];

export interface OpenOptions {
  title?: string;
  props?: Record<string, string>;
  /** Windows with the same key are reused instead of duplicated. */
  key?: string;
  /** Always open another window (ignored for single-window apps). */
  newWindow?: boolean;
}

/**
 * Opens an app the way a desktop dock does: if it already has a window, that
 * window comes to the front; pass `newWindow` to open another one instead.
 */
export function openApp(appId: string, opts: OpenOptions = {}) {
  const meta = APPS[appId];
  if (!meta) return;
  const wm = useWM.getState();
  const mine = wm.windows.filter((w) => w.appId === appId);

  if (!opts.key && !opts.newWindow && !meta.single && !opts.props && mine.length) {
    wm.focus(mine.reduce((a, b) => (b.z > a.z ? b : a)).id);
    useRecents.getState().push(appId);
    return;
  }

  // Number extra windows ("Files 2") so they're easy to tell apart.
  let title = opts.title ?? meta.title;
  if (!opts.title && !meta.single && mine.length) {
    const used = new Set(mine.map((w) => w.title));
    let n = 2;
    while (used.has(`${meta.title} ${n}`)) n++;
    title = `${meta.title} ${n}`;
  }

  wm.open({
    appId,
    title,
    props: opts.props,
    key: opts.key ?? (meta.single ? appId : undefined),
    size: meta.size,
  });
  if (!meta.hidden) useRecents.getState().push(appId);
}

/** Whether an app can have more than one window. */
export const canMultiWindow = (appId: string) => !!APPS[appId] && !APPS[appId].single;

export function visibleApps(isAdmin: boolean): AppMeta[] {
  return Object.values(APPS).filter((a) => !a.hidden && (isAdmin || !a.adminOnly));
}
