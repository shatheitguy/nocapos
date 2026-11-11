import { create } from 'zustand';
import { subscribe } from '../api/socket';
import { sessionsApi, type SignedInSession, type SigninAlert } from '../api/sessions';
import { tr, type T } from '../i18n';
import { desktopNotify } from '../lib/hub';
import { playCue } from '../lib/uiSound';
import { fmtTime, type TimePrefs } from '../lib/time';
import { confirmDialog } from './confirm';
import { usePrefs } from './prefs';
import { toast } from './toasts';

// Sign-in awareness on the web desktop: where your account is signed in
// right now, and a heads-up (banner, Notification Center, desktop
// notification) whenever it signs in somewhere else. New sign-ins arrive on
// the `account.sessions` topic; the list is reloaded when it changes.

export const SIGNIN_BANNER_MS = 15_000;
const RELOAD_MS = 60_000;

interface SigninStore {
  /** Where you're signed in (null until loaded). */
  sessions: SignedInSession[] | null;
  /** Sign-in alerts from the last day, newest first (dismissed ones left out). */
  alerts: SigninAlert[];
  /** Alerts that arrived while this desktop was open and haven't been looked at. */
  unseen: number;
  banners: SigninAlert[];
  load: () => Promise<void>;
  dismiss: (id: number) => void;
  dismissBanner: (id: number) => void;
  markSeen: () => void;
  _reset: () => void;
}

let dismissKey = '';

function readDismissed(): Set<number> {
  try {
    const raw = dismissKey ? localStorage.getItem(dismissKey) : null;
    return new Set(raw ? (JSON.parse(raw) as number[]) : []);
  } catch {
    return new Set();
  }
}

function saveDismissed(ids: Set<number>) {
  try {
    if (dismissKey) localStorage.setItem(dismissKey, JSON.stringify([...ids].slice(-100)));
  } catch {
    /* private mode: they come back on the next load, that's all */
  }
}

/** Alerts worth showing here: not dismissed, and not about this very session. */
function visible(alerts: SigninAlert[], sessions: SignedInSession[] | null): SigninAlert[] {
  const gone = readDismissed();
  const me = sessions?.find((s) => s.current)?.id;
  return alerts.filter((a) => !gone.has(a.id) && a.session_id !== me);
}

export const useSignins = create<SigninStore>((set, get) => ({
  sessions: null,
  alerts: [],
  unseen: 0,
  banners: [],
  load: async () => {
    const r = await sessionsApi.mine();
    if (!r.ok) return;
    set({ sessions: r.data.sessions, alerts: visible(r.data.alerts, r.data.sessions) });
  },
  dismiss: (id) => {
    const gone = readDismissed();
    gone.add(id);
    saveDismissed(gone);
    set({ alerts: get().alerts.filter((a) => a.id !== id), banners: get().banners.filter((a) => a.id !== id) });
  },
  dismissBanner: (id) => set({ banners: get().banners.filter((a) => a.id !== id) }),
  markSeen: () => set({ unseen: 0, banners: [] }),
  _reset: () => set({ sessions: null, alerts: [], unseen: 0, banners: [] }),
}));

function arrived(a: SigninAlert) {
  const s = useSignins.getState();
  if (s.alerts.some((x) => x.id === a.id) || s.sessions?.some((x) => x.current && x.id === a.session_id)) return;
  useSignins.setState({ alerts: [a, ...s.alerts].slice(0, 50), unseen: s.unseen + 1, banners: [...s.banners, a].slice(-2) });
  // Security first: this one shows even in Focus.
  const text = alertText(a, tr, usePrefs.getState());
  desktopNotify(text.title, text.body, 'signin:' + a.id, () => void signOutAlert(a));
  playCue('info');
}

let reloadTimer: number | undefined;

/** Load the list and follow new sign-ins for the signed-in person; returns a stop function. */
export function watchSignins(userId: string): () => void {
  dismissKey = `nocap.signins.dismissed.${userId}`;
  void useSignins.getState().load();
  const off = subscribe('account.sessions', (d) => {
    const e = d as { type: 'signin' | 'changed'; alert?: SigninAlert };
    if (e.type === 'signin' && e.alert) arrived(e.alert);
    window.clearTimeout(reloadTimer);
    reloadTimer = window.setTimeout(() => void useSignins.getState().load(), 300);
  });
  // "Last active" stays fresh, and a missed event catches up.
  const timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') void useSignins.getState().load();
  }, RELOAD_MS);
  return () => {
    off();
    window.clearInterval(timer);
    window.clearTimeout(reloadTimer);
    useSignins.getState()._reset();
    dismissKey = '';
  };
}

// ---------- words ----------

/** What signed in: the phone's name, "Chrome 131 on Windows", "NoCap Desktop on this machine". */
export function sessionLabel(s: Pick<SignedInSession, 'kind' | 'device_name' | 'browser' | 'os'>, t: T): string {
  if (s.device_name) return s.device_name;
  if (s.kind === 'desktop' && !s.browser) return t('ses.thisScreen');
  if (s.browser && s.os) return t('ses.browserOn', { browser: s.browser, os: s.os });
  return s.browser || s.os || (s.kind === 'desktop' ? t('ses.thisScreen') : t('ses.unknownDevice'));
}

export const kindLabel = (kind: SignedInSession['kind'], t: T) => t(`ses.kind.${kind}`);

/** "192.168.1.20 · local network" (just the address on the internet: no lookups). */
export function whereLabel(s: Pick<SignedInSession, 'ip' | 'network'>, t: T): string {
  if (!s.ip) return t('ses.noAddress');
  if (s.network === 'local') return t('ses.ipLocal', { ip: s.ip });
  if (s.network === 'lan') return t('ses.ipLan', { ip: s.ip });
  return s.ip;
}

/** "just now", "5 min ago", "3 h ago", "2 d ago". */
export function agoLabel(iso: string, t: T, now = Date.now()): string {
  const s = Math.max(0, (now - new Date(iso).getTime()) / 1000);
  if (s < 90) return t('ses.ago.now');
  if (s < 3600) return t('ses.ago.min', { n: Math.round(s / 60) });
  if (s < 86400) return t('ses.ago.hour', { n: Math.floor(s / 3600) });
  return t('ses.ago.day', { n: Math.floor(s / 86400) });
}

export function alertText(a: SigninAlert, t: T, tp: TimePrefs): { title: string; body: string } {
  return {
    title: t('ses.alertTitle'),
    body: t('ses.alertBody', { device: sessionLabel(a, t), where: whereLabel(a, t), time: fmtTime(new Date(a.at), tp, false) }),
  };
}

/** "Not you? Sign it out": ends the session (or unlinks the phone) the alert is about. */
export async function signOutAlert(a: SigninAlert): Promise<void> {
  const label = sessionLabel(a, tr);
  const ok = await confirmDialog({
    title: tr('ses.endConfirmTitle', { device: label }),
    message: a.source === 'device' ? tr('ses.unlinkConfirmBody') : tr('ses.endConfirmBody'),
    confirmLabel: tr('ses.signItOut'),
    danger: true,
  });
  if (!ok) return;
  const r = await sessionsApi.end(a.session_id);
  if (r.ok || r.status === 404) {
    toast('success', r.ok ? tr('ses.ended', { device: label }) : tr('ses.alreadyEnded'));
    useSignins.getState().dismiss(a.id);
    void useSignins.getState().load();
  } else toast('error', tr('ses.endFailed'), r.error);
}

/** Ends one session from a list, after asking. */
export async function endSession(s: SignedInSession, t: T): Promise<boolean> {
  const label = sessionLabel(s, t);
  const ok = await confirmDialog({
    title: s.current ? t('ses.endThisTitle') : t('ses.endConfirmTitle', { device: label }),
    message: s.current ? t('ses.endThisBody') : s.source === 'device' ? t('ses.unlinkConfirmBody') : t('ses.endConfirmBody'),
    confirmLabel: t('ses.signOut'),
    danger: true,
  });
  if (!ok) return false;
  const r = await sessionsApi.end(s.id);
  if (!r.ok && r.status !== 404) {
    toast('error', t('ses.endFailed'), r.error);
    return false;
  }
  toast('success', t('ses.ended', { device: label }));
  return true;
}
