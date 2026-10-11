import { usePrefs } from '../state/prefs';
import { Section, Toggle } from './Personalize';

// Settings → Focus.

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
