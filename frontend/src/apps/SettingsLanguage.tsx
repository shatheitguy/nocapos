import { useT } from '../i18n';
import { Icon } from '../components/Icon';
import { LANGUAGES, LINGOS, usePrefs } from '../state/prefs';
import { Row, Section } from './Personalize';

// Settings → Language & Region ("Local Vibes"): the wording style (Gen Z or
// Standard, saved with the person's other preferences), language and the
// region formats.

export function LanguageRegion() {
  const t = useT();
  const prefs = usePrefs();
  const locale = Intl.DateTimeFormat().resolvedOptions().locale;
  const sample = new Date();
  return (
    <div className="stack settings-page">
      <Section title={t('set.lang.voice')} hint={t('set.lang.voiceHint')}>
        <Row>
          <div className="lingo-cards" role="radiogroup" aria-label={t('set.lang.voice')}>
            {LINGOS.map((id) => (
              <button key={id} type="button" role="radio" aria-checked={prefs.lingo === id}
                className={`lingo-card ${prefs.lingo === id ? 'on' : ''}`} onClick={() => prefs.set({ lingo: id })}>
                <span className="lingo-name">
                  {id === 'genz' ? t('set.lang.genz') : t('set.lang.standard')}
                  {prefs.lingo === id && <Icon name="check" size={14} />}
                </span>
                <span className="lingo-desc">{id === 'genz' ? t('set.lang.genzDesc') : t('set.lang.standardDesc')}</span>
              </button>
            ))}
          </div>
        </Row>
      </Section>
      <Section title={t('set.lang.language')}>
        <Row label={t('set.lang.languageRow')}>
          {LANGUAGES.length > 1 ? (
            <select className="select" value={prefs.language} onChange={(e) => prefs.set({ language: e.target.value as typeof prefs.language })}>
              {LANGUAGES.map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name}
                </option>
              ))}
            </select>
          ) : (
            <span className="settings-value">{LANGUAGES[0].name}</span>
          )}
        </Row>
      </Section>
      <Section title={t('set.lang.region')} hint={t('set.lang.regionHint', { locale })}>
        <Row label={t('set.lang.date')}>
          <span className="settings-value">{sample.toLocaleDateString()}</span>
        </Row>
        <Row label={t('set.lang.number')}>
          <span className="settings-value">{(1234567.89).toLocaleString()}</span>
        </Row>
        <Row label={t('set.lang.currency')}>
          <span className="settings-value">{(1234.5).toLocaleString(undefined, { style: 'currency', currency: currencyFor(locale) })}</span>
        </Row>
      </Section>
    </div>
  );
}

function currencyFor(locale: string): string {
  const region = locale.split('-')[1]?.toUpperCase();
  const map: Record<string, string> = { AE: 'AED', US: 'USD', GB: 'GBP', IN: 'INR', SA: 'SAR', JP: 'JPY', CN: 'CNY', EU: 'EUR', DE: 'EUR', FR: 'EUR' };
  return map[region ?? ''] ?? 'USD';
}
