import { useEffect, useState } from 'react';
import { Icon } from '../components/Icon';
import { useWM } from '../state/windows';

// Notices when NoCapOS has been updated on the server while this tab was open
// (the page's bundle no longer matches the one the server hands out), so a
// new version shows up without a manual refresh. With no windows open it just
// reloads; otherwise it asks, so nothing unsaved is lost.

const CHECK_MS = 60_000;

/** The main script of a page, e.g. "/assets/index-CoHAIoNJ.js". */
const bundleOf = (html: string) => html.match(/\/assets\/index-[\w-]+\.js/)?.[0] ?? null;

const current = () =>
  [...document.scripts].map((s) => s.getAttribute('src') ?? '').find((s) => /\/assets\/index-[\w-]+\.js/.test(s))?.replace(/^.*(\/assets\/)/, '$1') ?? null;

async function serverBundle(): Promise<string | null> {
  try {
    const r = await fetch('/', { cache: 'no-store', credentials: 'same-origin' });
    return r.ok ? bundleOf(await r.text()) : null;
  } catch {
    return null;
  }
}

export function UpdateReady() {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const mine = current();
    if (!mine || import.meta.env.DEV) return;
    let stop = false;
    const check = async () => {
      const theirs = await serverBundle();
      if (stop || !theirs || theirs === mine) return;
      if (!useWM.getState().windows.length) window.location.reload();
      else setReady(true);
    };
    const onShow = () => document.visibilityState === 'visible' && void check();
    const t = window.setInterval(check, CHECK_MS);
    document.addEventListener('visibilitychange', onShow);
    window.addEventListener('focus', onShow);
    window.addEventListener('online', onShow);
    return () => {
      stop = true;
      window.clearInterval(t);
      document.removeEventListener('visibilitychange', onShow);
      window.removeEventListener('focus', onShow);
      window.removeEventListener('online', onShow);
    };
  }, []);

  if (!ready) return null;
  return (
    <div className="update-ready" role="status">
      <Icon name="refresh" size={14} />
      <span>NoCapOS was updated</span>
      <button type="button" onClick={() => window.location.reload()}>
        Reload
      </button>
      <button type="button" className="update-ready-x" aria-label="Later" title="Later" onClick={() => setReady(false)}>
        <Icon name="close" size={10} />
      </button>
    </div>
  );
}
