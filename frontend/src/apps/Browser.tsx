import { useCallback, useEffect, useRef, useState } from 'react';
import { BRAVE_URL, braveStatus, startBrave, type BraveStatus } from '../api/brave';
import { Icon } from '../components/Icon';

// The Brave app streams the real Brave browser installed natively on the Linux
// host. alfad installs it on first launch, runs it on a virtual display and
// proxies the screen to this iframe, so every website works.
export function Browser() {
  const [status, setStatus] = useState<BraveStatus>({ phase: 'starting', message: 'Preparing Brave…', ready: false });
  const [fatal, setFatal] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0); // bump to reload the iframe
  const frame = useRef<HTMLIFrameElement>(null);
  const polling = useRef<number | null>(null);

  const begin = useCallback(async () => {
    setFatal(null);
    setStatus({ phase: 'starting', message: 'Preparing Brave…', ready: false });
    const r = await startBrave();
    if (!r.ok) {
      setFatal(r.status === 403 ? 'Only an administrator can start Brave.' : r.error || 'Could not start Brave.');
      return;
    }
    setStatus(r.data);
    if (polling.current) window.clearInterval(polling.current);
    polling.current = window.setInterval(async () => {
      const s = await braveStatus();
      if (!s.ok) return;
      setStatus(s.data);
      if (s.data.ready || s.data.phase === 'error') {
        if (polling.current) window.clearInterval(polling.current);
        polling.current = null;
      }
    }, 1500);
  }, []);

  useEffect(() => {
    void begin();
    return () => {
      if (polling.current) window.clearInterval(polling.current);
    };
  }, [begin]);

  const reload = () => setNonce((n) => n + 1);
  const openTab = () => window.open(BRAVE_URL, '_blank', 'noopener');

  return (
    <div className="browser">
      <div className="browser-bar">
        <div className="brave-badge">
          <Icon name="brave" size={16} /> Brave
        </div>
        <span className="browser-state muted small">
          {status.ready ? 'Connected' : status.message}
        </span>
        <span className="spacer" />
        <button type="button" title="Reload" aria-label="Reload" onClick={reload} disabled={!status.ready}>
          <Icon name="restart" size={15} />
        </button>
        <button type="button" title="Open in a new tab" aria-label="Open in new tab" onClick={openTab} disabled={!status.ready}>
          <Icon name="external" size={15} />
        </button>
      </div>

      <div className="browser-view">
        {status.ready ? (
          <iframe
            key={nonce}
            ref={frame}
            className="browser-frame"
            src={BRAVE_URL}
            title="Brave"
            allow="clipboard-read; clipboard-write; fullscreen"
          />
        ) : (
          <div className="brave-boot">
            <div className="brave-logo">
              <Icon name="brave" size={46} />
            </div>
            {fatal || status.phase === 'error' ? (
              <>
                <b>Brave couldn’t start</b>
                <p className="muted small">{fatal || status.message}</p>
                <button type="button" onClick={() => void begin()}>
                  <Icon name="restart" size={14} /> Try again
                </button>
              </>
            ) : (
              <>
                <div className="spinner" />
                <b>{status.phase === 'installing' ? 'Installing Brave' : 'Starting Brave'}</b>
                <p className="muted small">{status.message}</p>
                {status.phase === 'installing' && (
                  <p className="muted small">
                    First launch installs Brave from its official repository onto this system. Later launches are instant.
                  </p>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
