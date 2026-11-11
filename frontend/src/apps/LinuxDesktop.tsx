import { useCallback, useEffect, useRef, useState } from 'react';
import { getUser } from '../api/client';
import { nativeApi, streamURL, type Launched, type NativeStatus, type NativeStream } from '../api/nativeApps';
import type { ApiResult } from '../api/client';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { bridgeClipboard } from '../lib/vncClipboard';
import type { WinState } from '../state/windows';

type Kind = 'desktop' | 'screen';

// A complete Linux desktop of this person's own (panel, app menu with the
// installed Linux apps, files, terminal), running on the server and streamed
// into this window. Admins can also look at (or use) the server's own screen
// when the NoCap Desktop session is on.
export function LinuxDesktop(_: { win: WinState }) {
  const t = useT();
  const isAdmin = getUser()?.role === 'admin';
  const [status, setStatus] = useState<NativeStatus | null>(null);
  const [kind, setKind] = useState<Kind | null>(null);
  const [stream, setStream] = useState<NativeStream | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [control, setControl] = useState(false);
  const [nonce, setNonce] = useState(0);
  const polling = useRef<number | null>(null);
  const frame = useRef<HTMLIFrameElement>(null);

  const stopPolling = () => {
    if (polling.current) window.clearInterval(polling.current);
    polling.current = null;
  };

  const loadStatus = useCallback(async () => {
    const r = await nativeApi.installed();
    if (r.ok) setStatus(r.data.status);
  }, []);

  useEffect(() => {
    void loadStatus();
    return stopPolling;
  }, [loadStatus]);

  const watch = (sid: string) => {
    stopPolling();
    const tick = async () => {
      const r = await nativeApi.stream(sid);
      if (!r.ok) {
        stopPolling();
        setStream(null);
        setError(t('ldesk.gone'));
        return;
      }
      setStream(r.data);
      if (r.data.phase === 'error' || r.data.phase === 'idle') stopPolling();
      else if (r.data.ready) {
        // Ready: check in now and then (renews access, notices the end).
        stopPolling();
        polling.current = window.setInterval(async () => {
          const s = await nativeApi.stream(sid);
          if (!s.ok || !s.data.ready) {
            stopPolling();
            setStream(s.ok ? s.data : null);
            if (!s.ok) setError(t('ldesk.gone'));
          }
        }, 60_000);
      }
    };
    void tick();
    polling.current = window.setInterval(tick, 1500);
  };

  const open = async (k: Kind) => {
    setError(null);
    setStream(null);
    setKind(k);
    setControl(false);
    const r: ApiResult<Launched> = k === 'desktop' ? await nativeApi.desktop() : await nativeApi.serverScreen();
    if (!r.ok || !r.data.stream) {
      setError((!r.ok && r.error) || t('ldesk.failed'));
      return;
    }
    setStream(r.data.stream);
    watch(r.data.stream.id);
  };

  const close = async () => {
    stopPolling();
    if (stream) await nativeApi.stopStream(stream.id);
    setStream(null);
    setKind(null);
    setError(null);
    void loadStatus();
  };

  const ready = !!stream?.ready && !error;
  const sid = stream?.id ?? '';

  useEffect(() => {
    if (!ready || !frame.current) return;
    return bridgeClipboard(frame.current);
  }, [ready, sid, nonce]);

  const failed = error || stream?.phase === 'error' || stream?.phase === 'idle';
  const screenOn = isAdmin && !!status?.screen;

  return (
    <div className="browser native-stream">
      <div className="browser-bar">
        <div className="brave-badge">
          <Icon name="desktop" size={16} /> {kind === 'screen' ? t('ldesk.screenTitle') : t('ldesk.title')}
        </div>
        <span className="browser-state muted small">{ready ? t('ldesk.connected') : kind ? (stream?.message ?? t('ldesk.starting')) : ''}</span>
        <span className="spacer" />
        {kind === 'screen' && ready && (
          <button type="button" className="small" onClick={() => setControl((c) => !c)} title={control ? t('ldesk.viewOnlyHint') : t('ldesk.controlHint')}>
            <Icon name={control ? 'eye' : 'desktop'} size={14} /> {control ? t('ldesk.viewOnly') : t('ldesk.takeControl')}
          </button>
        )}
        {kind && (
          <>
            <button type="button" title={t('ldesk.reload')} aria-label={t('ldesk.reload')} onClick={() => setNonce((n) => n + 1)} disabled={!ready}>
              <Icon name="restart" size={15} />
            </button>
            <button type="button" title={t('ldesk.end')} aria-label={t('ldesk.end')} onClick={() => void close()}>
              <Icon name="power" size={15} />
            </button>
          </>
        )}
      </div>

      <div className="browser-view">
        {ready ? (
          <iframe
            ref={frame}
            key={`${sid}-${nonce}-${control}`}
            className="browser-frame"
            src={streamURL(sid, { remoteResize: false, viewOnly: kind === 'screen' && !control })}
            title={kind === 'screen' ? t('ldesk.screenTitle') : t('ldesk.title')}
            allow="clipboard-read; clipboard-write; fullscreen"
          />
        ) : (
          <div className="brave-boot">
            <div className="brave-logo">
              <Icon name="desktop" size={40} />
            </div>
            {!kind ? (
              <>
                <b>{t('ldesk.title')}</b>
                <p className="muted small">{t('ldesk.intro')}</p>
                {status && !status.desktop ? (
                  <p className="muted small">{status.reason || t('ldesk.unsupported')}</p>
                ) : (
                  <button type="button" onClick={() => void open('desktop')} disabled={!status}>
                    <Icon name="desktop" size={14} /> {t('ldesk.open')}
                  </button>
                )}
                {screenOn && (
                  <button type="button" className="ghost" onClick={() => void open('screen')}>
                    <Icon name="eye" size={14} /> {t('ldesk.viewScreen')}
                  </button>
                )}
              </>
            ) : failed ? (
              <>
                <b>{stream?.phase === 'idle' && !error ? t('ldesk.ended') : t('ldesk.failed')}</b>
                <p className="muted small">{error || stream?.message}</p>
                <button type="button" onClick={() => void open(kind)}>
                  <Icon name="restart" size={14} /> {t('ldesk.again')}
                </button>
                <button type="button" className="ghost" onClick={() => void close()}>
                  {t('ldesk.back')}
                </button>
              </>
            ) : (
              <>
                <div className="spinner" />
                <b>{kind === 'screen' ? t('ldesk.screenTitle') : t('ldesk.starting')}</b>
                <p className="muted small">{t('apps.nativeStarting')}</p>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
