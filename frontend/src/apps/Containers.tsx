import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../api/client';
import type { Container, ContainerDetail, ContainerStats, DockerEvent } from '../api/types';
import { Bar, Sparkline } from '../components/Charts';
import { Icon } from '../components/Icon';
import { fmtAgo, fmtBytes, fmtPct, fmtRate } from '../lib/format';
import { usePoll, useTopic } from '../lib/hooks';
import { resubscribe } from '../api/socket';
import { toast } from '../state/toasts';
import { openApp } from './meta';
import { Empty, Stat } from './Monitor';
import { ContainerEditor } from './ContainerEditor';
import { DockerNetworks } from './DockerNetworks';
import { Stacks } from './Stacks';
import { Choice } from './Personalize';
import { dockerApi } from '../api/docker';
import { confirmDialog } from '../state/confirm';

type Action = 'start' | 'stop' | 'restart';

export function Containers() {
  const [list, setList] = useState<Container[] | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [filter, setFilter] = useState('');
  const [showStopped, setShowStopped] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [tab, setTab] = useState<'containers' | 'stacks' | 'networks'>('containers');
  /** The container being edited ('new' = adding one). */
  const [editing, setEditing] = useState<string | null>(null);
  const reloadTimer = useRef<number | undefined>(undefined);
  const quietUntil = useRef(new Map<string, number>());

  const load = useCallback(async () => {
    const r = await api<Container[]>('/api/v1/containers');
    if (!r.ok) {
      setError({ status: r.status, message: r.error ?? 'Request failed' });
      setList(null);
      return;
    }
    setError(null);
    setList(r.data.sort((a, b) => a.name.localeCompare(b.name)));
  }, []);

  useEffect(() => {
    void load();
  }, [load]);
  usePoll(load, 15_000);

  useTopic<DockerEvent>(
    'docker.events',
    (ev) => {
      if (ev.type !== 'container') return;
      window.clearTimeout(reloadTimer.current);
      reloadTimer.current = window.setTimeout(() => void load(), 300);
      // 143 is a normal stop; containers being edited stop on purpose too.
      const quiet = ev.name !== undefined && (quietUntil.current.get(ev.name) ?? 0) > Date.now();
      if (ev.action === 'die' && ev.exit_code && ev.exit_code !== '0' && ev.exit_code !== '143' && !quiet) {
        toast('error', `${ev.name ?? 'Container'} exited`, `Exit code ${ev.exit_code}`);
      }
    },
    // The feed ends while Docker is down; retry so events resume on reconnect.
    () => window.setTimeout(() => resubscribe('docker.events'), 10_000),
  );

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return (list ?? []).filter(
      (c) =>
        (showStopped || c.state === 'running') &&
        (!q || c.name.toLowerCase().includes(q) || c.image.toLowerCase().includes(q) || c.project?.toLowerCase().includes(q)),
    );
  }, [list, filter, showStopped]);

  const act = async (c: Container, action: Action) => {
    setBusy(c.id);
    const r = await api(`/api/v1/containers/${encodeURIComponent(c.id)}/${action}`, { method: 'POST' });
    setBusy(null);
    if (r.ok) toast('success', `${c.name}: ${action} done`);
    else toast('error', `Could not ${action} ${c.name}`, r.error);
    void load();
  };

  if (error) {
    const offline = error.status === 503 || error.status === 0;
    return (
      <Empty
        icon="containers"
        text={
          offline
            ? 'Docker is not running on this machine. Start Docker (or Docker Desktop) — NoCapOS connects automatically.'
            : error.message
        }
      >
        <button type="button" onClick={() => void load()}>
          <Icon name="restart" size={15} /> Retry
        </button>
      </Empty>
    );
  }
  if (!list) return <Empty icon="containers" text="Loading containers…" />;

  const running = list.filter((c) => c.state === 'running').length;
  const current = list.find((c) => c.id === selected) ?? null;

  const remove = async (c: Container) => {
    const ok = await confirmDialog({
      title: `Remove ${c.name}?`,
      message: 'The container is deleted. Its named volumes and folders on the server stay.',
      confirmLabel: 'Remove',
      danger: true,
    });
    if (!ok) return;
    const r = await dockerApi.remove(c.id);
    if (!r.ok) return toast('error', `Could not remove ${c.name}`, r.error);
    setSelected(null);
    void load();
  };

  if (editing) {
    return (
      <div className="containers">
        <ContainerEditor
          containerId={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onApplying={(name) => quietUntil.current.set(name, Date.now() + 120_000)}
          onSaved={() => {
            setEditing(null);
            setSelected(null);
            void load();
          }}
        />
      </div>
    );
  }

  const tabs = (
    <Choice
      value={tab}
      options={[
        { id: 'containers', label: 'Containers' },
        { id: 'stacks', label: 'Stacks' },
        { id: 'networks', label: 'Networks' },
      ]}
      onChange={setTab}
    />
  );
  if (tab === 'networks' || tab === 'stacks') {
    return (
      <div className="containers">
        <div className="toolbar">{tabs}</div>
        <div className="containers-scroll">{tab === 'stacks' ? <Stacks /> : <DockerNetworks />}</div>
      </div>
    );
  }

  return (
    <div className="containers">
      <div className="toolbar">
        {tabs}
        <label className="search compact">
          <Icon name="search" size={15} />
          <input placeholder="Filter by name, image or project" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
        <label className="toggle">
          <input type="checkbox" checked={showStopped} onChange={(e) => setShowStopped(e.target.checked)} /> Show stopped
        </label>
        <span className="spacer" />
        <span className="chip good">{running} running</span>
        <span className="chip">{list.length} total</span>
        <button type="button" className="ghost icon-btn" aria-label="Refresh" onClick={() => void load()}>
          <Icon name="restart" size={15} />
        </button>
        <button type="button" onClick={() => setEditing('new')}>
          <Icon name="plus" size={14} /> Add Container
        </button>
      </div>

      <div className={`containers-body ${current ? 'with-detail' : ''}`}>
        <div className="table-wrap">
          {shown.length === 0 ? (
            <Empty icon="containers" text={list.length ? 'No containers match the filter.' : 'No containers yet. Apps from the App Center will appear here.'} />
          ) : (
            <table className="table hover">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>State</th>
                  <th>Image</th>
                  <th>Ports</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {shown.map((c) => (
                  <tr key={c.id} className={selected === c.id ? 'selected' : ''} onClick={() => setSelected(c.id)}>
                    <td>
                      <b>{c.name}</b>
                      {c.system && <span className="chip tiny">system</span>}
                      {c.project && <div className="muted small">{c.project}</div>}
                    </td>
                    <td>
                      <span className={`chip ${stateClass(c.state)}`}>{c.state}</span>
                      <div className="muted small">{c.status}</div>
                    </td>
                    <td className="ellipsis mono small">{c.image}</td>
                    <td className="small">
                      {c.ports.filter((p) => p.public).map((p) => `${p.public}→${p.private}`).join(', ') || '–'}
                    </td>
                    <td className="row-actions" onClick={(e) => e.stopPropagation()}>
                      <ActionButtons c={c} busy={busy === c.id} onAct={act} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {current && (
          <Detail c={current} onClose={() => setSelected(null)} onAct={act} busy={busy === current.id} onEdit={() => setEditing(current.id)} onRemove={() => void remove(current)} />
        )}
      </div>
    </div>
  );
}

function stateClass(state: string) {
  return state === 'running' ? 'good' : state === 'exited' || state === 'dead' ? 'bad' : 'warn';
}

function ActionButtons({ c, busy, onAct }: { c: Container; busy: boolean; onAct: (c: Container, a: Action) => void }) {
  const locked = c.system;
  return (
    <div className="btn-group">
      {c.state === 'running' ? (
        <>
          <button type="button" className="ghost icon-btn" title="Restart" disabled={busy || locked} onClick={() => onAct(c, 'restart')}>
            <Icon name="restart" size={15} />
          </button>
          <button type="button" className="ghost icon-btn danger" title={locked ? 'System containers are protected' : 'Stop'} disabled={busy || locked} onClick={() => onAct(c, 'stop')}>
            <Icon name="stop" size={15} />
          </button>
        </>
      ) : (
        <button type="button" className="ghost icon-btn" title="Start" disabled={busy} onClick={() => onAct(c, 'start')}>
          <Icon name="play" size={15} />
        </button>
      )}
      <button
        type="button"
        className="ghost icon-btn"
        title="Logs"
        onClick={() => openApp('logs', { title: `Logs — ${c.name}`, props: { id: c.id, name: c.name }, key: `logs:${c.id}` })}
      >
        <Icon name="logs" size={15} />
      </button>
      {c.state === 'running' && (
        <button
          type="button"
          className="ghost icon-btn"
          title="Terminal"
          onClick={() =>
            openApp('terminal', {
              title: `Terminal — ${c.name}`,
              props: { target: `container:${c.id}`, label: c.name },
              key: `terminal:${c.id}`,
            })
          }
        >
          <Icon name="terminal" size={15} />
        </button>
      )}
    </div>
  );
}

function Detail({
  c,
  onClose,
  onAct,
  busy,
  onEdit,
  onRemove,
}: {
  c: Container;
  onClose: () => void;
  onAct: (c: Container, a: Action) => void;
  busy: boolean;
  onEdit: () => void;
  onRemove: () => void;
}) {
  const [detail, setDetail] = useState<ContainerDetail | null>(null);
  const [stats, setStats] = useState<ContainerStats | null>(null);
  const [cpuHist, setCpuHist] = useState<number[]>([]);

  useEffect(() => {
    setDetail(null);
    setStats(null);
    setCpuHist([]);
    void api<ContainerDetail>(`/api/v1/containers/${encodeURIComponent(c.id)}`).then((r) => r.ok && setDetail(r.data));
  }, [c.id, c.state]);

  useTopic<ContainerStats>(c.state === 'running' ? `container.stats/${c.id}` : null, (s) => {
    setStats(s);
    setCpuHist((h) => [...h, s.cpu_percent].slice(-60));
  });

  return (
    <aside className="detail">
      <div className="row-head">
        <b className="ellipsis">{c.name}</b>
        <button type="button" className="ghost icon-btn" aria-label="Close details" onClick={onClose}>
          <Icon name="close" size={14} />
        </button>
      </div>
      <div className="chip-row">
        <span className={`chip ${stateClass(c.state)}`}>{c.state}</span>
        {detail?.health && <span className="chip">{detail.health}</span>}
        {detail?.privileged && <span className="chip warn">privileged</span>}
      </div>
      <ActionButtons c={c} busy={busy} onAct={onAct} />
      {!c.system && (
        <div className="ct-edit">
          <button type="button" className="ghost" disabled={!!c.project} title={c.project ? `Part of the stack ${c.project}` : undefined} onClick={onEdit}>
            <Icon name="pencil" size={14} /> Edit
          </button>
          {!c.labels?.['nocapos.app'] && !c.project && (
            <button type="button" className="ghost danger" onClick={onRemove}>
              <Icon name="trash" size={14} /> Remove
            </button>
          )}
        </div>
      )}

      {stats && (
        <div className="panel tight">
          <div className="row-head small">
            <span>CPU</span>
            <span className="muted">{fmtPct(stats.cpu_percent, 1)}</span>
          </div>
          <Sparkline values={cpuHist} height={40} className="accent" />
          <div className="row-head small">
            <span>Memory</span>
            <span className="muted">
              {fmtBytes(stats.mem_usage)} / {fmtBytes(stats.mem_limit)}
            </span>
          </div>
          <Bar value={stats.mem_percent} />
          <div className="stats-row">
            <Stat label="Net ↓" value={fmtRate(stats.net_rx_rate)} />
            <Stat label="Net ↑" value={fmtRate(stats.net_tx_rate)} />
            <Stat label="PIDs" value={String(stats.pids)} />
          </div>
        </div>
      )}

      <dl className="kv small">
        <dt>Image</dt>
        <dd className="mono">{c.image}</dd>
        <dt>Created</dt>
        <dd>{fmtAgo(c.created)}</dd>
        {detail && (
          <>
            <dt>Restart</dt>
            <dd>{detail.restart_policy || 'no'}</dd>
            <dt>Network</dt>
            <dd>{detail.networks.map((n) => `${n.name}${n.ip ? ` (${n.ip})` : ''}`).join(', ') || detail.network_mode}</dd>
            {detail.restart_count > 0 && (
              <>
                <dt>Restarts</dt>
                <dd>{detail.restart_count}</dd>
              </>
            )}
          </>
        )}
        <dt>Ports</dt>
        <dd>{c.ports.map((p) => (p.public ? `${p.public}→${p.private}/${p.protocol}` : `${p.private}/${p.protocol}`)).join(', ') || '–'}</dd>
      </dl>

      {detail && detail.mounts.length > 0 && (
        <>
          <div className="muted small upper">Volumes</div>
          <ul className="mounts">
            {detail.mounts.map((m, i) => (
              <li key={i} className="small">
                <span className="mono ellipsis">{m.source || m.type}</span>
                <span className="muted"> → </span>
                <span className="mono">{m.destination}</span>
                {!m.rw && <span className="chip tiny">ro</span>}
              </li>
            ))}
          </ul>
        </>
      )}
    </aside>
  );
}
