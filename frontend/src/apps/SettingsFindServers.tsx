import { useCallback, useEffect, useRef, useState } from 'react';
import { api, getUser } from '../api/client';
import { Icon } from '../components/Icon';
import { NoCapRadar } from '../components/Radar';
import { tr, useT, type T } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import {
  canAsk,
  mergeFound,
  minutesLeft,
  prettyFingerprint,
  safeURL,
  shortFingerprint,
  statusOf,
  type FoundServer,
  type LinkedServer,
  type PhoneAsk,
  type ServerAsk,
  type Squad,
  type SquadStatus,
} from '../lib/squad';
import type { FailoverInfo } from '../lib/failover';
import { FailoverSection, FailoverSheet, failoverApi } from './SettingsFailover';
import { Section, Toggle } from './Personalize';

// Settings → Your Squad: other NoCap servers on this network (found over
// mDNS by this server, with the NoCap radar while it looks), link requests
// between servers, the servers linked with this one, and, for
// administrators, whether phones nearby may find this one over Bluetooth.
// Bluetooth is only for finding: linking and setup go over the network.
// Phones asking to link without a code show up here too.

export type { FoundServer };

interface Bluetooth {
  mode: 'on' | 'off' | 'auto';
  enabled: boolean;
  supported: boolean;
  adapter: boolean;
  advertising: boolean;
  setup_new: boolean;
  error?: string;
}

export const findApi = {
  discover: () => api<{ available: boolean; servers: FoundServer[] }>('/api/v1/link/discover'),
  bluetooth: () => api<Bluetooth>('/api/v1/link/bluetooth'),
  setBluetooth: (enabled: boolean) => api<Bluetooth>('/api/v1/link/bluetooth', { method: 'PUT', body: { enabled } }),
};

export const squadApi = {
  get: () => api<Squad>('/api/v1/squad'),
  request: (s: FoundServer) => api<ServerAsk>('/api/v1/squad/request', { method: 'POST', body: { url: s.url, nocap_id: s.nocap_id } }),
  accept: (id: string) => api<{ ok: boolean }>(`/api/v1/squad/requests/${encodeURIComponent(id)}/accept`, { method: 'POST' }),
  decline: (id: string) => api<{ ok: boolean }>(`/api/v1/squad/requests/${encodeURIComponent(id)}/decline`, { method: 'POST' }),
  unlink: (id: string) => api<void>(`/api/v1/squad/links/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  phoneAsks: () => api<PhoneAsk[]>('/api/v1/squad/phone-asks'),
  acceptPhone: (id: string) => api<{ ok: boolean }>(`/api/v1/squad/phone-asks/${encodeURIComponent(id)}/accept`, { method: 'POST' }),
  declinePhone: (id: string) => api<{ ok: boolean }>(`/api/v1/squad/phone-asks/${encodeURIComponent(id)}/decline`, { method: 'POST' }),
};

/** The radar keeps spinning at least this long, so a quick answer still reads as a search. */
const MIN_SCAN_MS = 2400;
const POLL_MS = 5000;

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Refreshes while the page is open (and visible). */
function usePoll(fn: () => void, ms: number) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => {
    ref.current();
    const id = window.setInterval(() => {
      if (document.visibilityState !== 'hidden') ref.current();
    }, ms);
    return () => window.clearInterval(id);
  }, [ms]);
}

function Fingerprints({ t, theirs, ours, name }: { t: T; theirs: string; ours: string; name: string }) {
  return (
    <div className="squad-fp">
      <span>{t('squad.theirFp', { name })}</span>
      <code title={prettyFingerprint(theirs)}>{prettyFingerprint(theirs) || '—'}</code>
      <span>{t('squad.ourFp')}</span>
      <code title={prettyFingerprint(ours)}>{prettyFingerprint(ours) || '—'}</code>
    </div>
  );
}

export function FindServersSection() {
  const t = useT();
  const isAdmin = getUser()?.role === 'admin';
  const [found, setFound] = useState<{ available: boolean; servers: FoundServer[] } | null>(null);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const [scanning, setScanning] = useState(false);
  const [squad, setSquad] = useState<Squad | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [bt, setBt] = useState<Bluetooth | null>(null);
  const [btBusy, setBtBusy] = useState(false);
  const [fo, setFo] = useState<FailoverInfo | null>(null);
  const [foFor, setFoFor] = useState<LinkedServer | null>(null);

  useEffect(() => {
    if (isAdmin) void findApi.bluetooth().then((r) => r.ok && setBt(r.data));
  }, [isAdmin]);

  const loadSquad = useCallback(() => {
    if (!isAdmin) return;
    void squadApi.get().then((r) => r.ok && setSquad(r.data));
    void failoverApi.get().then((r) => r.ok && setFo(r.data));
  }, [isAdmin]);
  const foChanged = (i?: FailoverInfo) => (i ? setFo(i) : loadSquad());
  usePoll(loadSquad, POLL_MS);

  const look = async () => {
    setScanning(true);
    const [r] = await Promise.all([findApi.discover(), wait(MIN_SCAN_MS)]);
    setScanning(false);
    if (!r.ok) return toast('error', tr('find.failed'), r.error);
    const m = mergeFound(found?.servers ?? [], r.data.servers ?? []);
    setFresh(new Set(m.fresh));
    setFound({ available: r.data.available, servers: m.list });
  };

  const flip = async (on: boolean) => {
    setBtBusy(true);
    const r = await findApi.setBluetooth(on);
    setBtBusy(false);
    if (!r.ok) return toast('error', tr('find.btFailed'), r.error);
    setBt(r.data);
  };

  const act = async (key: string, fn: () => Promise<{ ok: boolean; error?: string }>, failKey: string, okMsg?: string) => {
    setBusy(key);
    const r = await fn();
    setBusy(null);
    if (!r.ok) {
      toast('error', tr(failKey), r.error);
    } else if (okMsg) {
      toast('success', okMsg);
    }
    loadSquad();
  };

  const send = (s: FoundServer) => void act('send:' + s.nocap_id, () => squadApi.request(s), 'squad.sendFail', tr('squad.sentToast', { name: s.name || s.nocap_id }));
  const accept = (a: ServerAsk) => void act('in:' + a.id, () => squadApi.accept(a.id), 'squad.acceptFail', tr('squad.acceptedToast', { name: a.name }));
  const decline = (a: ServerAsk) => void act('in:' + a.id, () => squadApi.decline(a.id), 'squad.declineFail');
  const unlink = async (l: LinkedServer) => {
    const ok = await confirmDialog({ title: tr('squad.unlinkTitle', { name: l.name }), message: tr('squad.unlinkMsg', { name: l.name }), confirmLabel: tr('squad.unlink'), danger: true });
    if (ok) void act('link:' + l.nocap_id, () => squadApi.unlink(l.nocap_id), 'squad.unlinkFail');
  };

  const btHint = !bt
    ? t('find.btHint')
    : !bt.supported
      ? t('find.btUnsupported')
      : !bt.adapter
        ? t('find.btNoAdapter')
        : bt.advertising
          ? t('find.btOn')
          : t('find.btHint');

  const servers = found?.servers ?? [];
  const n = servers.length;
  const self = squad?.self;
  const blips = servers.map((s) => ({ id: s.nocap_id, label: s.name || s.nocap_id, linked: statusOf(s, squad) === 'linked' }));
  const foundIds = new Set(servers.map((s) => s.nocap_id));
  // Requests this server sent to servers that aren't in the found list (yet).
  const otherOut = (squad?.outgoing ?? []).filter((a) => !foundIds.has(a.nocap_id));

  return (
    <>
      {isAdmin && bt && (
        <Section title={t('find.btTitle')}>
          <Toggle label={t('find.btLabel')} hint={btHint} checked={bt.enabled} disabled={btBusy || !bt.supported} onChange={(v) => void flip(v)} />
        </Section>
      )}

      <Section title={t('find.title')} hint={t('find.hint')}>
        <div className="squad-scan">
          <NoCapRadar scanning={scanning} blips={blips} size={176} label={scanning ? t('find.searching', { n }) : t('find.radarIdle')} />
          <div className="squad-scan-text">
            <b aria-live="polite">{scanning ? t('find.searching', { n }) : found ? t('find.count', { n }) : t('find.idle')}</b>
            {self && !self.can_link && <p className="muted">{t('find.needsHttps')}</p>}
            {self?.fingerprint && (
              <p className="muted" title={prettyFingerprint(self.fingerprint)}>
                {t('squad.selfFp', { fp: shortFingerprint(self.fingerprint) })}
              </p>
            )}
            <button type="button" className="small" disabled={scanning} onClick={() => void look()}>
              <Icon name="search" size={14} /> {found ? t('find.again') : t('find.go')}
            </button>
          </div>
        </div>
        {found && !found.available && <p className="squad-empty">{t('find.unavailable')}</p>}
        {found?.available && n === 0 && !scanning && <p className="squad-empty">{t('find.none')}</p>}
        {n > 0 && (
          <div className="squad-grid">
            {servers.map((s, i) => (
              <FoundCard
                key={s.nocap_id}
                t={t}
                s={s}
                st={statusOf(s, squad)}
                ask={squad?.outgoing.find((a) => a.nocap_id === s.nocap_id)}
                fresh={fresh.has(s.nocap_id)}
                delay={i * 70}
                canLink={!!self?.can_link}
                busy={busy === 'send:' + s.nocap_id}
                onSend={() => send(s)}
              />
            ))}
          </div>
        )}
      </Section>

      {squad && (squad.incoming.length > 0 || otherOut.length > 0) && (
        <Section title={t('squad.requestsTitle')} hint={t('squad.requestsHint')}>
          <div className="squad-grid">
            {squad.incoming.map((a) => (
              <div key={a.id} className="squad-card asking fresh">
                <div className="squad-card-head">
                  <span className="ld-card-icon">
                    <Icon name="link" size={20} />
                  </span>
                  <span className="squad-card-title">
                    <b>{t('squad.wants', { name: a.name || a.nocap_id })}</b>
                    <span className="squad-card-sub">{t('squad.from', { address: a.address, id: a.nocap_id })}</span>
                    {a.version && <span className="squad-card-sub">{t('find.version', { v: a.version })}</span>}
                  </span>
                  <span className="squad-chip red">{t('squad.expires', { m: minutesLeft(a.expires_at) })}</span>
                </div>
                <Fingerprints t={t} theirs={a.fingerprint} ours={squad.self.fingerprint} name={a.name || a.nocap_id} />
                <p className="squad-card-sub">{t('squad.compare', { name: a.name || a.nocap_id })}</p>
                <div className="squad-actions">
                  <button type="button" className="small" disabled={busy === 'in:' + a.id} onClick={() => accept(a)}>
                    <Icon name="check" size={14} /> {t('squad.accept')}
                  </button>
                  <button type="button" className="small ghost" disabled={busy === 'in:' + a.id} onClick={() => decline(a)}>
                    {t('squad.decline')}
                  </button>
                </div>
              </div>
            ))}
            {otherOut.map((a) => (
              <div key={a.id} className="squad-card">
                <div className="squad-card-head">
                  <span className="ld-card-icon">
                    <Icon name="send" size={18} />
                  </span>
                  <span className="squad-card-title">
                    <b>{a.state === 'declined' ? t('squad.declinedBy', { name: a.name || a.nocap_id }) : t('squad.waiting', { name: a.name || a.nocap_id })}</b>
                    <span className="squad-card-sub">{t('squad.from', { address: a.address, id: a.nocap_id })}</span>
                  </span>
                </div>
                <div className="squad-actions">
                  <button type="button" className="small ghost" disabled={busy === 'in:' + a.id} onClick={() => decline(a)}>
                    {a.state === 'declined' ? t('squad.dismiss') : t('squad.cancel')}
                  </button>
                </div>
              </div>
            ))}
          </div>
        </Section>
      )}

      <FailoverSection info={fo} onChanged={foChanged} />

      {squad && (
        <Section title={t('squad.linkedTitle')} hint={t('squad.linkedHint')}>
          {squad.linked.length === 0 ? (
            <p className="squad-empty">{t('squad.linkedEmpty')}</p>
          ) : (
            <div className="squad-grid">
              {squad.linked.map((l) => {
                const href = safeURL(l.url);
                return (
                  <div key={l.nocap_id} className={`squad-card linked${l.online ? ' online' : ''}`}>
                    <div className="squad-card-head">
                      <span className="ld-card-icon">
                        <Icon name="monitor" size={20} />
                        <span className="ld-dot" />
                      </span>
                      <span className="squad-card-title">
                        <b>{l.name || l.nocap_id}</b>
                        <span className="squad-card-sub">
                          {[l.version ? t('find.version', { v: l.version }) : '', l.nocap_id].filter(Boolean).join(' · ')}
                        </span>
                        {l.linked_by && <span className="squad-card-sub">{t('squad.byUser', { user: l.linked_by })}</span>}
                      </span>
                      {l.role === 'failover' && <span className="squad-chip red">{t('fo.chipFailover')}</span>}
                      <span className={`squad-chip${l.online ? ' on' : ''}`}>{l.online ? t('squad.online') : t('squad.offline')}</span>
                    </div>
                    <div className="squad-fp">
                      <span>{t('squad.pinned')}</span>
                      <code title={prettyFingerprint(l.fingerprint)}>{shortFingerprint(l.fingerprint)}</code>
                    </div>
                    <div className="squad-actions">
                      {href && (
                        <a className="btn small" href={href} target="_blank" rel="noreferrer noopener">
                          {t('find.open')} <Icon name="external" size={13} />
                        </a>
                      )}
                      {fo && !fo.pair && (
                        <button type="button" className="small" disabled={!l.online} title={l.online ? undefined : t('fo.needsOnline')} onClick={() => setFoFor(l)}>
                          <Icon name="swap" size={14} /> {t('fo.make')}
                        </button>
                      )}
                      <button type="button" className="small ghost danger" disabled={busy === 'link:' + l.nocap_id} onClick={() => void unlink(l)}>
                        {t('squad.unlink')}
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Section>
      )}
      {foFor && (
        <FailoverSheet
          server={foFor}
          info={fo}
          onClose={() => setFoFor(null)}
          onDone={(i) => {
            setFoFor(null);
            setFo(i);
          }}
        />
      )}
    </>
  );
}

function statusChip(t: T, st: SquadStatus) {
  switch (st) {
    case 'linked':
      return <span className="squad-chip on">{t('find.linked')}</span>;
    case 'incoming':
      return <span className="squad-chip red">{t('find.asking')}</span>;
    case 'sent':
      return <span className="squad-chip">{t('find.sent')}</span>;
    case 'declined':
      return <span className="squad-chip">{t('find.declined')}</span>;
    case 'new':
      return <span className="squad-chip red">{t('find.new')}</span>;
  }
  return null;
}

function FoundCard(p: {
  t: T;
  s: FoundServer;
  st: SquadStatus;
  ask?: ServerAsk;
  fresh: boolean;
  delay: number;
  canLink: boolean;
  busy: boolean;
  onSend: () => void;
}) {
  const { t, s, st, ask } = p;
  const href = safeURL(s.url);
  return (
    <div className={`squad-card${p.fresh ? ' fresh' : ''}${st === 'linked' ? ' linked online' : ''}${st === 'incoming' ? ' asking' : ''}`} style={p.fresh ? { animationDelay: `${p.delay}ms` } : undefined}>
      <div className="squad-card-head">
        <span className="ld-card-icon">
          <Icon name="monitor" size={20} />
        </span>
        <span className="squad-card-title">
          <b>{s.name || s.hostname || s.nocap_id}</b>
          <span className="squad-card-sub">{[s.addresses.join(' · '), s.version ? t('find.version', { v: s.version }) : ''].filter(Boolean).join(' — ')}</span>
          <span className="squad-card-sub">{s.nocap_id}</span>
        </span>
        {statusChip(t, st)}
      </div>
      {st === 'sent' && ask && (
        <>
          <p className="squad-card-sub">{t('find.sentHint', { name: s.name || s.nocap_id, m: minutesLeft(ask.expires_at) })}</p>
          <div className="squad-fp">
            <span>{t('squad.theirFp', { name: s.name || s.nocap_id })}</span>
            <code title={prettyFingerprint(ask.fingerprint)}>{prettyFingerprint(ask.fingerprint)}</code>
          </div>
        </>
      )}
      {st === 'incoming' && <p className="squad-card-sub">{t('find.askingHint')}</p>}
      <div className="squad-actions">
        {canAsk(st) && (
          <button type="button" className="small" disabled={p.busy || !p.canLink || !href.startsWith('https:')} onClick={p.onSend}>
            {p.busy ? <span className="spinner sm" /> : <Icon name="link" size={14} />} {t('find.sendRequest')}
          </button>
        )}
        {href && (
          <a className="btn small ghost" href={href} target="_blank" rel="noreferrer noopener">
            {st === 'new' ? t('find.setUp') : t('find.open')} <Icon name="external" size={13} />
          </a>
        )}
      </div>
    </div>
  );
}

/**
 * Phones asking to link without a code: administrators see every request,
 * everyone else the ones naming them. Shown to everyone in Your Squad
 * (nothing when there are none).
 */
export function PhoneAsksSection({ onLinked }: { onLinked?: () => void }) {
  const t = useT();
  const [asks, setAsks] = useState<PhoneAsk[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(() => {
    void squadApi.phoneAsks().then((r) => r.ok && setAsks(r.data ?? []));
  }, []);
  usePoll(load, POLL_MS);

  const answer = async (a: PhoneAsk, yes: boolean) => {
    setBusy(a.id);
    const r = yes ? await squadApi.acceptPhone(a.id) : await squadApi.declinePhone(a.id);
    setBusy(null);
    if (!r.ok) toast('error', tr(yes ? 'squad.acceptFail' : 'squad.declineFail'), r.error);
    else if (yes) {
      toast('success', tr('squad.phoneAccepted', { name: a.device_name }));
      onLinked?.();
    }
    load();
  };

  if (asks.length === 0) return null;
  return (
    <Section title={t('squad.phonesTitle')} hint={t('squad.phonesHint')}>
      <div className="squad-grid">
        {asks.map((a) => (
          <div key={a.id} className="squad-card asking fresh">
            <div className="squad-card-head">
              <span className="ld-card-icon">
                <Icon name={a.platform === 'android' ? 'android' : 'phone'} size={20} />
              </span>
              <span className="squad-card-title">
                <b>{t('squad.phoneWants', { name: a.device_name })}</b>
                <span className="squad-card-sub">{[a.model, a.os_version].filter(Boolean).join(' · ')}</span>
                <span className="squad-card-sub">{t('squad.phoneFrom', { address: a.address })}</span>
              </span>
              <span className="squad-chip red">{t('squad.expires', { m: minutesLeft(a.expires_at) })}</span>
            </div>
            <div>
              <span className="squad-card-sub">{t('squad.checkHint')}</span>
              <div className="squad-check">{a.check}</div>
            </div>
            {a.username && (
              <p className="squad-card-sub">
                {a.note === 'unknown_user'
                  ? t('squad.phoneUnknown', { user: a.username })
                  : a.note === 'for_user'
                    ? t('squad.phoneForOther', { user: a.username })
                    : t('squad.phoneFor', { user: a.username })}
              </p>
            )}
            <div className="squad-actions">
              {a.can_accept && (
                <button type="button" className="small" disabled={busy === a.id} onClick={() => void answer(a, true)}>
                  <Icon name="check" size={14} /> {t('squad.accept')}
                </button>
              )}
              <button type="button" className="small ghost" disabled={busy === a.id} onClick={() => void answer(a, false)}>
                {t('squad.decline')}
              </button>
            </div>
          </div>
        ))}
      </div>
    </Section>
  );
}
