// Settings → General → This Screen & Access (admins): NoCap Desktop, the
// native desktop on the server's own monitor (on or off), and whether other
// devices may open NoCapOS at all.
import { useEffect, useState } from 'react';
import { hostAccessApi, type DesktopHealth, type HostAccess } from '../api/hostaccess';
import { useT, type T } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { copyWithToast, missing } from '../lib/settingsKit';
import { openApp } from './meta';
import { Row, Section as Group, Toggle } from './Personalize';

export function HostAccessPage() {
  const t = useT();
  const [st, setSt] = useState<HostAccess | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'desktop' | 'remote' | 'repair' | null>(null);
  const [diag, setDiag] = useState<string | null>(null);
  const [diagOpen, setDiagOpen] = useState(false);

  useEffect(() => {
    void hostAccessApi.get().then((r) => (r.ok && !missing(r) ? setSt(r.data) : setError(missing(r) ? t('set.notAvailable') : (r.error ?? t('set.loadFail')))));
  }, []);

  const setDesktop = async (on: boolean) => {
    if (!st || on === (st.desktop.mode === 'full')) return;
    // Turning it off signs out whoever is using it.
    if (!on && st.desktop.session_user) {
      const ok = await confirmDialog({ title: t('nd.confirmTitle'), message: t('nd.confirmBody'), confirmLabel: t('nd.confirmGo'), danger: true });
      if (!ok) return;
    }
    setBusy('desktop');
    const r = await hostAccessApi.setDesktopMode(on ? 'full' : 'off');
    setBusy(null);
    if (!r.ok) return toast('error', t('nd.switchFailed'), r.error);
    setSt(r.data);
    toast('success', t(on ? 'nd.switched.on' : 'nd.switched.off'));
  };

  const setAutologin = async (on: boolean) => {
    setBusy('desktop');
    const r = await hostAccessApi.setAutologin(on);
    setBusy(null);
    if (!r.ok) return toast('error', t('nd.autologin.failed'), r.error);
    setSt(r.data);
  };

  const repair = async () => {
    const ok = await confirmDialog({ title: t('nd.repair.confirmTitle'), message: t('nd.repair.confirmBody'), confirmLabel: t('nd.repair.confirmGo'), danger: true });
    if (!ok) return;
    setBusy('repair');
    const r = await hostAccessApi.repairDesktop();
    setBusy(null);
    if (!r.ok) return toast('error', t('nd.repair.failed'), r.error);
    setSt(r.data.state);
    toast('success', t('nd.repair.done'), r.data.actions.join(' · '));
  };

  const toggleDiagnostics = async () => {
    if (diagOpen) return setDiagOpen(false);
    setDiagOpen(true);
    setDiag(null);
    const r = await hostAccessApi.desktopDiagnostics();
    if (!r.ok) {
      setDiagOpen(false);
      return toast('error', t('nd.diag.failed'), r.error);
    }
    setDiag(r.data.text);
  };

  const setRemote = async (on: boolean) => {
    if (!st) return;
    let confirm = false;
    if (!on) {
      const away = !st.client_local;
      const ok = await confirmDialog({
        title: t(away ? 'su.lockOutTitle' : 'su.localTitle'),
        message: t(away ? 'su.lockOutMsg' : 'su.localMsg'),
        confirmLabel: t(away ? 'su.lockOutGo' : 'su.localGo'),
        danger: true,
      });
      if (!ok) return;
      confirm = true;
    }
    setBusy('remote');
    const r = await hostAccessApi.setRemote(on, confirm);
    setBusy(null);
    if (!r.ok) return toast('error', t('su.remoteFail'), r.error);
    setSt(r.data);
    if (on) toast('success', t('su.remoteBack'));
    else if (!st.client_local) toast('info', t('su.lockedOut'));
    else toast('success', t('su.localNow'));
  };

  if (error) return <p className="muted">{error}</p>;
  if (!st) return <p className="muted">{t('sp.loading')}</p>;

  const d = st.desktop;
  const status = d.mode === 'off'
    ? t('nd.status.off')
    : !d.active
      ? t('nd.status.stopped')
      : d.session_user
        ? t('nd.status.signedIn', { user: d.session_user })
        : d.sign_in_screen
          ? t('nd.status.signInScreen')
          : t('nd.status.running');

  const remoteHint = !st.remote.supported
    ? (st.remote.reason ?? t('set.notAvailable'))
    : st.remote.enabled
      ? t('su.remoteOn')
      : t('su.remoteOff');

  return (
    <div className="stack settings-page">
      <Group title={t('nd.screen')} hint={t('nd.screen.hint')}>
        {!d.available ? (
          <Row label={t('nd.mode')} hint={d.reason ?? t('nd.notSetUp')}>
            <span className="settings-value">{t('nd.mode.off')}</span>
          </Row>
        ) : (
          <>
            <Toggle label={t('nd.mode')} hint={t(d.mode === 'full' ? 'nd.full.hint' : 'nd.off.hint')}
              checked={d.mode === 'full'} disabled={busy === 'desktop'} onChange={(on) => void setDesktop(on)} />
            <Row label={t('nd.status')} hint={d.display ? undefined : t('nd.noScreen')}>
              <span className="settings-value">
                <span className={`status-dot ${d.active ? 'on' : 'off'}`} /> {status}
              </span>
            </Row>
            {d.mode === 'full' && d.autologin_supported && (
              <Toggle label={t('nd.autologin')} hint={d.autologin && d.autologin_user ? t('nd.autologin.on', { user: d.autologin_user }) : t('nd.autologin.hint')}
                checked={d.autologin} disabled={busy === 'desktop'} onChange={(on) => void setAutologin(on)} />
            )}
            {d.health && d.health.status !== 'off' && d.health.status !== 'unavailable' && (
              <Row label={t('nd.health')} hint={healthHint(d.health, t)}>
                <span className="settings-value">
                  <span className={`status-dot ${d.health.status === 'ok' ? 'on' : 'off'}`} /> {t(`nd.health.${d.health.status}`)}
                </span>
              </Row>
            )}
            <Row label={t('nd.repair')} hint={t('nd.repair.hint')}>
              <button type="button" disabled={busy === 'repair'} onClick={() => void repair()}>{t('nd.repair.button')}</button>
            </Row>
            <Row label={t('nd.diag')} hint={t('nd.diag.hint')}>
              <button type="button" className="ghost" onClick={() => void toggleDiagnostics()}>{t(diagOpen ? 'nd.diag.hide' : 'nd.diag.button')}</button>
            </Row>
            {diagOpen && (
              <div className="stack">
                {diag === null ? (
                  <p className="muted">{t('nd.diag.loading')}</p>
                ) : (
                  <>
                    <div>
                      <button type="button" className="ghost" onClick={() => void copyWithToast(diag, t('nd.diag.copied'))}>
                        {t('nd.diag.copy')}
                      </button>
                    </div>
                    <pre style={{ maxHeight: '24rem', overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: '0.75rem', margin: 0 }}>{diag}</pre>
                  </>
                )}
              </div>
            )}
            {d.remote && !st.client_local && (
              <Row label={t('nd.open')} hint={t('nd.open.hint')}>
                <button type="button" onClick={() => openApp('nocapdesktop')}>{t('nd.open.button')}</button>
              </Row>
            )}
          </>
        )}
      </Group>
      <Group title={t('su.remoteTitle')}>
        <Toggle label={t('su.remoteRow')} hint={remoteHint} checked={st.remote.enabled}
          disabled={!st.remote.supported || busy === 'remote'} onChange={(on) => void setRemote(on)} />
        <Row label={t('su.thisBrowser')} hint={t(st.client_local ? 'su.onServerHint' : 'su.otherDeviceHint')}>
          <span className="settings-value">{t(st.client_local ? 'su.onServer' : 'su.otherDevice')}</span>
        </Row>
      </Group>
    </div>
  );
}

/** What's wrong with the screen, in the reader's voice (problems, then the fallback in use). */
function healthHint(h: DesktopHealth, t: T): string | undefined {
  const parts = (h.problems ?? []).map((p) => t(`nd.health.p.${p.code}`));
  if (h.fallback) parts.push(t(`nd.health.fallback.${h.fallback}`));
  return parts.length ? parts.join(' ') : undefined;
}
