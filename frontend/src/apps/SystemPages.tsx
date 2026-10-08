import { useEffect, useState } from 'react';
import { logout } from '../api/client';
import { hostApi, type NetworkState, type PowerInfo } from '../api/hostctl';
import { Icon } from '../components/Icon';
import { powerHost, setNetwork } from '../lib/hostActions';
import { usePrefs } from '../state/prefs';
import { IPv4Editor, IPv4Summary, WifiList } from './NetworkParts';
import { Row, Section, Toggle } from './Personalize';

// Settings → Network, Focus and Power.

export function NetworkSettings() {
  const [net, setNet] = useState<NetworkState | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [rev, setRev] = useState(0); // remount editors after a change

  const load = () =>
    void hostApi.network().then((r) => {
      if (r.ok) {
        setNet(r.data);
        setRev((v) => v + 1);
      } else setError(r.error ?? 'Could not read network status');
    });
  useEffect(load, []);

  const flip = async (kind: 'wifi' | 'networking', on: boolean) => {
    setBusy(true);
    const next = await setNetwork(kind, on);
    if (next) setNet(next);
    setBusy(false);
  };

  if (error) return <p className="error">{error}</p>;
  if (!net) return <p className="muted">Checking network…</p>;
  const managed = net.manager === 'networkmanager';
  const wireless = net.interfaces.find((i) => i.kind === 'wifi');
  return (
    <div className="stack settings-page">
      {!managed && (
        <div className="users-mode">
          <Icon name="info" size={18} />
          <div>
            <b>Viewing only</b>
            <span>{net.wifi.reason ?? 'Changing network settings needs NetworkManager on a Linux host.'}</span>
          </div>
        </div>
      )}

      <Section title="Wi-Fi">
        <Toggle label="Wi-Fi" hint={net.wifi.supported ? (net.wifi.enabled ? wireless?.connection || 'Not connected' : 'Off') : 'Not available on this host'}
          checked={net.wifi.enabled} disabled={!net.wifi.supported || busy} onChange={(on) => void flip('wifi', on)} />
        {net.wifi.supported && net.wifi.enabled && (
          <div className="settings-row full">
            <WifiList device={wireless?.name} onChanged={load} />
          </div>
        )}
      </Section>

      <Section title="Networking">
        <Toggle label="Networking" hint="Every network connection on this machine"
          checked={net.networking.enabled} disabled={!net.networking.supported || busy} onChange={(on) => void flip('networking', on)} />
      </Section>

      {net.interfaces.map((i) => (
        <Section key={`${i.name}-${rev}`} title={i.name} hint={[i.kind === 'wifi' ? 'Wi-Fi' : i.kind === 'ethernet' ? 'Ethernet' : 'Adapter', i.connection, i.mac].filter(Boolean).join(' · ')}>
          <div className="settings-row full">
            <IPv4Summary iface={i} />
          </div>
          <IPv4Editor iface={i} onApplied={load} />
        </Section>
      ))}

      <button type="button" className="ghost small align-start" onClick={load}>
        <Icon name="restart" size={13} /> Refresh
      </button>
    </div>
  );
}

export function FocusSettings() {
  const focus = usePrefs((s) => s.focusMode);
  const set = usePrefs((s) => s.set);
  return (
    <div className="stack settings-page">
      <Section title="Focus">
        <Toggle label="Focus" hint="Hides notifications and silences their sounds. Errors still show so nothing important is missed."
          checked={focus} onChange={(focusMode) => set({ focusMode })} />
      </Section>
      <p className="settings-tip">Tip: Focus is also a tile in the Control Center.</p>
    </div>
  );
}

export function PowerSettings({ isAdmin }: { isAdmin: boolean }) {
  const [info, setInfo] = useState<PowerInfo | null>(null);
  useEffect(() => {
    void hostApi.power().then((r) => r.ok && setInfo(r.data));
  }, []);
  const can = isAdmin && !!info?.supported;
  const why = !isAdmin ? 'Only an administrator can restart or shut down this machine.' : info && !info.supported ? info.reason : undefined;
  return (
    <div className="stack settings-page">
      <Section title="This machine" hint={why}>
        <Row label="Restart" hint="Closes every app and session, then starts the machine again">
          <button type="button" className="ghost small" disabled={!can} onClick={() => void powerHost('reboot')}>
            <Icon name="restart" size={13} /> Restart…
          </button>
        </Row>
        <Row label="Shut down" hint="Powers the machine off — you'll need local access to turn it on">
          <button type="button" className="ghost small danger" disabled={!can} onClick={() => void powerHost('shutdown')}>
            <Icon name="power" size={13} /> Shut down…
          </button>
        </Row>
      </Section>
      <Section title="Session">
        <Row label="Sign out" hint="Ends your NoCapOS session on this browser">
          <button type="button" className="ghost small" onClick={() => void logout()}>
            <Icon name="logout" size={13} /> Sign out
          </button>
        </Row>
      </Section>
    </div>
  );
}
