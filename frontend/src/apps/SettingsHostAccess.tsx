// Settings → General → This Screen & Access (admins): NoCap Desktop, the
// native desktop on the server's own monitor (on or off), and whether other
// devices may open NoCapOS at all.
import { useEffect, useState } from 'react';
import { hostAccessApi, type HostAccess } from '../api/hostaccess';
import { useT } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { openApp } from './meta';
import { Row, Section as Group, Toggle } from './Personalize';

export function HostAccessPage() {
  const t = useT();
  const [st, setSt] = useState<HostAccess | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'desktop' | 'remote' | null>(null);

  useEffect(() => {
    void hostAccessApi.get().then((r) => (r.ok ? setSt(r.data) : setError(r.error ?? 'Couldn’t load these settings')));
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

  const setRemote = async (on: boolean) => {
    if (!st) return;
    let confirm = false;
    if (!on) {
      const away = !st.client_local;
      const ok = await confirmDialog({
        title: away ? 'Lock this browser out?' : 'Make NoCapOS local-only?',
        message: away
          ? 'You’ll lock yourself out of this browser — undo from the server’s screen or `sudo cap web on`. Linked phones and every other device won’t reach the server either while it’s off.'
          : 'Only this machine’s own screen will open NoCapOS. Linked phones and every other device won’t reach the server while it’s off. Undo here or with `sudo cap web on`.',
        confirmLabel: away ? 'Lock me out' : 'Go local-only',
        danger: true,
      });
      if (!ok) return;
      confirm = true;
    }
    setBusy('remote');
    const r = await hostAccessApi.setRemote(on, confirm);
    setBusy(null);
    if (!r.ok) return toast('error', 'Couldn’t change who can open NoCapOS', r.error);
    setSt(r.data);
    if (on) toast('success', 'Other devices can open NoCapOS again');
    else if (!st.client_local) toast('info', 'Local-only is on. This browser is locked out now — see you on the server’s screen.');
    else toast('success', 'NoCapOS is local-only now');
  };

  if (error) return <p className="muted">{error}</p>;
  if (!st) return <p className="muted">Loading…</p>;

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
    ? (st.remote.reason ?? 'Not available here.')
    : st.remote.enabled
      ? 'Phones, laptops and other devices on your network can open NoCapOS (they still sign in).'
      : 'Local-only: just this machine’s own screen. Linked phones and other devices can’t reach it. Undo from the console: sudo cap web on';

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
            {d.remote && !st.client_local && (
              <Row label={t('nd.open')} hint={t('nd.open.hint')}>
                <button type="button" onClick={() => openApp('nocapdesktop')}>{t('nd.open.button')}</button>
              </Row>
            )}
          </>
        )}
      </Group>
      <Group title="Access from other devices">
        <Toggle label="Let other devices open NoCapOS" hint={remoteHint} checked={st.remote.enabled}
          disabled={!st.remote.supported || busy === 'remote'} onChange={(on) => void setRemote(on)} />
        <Row label="This browser" hint={st.client_local ? 'You’re on the server itself, so you stay in either way.' : 'You’re on another device. Turning access off locks this browser out.'}>
          <span className="settings-value">{st.client_local ? 'On the server' : 'Another device'}</span>
        </Row>
      </Group>
    </div>
  );
}
