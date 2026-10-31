import { useCallback, useEffect, useRef, useState } from 'react';
import { nativeApi, streamURL, type NativeStream } from '../api/nativeApps';
import { Icon } from '../components/Icon';
import { NativeAppIcon } from '../shell/NativeTile';
import { useWM, type WinState } from '../state/windows';

// A Linux app installed on the server, running on its own virtual display
// and streamed into this window, the same way the Brave window works.
export function NativeAppWindow({ win }: { win: WinState }) {
  const id = win.props?.id ?? '';
  const name = win.props?.name ?? 'App';
  const [sid, setSid] = useState(win.props?.stream ?? '');
  const [status, setStatus] = useState<NativeStream | null>(null);
  const [fatal, setFatal] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);
  const polling = useRef<number | null>(null);

  // Opened again from Launchpad: a new stream id arrives in the props.
  useEffect(() => {
    if (win.props?.stream) setSid(win.props.stream);
  }, [win.props?.stream]);

  const stopPolling = () => {
    if (polling.current) window.clearInterval(polling.current);
    polling.current = null;
  };

  const watch = useCallback((stream: string) => {
    stopPolling();
    const tick = async () => {
      const r = await nativeApi.stream(stream);
      if (!r.ok) {
        stopPolling();
        setStatus(null);
        setFatal(r.status === 404 ? `${name} isn't running anymore.` : r.error || `Couldn't reach ${name}.`);
        return;
      }
      setStatus(r.data);
      if (r.data.phase === 'error' || r.data.phase === 'idle') stopPolling();
    };
    void tick();
    polling.current = window.setInterval(tick, 1500);
  }, [name]);

  useEffect(() => {
    setFatal(null);
    setStatus(null);
    if (sid) watch(sid);
    else setFatal(`Couldn't start ${name}.`);
    return stopPolling;
  }, [sid, watch, name]);

  // Once it's up, check in now and then (renews access, notices the app closing).
  useEffect(() => {
    if (!status?.ready) return;
    stopPolling();
    polling.current = window.setInterval(async () => {
      const r = await nativeApi.stream(sid);
      if (!r.ok || !r.data.ready) {
        stopPolling();
        setStatus(r.ok ? r.data : null);
        if (!r.ok) setFatal(`${name} isn't running anymore.`);
      }
    }, 60_000);
    return stopPolling;
  }, [status?.ready, sid, name]);

  const relaunch = async () => {
    setFatal(null);
    setStatus(null);
    const r = await nativeApi.launch(id);
    if (!r.ok) {
      setFatal(r.error || `Couldn't start ${name}.`);
      return;
    }
    if (r.data.where === 'screen') {
      setFatal("Opened on the server's screen instead.");
      return;
    }
    setSid(r.data.stream?.id ?? '');
    setNonce((n) => n + 1);
  };

  const quit = async () => {
    if (sid) await nativeApi.stopStream(sid);
    useWM.getState().close(win.id);
  };

  const ready = !!status?.ready && !fatal;
  const failed = fatal || status?.phase === 'error' || status?.phase === 'idle';

  return (
    <div className="browser native-stream">
      <div className="browser-bar">
        <div className="brave-badge">
          <NativeAppIcon app={{ id }} size={16} /> {name}
        </div>
        <span className="browser-state muted small">{ready ? 'Connected' : (status?.message ?? 'Starting…')}</span>
        <span className="spacer" />
        <button type="button" title="Reload" aria-label="Reload" onClick={() => setNonce((n) => n + 1)} disabled={!ready}>
          <Icon name="restart" size={15} />
        </button>
        <button type="button" title="Open in a new tab" aria-label="Open in new tab" onClick={() => window.open(streamURL(sid), '_blank', 'noopener')} disabled={!ready}>
          <Icon name="external" size={15} />
        </button>
        <button type="button" title={`Quit ${name}`} aria-label={`Quit ${name}`} onClick={() => void quit()}>
          <Icon name="power" size={15} />
        </button>
      </div>

      <div className="browser-view">
        {ready ? (
          <iframe key={`${sid}-${nonce}`} className="browser-frame" src={streamURL(sid)} title={name} allow="clipboard-read; clipboard-write; fullscreen" />
        ) : (
          <div className="brave-boot">
            <div className="brave-logo">
              <NativeAppIcon app={{ id }} size={46} />
            </div>
            {failed ? (
              <>
                <b>{status?.phase === 'idle' && !fatal ? `${name} closed` : `${name} didn't open`}</b>
                <p className="muted small">{fatal || status?.message}</p>
                <button type="button" onClick={() => void relaunch()}>
                  <Icon name="restart" size={14} /> Open again
                </button>
              </>
            ) : (
              <>
                <div className="spinner" />
                <b>Starting {name}</b>
                <p className="muted small">Spinning up its own screen on the server… first launch can take a few secs.</p>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
