import { Fragment, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api } from '../api/client';
import type { Container, ContainerDetail, ContainerStats, LogLine } from '../api/types';
import { dockerApi, type ContainerAction, type ExecResult } from '../api/docker';
import { Sparkline } from '../components/Charts';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo, fmtBytes, fmtPct, fmtRate } from '../lib/format';
import { useTopic } from '../lib/hooks';
import { levelShown, logLevel, type Level } from '../lib/containers';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';
import { TypeConfirm } from './StorageParts';
import { WebAddressField } from './WebAddress';
import { ContainerFiles } from './ContainerFiles';
import { Badges, LocalLink, PortLinks, containerLink, isRunning, openTerminal, stateTone, uptime } from './ContainerBits';

// One container's page: usage graphs, details, live logs, a command line,
// its files, and the actions (start/stop/…, edit, update image, remove).

/** Recent samples per container, for the graphs. */
export interface History {
  cpu: number[];
  mem: number[];
  net: number[];
  disk: number[];
}

type PageTab = 'overview' | 'details' | 'logs' | 'command' | 'files';

export function ContainerPage({ c, stats, hist, size, busy, onBack, onAct, onEdit, onChanged, onGone, onQuiet }: {
  c: Container;
  stats?: ContainerStats;
  hist?: History;
  size?: { rw: number; rootfs: number };
  busy: boolean;
  onBack: () => void;
  onAct: (c: Container, a: ContainerAction) => void;
  onEdit: () => void;
  onChanged: () => Promise<void>;
  onGone: () => void;
  onQuiet: (name: string) => void;
}) {
  const t = useT();
  const [tab, setTab] = useState<PageTab>('overview');
  const [detail, setDetail] = useState<ContainerDetail | null>(null);
  const [updating, setUpdating] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [withVolumes, setWithVolumes] = useState(false);

  const loadDetail = useCallback(async () => {
    const r = await api<ContainerDetail>(`/api/v1/containers/${encodeURIComponent(c.id)}`);
    if (r.ok) setDetail(r.data);
  }, [c.id]);
  useEffect(() => {
    void loadDetail();
  }, [loadDetail, c.state]);

  const app = c.labels?.['nocapos.app'];
  const link = containerLink({ web_url: detail?.web_url ?? c.web_url, ports: c.ports });
  const locked = c.system;
  const running = isRunning(c);
  const removable = !locked && !app && !c.project;

  const update = async () => {
    const ok = await confirmDialog({ title: tr('ctr.updateTitle', { name: c.name }), message: tr('ctr.updateMsg', { image: c.image }), confirmLabel: tr('ctr.update') });
    if (!ok) return;
    setUpdating(true);
    onQuiet(c.name);
    const r = await dockerApi.update(c.id);
    setUpdating(false);
    if (!r.ok) return toast('error', tr('ctr.updateFail', { name: c.name }), r.error);
    if (!r.data.updated) toast('success', tr('ctr.upToDate', { name: c.name }));
    else toast(r.data.warning ? 'error' : 'success', tr('ctr.updated', { name: c.name }), r.data.warning);
    if (r.data.updated && r.data.id !== c.id) onGone();
    else void onChanged();
  };

  const actions: { a: ContainerAction; icon: IconName; hidden: boolean; locked?: boolean; danger?: boolean }[] = [
    { a: 'start', icon: 'play', hidden: running || c.state === 'paused' },
    { a: 'unpause', icon: 'play', hidden: c.state !== 'paused' },
    { a: 'restart', icon: 'restart', hidden: !running, locked },
    { a: 'pause', icon: 'pause', hidden: !running, locked },
    { a: 'stop', icon: 'stop', hidden: !running && c.state !== 'paused', locked },
    { a: 'kill', icon: 'alert', hidden: !running && c.state !== 'paused', locked, danger: true },
  ];

  return (
    <div className="ct-page">
      <button type="button" className="ghost small pill ct-back" onClick={onBack}>
        <Icon name="chevronLeft" size={14} /> {t('ctr.allContainers')}
      </button>
      <header className="ct-page-head">
        <span className={`ct-hero ${stateTone(c.state)}`}>
          <Icon name="containers" size={26} />
        </span>
        <div className="ct-page-title">
          <h2 className="ellipsis">{c.name}</h2>
          <span className="mono small muted ellipsis">{c.image}</span>
          <span className="ct-badges">
            <span className={`chip tiny ${stateTone(c.state)}`}>{c.state}</span>
            <Badges c={c} />
            {c.project && <span className="chip tiny" title={t('ctr.projectHint')}><Icon name="layers" size={11} /> {c.project}</span>}
            {detail?.privileged && <span className="chip tiny warn">{t('ctr.privileged')}</span>}
            {locked && <span className="chip tiny" title={t('ctr.protected')}><Icon name="shield" size={11} /> {t('ctr.protectedChip')}</span>}
          </span>
        </div>
        <div className="ct-page-actions">
          {link && running && (
            <a className="button solid" href={link} target="_blank" rel="noopener noreferrer" title={link}>
              <Icon name="external" size={14} /> {t('ctr.open')}
            </a>
          )}
          {running && (
            <button type="button" className="ghost" onClick={() => openTerminal(c)}>
              <Icon name="terminal" size={14} /> {t('ctr.terminal')}
            </button>
          )}
        </div>
      </header>

      <div className="ct-actionbar">
        {actions.filter((x) => !x.hidden).map((x) => (
          <button key={x.a} type="button" className={`ghost small ${x.danger ? 'danger' : ''}`} disabled={busy || x.locked} title={x.locked ? t('ctr.protected') : undefined} onClick={() => onAct(c, x.a)}>
            <Icon name={x.icon} size={14} /> {t(`ctr.act.${x.a}`)}
          </button>
        ))}
        <span className="spacer" />
        {!locked && (
          <button type="button" className="ghost small" disabled={!!c.project} title={c.project ? t('ctr.editStack', { name: c.project }) : undefined} onClick={onEdit}>
            <Icon name="pencil" size={14} /> {t('ctr.edit')}
          </button>
        )}
        {!locked && (
          <button type="button" className="ghost small" disabled={updating || !!c.project} title={c.project ? t('ctr.updateStack') : t('ctr.updateHint')} onClick={() => void update()}>
            <Icon name="download" size={14} /> {updating ? t('ctr.updating') : t('ctr.update')}
          </button>
        )}
        {removable && (
          <button type="button" className="ghost small danger" onClick={() => setRemoving(true)}>
            <Icon name="trash" size={14} /> {t('ctr.act.remove')}
          </button>
        )}
      </div>

      <div className="ct-tabs" role="tablist">
        {(['overview', 'details', 'logs', 'command', 'files'] as const).map((x) => (
          <button key={x} type="button" role="tab" aria-selected={tab === x} className={tab === x ? 'on' : ''} onClick={() => setTab(x)}>
            {t(`ctr.tab.${x}`)}
          </button>
        ))}
      </div>

      {tab === 'overview' && <Overview c={c} s={stats} hist={hist} size={size} detail={detail} onChanged={() => { void loadDetail(); void onChanged(); }} />}
      {tab === 'details' && <Details c={c} detail={detail} />}
      {tab === 'logs' && <ContainerLogs id={c.id} />}
      {tab === 'command' && <CommandLine c={c} />}
      {tab === 'files' && <ContainerFiles c={c} />}

      {removing && (
        <TypeConfirm
          title={t('ctr.removeTitle', { name: c.name })}
          word={c.name}
          action={withVolumes ? t('ctr.removeWithVolumes') : t('ctr.act.remove')}
          message={
            <>
              <b>{t('ctr.removeMsg')}</b>
              <p>{t('ctr.removeFolders')}</p>
              <label className="ct-check">
                <input type="checkbox" checked={withVolumes} onChange={(e) => setWithVolumes(e.target.checked)} />
                <span>{t('ctr.removeVolumesToo')}</span>
              </label>
            </>
          }
          onClose={() => setRemoving(false)}
          onConfirm={async () => {
            onQuiet(c.name);
            const r = withVolumes ? await dockerApi.removeWithVolumes(c.id) : await dockerApi.remove(c.id);
            if (!r.ok) return r.error ?? tr('ctr.removeFail');
            const vols = withVolumes && r.data && typeof r.data === 'object' && 'volumes' in r.data ? r.data.volumes : [];
            toast('success', tr('ctr.removed', { name: c.name }), vols.length ? tr('ctr.removedVolumes', { list: vols.join(', ') }) : undefined);
            setRemoving(false);
            onGone();
            return null;
          }}
        />
      )}
    </div>
  );
}

function Tile({ icon, label, value, sub, children }: { icon: IconName; label: string; value: string; sub?: string; children?: ReactNode }) {
  return (
    <div className="ucard ct-tile">
      <span className="ct-tile-head">
        <Icon name={icon} size={14} /> {label}
      </span>
      <b className="ct-tile-value">{value}</b>
      {sub && <span className="ct-sub">{sub}</span>}
      {children}
    </div>
  );
}

function Overview({ c, s, hist, size, detail, onChanged }: {
  c: Container; s?: ContainerStats; hist?: History; size?: { rw: number; rootfs: number }; detail: ContainerDetail | null; onChanged: () => void;
}) {
  const t = useT();
  const running = isRunning(c);
  const up = uptime(c);
  const locals = detail?.local_addresses ?? c.local_addresses ?? [];
  return (
    <div className="ct-overview">
      {running && (
        <div className="ct-tiles">
          <Tile icon="cpu" label={t('ctr.cpu')} value={s ? fmtPct(s.cpu_percent, 1) : '–'} sub={s ? t('ctr.processes', { n: s.pids }) : undefined}>
            <Sparkline values={hist?.cpu ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="memory" label={t('ctr.memory')} value={s ? fmtBytes(s.mem_usage) : '–'} sub={s ? t('ctr.memOf', { limit: fmtBytes(s.mem_limit), pct: fmtPct(s.mem_percent) }) : undefined}>
            <Sparkline values={hist?.mem ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="network" label={t('ctr.network')} value={s ? `↓ ${fmtRate(s.net_rx_rate)}` : '–'} sub={s ? `↑ ${fmtRate(s.net_tx_rate)}` : undefined}>
            <Sparkline values={hist?.net ?? []} height={40} className="accent" />
          </Tile>
          <Tile icon="disk" label={t('ctr.disk')} value={s ? `R ${fmtRate(s.block_read_rate ?? 0)}` : '–'} sub={s ? `W ${fmtRate(s.block_write_rate ?? 0)}` : undefined}>
            <Sparkline values={hist?.disk ?? []} height={40} className="accent" />
          </Tile>
        </div>
      )}
      <div className="ct-two">
        <div className="ucard pad">
          <span className="section-label">{t('ctr.status')}</span>
          <dl className="kv small">
            <dt>{t('ctr.status')}</dt>
            <dd>{c.status}</dd>
            {up && (
              <>
                <dt>{t('ctr.uptime')}</dt>
                <dd>{up}</dd>
              </>
            )}
            <dt>{t('ctr.created')}</dt>
            <dd>{fmtAgo(c.created)}</dd>
            {detail && (
              <>
                <dt>{t('ctr.restarts')}</dt>
                <dd>{detail.restart_count}</dd>
                {!running && detail.finished_at && !detail.finished_at.startsWith('0001') && (
                  <>
                    <dt>{t('ctr.exitedAt')}</dt>
                    <dd>
                      {t('ctr.exitedWith', { ago: fmtAgo(detail.finished_at), code: detail.exit_code })}
                      {detail.oom_killed && <span className="chip tiny bad">{t('ctr.oom')}</span>}
                    </dd>
                  </>
                )}
              </>
            )}
            <dt>{t('ctr.size')}</dt>
            <dd>{size ? t('ctr.sizeValue', { rw: fmtBytes(size.rw), total: fmtBytes(size.rootfs) }) : '–'}</dd>
            <dt>{t('ctr.ports')}</dt>
            <dd>
              <PortLinks c={c} max={12} />
            </dd>
            <dt>{t('ctr.localAddr')}</dt>
            <dd className="ct-locals">
              {locals.length ? locals.map((u) => <LocalLink key={u} url={u} />) : <span className="muted">{t('ctr.localNone')}</span>}
            </dd>
          </dl>
        </div>
        <div className="ucard pad">
          <WebAddressField
            value={detail?.web_address ?? c.web_address ?? ''}
            automatic={(detail?.web_address ? null : containerLink({ web_url: detail?.web_url ?? c.web_url, ports: c.ports })) ?? null}
            onSave={(u) => dockerApi.setWebAddress(c.id, u)}
            onSaved={onChanged}
          />
          <p className="muted small ct-hint">{t('ctr.webHint')}</p>
        </div>
      </div>
    </div>
  );
}

const joinArgs = (a?: string[] | null) => (a ?? []).map((w) => (/\s|^$/.test(w) ? JSON.stringify(w) : w)).join(' ');

function Details({ c, detail }: { c: Container; detail: ContainerDetail | null }) {
  const t = useT();
  const [show, setShow] = useState(false);
  if (!detail) return <p className="muted">{t('ctr.loading')}</p>;
  const labels = Object.entries(detail.labels ?? c.labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const none = <span className="muted">{t('ctr.none2')}</span>;
  return (
    <div className="ct-inspect">
      <section className="ucard pad">
        <span className="section-label">{t('ctr.run')}</span>
        <dl className="kv small">
          <dt>{t('ctr.image')}</dt>
          <dd className="mono">{detail.image}</dd>
          {detail.image_id && (
            <>
              <dt>{t('ctr.imageId')}</dt>
              <dd className="mono">{detail.image_id.replace('sha256:', '').slice(0, 24)}</dd>
            </>
          )}
          <dt>{t('ctr.command')}</dt>
          <dd className="mono">{joinArgs(detail.entrypoint) + (detail.entrypoint?.length && detail.cmd?.length ? ' ' : '') + joinArgs(detail.cmd) || none}</dd>
          {detail.working_dir && (
            <>
              <dt>{t('ctr.workdir')}</dt>
              <dd className="mono">{detail.working_dir}</dd>
            </>
          )}
          {detail.user && (
            <>
              <dt>{t('ctr.user')}</dt>
              <dd className="mono">{detail.user}</dd>
            </>
          )}
          <dt>{t('ctr.restartPolicy')}</dt>
          <dd>{detail.restart_policy || 'no'}</dd>
          <dt>{t('ctr.limits')}</dt>
          <dd>
            {t('ctr.limitsValue', {
              mem: detail.memory_mb ? `${detail.memory_mb} MB` : t('ctr.noLimit'),
              cpu: detail.cpus ? String(detail.cpus) : t('ctr.noLimit'),
              pids: detail.pids_limit ? String(detail.pids_limit) : t('ctr.noLimit'),
            })}
          </dd>
          <dt>{t('ctr.security')}</dt>
          <dd className="ct-chips">
            {detail.privileged && <span className="chip tiny warn">{t('ctr.privileged')}</span>}
            {detail.read_only && <span className="chip tiny good">{t('ctr.readOnlyFs')}</span>}
            {(detail.cap_add ?? []).map((x) => <span key={x} className="chip tiny warn">+{x}</span>)}
            {(detail.cap_drop ?? []).map((x) => <span key={x} className="chip tiny">−{x}</span>)}
            {!detail.privileged && !detail.read_only && !detail.cap_add?.length && !detail.cap_drop?.length && <span className="muted">{t('ctr.defaults')}</span>}
          </dd>
          {!!detail.devices?.length && (
            <>
              <dt>{t('ctr.devices')}</dt>
              <dd className="mono">{detail.devices.join(', ')}</dd>
            </>
          )}
          {detail.runtime && (
            <>
              <dt>{t('ctr.runtime')}</dt>
              <dd>{detail.runtime}</dd>
            </>
          )}
        </dl>
      </section>

      <section className="ucard pad">
        <span className="section-label">{t('ctr.env')}</span>
        <p className="muted small">{t('ctr.envHint')}</p>
        {detail.env?.length ? (
          <>
            <dl className="kv small ct-envlist">
              {detail.env.map((e) => (
                <Fragment key={e.key}>
                  <dt className="mono">{e.key}</dt>
                  <dd className="mono">{e.secret ? <span className="muted">••••••</span> : show || e.value.length < 80 ? e.value : `${e.value.slice(0, 80)}…`}</dd>
                </Fragment>
              ))}
            </dl>
            {detail.env.some((e) => !e.secret && e.value.length >= 80) && (
              <button type="button" className="ghost small" onClick={() => setShow(!show)}>{show ? t('ctr.showLess') : t('ctr.showAll')}</button>
            )}
          </>
        ) : (
          none
        )}
      </section>

      <section className="ucard pad">
        <span className="section-label">{t('ctr.storage')}</span>
        {detail.mounts.length ? (
          <div className="ct-table-wrap">
            <table className="table ct-mini">
              <thead>
                <tr>
                  <th>{t('ctr.type')}</th>
                  <th>{t('ctr.source')}</th>
                  <th>{t('ctr.inside')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {detail.mounts.map((m, i) => (
                  <tr key={i}>
                    <td>{m.type}</td>
                    <td className="mono ellipsis" title={m.source}>{m.type === 'volume' ? m.name || m.source : m.source}</td>
                    <td className="mono">{m.destination}</td>
                    <td>{!m.rw && <span className="chip tiny">{t('ctr.readOnly')}</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted small">{t('ctr.noMounts')}</p>
        )}
      </section>

      <section className="ucard pad">
        <span className="section-label">{t('ctr.networks')}</span>
        {detail.networks.length ? (
          <div className="ct-table-wrap">
            <table className="table ct-mini">
              <thead>
                <tr>
                  <th>{t('ctr.network')}</th>
                  <th>{t('ctr.driver')}</th>
                  <th>IP</th>
                  <th>{t('ctr.gateway')}</th>
                </tr>
              </thead>
              <tbody>
                {detail.networks.map((n) => (
                  <tr key={n.name}>
                    <td>{n.name}</td>
                    <td>{n.driver || '–'}</td>
                    <td className="mono">{n.ip || '–'}</td>
                    <td className="mono">{n.gateway || '–'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="muted small">{detail.network_mode === 'host' ? t('ctr.hostNet') : t('ctr.noNets')}</p>
        )}
      </section>

      {labels.length > 0 && (
        <details className="ucard pad ct-labels">
          <summary className="section-label">{t('ctr.labels', { n: labels.length })}</summary>
          <dl className="kv small">
            {labels.map(([k, v]) => (
              <Fragment key={k}>
                <dt className="mono">{k}</dt>
                <dd className="mono">{v}</dd>
              </Fragment>
            ))}
          </dl>
        </details>
      )}
    </div>
  );
}

// ---------------- logs ----------------

const MAX_LINES = 5000;
type LevelFilter = 'all' | 'error' | 'warn' | 'info';
interface Line extends LogLine {
  level: Level;
}

export function ContainerLogs({ id }: { id: string }) {
  const t = useT();
  const [lines, setLines] = useState<Line[]>([]);
  const [ended, setEnded] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [timestamps, setTimestamps] = useState(false);
  const [find, setFind] = useState('');
  const [level, setLevel] = useState<LevelFilter>('all');
  const box = useRef<HTMLDivElement>(null);
  const held = useRef<Line[]>([]);
  const pausedRef = useRef(false);
  pausedRef.current = paused;

  useTopic<LogLine[]>(
    `container.logs/${id}`,
    (batch) => {
      const add = batch.map((l) => ({ ...l, level: logLevel(l.text, l.stream) }));
      if (pausedRef.current) held.current = [...held.current, ...add].slice(-MAX_LINES);
      else setLines((cur) => [...cur, ...add].slice(-MAX_LINES));
    },
    (err) => setEnded(err ?? tr('ctr.logsEnded')),
  );

  const resume = () => {
    setLines((cur) => [...cur, ...held.current].slice(-MAX_LINES));
    held.current = [];
    setPaused(false);
  };

  const q = find.trim().toLowerCase();
  const shown = useMemo(() => lines.filter((l) => levelShown(l.level, level) && (!q || l.text.toLowerCase().includes(q))), [lines, level, q]);
  const counts = useMemo(() => ({ error: lines.filter((l) => l.level === 'error').length, warn: lines.filter((l) => l.level === 'warn').length }), [lines]);

  useLayoutEffect(() => {
    if (!paused && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [shown, paused]);

  const mark = (text: string) => {
    if (!q) return text;
    const out: ReactNode[] = [];
    const low = text.toLowerCase();
    let i = 0;
    for (let j = low.indexOf(q); j >= 0 && out.length < 40; j = low.indexOf(q, i)) {
      out.push(text.slice(i, j), <mark key={j}>{text.slice(j, j + q.length)}</mark>);
      i = j + q.length;
    }
    out.push(text.slice(i));
    return out;
  };

  return (
    <div className="ucard ct-logs">
      <div className="ct-logbar">
        <label className="search compact">
          <Icon name="search" size={15} />
          <input placeholder={t('ctr.logs.find')} aria-label={t('ctr.logs.find')} value={find} onChange={(e) => setFind(e.target.value)} />
        </label>
        <select className="ct-select" value={level} aria-label={t('ctr.logs.level')} onChange={(e) => setLevel(e.target.value as LevelFilter)}>
          <option value="all">{t('ctr.logs.all')}</option>
          <option value="error">{t('ctr.logs.errors', { n: counts.error })}</option>
          <option value="warn">{t('ctr.logs.warnings', { n: counts.warn + counts.error })}</option>
          <option value="info">{t('ctr.logs.info')}</option>
        </select>
        <button type="button" className={`ghost small ${paused ? 'on' : ''}`} onClick={() => (paused ? resume() : setPaused(true))} aria-pressed={paused}>
          <Icon name={paused ? 'play' : 'pause'} size={13} /> {paused ? t('ctr.logs.follow', { n: held.current.length }) : t('ctr.logs.pause')}
        </button>
        <label className="toggle small">
          <input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} /> {t('ctr.logs.time')}
        </label>
        <span className="spacer" />
        <span className="muted small">{t('ctr.logs.count', { n: shown.length })}</span>
        <button type="button" className="ghost icon-btn" title={t('ctr.logs.clear')} aria-label={t('ctr.logs.clear')} onClick={() => setLines([])}>
          <Icon name="trash" size={15} />
        </button>
      </div>
      <div ref={box} className="log-view mono" onWheel={(e) => e.deltaY < 0 && !paused && setPaused(true)}>
        {shown.map((l, i) => (
          <div key={i} className={`log-line ${l.stream} lvl-${l.level}`}>
            {timestamps && l.time && <span className="log-ts">{new Date(l.time).toLocaleTimeString()} </span>}
            {mark(l.text)}
          </div>
        ))}
        {ended && <div className="log-end">— {ended} —</div>}
        {!shown.length && !ended && <div className="muted">{lines.length ? t('ctr.logs.noMatch') : t('ctr.logs.waiting')}</div>}
      </div>
    </div>
  );
}

// ---------------- command line ----------------

interface Run extends ExecResult {
  command: string;
  error?: string;
}

function CommandLine({ c }: { c: Container }) {
  const t = useT();
  const [cmd, setCmd] = useState('');
  const [runs, setRuns] = useState<Run[]>([]);
  const [busy, setBusy] = useState(false);
  const [hist, setHist] = useState(-1);
  const running = isRunning(c);

  const run = async () => {
    const command = cmd.trim();
    if (!command || busy) return;
    setBusy(true);
    const r = await dockerApi.exec(c.id, command);
    setBusy(false);
    setHist(-1);
    setRuns((cur) => [...cur.slice(-19), r.ok ? { ...r.data, command } : { command, stdout: '', stderr: '', exit_code: -1, truncated: false, error: r.error }]);
    setCmd('');
  };
  const past = runs.map((r) => r.command);

  return (
    <div className="ct-cmd">
      <div className="ucard pad ct-cmd-head">
        <p className="muted small">{t('ctr.cmd.hint')}</p>
        <button type="button" className="ghost" disabled={!running} onClick={() => openTerminal(c)}>
          <Icon name="terminal" size={14} /> {t('ctr.cmd.openTerminal')}
        </button>
      </div>
      {!running && <p className="muted">{t('ctr.cmd.notRunning')}</p>}
      <div className="ct-cmd-out mono" aria-live="polite">
        {runs.map((r, i) => (
          <div key={i} className="ct-cmd-run">
            <div className="ct-cmd-prompt">$ {r.command}</div>
            {r.error ? <div className="ct-cmd-err">{r.error}</div> : null}
            {r.stdout && <pre>{r.stdout}</pre>}
            {r.stderr && <pre className="ct-cmd-err">{r.stderr}</pre>}
            {!r.error && (
              <div className={`ct-cmd-exit ${r.exit_code === 0 ? 'ok' : 'bad'}`}>
                {t('ctr.cmd.exit', { code: r.exit_code })}
                {r.truncated && ` · ${t('ctr.cmd.truncated')}`}
              </div>
            )}
          </div>
        ))}
      </div>
      <form
        className="ct-cmd-form"
        onSubmit={(e) => {
          e.preventDefault();
          void run();
        }}
      >
        <span className="mono muted">$</span>
        <input
          className="mono"
          value={cmd}
          disabled={!running}
          spellCheck={false}
          autoCapitalize="off"
          placeholder={t('ctr.cmd.placeholder')}
          aria-label={t('ctr.cmd.label')}
          onChange={(e) => setCmd(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'ArrowUp' && past.length) {
              e.preventDefault();
              const i = hist < 0 ? past.length - 1 : Math.max(0, hist - 1);
              setHist(i);
              setCmd(past[i]);
            } else if (e.key === 'ArrowDown' && hist >= 0) {
              e.preventDefault();
              const i = hist + 1;
              setHist(i >= past.length ? -1 : i);
              setCmd(i >= past.length ? '' : past[i]);
            }
          }}
        />
        <button type="submit" disabled={!running || busy || !cmd.trim()}>
          {busy ? <span className="spinner sm" /> : <Icon name="send" size={14} />} {t('ctr.cmd.run')}
        </button>
      </form>
    </div>
  );
}
