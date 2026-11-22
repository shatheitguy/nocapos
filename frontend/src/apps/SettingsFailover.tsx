import { useCallback, useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { api, type ApiResult } from '../api/client';
import { Icon } from '../components/Icon';
import { WinClose } from '../components/WinClose';
import { tr, useT, type T } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { relTime } from '../state/notifications';
import { fmtBytes } from '../lib/format';
import { changedFields, syncHealth, type FailoverInfo, type FailoverPair, type SyncConflict, type SyncHealth } from '../lib/failover';
import type { LinkedServer } from '../lib/squad';
import { Section } from './Personalize';

// Settings → Your Squad → Failover: a linked server kept in sync with this
// one. Turning it on (both administrators agree, one server is primary for
// the first sync), how each kind of setting is syncing, conflicts with
// "keep mine / keep theirs", pause and sync now.

export const failoverApi = {
  get: () => api<FailoverInfo>('/api/v1/failover'),
  request: (nocapID: string, primary: 'self' | 'peer') => api<FailoverInfo>('/api/v1/failover/request', { method: 'POST', body: { nocap_id: nocapID, primary } }),
  accept: () => api<FailoverInfo>('/api/v1/failover/accept', { method: 'POST' }),
  off: () => api<FailoverInfo>('/api/v1/failover', { method: 'DELETE' }),
  pause: (paused: boolean) => api<FailoverInfo>('/api/v1/failover/pause', { method: 'POST', body: { paused } }),
  syncNow: () => api<FailoverInfo>('/api/v1/failover/sync', { method: 'POST' }),
  conflicts: () => api<SyncConflict[]>('/api/v1/failover/conflicts'),
  resolve: (id: number, keep: 'mine' | 'theirs') => api<{ ok: boolean }>(`/api/v1/failover/conflicts/${id}`, { method: 'POST', body: { keep } }),
};

const HEALTH_CHIP: Record<SyncHealth, string> = {
  asked: '',
  asking: ' red',
  starting: '',
  paused: '',
  failing: ' red',
  behind: '',
  ok: ' on',
};

const when = (iso?: string) => (iso ? relTime(iso) : '—');

/** "Make this my failover": what syncs, the space it takes, which side is primary. */
export function FailoverSheet({ server, info, onClose, onDone }: { server: LinkedServer; info: FailoverInfo | null; onClose: () => void; onDone: (i: FailoverInfo) => void }) {
  const t = useT();
  const [primary, setPrimary] = useState<'self' | 'peer'>('self');
  const [busy, setBusy] = useState(false);
  const name = server.name || server.nocap_id;

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const send = async () => {
    setBusy(true);
    const r = await failoverApi.request(server.nocap_id, primary);
    setBusy(false);
    if (!r.ok) return toast('error', tr('fo.requestFail'), r.error);
    toast('success', tr('fo.requestSent', { name }));
    onDone(r.data);
  };

  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="confirm-dialog glass fo-sheet" role="dialog" aria-modal="true" aria-labelledby="fo-sheet-title">
        <WinClose onClick={onClose} label={t('fo.close')} />
        <div className="confirm-icon">
          <Icon name="swap" size={22} />
        </div>
        <h3 id="fo-sheet-title">{t('fo.sheetTitle', { name })}</h3>
        <p>{t('fo.sheetIntro', { name })}</p>
        <ul className="fo-what">
          {(info?.categories ?? []).map((c) => (
            <li key={c}>
              <Icon name="check" size={13} /> <b>{t(`fo.cat.${c}`)}</b> <span className="muted">{t(`fo.catHint.${c}`)}</span>
            </li>
          ))}
        </ul>
        <p className="muted small">{t('fo.notYet')}</p>
        <p className="muted small">{t('fo.space', { size: fmtBytes(info?.db_bytes ?? 0) })}</p>
        <fieldset className="fo-primary">
          <legend>{t('fo.primaryTitle')}</legend>
          <label>
            <input type="radio" name="fo-primary" checked={primary === 'self'} onChange={() => setPrimary('self')} />
            <span>
              <b>{t('fo.primarySelf')}</b>
              <span className="muted small">{t('fo.primarySelfHint', { name })}</span>
            </span>
          </label>
          <label>
            <input type="radio" name="fo-primary" checked={primary === 'peer'} onChange={() => setPrimary('peer')} />
            <span>
              <b>{t('fo.primaryPeer', { name })}</b>
              <span className="muted small">{t('fo.primaryPeerHint', { name })}</span>
            </span>
          </label>
        </fieldset>
        <p className="muted small">{t('fo.bothAgree', { name })}</p>
        <div className="confirm-actions">
          <button type="button" className="ghost" onClick={onClose}>
            {t('fo.cancel')}
          </button>
          <button type="button" disabled={busy || !server.online} onClick={() => void send()}>
            {busy ? <span className="spinner sm" /> : <Icon name="send" size={14} />} {t('fo.sendRequest')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function healthText(t: T, h: SyncHealth, p: FailoverPair) {
  const name = p.name || p.nocap_id;
  switch (h) {
    case 'asked':
      return t('fo.hAsked', { name });
    case 'asking':
      return t('fo.hAsking', { name });
    case 'starting':
      return p.primary === 'self' ? t('fo.hStartingPrimary', { name }) : t('fo.hStarting', { name });
    case 'paused':
      return t('fo.hPaused');
    case 'failing':
      return t('fo.hFailing');
    case 'behind':
      return t('fo.hBehind');
  }
  return t('fo.hOk');
}

/** The pair, its sync status per kind of setting, and conflicts. Nothing when there's no pair. */
export function FailoverSection({ info, onChanged }: { info: FailoverInfo | null; onChanged: (i?: FailoverInfo) => void }) {
  const t = useT();
  const [busy, setBusy] = useState<string | null>(null);
  const [conflicts, setConflicts] = useState<SyncConflict[]>([]);
  const p = info?.pair ?? null;
  const nConflicts = p?.conflicts ?? 0;

  const loadConflicts = useCallback(() => {
    void failoverApi.conflicts().then((r) => r.ok && setConflicts(r.data ?? []));
  }, []);
  useEffect(() => {
    if (nConflicts > 0) loadConflicts();
    else setConflicts([]);
  }, [nConflicts, loadConflicts]);

  if (!p) return null;
  const name = p.name || p.nocap_id;
  const h = syncHealth(p);

  const run = async (key: string, fn: () => Promise<ApiResult<FailoverInfo>>, failKey: string, okMsg?: string) => {
    setBusy(key);
    const r = await fn();
    setBusy(null);
    if (!r.ok) {
      toast('error', tr(failKey), r.error);
      onChanged();
      return;
    }
    if (okMsg) toast('success', okMsg);
    onChanged(r.data);
  };

  const off = async () => {
    const asking = p.state === 'asking';
    const ok = await confirmDialog({
      title: asking ? tr('fo.declineTitle', { name }) : tr('fo.offTitle', { name }),
      message: asking ? tr('fo.declineMsg', { name }) : tr('fo.offMsg', { name }),
      confirmLabel: asking ? tr('fo.decline') : tr('fo.turnOff'),
      danger: !asking,
    });
    if (ok) void run('off', failoverApi.off, 'fo.offFail');
  };

  const accept = async () => {
    const ok = await confirmDialog({
      title: tr('fo.acceptTitle', { name }),
      message: p.primary === 'self' ? tr('fo.acceptMsgSelf', { name }) : tr('fo.acceptMsgPeer', { name }),
      confirmLabel: tr('fo.accept'),
      danger: p.primary === 'peer',
    });
    if (ok) void run('accept', failoverApi.accept, 'fo.acceptFail', tr('fo.accepted', { name }));
  };

  const resolve = async (c: SyncConflict, keep: 'mine' | 'theirs') => {
    setBusy('c:' + c.id);
    const r = await failoverApi.resolve(c.id, keep);
    setBusy(null);
    if (!r.ok) toast('error', tr('fo.resolveFail'), r.error);
    loadConflicts();
    onChanged();
  };

  return (
    <Section title={t('fo.title')} hint={t('fo.hint')}>
      <div className={`squad-card fo-card${h === 'ok' ? ' linked online' : ''}${h === 'asking' || h === 'failing' ? ' asking' : ''}`}>
        <div className="squad-card-head">
          <span className="ld-card-icon">
            <Icon name="swap" size={20} />
            {p.online && <span className="ld-dot" />}
          </span>
          <span className="squad-card-title">
            <b>{t('fo.pairWith', { name })}</b>
            <span className="squad-card-sub">{p.primary === 'self' ? t('fo.primaryIsSelf') : t('fo.primaryIsPeer', { name })}</span>
            {p.asked_by && <span className="squad-card-sub">{t('fo.askedBy', { user: p.asked_by })}</span>}
          </span>
          <span className={`squad-chip${HEALTH_CHIP[h]}`}>{t(`fo.chip.${h}`)}</span>
        </div>
        <p className="squad-card-sub" aria-live="polite">
          {healthText(t, h, p)}
        </p>
        {p.last_error && h !== 'ok' && <p className="fo-error">{p.last_error}</p>}
        {p.state === 'active' && (
          <dl className="fo-facts">
            <dt>{t('fo.lastSync')}</dt>
            <dd>{when(p.last_ok)}</dd>
            <dt>{t('fo.queued')}</dt>
            <dd>{p.queued}</dd>
            <dt>{t('fo.conflicts')}</dt>
            <dd>{p.conflicts}</dd>
          </dl>
        )}
        <div className="squad-actions">
          {p.state === 'asking' && (
            <button type="button" className="small" disabled={!!busy} onClick={() => void accept()}>
              <Icon name="check" size={14} /> {t('fo.accept')}
            </button>
          )}
          {(p.state === 'active' || p.state === 'starting') && (
            <button type="button" className="small" disabled={!!busy} onClick={() => void run('sync', failoverApi.syncNow, 'fo.syncFail', tr('fo.synced'))}>
              {busy === 'sync' ? <span className="spinner sm" /> : <Icon name="restart" size={14} />} {t('fo.syncNow')}
            </button>
          )}
          {p.state === 'active' && (
            <button type="button" className="small ghost" disabled={!!busy} onClick={() => void run('pause', () => failoverApi.pause(!p.paused), 'fo.pauseFail')}>
              <Icon name={p.paused ? 'play' : 'pause'} size={14} /> {p.paused ? t('fo.resume') : t('fo.pause')}
            </button>
          )}
          <button type="button" className="small ghost danger" disabled={!!busy} onClick={() => void off()}>
            {p.state === 'asking' ? t('fo.decline') : p.state === 'asked' ? t('fo.cancelRequest') : t('fo.turnOff')}
          </button>
        </div>
      </div>

      {p.state === 'active' && (
        <div className="fo-cats">
          {p.categories.map((c) => (
            <div key={c.id} className="fo-cat">
              <b>{t(`fo.cat.${c.id}`)}</b>
              <span className={`squad-chip${c.queued > 0 ? '' : ' on'}`}>{c.queued > 0 ? t('fo.nQueued', { n: c.queued }) : t('fo.inSync')}</span>
              <span className="muted small">{c.last_change ? t('fo.changed', { when: when(c.last_change) }) : t('fo.noChanges')}</span>
            </div>
          ))}
        </div>
      )}

      {conflicts.length > 0 && (
        <div className="fo-conflicts">
          <h4>{t('fo.conflictsTitle')}</h4>
          <p className="muted small">{t('fo.conflictsHint', { name })}</p>
          {conflicts.map((c) => (
            <div key={c.id} className="fo-conflict">
              <div className="fo-conflict-head">
                <b>{t(`fo.cat.${c.category || 'settings'}`)}</b>
                <code>{c.table}</code>
                <span className="muted small">{c.key}</span>
                <span className="squad-chip">{c.winner === 'mine' ? t('fo.keptMine') : t('fo.keptTheirs')}</span>
              </div>
              {c.note && <p className="fo-error">{c.note}</p>}
              <ConflictDiff t={t} c={c} name={name} />
              <div className="squad-actions">
                <button type="button" className={`small${c.winner === 'mine' ? '' : ' ghost'}`} disabled={busy === 'c:' + c.id} onClick={() => void resolve(c, 'mine')}>
                  {t('fo.keepMine')}
                </button>
                <button type="button" className={`small${c.winner === 'theirs' ? '' : ' ghost'}`} disabled={busy === 'c:' + c.id} onClick={() => void resolve(c, 'theirs')}>
                  {t('fo.keepTheirs', { name })}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </Section>
  );
}

function ConflictDiff({ t, c, name }: { t: T; c: SyncConflict; name: string }) {
  const fields = changedFields(c).slice(0, 8);
  return (
    <table className="fo-diff">
      <thead>
        <tr>
          <th />
          <th>{t('fo.here')}</th>
          <th>{name}</th>
        </tr>
      </thead>
      <tbody>
        {!c.mine || !c.theirs ? (
          <tr>
            <td />
            <td>{c.mine ? t('fo.present') : t('fo.deleted')}</td>
            <td>{c.theirs ? t('fo.present') : t('fo.deleted')}</td>
          </tr>
        ) : fields.length === 0 ? (
          <tr>
            <td colSpan={3} className="muted">
              {t('fo.hiddenDiff')}
            </td>
          </tr>
        ) : (
          fields.map((f) => (
            <tr key={f}>
              <th>{f}</th>
              <td>{c.mine?.[f] ?? '—'}</td>
              <td>{c.theirs?.[f] ?? '—'}</td>
            </tr>
          ))
        )}
      </tbody>
    </table>
  );
}
