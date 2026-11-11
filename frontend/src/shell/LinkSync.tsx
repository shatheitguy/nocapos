import { useEffect, useState } from 'react';
import { tr, useT } from '../i18n';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { relTime } from '../state/notifications';
import { toast } from '../state/toasts';
import { CLIP_BANNER_MS, copyText, linkApi, readClipboard, useLink, type Clip, type PhoneNote } from '../state/link';

// NoCap Link in the Notification Center: the phone's notifications, the
// shared clipboard, and the "Paste from <phone>" banner.

type Look = 'mac' | 'windows';

const PHONE_TILE: [string, string] = ['#38bdf8', '#0ea5e9'];
const CLIP_TILE: [string, string] = ['#a78bfa', '#7c3aed'];

function NoteIcon({ n, size }: { n: PhoneNote; size: number }) {
  if (n.icon) return <AppIcon app={{ id: 'phone-' + n.package, icon: 'phone', tile: PHONE_TILE, logo: 'data:image/png;base64,' + n.icon }} size={size} />;
  return <AppIcon app={{ icon: 'phone', tile: PHONE_TILE }} size={size} />;
}

async function copy(text: string) {
  if (await copyText(text)) toast('success', tr('link.copied'));
  else toast('error', tr('link.copyFailed'));
}

function PhoneCard({ n, look }: { n: PhoneNote; look: Look }) {
  const t = useT();
  const dismiss = useLink((s) => s.dismiss);
  const now = useClock(30_000);
  const title = n.title ? t('link.noteTitle', { app: n.app || n.package, title: n.title }) : n.app || n.package;
  return (
    <div className={`nc-card ${look} lvl-info link-card`}>
      <button
        type="button"
        className="nb-x"
        aria-label={t('nc.clear')}
        title={t('nc.clear')}
        onClick={(e) => {
          e.stopPropagation();
          dismiss(n);
        }}
      >
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      <NoteIcon n={n} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{title}</b>
          <span className="nb-time">{relTime(new Date(n.posted).toISOString(), now.getTime())}</span>
        </div>
        {n.text && <div className="nb-body">{n.text}</div>}
        <div className="link-from">{t('link.from', { device: n.device_name })}</div>
      </div>
    </div>
  );
}

/** The phone's notifications, newest first. */
export function PhoneSection({ look }: { look: Look }) {
  const t = useT();
  const notes = useLink((s) => s.notes);
  if (!notes.length) return null;
  return (
    <section className="nc-group link-group">
      <div className={look === 'mac' ? 'nc-ghead' : 'nc-app'}>
        {look === 'windows' && <AppIcon app={{ icon: 'phone', tile: PHONE_TILE }} size={16} />}
        <b>{t('link.phoneTitle')}</b>
      </div>
      {notes.map((n) => (
        <PhoneCard key={n.device_id + '\u0000' + n.key} n={n} look={look} />
      ))}
    </section>
  );
}

function ClipRow({ c, look }: { c: Clip; look: Look }) {
  const t = useT();
  const now = useClock(30_000);
  const from = c.source === 'phone' ? t('link.from', { device: c.from }) : t('link.sentFromHere');
  return (
    <div className={`nc-card ${look} lvl-info link-card link-clip`}>
      <div className="nb-text">
        <div className="nb-top">
          <span className="link-from">{from}</span>
          <span className="nb-time">{relTime(c.at, now.getTime())}</span>
        </div>
        <div className="nb-body link-clip-text">{c.text}</div>
      </div>
      <button type="button" className="nb-tool link-copy" title={t('link.copy')} aria-label={t('link.copy')} onClick={() => void copy(c.text)}>
        <Icon name="copy" size={14} />
      </button>
    </div>
  );
}

/** Recent clips and "Send clipboard to phone". */
export function ClipboardSection({ look }: { look: Look }) {
  const t = useT();
  const clips = useLink((s) => s.clips);
  const phones = useLink((s) => s.phones);
  const [typing, setTyping] = useState(false);
  const [text, setText] = useState('');
  const [target, setTarget] = useState('');
  const [busy, setBusy] = useState(false);
  if (!phones.length && !clips.length) return null;

  const send = async (value: string) => {
    setBusy(true);
    const r = await linkApi.send(value, target || undefined);
    setBusy(false);
    if (!r.ok) {
      toast('error', tr('link.sendFailed'), r.error);
      return;
    }
    setTyping(false);
    setText('');
    toast('success', tr('link.sent'));
    void useLink.getState().load();
  };
  const sendClipboard = async () => {
    const v = await readClipboard();
    if (v == null) setTyping(true); // the browser won't share it: type or paste it instead
    else void send(v);
  };

  return (
    <section className="nc-group link-group">
      <div className={look === 'mac' ? 'nc-ghead' : 'nc-app'}>
        {look === 'windows' && <AppIcon app={{ icon: 'clipboard', tile: CLIP_TILE }} size={16} />}
        <b>{t('link.clipTitle')}</b>
        {look === 'windows' && <span className="spacer" />}
        {phones.length > 0 && (
          <button type="button" className={look === 'mac' ? 'nc-pill' : 'nc-clear'} disabled={busy} onClick={() => void sendClipboard()}>
            {t('link.sendClip')}
          </button>
        )}
      </div>
      {typing && (
        <div className={`nc-card ${look} link-card link-compose`}>
          <textarea
            autoFocus
            rows={3}
            maxLength={65536}
            placeholder={t('link.pastePlaceholder')}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <div className="link-compose-row">
            {phones.length > 1 && (
              <select value={target} onChange={(e) => setTarget(e.target.value)} aria-label={t('link.sendTo')}>
                <option value="">{t('link.allPhones')}</option>
                {phones.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}
            <span className="spacer" />
            <button type="button" className="nb-btn" onClick={() => setTyping(false)}>
              {t('link.cancel')}
            </button>
            <button type="button" className="nb-btn primary" disabled={busy || !text.trim()} onClick={() => void send(text)}>
              {t('link.send')}
            </button>
          </div>
        </div>
      )}
      {clips.slice(0, 3).map((c) => (
        <ClipRow key={c.id} c={c} look={look} />
      ))}
    </section>
  );
}

/** "Paste from <phone>": a clip that just arrived, with a Copy button. */
export function ClipBanner({ look }: { look: Look }) {
  const t = useT();
  const clip = useLink((s) => s.clipBanner);
  const [hold, setHold] = useState(false);
  const close = () => useLink.setState({ clipBanner: null });
  useEffect(() => {
    if (!clip || hold) return;
    const tm = window.setTimeout(close, CLIP_BANNER_MS);
    return () => window.clearTimeout(tm);
  }, [clip, hold]);
  if (!clip) return null;
  return (
    <div className={`nb ${look} lvl-info link-banner`} role="status" onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)}>
      <button type="button" className={look === 'mac' ? 'nb-x' : 'nb-tool link-banner-x'} aria-label={t('nc.clear')} title={t('nc.clear')} onClick={close}>
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      <AppIcon app={{ icon: 'clipboard', tile: CLIP_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <b className="nb-title">{t('link.pasteFrom', { device: clip.from })}</b>
        <div className="nb-body">{clip.text}</div>
      </div>
      <button
        type="button"
        className="nb-btn link-banner-copy"
        onClick={() => {
          void copy(clip.text);
          close();
        }}
      >
        {t('link.copy')}
      </button>
    </div>
  );
}
