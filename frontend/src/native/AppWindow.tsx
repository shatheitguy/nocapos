// NoCap Desktop: one NoCap OS app in a real window of its own (the host's
// `nocap-shell --app ID`). It is the browser's window, the same Window
// component with the same title bar, filling the native window; moving,
// resizing, minimizing and maximizing go to the compositor.
import { useEffect } from 'react';
import { APPS, winTitle } from '../apps/meta';
import { Window } from '../shell/Window';
import { followPrefs, usePrefs } from '../state/prefs';
import { connect, disconnect } from '../api/socket';
import { useWM } from '../state/windows';
import { hostOn, hostSend } from './platform';

export const NATIVE_WINDOW_ID = 'native';

/** The props an app was opened with (JSON text from the host), or none. */
export function parseProps(text: string | null | undefined): Record<string, string> | undefined {
  if (!text) return undefined;
  try {
    const v = JSON.parse(text) as unknown;
    if (!v || typeof v !== 'object' || Array.isArray(v)) return undefined;
    const out: Record<string, string> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) if (typeof val === 'string') out[k] = val;
    return out;
  } catch {
    return undefined;
  }
}

export function NativeAppWindow() {
  const q = new URLSearchParams(location.search);
  const appId = q.get('app') ?? '';
  const meta = APPS[appId];

  useEffect(() => {
    if (!meta) return;
    let maximized = false;
    const wm = useWM.getState();
    // The window's buttons and the app's own "close" act on the real window.
    useWM.setState({
      minimize: () => hostSend('window.action', { action: 'minimize' }),
      close: () => hostSend('window.action', { action: 'close' }),
      toggleMaximize: () => hostSend('window.action', { action: maximized ? 'restore' : 'maximize' }),
      snapTo: () => {},
      move: () => {},
      resize: () => {},
      restoreLayout: () => {},
    });
    wm.open({ appId, title: meta.title, auto: 1, props: parseProps(q.get('props')), key: NATIVE_WINDOW_ID, size: meta.size });
    const offProps = hostOn('app.props', (p) => {
      const props = parseProps(typeof p === 'string' ? p : null);
      if (props) useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === NATIVE_WINDOW_ID ? { ...w, props } : w)) }));
    });
    const offState = hostOn('window.state', (p) => {
      const st = p as { maximized?: boolean; tiled?: boolean; focused?: boolean };
      maximized = !!st.maximized;
      useWM.setState((s) => ({
        focused: st.focused === false ? null : NATIVE_WINDOW_ID,
        windows: s.windows.map((w) => (w.id === NATIVE_WINDOW_ID ? { ...w, maximized: !!st.maximized || !!st.tiled } : w)),
      }));
    });
    // The window's title follows the app's (Files shows the folder, say),
    // and the voice in use.
    let lastTitle = '';
    const sendTitle = () => {
      const w = useWM.getState().windows.find((x) => x.id === NATIVE_WINDOW_ID);
      const title = w ? winTitle(w) : '';
      if (w && title !== lastTitle) {
        lastTitle = title;
        hostSend('window.title', { title: title.slice(0, 256) });
      }
    };
    const offWindows = useWM.subscribe(sendTitle);
    const offVoice = usePrefs.subscribe(sendTitle);
    const offTitle = () => {
      offWindows();
      offVoice();
    };
    return () => {
      offProps();
      offState();
      offTitle();
    };
  }, [appId, meta]);

  // Settings changed in another window (Settings itself, say) show here
  // straight away: alfad's live feed says when to load them again.
  useEffect(() => {
    if (!meta) return;
    void connect();
    const offPrefs = followPrefs();
    return () => {
      offPrefs();
      disconnect();
    };
  }, [meta]);

  if (!meta) return null;
  return <Window id={NATIVE_WINDOW_ID} compact={false} />;
}
