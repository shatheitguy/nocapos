import { useEffect, useState } from 'react';
import { tr, useT } from '../i18n';
import { api } from '../api/client';
import { Icon } from '../components/Icon';
import { Logo } from '../components/Logo';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Choice, Toggle } from './Personalize';

// Glow Up on an image install of NoCapOS: the whole system updates as one
// image. It's written next to the running system while you keep working,
// then "Restart to finish" starts it once; it's kept only when it checks out,
// otherwise the machine goes back by itself (GET /api/v1/system/os).
// System packages never update one by one here: Debian's security fixes
// arrive as security updates of NoCapOS itself.

export interface OSFix {
  package: string;
  from?: string;
  to: string;
  cves?: string[];
  summary?: string;
}

export interface OSManifest {
  version: string;
  security: boolean;
  notes?: string;
  fixes?: OSFix[];
  release_url?: string;
  published?: string;
}

export interface OSUpdateState {
  phase: 'installing' | 'ready' | 'restarting' | 'failed';
  from: string;
  to: string;
  security?: boolean;
  progress: number;
  error?: string;
  seen?: boolean;
}

export interface OSStatus {
  image: boolean;
  info?: { target: string; version: string; channel: string; snapshot?: string };
  slots?: { active: string; default: string };
  settings?: { channel: 'stable' | 'beta'; auto_security: boolean; auto_time: string };
  latest?: OSManifest;
  available: boolean;
  security: boolean;
  size?: number;
  checked?: string;
  check_error?: string;
  state?: OSUpdateState;
  running: boolean;
  outcome?: 'updated' | 'rolled_back' | 'checking';
  reason?: string;
}

const POLL_MS = 1500;

function gb(bytes?: number) {
  if (!bytes) return '';
  return bytes >= 1 << 30 ? `${(bytes / (1 << 30)).toFixed(1)} GB` : `${Math.max(1, Math.round(bytes / (1 << 20)))} MB`;
}

const reasonKey: Record<string, string> = {
  nocapos_not_responding: 'gi.reasonNoc',
  desktop_not_starting: 'gi.reasonDesk',
  did_not_start: 'gi.reasonBoot',
  commit_failed: 'gi.reasonCommit',
};

export function ImageGlowUp({ initial }: { initial: OSStatus }) {
  const t = useT();
  const [st, setSt] = useState<OSStatus>(initial);
  const [checking, setChecking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);

  const reload = async (refresh = false) => {
    const r = await api<OSStatus>(`/api/v1/system/os${refresh ? '?refresh=1' : ''}`);
    if (r.ok) setSt(r.data);
    else if (refresh) toast('error', tr('sp.updCheckFail'), r.error);
    return r.ok ? r.data : null;
  };

  // Follow an install in progress.
  const installing = st.running || st.state?.phase === 'installing';
  useEffect(() => {
    if (!installing) return;
    const id = window.setInterval(() => void reload(), POLL_MS);
    return () => window.clearInterval(id);
  }, [installing]);

  const check = async () => {
    setChecking(true);
    await reload(true);
    setChecking(false);
  };

  const install = async () => {
    const m = st.latest!;
    const ok = await confirmDialog({
      title: m.security ? t('gi.askSecTitle') : t('gi.askTitle', { version: m.version }),
      message: t('gi.askBody', { size: gb(st.size) || '?' }),
      confirmLabel: t('gi.download'),
    });
    if (!ok) return;
    setBusy(true);
    const r = await api<OSStatus>('/api/v1/system/os/install', { method: 'POST', body: { version: m.version } });
    setBusy(false);
    if (r.ok) setSt(r.data);
    else {
      toast('error', t('gi.startFail'), r.error);
      void reload(true);
    }
  };

  const restart = async () => {
    const ok = await confirmDialog({ title: t('gi.restartTitle'), message: t('gi.restartBody'), confirmLabel: t('gi.restartNow') });
    if (!ok) return;
    setRestarting(true);
    const r = await api('/api/v1/system/os/restart', { method: 'POST' });
    if (!r.ok) {
      setRestarting(false);
      toast('error', t('gi.restartFail'), r.error);
    }
  };

  const dismiss = async () => {
    const r = await api<OSStatus>('/api/v1/system/os/dismiss', { method: 'POST' });
    if (r.ok) setSt(r.data);
  };

  const saveSettings = async (patch: Partial<NonNullable<OSStatus['settings']>>) => {
    const next = { ...st.settings!, ...patch };
    setSt({ ...st, settings: next });
    const r = await api<OSStatus>('/api/v1/system/os/settings', { method: 'PUT', body: next });
    if (r.ok) setSt(r.data);
    else toast('error', t('gi.settingsFail'), r.error);
  };

  const cur = st.info?.version ?? '';
  const phase = st.state?.phase;
  const m = st.latest;
  const ready = phase === 'ready';

  let headline: string;
  let detail = '';
  if (restarting) {
    headline = t('gi.restarting');
    detail = t('gi.restartingBody');
  } else if (installing) {
    headline = t('gi.installing', { version: st.state?.to ?? '' });
    detail = t('gi.installingBody');
  } else if (ready) {
    headline = t('gi.ready', { version: st.state?.to ?? '' });
    detail = t('gi.readyBody');
  } else if (st.outcome === 'checking') {
    headline = t('gi.checkingNew');
    detail = t('gi.checkingNewBody');
  } else if (checking) {
    headline = t('gu.checking');
  } else if (st.available && m) {
    headline = m.security ? t('gi.secAvailable') : t('gi.available', { version: m.version });
    detail = m.security ? t('gi.secBody', { n: m.fixes?.length ?? 0 }) : t('gi.availableBody');
  } else if (st.check_error) {
    headline = t('sp.updCantCheck');
    detail = st.check_error;
  } else {
    headline = t('gu.upToDate');
    detail = t('gi.upToDateBody', { version: cur });
  }

  const outcomeBanner = st.outcome && st.outcome !== 'checking' && st.state && !st.state.seen;

  return (
    <div className="stack settings-page">
      <div className="ucard set-update glowup">
        <Logo size={40} />
        <div className="set-update-text">
          <b>{headline}</b>
          {detail && <span>{detail}</span>}
          {st.checked && !installing && <small>{t('sp.updChecked', { time: new Date(st.checked).toLocaleString() })}</small>}
        </div>
        <div className="btn-group">
          {ready ? (
            <button type="button" className="pill small" disabled={restarting} onClick={() => void restart()}>
              {restarting ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} {t('gi.restartToFinish')}
            </button>
          ) : installing ? null : st.available && m ? (
            <button type="button" className="pill small" disabled={busy} onClick={() => void install()}>
              {busy ? <span className="spinner sm" /> : <Icon name="download" size={13} />} {m.security ? t('gi.installSec') : t('gi.install')}
            </button>
          ) : (
            <button type="button" className="pill ghost small" disabled={checking} onClick={() => void check()}>
              {checking ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} {t('gu.check')}
            </button>
          )}
        </div>

        {installing && (
          <div className="gi-progress" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(st.state?.progress ?? 0)}>
            <div className="gi-progress-fill" style={{ width: `${Math.max(2, st.state?.progress ?? 0)}%` }} />
            <span>{Math.round(st.state?.progress ?? 0)}%</span>
          </div>
        )}

        {phase === 'failed' && !installing && (
          <div className="set-update-result bad">
            <Icon name="alert" size={15} />
            <span>
              {t('gi.failed', { version: st.state?.to ?? '' })}
              {st.state?.error && <small className="set-update-err">{st.state.error}</small>}
            </span>
          </div>
        )}

        {outcomeBanner && st.outcome === 'updated' && (
          <div className="set-update-result ok">
            <Icon name="check" size={15} />
            <span>{t('gi.updated', { version: st.state!.to })}</span>
            <button type="button" className="ghost small" onClick={() => void dismiss()}>
              {t('gi.ok')}
            </button>
          </div>
        )}
        {outcomeBanner && st.outcome === 'rolled_back' && (
          <div className="set-update-result bad">
            <Icon name="alert" size={15} />
            <span>
              {t('gi.rolledBack', { version: st.state!.to, current: cur })}
              <small className="set-update-err">{t(reasonKey[st.reason ?? ''] ?? 'gi.reasonBoot')}</small>
            </span>
            <button type="button" className="ghost small" onClick={() => void dismiss()}>
              {t('gi.ok')}
            </button>
          </div>
        )}
      </div>

      {m && st.available && m.security && (m.fixes?.length ?? 0) > 0 && (
        <details className="settings-section glowup-details" open>
          <summary className="settings-section-title">{t('gi.fixesTitle')}</summary>
          <div className="settings-card">
            <div className="settings-row full sysupd">
              <ul className="sysupd-list">
                {m.fixes!.map((f) => (
                  <li key={f.package}>
                    <span className="sysupd-name">{f.package}</span>
                    <span className="sysupd-tag sec">{t('gu.tagSecurity')}</span>
                    <span className="sysupd-ver" title={(f.cves ?? []).join(', ')}>
                      {f.summary || t('gi.fixGeneric')}
                      {f.cves && f.cves.length > 0 && ` · ${f.cves.slice(0, 3).join(', ')}${f.cves.length > 3 ? ` +${f.cves.length - 3}` : ''}`}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </details>
      )}

      {m?.notes && st.available && !m.security && (
        <details className="settings-section glowup-details">
          <summary className="settings-section-title">{t('sp.whatsNew', { version: m.version })}</summary>
          <div className="settings-card">
            <div className="settings-row full">
              <pre className="set-notes">{m.notes}</pre>
            </div>
          </div>
        </details>
      )}

      {st.settings && (
        <div className="settings-section">
          <div className="settings-section-title">{t('gi.settingsTitle')}</div>
          <div className="settings-card">
            <div className="settings-row">
              <span className="settings-row-text">
                <span className="settings-row-label">{t('gi.channel')}</span>
                <span className="settings-row-hint">{t('gi.channelHint')}</span>
              </span>
              <Choice
                value={st.settings.channel}
                options={[
                  { id: 'stable', label: t('gi.stable') },
                  { id: 'beta', label: t('gi.beta') },
                ]}
                onChange={(v) => void saveSettings({ channel: v })}
              />
            </div>
            <Toggle
              label={t('gi.autoSec')}
              hint={t('gi.autoSecHint', { time: st.settings.auto_time })}
              checked={st.settings.auto_security}
              onChange={(on) => void saveSettings({ auto_security: on })}
            />
            {st.settings.auto_security && (
              <label className="settings-row">
                <span className="settings-row-text">
                  <span className="settings-row-label">{t('gi.autoTime')}</span>
                </span>
                <input type="time" value={st.settings.auto_time} onChange={(e) => e.target.value && void saveSettings({ auto_time: e.target.value })} />
              </label>
            )}
          </div>
          <p className="muted small glowup-note">{t('gi.aboutImage', { version: cur, snapshot: st.info?.snapshot?.slice(0, 8) ?? '' })}</p>
        </div>
      )}
    </div>
  );
}
