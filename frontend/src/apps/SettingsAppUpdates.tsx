import { useEffect, useState } from 'react';
import { storeApi, type AppUpdateSettings } from '../api/appstore';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { toast } from '../state/toasts';
import { useStoreApps } from '../state/storeApps';
import { Section, Toggle } from './Personalize';

// Settings → Containers → App updates: NoCapOS checks the App Store apps'
// registries for newer images by itself (at start and every few hours);
// whether that sends a notification, and a "check now".

export function AppUpdatesSettings() {
  const t = useT();
  const [state, setState] = useState<AppUpdateSettings | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void storeApi.updateSettings().then((r) => r.ok && setState(r.data));
  }, []);

  if (!state) return null;

  const setNotify = async (on: boolean) => {
    setBusy(true);
    const r = await storeApi.setUpdateSettings({ notify: on });
    setBusy(false);
    if (!r.ok) return toast('error', t('au.saveFail'), r.error);
    setState(r.data);
  };

  const checkNow = async () => {
    setBusy(true);
    const r = await storeApi.checkUpdates();
    setBusy(false);
    if (!r.ok) return toast('error', t('au.checkFail'), r.error);
    setState(r.data);
    await useStoreApps.getState().load();
    const n = useStoreApps.getState().apps.filter((a) => a.installed?.update_available).length;
    toast('success', n ? t('au.found', { n }) : t('au.none'));
  };

  const when = state.checked_at ? new Date(state.checked_at).toLocaleString() : '';
  return (
    <Section title={t('au.title')} hint={t('au.hint')}>
      <Toggle label={t('au.notify')} hint={t('au.notifyHint')} checked={state.notify} disabled={busy} onChange={(v) => void setNotify(v)} />
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">{t('au.check')}</span>
          <span className="settings-row-hint">{when ? t('au.checkedAt', { when }) : t('au.notYet')}</span>
        </span>
        <button type="button" className="ghost" disabled={busy} onClick={() => void checkNow()}>
          <Icon name="restart" size={14} /> {t('au.checkNow')}
        </button>
      </div>
    </Section>
  );
}
