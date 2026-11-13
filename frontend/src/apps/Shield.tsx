import { useCallback, useEffect, useState } from 'react';
import { shieldApi, type DnsStats, type DnsStatus, type FwState } from '../api/shield';
import { Icon, type IconName } from '../components/Icon';
import { useT } from '../i18n';
import { toast } from '../state/toasts';
import { Empty } from './Monitor';
import { Card, QueryBars, Stat, fmtNum, fwCall, pct, useSecondsLeft } from './ShieldCommon';
import { DnsTab } from './ShieldDns';
import { FirewallTab } from './ShieldFirewall';
import { LogsTab } from './ShieldLogs';
import { SettingsTab } from './ShieldSettings';

// Shield: one app for the firewall (Linux's own ufw does the work, this is
// its control panel) and NoCapOS's DNS ad and tracker blocker.

type Tab = 'overview' | 'firewall' | 'dns' | 'logs' | 'settings';
const TABS: { id: Tab; icon: IconName }[] = [
  { id: 'overview', icon: 'shield' },
  { id: 'firewall', icon: 'lock' },
  { id: 'dns', icon: 'globe' },
  { id: 'logs', icon: 'logs' },
  { id: 'settings', icon: 'sliders' },
];

export function Shield() {
  const t = useT();
  const [tab, setTab] = useState<Tab>('overview');
  const [fw, setFw] = useState<FwState | null>(null);
  const [dns, setDns] = useState<DnsStatus | null>(null);
  const [stats, setStats] = useState<DnsStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const r = await shieldApi.overview();
    if (!r.ok) {
      setError(r.error ?? t('sh.loadFail'));
      return;
    }
    setError(null);
    setFw(r.data.firewall);
    setDns(r.data.dns);
    setStats(r.data.stats);
  }, [t]);

  useEffect(() => {
    void load();
    const id = window.setInterval(() => void load(), 10000);
    return () => window.clearInterval(id);
  }, [load]);

  if (error && !fw) return <Empty icon="shield" text={error} />;
  if (!fw || !dns || !stats) return <Empty icon="shield" text={t('sh.loading')} />;

  return (
    <div className="sh-app">
      <nav className="sh-tabs" role="tablist" aria-label={t('sh.title')}>
        {TABS.map((x) => (
          <button key={x.id} type="button" role="tab" aria-selected={tab === x.id} className={`sh-tab ${tab === x.id ? 'on' : ''}`} onClick={() => setTab(x.id)}>
            <Icon name={x.icon} size={15} />
            <span>{t(`sh.tab.${x.id}`)}</span>
          </button>
        ))}
      </nav>
      <KeepBanner fw={fw} setFw={setFw} reload={load} />
      <div className="sh-body">
        {tab === 'overview' && <Overview fw={fw} dns={dns} stats={stats} setDns={setDns} go={setTab} />}
        {tab === 'firewall' && <FirewallTab fw={fw} setFw={setFw} />}
        {tab === 'dns' && <DnsTab dns={dns} setDns={setDns} stats={stats} />}
        {tab === 'logs' && <LogsTab stats={stats} logOn={dns.settings.log_enabled} />}
        {tab === 'settings' && <SettingsTab fw={fw} setFw={setFw} dns={dns} setDns={setDns} />}
      </div>
    </div>
  );
}

/** "Keep these changes?" with a countdown; the server undoes them at zero. */
function KeepBanner({ fw, setFw, reload }: { fw: FwState; setFw: (s: FwState) => void; reload: () => Promise<void> }) {
  const t = useT();
  const left = useSecondsLeft(fw.pending?.deadline);
  useEffect(() => {
    if (fw.pending && left === 0) {
      const id = window.setTimeout(() => void reload(), 2000);
      return () => window.clearTimeout(id);
    }
  }, [fw.pending, left, reload]);
  if (!fw.pending) return null;
  const p = fw.pending;
  return (
    <div className="sh-keep" role="alertdialog" aria-live="assertive">
      <Icon name="alert" size={18} />
      <div className="sh-keep-text">
        <strong>{t('sh.keep.title')}</strong>
        <span>{t('sh.keep.body', { n: left })}</span>
      </div>
      <div className="sh-keep-actions">
        <button type="button" className="ghost small" onClick={() => void fwCall(shieldApi.undo(p.id), setFw, t('sh.keep.undone'))}>{t('sh.keep.undo')}</button>
        <button type="button" className="small" onClick={() => void fwCall(shieldApi.keep(p.id), setFw, t('sh.keep.kept'))}>{t('sh.keep.keep')}</button>
      </div>
    </div>
  );
}

function Overview({ fw, dns, stats, setDns, go }: {
  fw: FwState;
  dns: DnsStatus;
  stats: DnsStats;
  setDns: (s: DnsStatus) => void;
  go: (t: Tab) => void;
}) {
  const t = useT();
  const paused = useSecondsLeft(dns.paused_until);
  const fwOn = fw.supported && fw.installed && fw.active;
  const dnsOn = dns.settings.enabled && dns.running;
  const level = (fwOn ? 1 : 0) + (dnsOn ? 1 : 0);
  const pause = async (m: number) => {
    const r = await shieldApi.pause(m);
    if (!r.ok) return toast('error', t('sh.dns.failed'), r.error);
    setDns(r.data);
  };
  return (
    <>
      <section className={`sh-hero lvl-${level}`}>
        <div className="sh-hero-badge"><Icon name="shield" size={40} /></div>
        <div className="sh-hero-text">
          <span className="sh-eyebrow">{t('sh.title')}</span>
          <h2>{t(`sh.hero.${level}`)}</h2>
          <p className="muted">{t(`sh.hero.${level}.sub`)}</p>
          <div className="chip-row">
            <span className={`chip ${fwOn ? 'good' : 'bad'}`}>{t(fwOn ? 'sh.fw.on' : 'sh.fw.off')}</span>
            <span className={`chip ${dnsOn ? (paused ? 'warn' : 'good') : 'bad'}`}>{paused ? t('sh.dns.pausedFor', { n: Math.ceil(paused / 60) }) : t(dnsOn ? 'sh.dns.on' : 'sh.dns.off')}</span>
          </div>
        </div>
      </section>

      <div className="sh-grid">
        <Card title={t('sh.ov.firewall')} icon="lock" actions={<button type="button" className="ghost small" onClick={() => go('firewall')}>{t('sh.open')}</button>}>
          {!fw.supported ? (
            <p className="muted">{fw.reason ?? t('sh.fw.unsupported')}</p>
          ) : !fw.installed ? (
            <p className="muted">{t('sh.fw.notInstalled')}</p>
          ) : (
            <div className="sh-stats">
              <Stat label={t('sh.ov.rules')} value={fmtNum(fw.rules.filter((r) => !r.v6).length)} />
              <Stat label={t('sh.ov.incoming')} value={t(`sh.policy.${fw.incoming}`)} tone={fw.incoming === 'allow' ? 'bad' : 'good'} />
              <Stat label={t('sh.ov.outgoing')} value={t(`sh.policy.${fw.outgoing}`)} />
            </div>
          )}
          {fw.supported && fw.installed && !fw.active && (
            <button type="button" onClick={() => go('firewall')}>{t('sh.fw.turnOnCta')}</button>
          )}
        </Card>

        <Card title={t('sh.ov.dns')} icon="globe" actions={<button type="button" className="ghost small" onClick={() => go('dns')}>{t('sh.open')}</button>}>
          <div className="sh-stats">
            <Stat label={t('sh.ov.queriesToday')} value={fmtNum(stats.today)} />
            <Stat label={t('sh.ov.blockedToday')} value={fmtNum(stats.blocked_today)} tone="accent" />
            <Stat label={t('sh.ov.blockedPct')} value={pct(stats.blocked_today, stats.today)} tone="accent" />
            <Stat label={t('sh.ov.domains')} value={fmtNum(dns.domains)} />
          </div>
          {dnsOn ? (
            <div className="btn-group">
              {paused ? (
                <button type="button" className="small" onClick={() => void pause(0)}><Icon name="play" size={13} />{t('sh.dns.resume')}</button>
              ) : (
                [5, 30, 60].map((m) => (
                  <button key={m} type="button" className="ghost small" onClick={() => void pause(m)}><Icon name="pause" size={13} />{t('sh.dns.pauseN', { n: m })}</button>
                ))
              )}
            </div>
          ) : (
            <button type="button" onClick={() => go('dns')}>{t('sh.dns.turnOnCta')}</button>
          )}
        </Card>
      </div>

      <Card title={t('sh.ov.live')} icon="monitor" hint={t('sh.ov.liveHint')}>
        <QueryBars points={stats.last_hour} label={t('sh.ov.live')} />
        <div className="sh-legend">
          <span><i className="sh-dot total" />{t('sh.ov.legendTotal')}</span>
          <span><i className="sh-dot blocked" />{t('sh.ov.legendBlocked')}</span>
        </div>
      </Card>

      {stats.log_enabled && stats.top_blocked.length > 0 && (
        <Card title={t('sh.ov.topBlocked')} icon="alert">
          <ol className="sh-top">
            {stats.top_blocked.slice(0, 5).map((c) => (
              <li key={c.name}><span className="ellipsis">{c.name}</span><span className="muted">{fmtNum(c.count)}</span></li>
            ))}
          </ol>
        </Card>
      )}
      {fwOn && fw.pending == null && fw.incoming === 'allow' && (
        <p className="sh-note"><Icon name="info" size={14} />{t('sh.ov.allowWarn')}</p>
      )}
    </>
  );
}
