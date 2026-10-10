import { create } from 'zustand';
import { storeApi, type StoreApp } from '../api/appstore';
import { choiceDialog } from './confirm';
import { toast } from './toasts';

// Installed App Store apps, for the home screen and Launchpad. Polled while
// signed in as an admin; faster while an install or uninstall is running.

interface StoreApps {
  apps: StoreApp[];
  loaded: boolean;
  load: () => Promise<void>;
}

export const useStoreApps = create<StoreApps>((set) => ({
  apps: [],
  loaded: false,
  load: async () => {
    const r = await storeApi.list();
    if (r.ok) set({ apps: r.data.apps, loaded: true });
  },
}));

/** Apps that are installed, or being installed or removed right now. */
export const shownOnHome = (a: StoreApp) => !!a.installed || (!!a.job && !a.job.done && a.job.action !== 'uninstall');

const busy = (apps: StoreApp[]) => apps.some((a) => a.job && !a.job.done);

/** Keeps the list fresh while the tab is visible; returns a stop function. */
export function watchStoreApps(): () => void {
  let timer: number | undefined;
  let stopped = false;
  let running = false;
  const tick = async () => {
    timer = undefined;
    // Hidden tab: stop here; becoming visible starts the loop again.
    if (stopped || running || document.hidden) return;
    running = true;
    try {
      await useStoreApps.getState().load();
    } finally {
      running = false;
    }
    if (!stopped) timer = window.setTimeout(tick, busy(useStoreApps.getState().apps) ? 2000 : 20000);
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

/** Ask, then uninstall (keeping or deleting the app's data). */
export async function uninstallStoreApp(app: StoreApp): Promise<boolean> {
  const pick = await choiceDialog({
    title: `Uninstall ${app.name}?`,
    message: 'Its containers are removed. Keep the data and a reinstall picks up where you left off, or delete it for good (settings, accounts and files stored inside the app).',
    choices: [
      { id: 'delete', label: 'Delete data too', danger: true },
      { id: 'keep', label: 'Uninstall, keep data' },
    ],
  });
  if (!pick) return false;
  const r = await storeApi.uninstall(app.id, pick === 'delete');
  if (!r.ok) {
    toast('error', `Could not uninstall ${app.name}`, r.error);
    return false;
  }
  toast('info', `Removing ${app.name}…`);
  void useStoreApps.getState().load();
  return true;
}
