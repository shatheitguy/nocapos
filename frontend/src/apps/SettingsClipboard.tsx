import { useEffect, useState } from 'react';
import { tr, useT } from '../i18n';
import { linkApi, useLink, type ClipSettings } from '../state/link';
import { clipSyncOn, setClipSyncOn } from '../state/clipSync';
import { toast } from '../state/toasts';
import { Row, Section as Group, Toggle } from './Personalize';

// Settings → Linked Devices → Clipboard: one clipboard across your own
// devices. Only you ever see it. This browser and NoCap Desktop can each be
// switched off here; each phone has its own switch (and apps it never
// shares from) in the phone app.

export function ClipboardSettings() {
  const t = useT();
  const [c, setC] = useState<ClipSettings | null>(null);
  const [here, setHere] = useState(clipSyncOn);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void linkApi.clipSettings().then((r) => r.ok && setC(r.data));
  }, []);

  const save = async (patch: Partial<ClipSettings>) => {
    if (!c) return;
    const before = c;
    setC({ ...c, ...patch });
    setBusy(true);
    const r = await linkApi.saveClipSettings(patch);
    setBusy(false);
    if (!r.ok) {
      setC(before);
      toast('error', tr('link.clipSaveFailed'), r.error);
      return;
    }
    setC(r.data);
    void useLink.getState().load();
  };

  const clear = async () => {
    const r = await linkApi.clearClips();
    if (!r.ok) return toast('error', tr('link.clipClearFailed'), r.error);
    toast('success', tr('link.clipCleared'));
    useLink.setState({ clips: [], clipBanner: null });
  };

  return (
    <Group title={t('link.clipTitle')} hint={t('link.clipHint')}>
      <Toggle
        label={t('link.clipHere')}
        hint={t('link.clipHereHint')}
        checked={here}
        onChange={(v) => {
          setClipSyncOn(v);
          setHere(v);
        }}
      />
      {c && (
        <>
          <Toggle label={t('link.clipDesktop')} hint={t('link.clipDesktopHint')} checked={c.desktop} disabled={busy} onChange={(v) => void save({ desktop: v })} />
          <Toggle label={t('link.clipHistory')} hint={t('link.clipHistoryHint')} checked={c.history} disabled={busy} onChange={(v) => void save({ history: v })} />
        </>
      )}
      <Row label={t('link.clipPhones')} hint={t('link.clipPhonesHint')}>
        <button type="button" className="ghost small" onClick={() => void clear()}>
          {t('link.clipClear')}
        </button>
      </Row>
    </Group>
  );
}
