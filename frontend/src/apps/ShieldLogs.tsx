import { useCallback, useEffect, useState } from 'react';
import { shieldApi, type BlockedConn, type DnsQuery, type DnsStats } from '../api/shield';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Card, Select, fmtClock, fmtNum, pct } from './ShieldCommon';

// Shield → Logs: the DNS query log (with a per-device view) and the
// connections the firewall turned away.

type View = 'queries' | 'devices' | 'blocked';

export function LogsTab({ stats, logOn }: { stats: DnsStats; logOn: boolean }) {
  const t = useT();
  const [view, setView] = useState<View>('queries');
  const [client, setClient] = useState('');
  return (
    <>
      <div className="sh-seg" role="tablist">
        {(['queries', 'devices', 'blocked'] as View[]).map((v) => (
          <button key={v} type="button" role="tab" aria-selected={view === v} className={`chip-btn ${view === v ? 'on' : ''}`} onClick={() => setView(v)}>{t(`sh.logs.${v}`)}</button>
        ))}
      </div>
      {view === 'queries' && <Queries logOn={logOn} client={client} setClient={setClient} />}
      {view === 'devices' && (
        <Devices
          stats={stats}
          logOn={logOn}
          pick={(c) => {
            setClient(c);
            setView('queries');
          }}
        />
      )}
      {view === 'blocked' && <BlockedConns />}
    </>
  );
}

const STATUSES = ['', 'blocked', 'forwarded', 'cached', 'local', 'error'] as const;

function Queries({ logOn, client, setClient }: { logOn: boolean; client: string; setClient: (c: string) => void }) {
  const t = useT();
  const [q, setQ] = useState('');
  const [status, setStatus] = useState<(typeof STATUSES)[number]>('');
  const [rows, setRows] = useState<DnsQuery[] | null>(null);
  const [more, setMore] = useState(false);

  const load = useCallback(async (before?: number) => {
    const r = await shieldApi.log({ client, q, status, before });
    if (!r.ok) return toast('error', t('sh.logs.failed'), r.error);
    setRows((old) => (before && old ? [...old, ...r.data.queries] : r.data.queries));
    setMore(r.data.queries.length >= 200);
  }, [client, q, status, t]);

  useEffect(() => {
    const id = window.setTimeout(() => void load(), 250);
    return () => window.clearTimeout(id);
  }, [load]);

  const clear = async () => {
    if (!(await confirmDialog({ title: t('sh.logs.clearTitle'), message: t('sh.logs.clearMsg'), confirmLabel: t('sh.logs.clear'), danger: true }))) return;
    const r = await shieldApi.clearLog();
    if (!r.ok) return toast('error', t('sh.logs.failed'), r.error);
    setRows([]);
  };

  if (!logOn) return <Card><p className="muted">{t('sh.logs.off')}</p></Card>;

  return (
    <Card
      title={t('sh.logs.queries')}
      icon="logs"
      hint={t('sh.logs.privacy')}
      actions={<button type="button" className="ghost small danger" onClick={() => void clear()}><Icon name="trash" size={13} />{t('sh.logs.clear')}</button>}
    >
      <div className="sh-filters">
        <input type="search" value={q} placeholder={t('sh.logs.search')} maxLength={253} onChange={(e) => setQ(e.target.value)} />
        <Select label={t('sh.logs.status')} value={status} options={STATUSES.map((s) => ({ id: s, label: t(s ? `sh.st.${s}` : 'sh.logs.all') }))} onChange={setStatus} />
        {client && (
          <button type="button" className="chip-btn on" onClick={() => setClient('')}>{client}<Icon name="close" size={11} /></button>
        )}
        <button type="button" className="icon-btn" aria-label={t('sh.logs.reload')} onClick={() => void load()}><Icon name="restart" size={15} /></button>
      </div>
      {!rows ? (
        <p className="muted">{t('sh.loading')}</p>
      ) : rows.length === 0 ? (
        <p className="muted">{t('sh.logs.empty')}</p>
      ) : (
        <ul className="sh-list sh-log">
          {rows.map((r, i) => (
            <li key={`${r.time}-${i}`} className={`sh-row st-${r.status}`}>
              <span className={`sh-st st-${r.status}`}>{t(`sh.st.${r.status}`)}</span>
              <span className="sh-row-text">
                <span className="sh-row-title ellipsis">{r.name} <span className="muted">{r.type}</span></span>
                <span className="sh-row-sub ellipsis">
                  {fmtClock(r.time)}
                  {r.client ? ` · ${r.client}` : ''}
                  {r.reason ? ` · ${r.reason}` : ''}
                  {r.status === 'forwarded' ? ` · ${r.ms} ms` : ''}
                  {r.rcode && r.rcode !== 'NOERROR' ? ` · ${r.rcode}` : ''}
                </span>
              </span>
            </li>
          ))}
        </ul>
      )}
      {more && rows && (
        <button type="button" className="ghost small" onClick={() => void load(new Date(rows[rows.length - 1].time).getTime())}>{t('sh.logs.more')}</button>
      )}
    </Card>
  );
}

function Devices({ stats, logOn, pick }: { stats: DnsStats; logOn: boolean; pick: (c: string) => void }) {
  const t = useT();
  if (!logOn) return <Card><p className="muted">{t('sh.logs.off')}</p></Card>;
  return (
    <Card title={t('sh.logs.devices')} icon="users" hint={t('sh.logs.devicesHint')}>
      {stats.clients.length === 0 ? (
        <p className="muted">{t('sh.logs.noDevices')}</p>
      ) : (
        <ul className="sh-list">
          {stats.clients.map((c) => (
            <li key={c.client} className="sh-row">
              <button type="button" className="sh-row-btn" onClick={() => pick(c.client)}>
                <span className="sh-row-ico"><Icon name="monitor" size={15} /></span>
                <span className="sh-row-text">
                  <span className="sh-row-title">{c.client}</span>
                  <span className="sh-row-sub">{t('sh.logs.deviceLine', { n: fmtNum(c.total), blocked: fmtNum(c.blocked), pct: pct(c.blocked, c.total), when: fmtClock(c.last_seen) })}</span>
                </span>
                <Icon name="chevronRight" size={15} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function BlockedConns() {
  const t = useT();
  const [rows, setRows] = useState<BlockedConn[] | null>(null);
  const load = useCallback(async () => {
    const r = await shieldApi.blocked();
    if (r.ok) setRows(r.data.blocked);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  return (
    <Card
      title={t('sh.logs.blocked')}
      icon="lock"
      hint={t('sh.logs.blockedHint')}
      actions={<button type="button" className="icon-btn" aria-label={t('sh.logs.reload')} onClick={() => void load()}><Icon name="restart" size={15} /></button>}
    >
      {!rows ? (
        <p className="muted">{t('sh.loading')}</p>
      ) : rows.length === 0 ? (
        <p className="muted">{t('sh.logs.noBlocked')}</p>
      ) : (
        <ul className="sh-list sh-log">
          {rows.map((b, i) => (
            <li key={`${b.time}-${i}`} className="sh-row">
              <span className="sh-st st-blocked">{b.proto.toUpperCase()}</span>
              <span className="sh-row-text">
                <span className="sh-row-title ellipsis">{b.src}{b.src_port ? `:${b.src_port}` : ''} → {b.dst}{b.dst_port ? `:${b.dst_port}` : ''}</span>
                <span className="sh-row-sub">{b.time.startsWith('0001') ? '' : fmtClock(b.time)}{b.interface ? ` · ${b.interface}` : ''} · {b.action}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
