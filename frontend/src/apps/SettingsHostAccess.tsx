// Settings → General → This Screen & Access (admins): NoCap Desktop on the
// server's own monitor, and whether other devices may open NoCapOS at all.
import { useEffect, useState } from 'react';
import { hostAccessApi, type HostAccess } from '../api/hostaccess';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Row, Section as Group, Toggle } from './Personalize';

export function HostAccessPage() {
  const [st, setSt] = useState<HostAccess | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'desktop' | 'remote' | null>(null);

  useEffect(() => {
    void hostAccessApi.get().then((r) => (r.ok ? setSt(r.data) : setError(r.error ?? 'Couldn’t load these settings')));
  }, []);

  const setDesktop = async (on: boolean) => {
    setBusy('desktop');
    const r = await hostAccessApi.setDesktop(on);
    setBusy(null);
    if (!r.ok) return toast('error', on ? 'NoCap Desktop didn’t start' : 'NoCap Desktop didn’t stop', r.error);
    setSt(r.data);
    toast('success', on ? 'NoCap Desktop is on — check the server’s screen' : 'NoCap Desktop is off');
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
  const desktopHint = !d.available
    ? (d.reason ?? 'NoCap Desktop isn’t set up on this server.')
    : d.enabled
      ? d.active
        ? 'Running on the server’s screen. It opens NoCapOS’s own sign-in, so nobody skips the password.'
        : 'On, but not running right now. Check the screen is plugged in, or turn it off and on again.'
      : d.display
        ? 'Turns the screen plugged into this server into a full NoCapOS desktop. Sign-in still needed.'
        : 'No screen detected right now. Plug one in and flip this on to use NoCapOS right on the box.';

  const remoteHint = !st.remote.supported
    ? (st.remote.reason ?? 'Not available here.')
    : st.remote.enabled
      ? 'Phones, laptops and other devices on your network can open NoCapOS (they still sign in).'
      : 'Local-only: just this machine’s own screen. Linked phones and other devices can’t reach it. Undo from the console: sudo cap web on';

  return (
    <div className="stack settings-page">
      <Group title="This screen" hint="Use the server like a real computer: monitor, keyboard, mouse, done.">
        <Toggle label="NoCap Desktop on this screen" hint={desktopHint} checked={d.available && d.enabled}
          disabled={!d.available || busy === 'desktop'} onChange={(on) => void setDesktop(on)} />
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
