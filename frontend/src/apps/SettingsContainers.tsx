import { useEffect, useState } from 'react';
import { dockerApi, downloadWithAuth, type LocalAddresses } from '../api/docker';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { toast } from '../state/toasts';
import { Section, Toggle } from './Personalize';

// Settings → Containers → Local addresses: give every container a name on
// the home network (http(s)://<name>.nocap.local), and download NoCapOS's
// certificate so devices trust the HTTPS ones. LAN only.

export function ContainersSettings() {
  const t = useT();
  const [state, setState] = useState<LocalAddresses | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void dockerApi.localAddresses().then((r) => (r.ok ? setState(r.data) : setError(r.error ?? t('ctr.la.loadFail'))));
  }, [t]);

  const toggle = async (on: boolean) => {
    setBusy(true);
    const r = await dockerApi.setLocalAddresses(on);
    setBusy(false);
    if (!r.ok) return toast('error', t('ctr.la.saveFail'), r.error);
    setState(r.data);
    toast('success', on ? t('ctr.la.on') : t('ctr.la.off'));
  };

  const download = async () => {
    const err = await downloadWithAuth('/api/v1/docker/local-addresses/ca.crt', 'nocapos-local-ca.crt');
    if (err) toast('error', t('ctr.la.caFail'), err);
  };

  if (error) return <p className="error">{error}</p>;
  if (!state) return <span className="spinner" />;
  const via = [state.mdns && t('ctr.la.viaMdns'), state.dns && t('ctr.la.viaDns')].filter(Boolean).join(' · ');

  return (
    <div className="stack settings-page">
      <Section title={t('ctr.la.title')} hint={t('ctr.la.hint')}>
        <Toggle label={t('ctr.la.toggle')} hint={t('ctr.la.toggleHint')} checked={state.enabled} disabled={busy} onChange={(v) => void toggle(v)} />
      </Section>
      {state.enabled && (
        <>
          <Section title={t('ctr.la.trustTitle')} hint={state.https ? t('ctr.la.trustHint') : t('ctr.la.noHttps')}>
            <div className="settings-row">
              <span className="settings-row-text">
                <span className="settings-row-label">{t('ctr.la.ca')}</span>
                <span className="settings-row-hint">{t('ctr.la.caHint')}</span>
              </span>
              <button type="button" className="ghost" onClick={() => void download()}>
                <Icon name="download" size={14} /> {t('ctr.la.download')}
              </button>
            </div>
          </Section>
          <Section title={t('ctr.la.listTitle', { n: state.addresses.length })} hint={via ? t('ctr.la.announced', { via }) : t('ctr.la.notAnnounced')}>
            {state.addresses.length ? (
              <div className="la-list">
                {state.addresses.map((a) => (
                  <div key={a.name} className="settings-row la-row">
                    <span className="settings-row-text">
                      <a className="settings-row-label mono" href={a.https || a.http} target="_blank" rel="noopener noreferrer">{a.name}</a>
                      <span className="settings-row-hint">{t(`ctr.la.src.${a.source}`, { name: a.container })}</span>
                    </span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="muted small la-empty">{t('ctr.la.empty')}</p>
            )}
            {state.error && <p className="error small">{state.error}</p>}
          </Section>
          <Section title={t('ctr.la.customTitle')} hint={t('ctr.la.customHint')}>
            <div className="settings-row">
              <code className="mono">nocap.domain=api.myapp</code>
            </div>
            <div className="settings-row">
              <code className="mono">nocap.port=8080</code>
            </div>
          </Section>
        </>
      )}
    </div>
  );
}
