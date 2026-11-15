// The on-screen display (260 x 72, bottom centre): volume or brightness level
// after a key press. The host shows the surface for 1.5 s per osd.show.
import { useEffect, useState } from 'react';
import { Icon, type IconName } from '../../components/Icon';
import { call } from '../bridge';
import { useEvent, useShellInfo } from '../hooks';
import { useT } from '../i18n';
import type { AudioState, OsdEvent } from '../protocol';

export function Osd() {
  useShellInfo();
  const t = useT();
  const [osd, setOsd] = useState<OsdEvent | null>(null);

  // Something sensible before the first event: the current volume.
  useEffect(() => {
    call<AudioState>('audio.get')
      .then((a) => setOsd((o) => o ?? { kind: 'volume', value: a.volume, muted: a.muted }))
      .catch(() => {});
  }, []);
  useEvent('osd.show', (p) => {
    const e = p as OsdEvent;
    if (!e || (e.kind !== 'volume' && e.kind !== 'brightness')) return;
    setOsd(e);
  });

  if (!osd) return <div className="nsh-osd-surface" />;
  const value = Math.max(0, Math.min(100, Math.round(osd.value)));
  const muted = osd.kind === 'volume' && (!!osd.muted || value === 0);
  const icon: IconName = osd.kind === 'brightness' ? (value < 50 ? 'moon' : 'sun') : muted ? 'mute' : 'sound';
  const label = osd.kind === 'brightness' ? t('qs.brightness') : t('nsh.cc.volume');
  return (
    <div className="nsh-osd-surface">
      <div className={`nsh-osd ${muted ? 'muted' : ''}`} role="status" aria-label={`${label} ${muted ? t('nsh.cc.muted') : `${value}%`}`}>
        <span className="nsh-osd-icon">
          <Icon name={icon} size={20} />
        </span>
        <span className="nsh-osd-bar">
          <span style={{ width: `${muted ? 0 : value}%` }} />
        </span>
        <span className="nsh-osd-value">{muted ? t('nsh.cc.muted') : value}</span>
      </div>
    </div>
  );
}
