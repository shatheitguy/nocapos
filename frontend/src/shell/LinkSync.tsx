import { useEffect, useState } from 'react';
import { tr, useT } from '../i18n';
import { AppIcon } from '../components/AppTile';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { relTime } from '../state/notifications';
import { toast } from '../state/toasts';
import {
  CLIP_BANNER_MS,
  CARRY_BANNER_MS,
  copyText,
  errorCode,
  linkApi,
  readClipboard,
  useLink,
  type Clip,
  type PhoneNote,
} from '../state/link';
import { copyPicture, MAX_CLIP_TEXT } from '../state/clipSync';
import { carryLabel, openCarry } from './linkActions';
import '../styles/link.css';

// NoCap Link in the Notification Center: the phones' notifications (grouped
// by phone, then by app; calls as call cards; quick replies), the shared
// clipboard, and the "Paste from <phone>" / "Open from <phone>" banners.

type Look = 'mac' | 'windows';

export const PHONE_TILE: [string, string] = ['#38bdf8', '#0ea5e9'];
const CLIP_TILE: [string, string] = ['#a78bfa', '#7c3aed'];
const CALL_TILE: [string, string] = ['#4ade80', '#16a34a'];
const MISSED_TILE: [string, string] = ['#f87171', '#dc2626'];
const CARRY_TILE: [string, string] = ['#fbbf24', '#f59e0b'];

function NoteIcon({ n, size }: { n: PhoneNote; size: number }) {
  if (n.icon) return <AppIcon app={{ id: 'phone-' + n.package, icon: 'phone', tile: PHONE_TILE, logo: 'data:image/png;base64,' + n.icon }} size={size} />;
  return <AppIcon app={{ icon: 'phone', tile: PHONE_TILE }} size={size} />;
}

async function copy(text: string) {
  if (await copyText(text)) toast('success', tr('link.copied'));
  else toast('error', tr('link.copyFailed'));
}

const isPicture = (c: Clip) => c.kind === 'image';

/** Copies a clip here: its text, or its picture (browsers take PNG only). */
async function copyClip(c: Clip) {
  if (!isPicture(c)) return copy(c.text);
  if (await copyPicture(c)) toast('success', tr('link.copied'));
  else toast('error', tr('link.pictureCopyFailed'));
}

/** What a clip shows: its text, or "Picture (120 KB)". */
function clipLabel(t: ReturnType<typeof useT>, c: Clip) {
  if (!isPicture(c)) return c.text;
  return t('link.clipPicture', { size: Math.max(1, Math.round((c.size ?? 0) / 1024)) });
}

const noteId = (n: PhoneNote) => n.device_id + '\u0000' + n.key;
const appName = (n: PhoneNote) => n.app || n.package;

function ClearX({ look, label, onClick }: { look: Look; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="nb-x"
      aria-label={label}
      title={label}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
    >
      <Icon name="close" size={look === 'mac' ? 9 : 12} />
    </button>
  );
}

/** Quick reply to a message notification, right from here. */
function ReplyBox({ n, onDone }: { n: PhoneNote; onDone: (sent: boolean) => void }) {
  const t = useT();
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const send = async () => {
    const v = text.trim();
    if (!v) return;
    setBusy(true);
    const r = await linkApi.action(n.device_id, { kind: 'reply', key: n.key, text: v });
    setBusy(false);
    if (r.ok) {
      onDone(true);
      return;
    }
    if (r.status === 409 && errorCode(r) === 'gone') {
      toast('info', tr('link.gone'));
      useLink.getState().drop(n);
      return;
    }
    toast('error', tr('link.replyFailed'), r.error);
  };
  return (
    <form
      className="link-reply"
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
    >
      <input
        autoFocus
        value={text}
        maxLength={2000}
        placeholder={t('link.replyPlaceholder', { name: n.sender || n.title || appName(n) })}
        aria-label={t('link.reply')}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => e.key === 'Escape' && (e.stopPropagation(), onDone(false))}
      />
      <button type="submit" className="nb-btn primary" disabled={busy || !text.trim()}>
        {busy ? t('link.sending') : t('link.send')}
      </button>
    </form>
  );
}

function PhoneCard({ n, look }: { n: PhoneNote; look: Look }) {
  const t = useT();
  const dismiss = useLink((s) => s.dismiss);
  const now = useClock(30_000);
  const [replying, setReplying] = useState(false);
  const [sent, setSent] = useState(false);
  useEffect(() => {
    if (!sent) return;
    const tm = window.setTimeout(() => setSent(false), 4000);
    return () => window.clearTimeout(tm);
  }, [sent]);
  const title = n.title ? t('link.noteTitle', { app: appName(n), title: n.title }) : appName(n);
  return (
    <div className={`nc-card ${look} lvl-info link-card`}>
      <ClearX look={look} label={t('nc.clear')} onClick={() => dismiss(n)} />
      <NoteIcon n={n} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{title}</b>
          <span className="nb-time">{relTime(new Date(n.posted).toISOString(), now.getTime())}</span>
        </div>
        {n.text && <div className="nb-body">{n.text}</div>}
        {n.can_reply && !replying && (
          <div className="link-actions">
            {sent ? (
              <span className="link-sent">
                <Icon name="check" size={12} /> {t('link.replySent')}
              </span>
            ) : (
              <button type="button" className="nb-btn" onClick={() => setReplying(true)}>
                <Icon name="send" size={12} /> {t('link.reply')}
              </button>
            )}
          </div>
        )}
        {n.can_reply && replying && (
          <ReplyBox
            n={n}
            onDone={(ok) => {
              setReplying(false);
              setSent(ok);
            }}
          />
        )}
      </div>
    </div>
  );
}

/** A call: incoming / ongoing / missed, who's calling, and Decline. */
function CallCard({ n, look }: { n: PhoneNote; look: Look }) {
  const t = useT();
  const dismiss = useLink((s) => s.dismiss);
  const now = useClock(30_000);
  const [busy, setBusy] = useState(false);
  const state = n.call_state ?? 'incoming';
  const who = n.title || n.sender || n.number || t('link.call.unknown');
  const sub = n.number && n.number !== who ? n.number : n.text && n.text !== who ? n.text : '';
  const decline = async () => {
    setBusy(true);
    const r = await linkApi.action(n.device_id, { kind: 'decline', key: n.key });
    setBusy(false);
    if (r.ok) {
      useLink.getState().drop(n);
      toast('success', tr('link.call.declined'));
    } else if (r.status === 409 && errorCode(r) === 'gone') {
      toast('info', tr('link.call.gone'));
      useLink.getState().drop(n);
    } else toast('error', tr('link.call.declineFailed'), r.error);
  };
  return (
    <div className={`nc-card ${look} lvl-info link-card link-call ${state}`}>
      {state === 'missed' && <ClearX look={look} label={t('nc.clear')} onClick={() => dismiss(n)} />}
      <AppIcon app={{ icon: 'phone', tile: state === 'missed' ? MISSED_TILE : CALL_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <div className="nb-top">
          <span className={`link-call-state ${state}`}>{t(`link.call.${state}`)}</span>
          <span className="nb-time">{relTime(new Date(n.posted).toISOString(), now.getTime())}</span>
        </div>
        <b className="nb-title link-caller">{who}</b>
        {sub && <div className="nb-body">{sub}</div>}
      </div>
      {n.can_decline && state !== 'missed' && (
        <button type="button" className="nb-btn link-decline" disabled={busy} onClick={() => void decline()}>
          {t('link.call.decline')}
        </button>
      )}
    </div>
  );
}

/** One app's notifications from one phone: the newest, or all of them when opened. */
function AppGroup({ list, look }: { list: PhoneNote[]; look: Look }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const dismiss = useLink((s) => s.dismiss);
  if (list.length === 1) return <PhoneCard n={list[0]} look={look} />;
  const name = appName(list[0]);
  return (
    <div className="link-app">
      <div className="link-app-head">
        <button type="button" className="link-app-toggle" aria-expanded={open} onClick={() => setOpen((v) => !v)}>
          <NoteIcon n={list[0]} size={16} />
          <span className="link-app-name">{name}</span>
          <span className="link-count">{list.length}</span>
          <Icon name={open ? 'chevronUp' : 'chevronDown'} size={12} />
        </button>
        <button
          type="button"
          className="nb-tool"
          aria-label={t('nc.clearFrom', { name })}
          title={t('nc.clearFrom', { name })}
          onClick={() => list.forEach(dismiss)}
        >
          <Icon name="close" size={11} />
        </button>
      </div>
      {(open ? list : list.slice(0, 1)).map((n) => (
        <PhoneCard key={noteId(n)} n={n} look={look} />
      ))}
      {!open && (
        <button type="button" className="link-more" onClick={() => setOpen(true)}>
          {t('link.showMore', { n: list.length - 1 })}
        </button>
      )}
    </div>
  );
}

interface DeviceGroup {
  id: string;
  name: string;
  calls: PhoneNote[];
  apps: PhoneNote[][];
}

/** Newest first; per phone, calls on top and then one group per app. */
function group(notes: PhoneNote[]): DeviceGroup[] {
  const sorted = [...notes].sort((a, b) => b.posted - a.posted);
  const devices = new Map<string, DeviceGroup & { byApp: Map<string, PhoneNote[]> }>();
  for (const n of sorted) {
    let d = devices.get(n.device_id);
    if (!d) {
      d = { id: n.device_id, name: n.device_name, calls: [], apps: [], byApp: new Map() };
      devices.set(n.device_id, d);
    }
    if (n.kind === 'call') {
      d.calls.push(n);
      continue;
    }
    const k = n.package || n.app;
    const g = d.byApp.get(k);
    if (g) g.push(n);
    else {
      const fresh = [n];
      d.byApp.set(k, fresh);
      d.apps.push(fresh);
    }
  }
  return [...devices.values()].map(({ id, name, calls, apps }) => ({ id, name, calls, apps }));
}

/** The phones' notifications, by phone and then by app. */
export function PhoneSection({ look }: { look: Look }) {
  const t = useT();
  const notes = useLink((s) => s.notes);
  if (!notes.length) return null;
  return (
    <>
      {group(notes).map((d) => (
        <section key={d.id} className="nc-group link-group">
          <div className={look === 'mac' ? 'nc-ghead' : 'nc-app'}>
            {look === 'windows' && <AppIcon app={{ icon: 'phone', tile: PHONE_TILE }} size={16} />}
            <b>{d.name || t('link.phoneTitle')}</b>
          </div>
          {d.calls.map((n) => (
            <CallCard key={noteId(n)} n={n} look={look} />
          ))}
          {d.apps.map((list) => (
            <AppGroup key={noteId(list[0])} list={list} look={look} />
          ))}
        </section>
      ))}
    </>
  );
}

function ClipRow({ c, look }: { c: Clip; look: Look }) {
  const t = useT();
  const now = useClock(30_000);
  const from = c.source === 'web' ? t('link.sentFromHere') : t('link.from', { device: c.from });
  return (
    <div className={`nc-card ${look} lvl-info link-card link-clip`}>
      <div className="nb-text">
        <div className="nb-top">
          <span className="link-from">{from}</span>
          <span className="nb-time">{relTime(c.at, now.getTime())}</span>
        </div>
        <div className="nb-body link-clip-text">{clipLabel(t, c)}</div>
      </div>
      <button type="button" className="nb-tool link-copy" title={t('link.copy')} aria-label={t('link.copy')} onClick={() => void copyClip(c)}>
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
            maxLength={MAX_CLIP_TEXT}
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

/** Auto-close a banner after ms unless the pointer is on it. */
function useBannerTimer(active: boolean, ms: number, close: () => void) {
  const [hold, setHold] = useState(false);
  useEffect(() => {
    if (!active || hold) return;
    const tm = window.setTimeout(close, ms);
    return () => window.clearTimeout(tm);
  }, [active, hold, ms, close]);
  return { onPointerEnter: () => setHold(true), onPointerLeave: () => setHold(false) };
}

const closeClip = () => useLink.setState({ clipBanner: null });
const closeCarry = () => useLink.setState({ carryBanner: null });

/** "Paste from <phone>": a clip that just arrived, with a Copy button. */
export function ClipBanner({ look }: { look: Look }) {
  const t = useT();
  const clip = useLink((s) => s.clipBanner);
  const hover = useBannerTimer(!!clip, CLIP_BANNER_MS, closeClip);
  if (!clip) return null;
  return (
    <div className={`nb ${look} lvl-info link-banner`} role="status" {...hover}>
      <button type="button" className={look === 'mac' ? 'nb-x' : 'nb-tool link-banner-x'} aria-label={t('nc.clear')} title={t('nc.clear')} onClick={closeClip}>
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      <AppIcon app={{ icon: 'clipboard', tile: CLIP_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <b className="nb-title">{t('link.pasteFrom', { device: clip.from })}</b>
        <div className="nb-body">{clipLabel(t, clip)}</div>
      </div>
      <button
        type="button"
        className="nb-btn link-banner-copy"
        onClick={() => {
          void copyClip(clip);
          closeClip();
        }}
      >
        {t('link.copy')}
      </button>
    </div>
  );
}

/** "Open <page> from <phone>": a phone did "Open on NoCap". Opening waits for a click (popup blockers). */
export function CarryBanner({ look }: { look: Look }) {
  const t = useT();
  const h = useLink((s) => s.carryBanner);
  const hover = useBannerTimer(!!h, CARRY_BANNER_MS, closeCarry);
  if (!h) return null;
  const isFile = !!(h.root && h.path);
  return (
    <div className={`nb ${look} lvl-info link-banner`} role="status" {...hover}>
      <button type="button" className={look === 'mac' ? 'nb-x' : 'nb-tool link-banner-x'} aria-label={t('nc.clear')} title={t('nc.clear')} onClick={closeCarry}>
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      <AppIcon app={{ icon: isFile ? 'file' : 'globe', tile: CARRY_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <b className="nb-title">{t('link.carryFrom', { device: h.device_name })}</b>
        <div className="nb-body">{carryLabel(h)}</div>
      </div>
      <button
        type="button"
        className="nb-btn link-banner-copy"
        onClick={() => {
          openCarry(h);
          closeCarry();
        }}
      >
        {t('link.open')}
      </button>
    </div>
  );
}
