import { useT } from '../i18n';
import { usePrefs } from '../state/prefs';
import { Section, Toggle } from './Personalize';

// Settings → Notifications → Focus.

export function FocusSettings() {
  const t = useT();
  const focus = usePrefs((s) => s.focusMode);
  const set = usePrefs((s) => s.set);
  return (
    <Section title={t('su.focus')} hint={t('su.focusHint')}>
      <Toggle label={t('su.focusRow')} hint={t('su.focusRowHint')} checked={focus} onChange={(focusMode) => set({ focusMode })} />
    </Section>
  );
}
