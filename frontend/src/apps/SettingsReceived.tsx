import { useEffect, useState } from 'react';
import { receivedApi } from '../api/received';
import { Icon } from '../components/Icon';
import { tr, useT } from '../i18n';
import { showInFiles, useReceived } from '../state/received';
import { toast } from '../state/toasts';
import { Row, Section as Group } from './Personalize';

// Settings → Linked Devices: where files your devices send land (your
// Received folder, one subfolder per device), and renaming it.

const BAD = /[/\\:*?"<>|]/;

export function ReceivedSettings() {
  const t = useT();
  const info = useReceived((s) => s.info);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void useReceived.getState().load();
  }, []);
  useEffect(() => {
    if (info) setName(info.folder);
  }, [info?.folder]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!info) return null;
  const n = name.trim();
  const valid = !!n && n.length <= 64 && !BAD.test(n) && !n.startsWith('.');
  const dirty = n !== info.folder;

  const save = async () => {
    if (!valid || !dirty) return;
    setBusy(true);
    const r = await receivedApi.setFolder(n);
    setBusy(false);
    if (!r.ok) return toast('error', tr('recv.saveFailed'), r.status === 409 ? tr('recv.taken') : r.error);
    useReceived.setState({ info: { ...r.data, devices: r.data.devices ?? {} } });
    toast('success', tr('recv.saved', { name: r.data.folder }));
  };

  return (
    <Group title={t('recv.title')} hint={t('recv.hint')}>
      <Row label={t('recv.folderLabel')} hint={info.available ? t('recv.folderHint', { path: info.path ?? '' }) : t('recv.noHome')}>
        <form
          className="recv-form"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <input value={name} maxLength={64} aria-label={t('recv.folderLabel')} aria-invalid={!valid} placeholder={info.default} onChange={(e) => setName(e.target.value)} />
          <button type="submit" className="small" disabled={busy || !valid || !dirty}>
            {t('ld.save')}
          </button>
        </form>
      </Row>
      {!valid && n !== '' && (
        <div className="settings-row full">
          <p className="error small no-margin">{t('recv.badName')}</p>
        </div>
      )}
      {info.available && info.root && info.path && (
        <Row label={t('recv.openLabel')} hint={info.unread ? t('recv.unread', { n: info.unread }) : t('recv.allSeen')}>
          <button
            type="button"
            className="small neutral"
            onClick={() => {
              showInFiles(info.root!, info.path!);
              useReceived.getState().markSeen();
            }}
          >
            <Icon name="folder" size={14} /> {t('recv.showInFiles')}
          </button>
        </Row>
      )}
    </Group>
  );
}
