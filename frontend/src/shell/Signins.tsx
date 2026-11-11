import { useCallback, useEffect, useState } from 'react';
import { useT } from '../i18n';
import { getUser } from '../api/client';
import { sessionsApi, type SignedInSession, type SigninAlert } from '../api/sessions';
import { openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { Icon, type IconName } from '../components/Icon';
import { useClock, usePoll } from '../lib/hooks';
import { useTimePrefs } from '../lib/time';
import { relTime } from '../state/notifications';
import { SIGNIN_BANNER_MS, agoLabel, alertText, kindLabel, sessionLabel, signOutAlert, useSignins, whereLabel } from '../state/signins';

// Sign-in awareness on the desktop: the "New sign-in" banner, the Notification
// Center's sign-ins, and the Signed-in devices widget.

type Look = 'mac' | 'windows';

const SHIELD_TILE: [string, string] = ['#ff9f0a', '#ff375f'];

export const KIND_ICON: Record<SignedInSession['kind'], IconName> = {
  web: 'globe',
  phone: 'phone',
  desktop: 'desktop',
  cli: 'terminal',
};

const openAccount = () => openApp('settings', { props: { section: 'account' } });

/** A new sign-in, while it's fresh: who, where, and "Not you? Sign it out". */
function SigninBanner({ a, look }: { a: SigninAlert; look: Look }) {
  const t = useT();
  const tp = useTimePrefs();
  const [hold, setHold] = useState(false);
  const close = () => useSignins.getState().dismissBanner(a.id);
  useEffect(() => {
    if (hold) return;
    const tm = window.setTimeout(close, SIGNIN_BANNER_MS);
    return () => window.clearTimeout(tm);
  }, [hold]); // close only uses the store
  const text = alertText(a, t, tp);
  return (
    <div className={`nb ${look} lvl-warning link-banner signin-banner`} role="alert" onPointerEnter={() => setHold(true)} onPointerLeave={() => setHold(false)}>
      <button type="button" className={look === 'mac' ? 'nb-x' : 'nb-tool link-banner-x'} aria-label={t('nc.clear')} title={t('nc.clear')} onClick={close}>
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      <AppIcon app={{ icon: 'shield', tile: SHIELD_TILE }} size={look === 'mac' ? 36 : 28} />
      <div className="nb-text">
        <b className="nb-title">{text.title}</b>
        <div className="nb-body">{text.body}</div>
      </div>
      <button
        type="button"
        className="nb-btn link-banner-copy signin-out"
        onClick={() => {
          close();
          void signOutAlert(a);
        }}
      >
        {t('ses.notYou')}
      </button>
    </div>
  );
}

/** The sign-in banners (they sit in the notification banner stack). */
export function SigninBanners({ look }: { look: Look }) {
  const banners = useSignins((s) => s.banners);
  return (
    <>
      {banners.map((a) => (
        <SigninBanner key={a.id} a={a} look={look} />
      ))}
    </>
  );
}

function SigninCard({ a, look }: { a: SigninAlert; look: Look }) {
  const t = useT();
  const tp = useTimePrefs();
  const now = useClock(30_000);
  const text = alertText(a, t, tp);
  return (
    <div className={`nc-card ${look} lvl-warning link-card signin-card`}>
      <button
        type="button"
        className="nb-x"
        aria-label={t('nc.clear')}
        title={t('nc.clear')}
        onClick={(e) => {
          e.stopPropagation();
          useSignins.getState().dismiss(a.id);
        }}
      >
        <Icon name="close" size={look === 'mac' ? 9 : 12} />
      </button>
      {look === 'mac' && <AppIcon app={{ icon: 'shield', tile: SHIELD_TILE }} size={36} />}
      <div className="nb-text">
        <div className="nb-top">
          <b className="nb-title">{text.title}</b>
          <span className="nb-time">{relTime(a.at, now.getTime())}</span>
        </div>
        <div className="nb-body">{text.body}</div>
        <div className="signin-actions">
          <button type="button" className="nb-btn signin-out" onClick={() => void signOutAlert(a)}>
            {t('ses.notYou')}
          </button>
          <button type="button" className="nb-btn" onClick={openAccount}>
            {t('ses.seeAll')}
          </button>
        </div>
      </div>
    </div>
  );
}

/** New sign-ins to your account, in the Notification Center. */
export function SigninSection({ look }: { look: Look }) {
  const t = useT();
  const alerts = useSignins((s) => s.alerts);
  if (!alerts.length) return null;
  return (
    <section className="nc-group link-group">
      <div className={look === 'mac' ? 'nc-ghead' : 'nc-app'}>
        {look === 'windows' && <AppIcon app={{ icon: 'shield', tile: SHIELD_TILE }} size={16} />}
        <b>{t('ses.centerTitle')}</b>
      </div>
      {alerts.slice(0, 5).map((a) => (
        <SigninCard key={a.id} a={a} look={look} />
      ))}
    </section>
  );
}

// ---------- widget ----------

/** How many places you're signed in right now, with the list (administrators also see everyone's total). */
export function SessionsWidget() {
  const t = useT();
  const sessions = useSignins((s) => s.sessions);
  const now = useClock(30_000);
  const isAdmin = getUser()?.role === 'admin';
  const [everyone, setEveryone] = useState<number | null>(null);
  const countAll = useCallback(async () => {
    const r = await sessionsApi.all();
    if (r.ok) setEveryone(r.data.sessions.length);
  }, []);
  useEffect(() => {
    if (isAdmin) void countAll();
  }, [isAdmin, countAll, sessions?.length]);
  usePoll(countAll, 60_000, isAdmin);
  return (
    <button type="button" className="widget glass widget-col sessions-widget" onClick={openAccount}>
      <div className="widget-head">
        <span>
          <Icon name="shield" size={15} /> <span className="widget-title">{t('ses.widgetTitle')}</span>
        </span>
      </div>
      {!sessions ? (
        <p className="muted small">{t('ses.loading')}</p>
      ) : (
        <>
          <div className="sessions-widget-count">
            <b>{sessions.length}</b>
            <span className="muted small">{t(sessions.length === 1 ? 'ses.countOne' : 'ses.countMany', { n: sessions.length })}</span>
            {isAdmin && everyone != null && <span className="muted small sessions-everyone">{t('ses.everyone', { n: everyone })}</span>}
          </div>
          <div className="widget-list">
            {sessions.map((s) => (
              <div key={s.id} className="widget-row sessions-widget-row">
                <div className="row-head small">
                  <span className="widget-title">
                    <Icon name={KIND_ICON[s.kind]} size={13} /> {sessionLabel(s, t)}
                  </span>
                  <span className={`muted widget-aside ${s.current ? 'sessions-here' : ''}`}>
                    {s.current ? t('ses.thisDevice') : agoLabel(s.last_seen_at, t, now.getTime())}
                  </span>
                </div>
                <div className="muted small sessions-widget-sub">
                  {kindLabel(s.kind, t)} · {whereLabel(s, t)}
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </button>
  );
}
