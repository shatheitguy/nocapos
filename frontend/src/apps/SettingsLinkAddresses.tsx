import { useEffect, useState } from 'react';
import { tr, useT } from '../i18n';
import { getUser } from '../api/client';
import { linkApi, type LinkAddresses } from '../state/link';
import { toast } from '../state/toasts';
import { Section as Group } from './Personalize';
import { validWebAddress } from './WebAddress';

// Settings → Devices: the addresses phones use to reach this server — on the
// same network ("Nearby") and over the internet ("Remote"). Everyone sees
// them; an administrator can set the remote address and extra LAN addresses.

const lines = (s: string) =>
  s
    .split(/[\n,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

export function LinkAddressesSettings() {
  const t = useT();
  const isAdmin = getUser()?.role === 'admin';
  const [a, setA] = useState<LinkAddresses | null>(null);
  const [remote, setRemote] = useState('');
  const [lan, setLan] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = (d: LinkAddresses) => {
    setA(d);
    setRemote(d.saved_remote ?? '');
    setLan((d.saved_lan ?? []).join('\n'));
  };
  useEffect(() => {
    void linkApi.addresses().then((r) => r.ok && apply(r.data));
  }, []);

  if (!a) return null;
  const lanList = lines(lan);
  const badLan = lanList.filter((x) => !validWebAddress(x) && !/^[\w.-]+(:\d+)?$/.test(x) && !/^\[[0-9a-f:]+\](:\d+)?$/i.test(x));
  const remoteOk = validWebAddress(remote);
  const dirty = remote.trim() !== (a.saved_remote ?? '') || lanList.join('\n') !== (a.saved_lan ?? []).join('\n');

  const save = async () => {
    setBusy(true);
    const r = await linkApi.saveAddresses(remote.trim(), lanList);
    setBusy(false);
    if (!r.ok) return toast('error', tr('link.addr.saveFailed'), r.error);
    toast('success', tr('link.addr.saved'));
    apply(r.data);
  };

  return (
    <Group title={t('link.addr.title')} hint={t('link.addr.hint')}>
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">{t('link.route.local')}</span>
          <span className="settings-row-hint link-addr-list">{a.lan.length ? a.lan.join(' · ') : t('link.addr.none')}</span>
        </span>
      </div>
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">{t('link.route.remote')}</span>
          <span className="settings-row-hint link-addr-list">{a.remote || t('link.addr.noRemote')}</span>
        </span>
      </div>
      {isAdmin && (
        <form
          className="settings-row stacked link-addr-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (remoteOk && !badLan.length && dirty) void save();
          }}
        >
          <label>
            <span className="section-label">{t('link.addr.remoteLabel')}</span>
            <input
              value={remote}
              placeholder="https://nocap.example.com"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={!remoteOk}
              onChange={(e) => setRemote(e.target.value)}
            />
          </label>
          <label>
            <span className="section-label">{t('link.addr.lanLabel')}</span>
            <textarea
              rows={3}
              value={lan}
              placeholder={'192.168.1.20:8080\nnocap.lan'}
              spellCheck={false}
              aria-invalid={badLan.length > 0}
              onChange={(e) => setLan(e.target.value)}
            />
          </label>
          <div className="web-address-row">
            <span className={`small ${remoteOk && !badLan.length ? 'muted' : 'error'}`}>
              {!remoteOk ? t('link.addr.badRemote') : badLan.length ? t('link.addr.badLan', { addr: badLan[0] }) : t('link.addr.adminHint')}
            </span>
            <span className="spacer" />
            <button type="submit" className="small" disabled={busy || !remoteOk || badLan.length > 0 || !dirty}>
              {busy ? t('link.addr.saving') : t('link.addr.save')}
            </button>
          </div>
        </form>
      )}
    </Group>
  );
}
