import { useEffect, useState } from 'react';
import { api, getUser } from '../api/client';
import { Icon } from '../components/Icon';
import { tr, useT } from '../i18n';
import { toast } from '../state/toasts';
import { Section, Toggle } from './Personalize';

// Settings → Network: other NoCap servers on this network (found over mDNS
// by this server), and, for administrators, whether phones nearby may find
// this one over Bluetooth. Bluetooth is only for finding: linking and setup
// still go over the home network.

export interface FoundServer {
  nocap_id: string;
  name: string;
  hostname: string;
  addresses: string[];
  url: string;
  version: string;
  setup: 'new' | 'done' | '';
}

interface Bluetooth {
  mode: 'on' | 'off' | 'auto';
  enabled: boolean;
  supported: boolean;
  adapter: boolean;
  advertising: boolean;
  setup_new: boolean;
  error?: string;
}

export const findApi = {
  discover: () => api<{ available: boolean; servers: FoundServer[] }>('/api/v1/link/discover'),
  bluetooth: () => api<Bluetooth>('/api/v1/link/bluetooth'),
  setBluetooth: (enabled: boolean) => api<Bluetooth>('/api/v1/link/bluetooth', { method: 'PUT', body: { enabled } }),
};

/** Only http(s) links leave the page. */
const safeURL = (u: string) => (/^https?:\/\//i.test(u) ? u : '');

export function FindServersSection() {
  const t = useT();
  const isAdmin = getUser()?.role === 'admin';
  const [found, setFound] = useState<{ available: boolean; servers: FoundServer[] } | null>(null);
  const [looking, setLooking] = useState(false);
  const [bt, setBt] = useState<Bluetooth | null>(null);
  const [btBusy, setBtBusy] = useState(false);

  useEffect(() => {
    if (isAdmin) void findApi.bluetooth().then((r) => r.ok && setBt(r.data));
  }, [isAdmin]);

  const look = async () => {
    setLooking(true);
    const r = await findApi.discover();
    setLooking(false);
    if (!r.ok) return toast('error', tr('find.failed'), r.error);
    setFound({ available: r.data.available, servers: r.data.servers ?? [] });
  };

  const flip = async (on: boolean) => {
    setBtBusy(true);
    const r = await findApi.setBluetooth(on);
    setBtBusy(false);
    if (!r.ok) return toast('error', tr('find.btFailed'), r.error);
    setBt(r.data);
  };

  const btHint = !bt
    ? t('find.btHint')
    : !bt.supported
      ? t('find.btUnsupported')
      : !bt.adapter
        ? t('find.btNoAdapter')
        : bt.advertising
          ? t('find.btOn')
          : t('find.btHint');

  return (
    <>
      {isAdmin && bt && (
        <Section title={t('find.btTitle')}>
          <Toggle label={t('find.btLabel')} hint={btHint} checked={bt.enabled} disabled={btBusy || !bt.supported} onChange={(v) => void flip(v)} />
        </Section>
      )}
      <Section title={t('find.title')} hint={t('find.hint')}>
        <div className="settings-row">
          <span className="settings-row-text">
            <span className="settings-row-label">{t('find.label')}</span>
            <span className="settings-row-hint">{looking ? t('find.looking') : found ? t('find.count', { n: found.servers.length }) : t('find.idle')}</span>
          </span>
          <div className="settings-row-control">
            <button type="button" className="small" disabled={looking} onClick={() => void look()}>
              {looking ? <span className="spinner sm" /> : <Icon name="search" size={14} />} {found ? t('find.again') : t('find.go')}
            </button>
          </div>
        </div>
        {found && !found.available && (
          <div className="settings-row">
            <span className="settings-row-hint">{t('find.unavailable')}</span>
          </div>
        )}
        {found?.available && found.servers.length === 0 && (
          <div className="settings-row">
            <span className="settings-row-hint">{t('find.none')}</span>
          </div>
        )}
        {found?.servers.map((s) => {
          const href = safeURL(s.url);
          return (
            <div key={s.nocap_id} className="settings-row find-server">
              <span className="settings-row-text">
                <span className="settings-row-label">
                  {s.name || s.hostname || s.nocap_id}
                  {s.setup === 'new' && <span className="find-badge">{t('find.new')}</span>}
                </span>
                <span className="settings-row-hint">
                  {[s.addresses.join(' · '), s.version ? t('find.version', { v: s.version }) : '', s.nocap_id].filter(Boolean).join(' — ')}
                </span>
              </span>
              {href && (
                <div className="settings-row-control">
                  <a className="button small ghost" href={href} target="_blank" rel="noreferrer noopener">
                    {s.setup === 'new' ? t('find.setUp') : t('find.open')} <Icon name="external" size={13} />
                  </a>
                </div>
              )}
            </div>
          );
        })}
      </Section>
    </>
  );
}
