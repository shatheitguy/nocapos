import { useState } from 'react';
import { shieldApi, type DnsSettings, type DnsStatus, type FwState } from '../api/shield';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { Toggle } from './Personalize';
import { Card, Select, fwCall } from './ShieldCommon';
import { useDnsSave } from './ShieldDns';

// Shield → Settings: upstream resolvers, blocking style, cache, the query
// log's privacy, and the firewall's logging and IPv6.

// Presets by address only: each works over plain DNS, TLS and HTTPS.
const UPSTREAM_PRESETS = ['9.9.9.9', '1.1.1.1', 'tls://9.9.9.9', 'tls://1.1.1.1', 'https://9.9.9.9/dns-query', 'https://1.1.1.1/dns-query'];

export function SettingsTab({ fw, setFw, dns, setDns }: { fw: FwState; setFw: (s: FwState) => void; dns: DnsStatus; setDns: (s: DnsStatus) => void }) {
  const t = useT();
  const save = useDnsSave(setDns);
  const s = dns.settings;
  const patch = (p: Partial<DnsSettings>) => void save({ ...s, ...p });
  const [ups, setUps] = useState(s.upstreams.join('\n'));
  const upsDirty = ups.trim() !== s.upstreams.join('\n').trim();

  return (
    <>
      <Card title={t('sh.set.upstreams')} icon="globe" hint={t('sh.set.upstreamsHint')}>
        <textarea className="sh-textarea" rows={4} spellCheck={false} value={ups} placeholder={t('sh.set.upstreamsPh')} onChange={(e) => setUps(e.target.value)} />
        <div className="chip-row">
          {UPSTREAM_PRESETS.map((p) => (
            <button key={p} type="button" className="chip-btn" onClick={() => setUps((x) => (x.split('\n').includes(p) ? x : `${x.trim()}\n${p}`.trim()))}>
              <Icon name="plus" size={11} />{p}
            </button>
          ))}
        </div>
        <div className="btn-group">
          <button type="button" className="small" disabled={!upsDirty} onClick={() => void save({ ...s, upstreams: ups.split(/\s+/).filter(Boolean) }, t('sh.saved'))}>{t('sh.save')}</button>
        </div>
        <p className="muted sh-small">{t('sh.set.kinds')}</p>
      </Card>

      <Card title={t('sh.set.blocking')} icon="shield">
        <div className="sh-defaults">
          <label>
            <span>{t('sh.set.blockMode')}</span>
            <Select label={t('sh.set.blockMode')} value={s.block_mode} options={[{ id: 'null', label: t('sh.set.modeNull') }, { id: 'nxdomain', label: t('sh.set.modeNx') }]} onChange={(v) => patch({ block_mode: v })} />
          </label>
          <label>
            <span>{t('sh.set.refresh')}</span>
            <Select label={t('sh.set.refresh')} value={s.refresh_hours} options={[0, 6, 12, 24, 72, 168].map((h) => ({ id: h, label: h ? t('sh.set.everyH', { n: h }) : t('sh.set.never') }))} onChange={(v) => patch({ refresh_hours: v })} />
          </label>
          <label>
            <span>{t('sh.set.cache')}</span>
            <Select label={t('sh.set.cache')} value={s.cache_size} options={[0, 1000, 10000, 50000].map((n) => ({ id: n, label: n ? n.toLocaleString() : t('sh.set.off') }))} onChange={(v) => patch({ cache_size: v })} />
          </label>
          <label>
            <span>{t('sh.set.listen')}</span>
            <Select label={t('sh.set.listen')} value={s.listen} options={(['auto', 'all', 'lan'] as const).map((m) => ({ id: m, label: t(`sh.set.listen.${m}`) }))} onChange={(v) => patch({ listen: v })} />
          </label>
        </div>
        <Toggle label={t('sh.set.cnames')} hint={t('sh.set.cnamesHint')} checked={s.check_cnames} onChange={(v) => patch({ check_cnames: v })} />
      </Card>

      <Card title={t('sh.set.privacy')} icon="eye" hint={t('sh.set.privacyHint')}>
        <Toggle label={t('sh.set.log')} hint={t('sh.set.logHint')} checked={s.log_enabled} onChange={(v) => patch({ log_enabled: v })} />
        <Toggle label={t('sh.set.logClients')} hint={t('sh.set.logClientsHint')} checked={s.log_clients} disabled={!s.log_enabled} onChange={(v) => patch({ log_clients: v })} />
        <label className="sh-inline">
          <span>{t('sh.set.keep')}</span>
          <Select label={t('sh.set.keep')} value={s.log_hours} disabled={!s.log_enabled} options={[1, 24, 168, 720].map((h) => ({ id: h, label: t(`sh.set.keep.${h}`) }))} onChange={(v) => patch({ log_hours: v })} />
        </label>
      </Card>

      {fw.supported && fw.installed && (
        <Card title={t('sh.set.firewall')} icon="lock">
          <label className="sh-inline">
            <span>{t('sh.set.fwLogging')}</span>
            <Select label={t('sh.set.fwLogging')} value={fw.logging} options={(['off', 'low', 'medium', 'high', 'full'] as const).map((l) => ({ id: l, label: t(`sh.set.lvl.${l}`) }))} onChange={(v) => void fwCall(shieldApi.logging(v), setFw)} />
          </label>
          <Toggle label={t('sh.set.ipv6')} hint={t('sh.set.ipv6Hint')} checked={fw.ipv6} disabled={!!fw.pending} onChange={(v) => void fwCall(shieldApi.ipv6(v), setFw)} />
        </Card>
      )}
    </>
  );
}
