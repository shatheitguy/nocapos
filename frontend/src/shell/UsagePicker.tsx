import type { CSSProperties } from 'react';
import { useT } from '../i18n';
import { Icon, type IconName } from '../components/Icon';
import type { Usage } from '../api/hostaccess';

// "How will you use NoCapOS?": the same three choices in the first-run
// wizard and in Settings > This Screen & Access.

export function UsagePicker({ value, onChange, disabled }: { value: Usage | null; onChange: (u: Usage) => void; disabled?: boolean }) {
  const t = useT();
  const opts: { id: Usage; icon: IconName; title: string; text: string }[] = [
    { id: 'desktop-web', icon: 'devices', title: t('use.both'), text: t('use.bothText') },
    { id: 'desktop', icon: 'desktop', title: t('use.desktop'), text: t('use.desktopText') },
    { id: 'web', icon: 'globe', title: t('use.web'), text: t('use.webText') },
  ];
  return (
    <div className="usage-picker" role="radiogroup" aria-label={t('use.title')}>
      {opts.map((o, i) => (
        <button
          type="button"
          key={o.id}
          role="radio"
          aria-checked={value === o.id}
          disabled={disabled}
          className={`usage-opt ${value === o.id ? 'on' : ''}`}
          style={{ '--i': i } as CSSProperties}
          onClick={() => onChange(o.id)}
        >
          <Icon name={o.icon} size={22} />
          <b>{o.title}</b>
          <span>{o.text}</span>
        </button>
      ))}
    </div>
  );
}
