import { create } from 'zustand';
import { fetchNativeIcon, nativeApi, type NativeApp, type NativeJob, type NativeStatus } from '../api/nativeApps';
import { getUser } from '../api/client';
import { openApp } from '../apps/meta';
import { confirmDialog } from './confirm';
import { toast } from './toasts';

// Installed Linux apps, for Launchpad and desktop shortcuts. Everyone signed
// in sees them; they never land on the desktop by themselves.

interface NativeApps {
  status: NativeStatus | null;
  apps: NativeApp[];
  jobs: NativeJob[];
  loaded: boolean;
  load: () => Promise<void>;
}

export const useNativeApps = create<NativeApps>((set) => ({
  status: null,
  apps: [],
  jobs: [],
  loaded: false,
  load: async () => {
    const r = await nativeApi.installed();
    if (r.ok) set({ status: r.data.status, apps: r.data.apps ?? [], jobs: r.data.jobs ?? [], loaded: true });
  },
}));

// A desktop shortcut to a Linux app is keyed "native:<flatpak id>", next to
// NoCapOS's own app ids, "folder:<id>" and "store:<id>".
const NATIVE_PREFIX = 'native:';
export const nativeKey = (id: string) => NATIVE_PREFIX + id;
export const isNativeKey = (key: string) => key.startsWith(NATIVE_PREFIX);
export const nativeIdOf = (key: string) => key.slice(NATIVE_PREFIX.length);
/** The installed app behind a "native:<id>" key, if it is still installed. */
export const nativeAppOf = (key: string) => (isNativeKey(key) ? useNativeApps.getState().apps.find((a) => a.id === nativeIdOf(key)) : undefined);

// ---- icons: fetched once per app through alfad, shown as blob: URLs ----

interface IconCache {
  urls: Record<string, string | null>;
}
export const useNativeIcons = create<IconCache>(() => ({ urls: {} }));
const pending = new Set<string>();

export function ensureNativeIcon(id: string) {
  if (pending.has(id) || id in useNativeIcons.getState().urls) return;
  pending.add(id);
  void fetchNativeIcon(id).then((url) => {
    pending.delete(id);
    useNativeIcons.setState((s) => ({ urls: { ...s.urls, [id]: url } }));
  });
}

/** Icon props for <AppIcon>: the real icon once loaded, a neutral tile until then. */
export function nativeIconApp(app: { id: string }, url: string | null | undefined) {
  return { id: `native:${app.id}`, icon: 'linux' as const, tile: ['#7c8aa5', '#3b4458'] as [string, string], logo: url ?? undefined };
}

// ---- opening ----

/** Opens an app: on the server's screen when it's on, else streamed into a window. */
export async function launchNativeApp(app: Pick<NativeApp, 'id' | 'name'>) {
  const r = await nativeApi.launch(app.id);
  if (!r.ok) {
    toast('error', `Couldn't open ${app.name}`, r.error);
    return;
  }
  if (r.data.where === 'screen') {
    toast('success', "Opened on the server's screen", `${app.name} is up on the monitor plugged into this server.`);
    return;
  }
  openApp('nativeapp', {
    key: nativeKey(app.id),
    title: app.name,
    props: { id: app.id, name: app.name, stream: r.data.stream?.id ?? '' },
  });
}

/** Admins: ask, then uninstall. */
export async function uninstallNativeApp(app: Pick<NativeApp, 'id' | 'name'>): Promise<boolean> {
  if (getUser()?.role !== 'admin') return false;
  const ok = await confirmDialog({
    title: `Remove ${app.name}?`,
    message: 'It comes off this server for everyone. People’s own files stay put.',
    confirmLabel: 'Remove',
    danger: true,
  });
  if (!ok) return false;
  const r = await nativeApi.uninstall(app.id, app.name);
  if (!r.ok) {
    toast('error', `Couldn't remove ${app.name}`, r.error);
    return false;
  }
  toast('info', `Removing ${app.name}…`, 'Hang tight, this takes a sec.');
  void useNativeApps.getState().load();
  return true;
}

const busy = (jobs: NativeJob[]) => jobs.some((j) => !j.done);

/** Keeps the list fresh while the tab is visible; returns a stop function. */
export function watchNativeApps(): () => void {
  let timer: number | undefined;
  let stopped = false;
  let running = false;
  const tick = async () => {
    timer = undefined;
    if (stopped || running || document.hidden) return;
    running = true;
    try {
      await useNativeApps.getState().load();
    } finally {
      running = false;
    }
    if (!stopped) timer = window.setTimeout(tick, busy(useNativeApps.getState().jobs) ? 2000 : 30000);
  };
  const onVisible = () => {
    if (!document.hidden && timer === undefined) void tick();
  };
  document.addEventListener('visibilitychange', onVisible);
  void tick();
  return () => {
    stopped = true;
    window.clearTimeout(timer);
    document.removeEventListener('visibilitychange', onVisible);
  };
}
