// Other apps' windows on NoCap Desktop: their title bars (drawn by the
// compositor) follow Settings → title buttons, like NoCap OS's own windows:
// round buttons on the right, or coloured dots on the left. The chrome tells
// NoCap Shell once the person's settings are in, and on every change.
import { usePrefs, usePrefsSynced } from '../state/prefs';
import { hostCall } from './platform';

let sent = '';

function send() {
  if (!usePrefsSynced.getState().synced) return; // not the defaults before the settings load
  const buttons = usePrefs.getState().titleButtons === 'left' ? 'left' : 'right';
  if (buttons === sent) return;
  sent = buttons;
  hostCall('wm.style', { buttons }).catch(() => {
    sent = ''; // an older NoCap Shell, or it failed: try again on the next change
  });
}

export function startWmStyle() {
  usePrefs.subscribe((p, prev) => {
    if (p.titleButtons !== prev.titleButtons) send();
  });
  usePrefsSynced.subscribe((s) => s.synced && send());
  send();
}
