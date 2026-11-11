import { useCallback, useEffect, useState } from 'react';
import { useT, type T } from '../i18n';
import { sessionsApi, type SignedInSession } from '../api/sessions';
import { Icon } from '../components/Icon';
import { useClock } from '../lib/hooks';
import { logOutClean } from '../lib/signOut';
import { fmtTime, useTimePrefs } from '../lib/time';
import { confirmDialog } from '../state/confirm';
import { agoLabel, endSession, kindLabel, sessionLabel, useSignins, whereLabel } from '../state/signins';
import { toast } from '../state/toasts';
import { KIND_ICON } from '../shell/Signins';
import { Section } from './Personalize';

// Where an account is signed in: Settings → Your Account (your own), and
// Settings → Users for administrators (anyone's). Each session can be
// signed out on its own, or all the others at once.

function SessionRow({ s, onEnd, busy }: { s: SignedInSession; onEnd: () => void; busy: boolean }) {
  const t = useT();
  const tp = useTimePrefs();
  const now = useClock(30_000);
  const since = new Date(s.created_at);
  const bits = [
    kindLabel(s.kind, t),
    s.device_name && (s.browser || s.os) ? [s.browser, s.os].filter(Boolean).join(' · ') : null,
    whereLabel(s, t),
    t('ses.signedInAt', { date: since.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }), time: fmtTime(since, tp, false) }),
    s.current ? null : t('ses.lastActive', { ago: agoLabel(s.last_seen_at, t, now.getTime()) }),
  ].filter(Boolean);
  return (
    <div className={`settings-row dev-row session-row ${s.current ? 'current' : ''}`}>
      <span className="urow-icon dev-row-icon">
        <Icon name={KIND_ICON[s.kind]} size={18} />
      </span>
      <span className="settings-row-text">
        <span className="settings-row-label">
          {sessionLabel(s, t)}
          {s.current && <span className="badge you session-here">{t('ses.thisDevice')}</span>}
        </span>
        <span className="settings-row-hint">{bits.join(' · ')}</span>
      </span>
      <div className="settings-row-control">
        <button type="button" className="ghost small danger" disabled={busy} onClick={onEnd}>
          {s.source === 'device' ? t('ses.unlink') : t('ses.signOut')}
        </button>
      </div>
    </div>
  );
}

function SessionRows({ list, t, onEnded }: { list: SignedInSession[] | null; t: T; onEnded: (s: SignedInSession) => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  if (!list) {
    return (
      <div className="settings-row full">
        <p className="muted small">{t('ses.loading')}</p>
      </div>
    );
  }
  if (!list.length) {
    return (
      <div className="settings-row full">
        <p className="muted small">{t('ses.none')}</p>
      </div>
    );
  }
  return (
    <>
      {list.map((s) => (
        <SessionRow
          key={s.id}
          s={s}
          busy={busy === s.id}
          onEnd={async () => {
            setBusy(s.id);
            const ok = await endSession(s, t);
            setBusy(null);
            if (ok) onEnded(s);
          }}
        />
      ))}
    </>
  );
}

/** Settings → Your Account: every place you're signed in right now. */
export function YourSessions() {
  const t = useT();
  const sessions = useSignins((s) => s.sessions);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void useSignins.getState().load();
  }, []);
  const others = (sessions ?? []).filter((s) => !s.current).length;

  const endOthers = async () => {
    const ok = await confirmDialog({
      title: t('ses.endOthersTitle'),
      message: t('ses.endOthersBody'),
      confirmLabel: t('ses.endOthers'),
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    const r = await sessionsApi.endOthers();
    setBusy(false);
    if (!r.ok) return toast('error', t('ses.endFailed'), r.error);
    toast('success', t('ses.endedOthers', { n: r.data.ended }));
    void useSignins.getState().load();
  };

  return (
    <Section title={t('ses.yourTitle')} hint={t('ses.yourHint')}>
      <SessionRows
        list={sessions}
        t={t}
        onEnded={(s) => {
          if (s.current) void logOutClean();
          else void useSignins.getState().load();
        }}
      />
      {others > 0 && (
        <div className="settings-row session-foot">
          <span className="settings-row-text">
            <span className="settings-row-label">{t('ses.endOthers')}</span>
            <span className="settings-row-hint">{t('ses.endOthersHint')}</span>
          </span>
          <div className="settings-row-control">
            <button type="button" className="ghost small danger" disabled={busy} onClick={() => void endOthers()}>
              <Icon name="logout" size={13} /> {t('ses.endOthersBtn')}
            </button>
          </div>
        </div>
      )}
    </Section>
  );
}

/** Settings → Users (administrators): where one person is signed in. */
export function PersonSessionsPanel({ username, you }: { username: string; you: boolean }) {
  const t = useT();
  const [list, setList] = useState<SignedInSession[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    const r = await sessionsApi.all(username);
    if (r.ok) {
      setList(r.data.sessions);
      setError(null);
    } else setError(r.error ?? t('ses.loadFailed'));
  }, [username, t]);
  useEffect(() => {
    void load();
  }, [load]);

  const endAll = async () => {
    const ok = await confirmDialog({
      title: t('ses.endAllTitle', { name: username }),
      message: you ? t('ses.endOthersBody') : t('ses.endAllBody', { name: username }),
      confirmLabel: t('ses.endAllBtn'),
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    const r = await sessionsApi.endAllFor(username);
    setBusy(false);
    if (!r.ok) return toast('error', t('ses.endFailed'), r.error);
    toast('success', t('ses.endedFor', { n: r.data.ended, name: username }));
    void load();
  };

  return (
    <div className="person-sessions">
      <div className="person-sessions-head">
        <b>{t('ses.personTitle', { n: list?.length ?? 0 })}</b>
        {list && list.some((s) => !you || !s.current) && (
          <button type="button" className="ghost small danger" disabled={busy} onClick={() => void endAll()}>
            <Icon name="logout" size={13} /> {you ? t('ses.endOthersBtn') : t('ses.endAllBtn')}
          </button>
        )}
      </div>
      {error ? (
        <p className="error small">{error}</p>
      ) : (
        <SessionRows
          list={list}
          t={t}
          onEnded={(s) => {
            if (s.current) void logOutClean();
            else void load();
          }}
        />
      )}
    </div>
  );
}
