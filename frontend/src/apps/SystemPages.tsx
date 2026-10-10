import { useEffect, useState } from 'react';
import { logout } from '../api/client';
import { hostApi, type PowerInfo } from '../api/hostctl';
import { Icon } from '../components/Icon';
import { powerHost } from '../lib/hostActions';
import { usePrefs } from '../state/prefs';
import { Row, Section, Toggle } from './Personalize';

// Settings → Focus and Power.

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
