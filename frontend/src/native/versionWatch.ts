// NoCap Desktop after an update: the desktop and the chrome (bar, dock, app
// list) keep running across a NoCapOS update, so they'd go on showing the
// old version until the next sign-in. They hold nothing unsaved, so when
// the server's version changes they load the new one by themselves.
import { api } from '../api/client';
import type { SystemInfo } from '../api/types';
import { shouldReload } from './versionCheck';

const CHECK_MS = 60_000;

async function serverVersion(): Promise<string | null> {
  const r = await api<SystemInfo>('/api/v1/system/info').catch(() => null);
  return r?.ok && typeof r.data.version === 'string' ? r.data.version : null;
}

/** Reloads this page once the server runs a different NoCapOS version. */
export function startVersionWatch() {
  let mine: string | null = null;
  let busy = false;
  const check = async () => {
    if (busy) return;
    busy = true;
    try {
      const v = await serverVersion();
      if (mine === null) mine = v;
      else if (shouldReload(mine, v)) window.location.reload();
    } finally {
      busy = false;
    }
  };
  void check();
  window.setInterval(() => void check(), CHECK_MS);
  window.addEventListener('online', () => void check());
}
