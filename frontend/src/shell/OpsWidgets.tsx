// Phase 4 widgets: a holographic container card grid and the Quick Script
// Launcher. Both are admin tools (they act on the host).
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import { scriptsApi, type Script } from '../api/scripts';
import type { Container, DockerEvent } from '../api/types';
import { openApp } from '../apps/meta';
import { Icon } from '../components/Icon';
import { usePoll, useTopic } from '../lib/hooks';
import { runStatus, useScriptRun } from '../lib/scriptRun';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

// ---------- container cards ----------

export function ContainerGridWidget() {
  const [list, setList] = useState<Container[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const reload = useRef<number | undefined>(undefined);

  const load = useCallback(async () => {
    const r = await api<Container[]>('/api/v1/containers');
    if (r.ok) {
      setError(null);
      // Running first, then by name.
      setList(r.data.sort((a, b) => Number(b.state === 'running') - Number(a.state === 'running') || a.name.localeCompare(b.name)));
    } else setError(r.status === 503 || r.status === 0 ? 'Docker is not running.' : (r.error ?? 'Could not load containers'));
  }, []);

  useEffect(() => {
    void load();
    return () => window.clearTimeout(reload.current);
  }, [load]);
  usePoll(load, 15_000);

  useTopic<DockerEvent>('docker.events', (ev) => {
    if (ev.type !== 'container') return;
    window.clearTimeout(reload.current);
    reload.current = window.setTimeout(() => void load(), 300);
  });

  const act = async (c: Container, action: 'start' | 'restart') => {
    if (action === 'restart') {
      const ok = await confirmDialog({ title: `Restart ${c.name}?`, message: 'Anything connected to it is briefly interrupted.', confirmLabel: 'Restart' });
      if (!ok) return;
    }
    setBusy(c.id);
    const r = await api(`/api/v1/containers/${encodeURIComponent(c.id)}/${action}`, { method: 'POST' });
    setBusy(null);
    if (!r.ok) toast('error', `Could not ${action} ${c.name}`, r.error);
    void load();
  };

  const running = list?.filter((c) => c.state === 'running').length ?? 0;

  return (
    <div className="widget glass holo cgrid-widget">
      <div className="widget-head">
        <button type="button" className="link-head" onClick={() => openApp('containers')}>
          <Icon name="containers" size={15} /> Containers
        </button>
        {list && (
          <span className="holo-stat">
            {running}/{list.length}
          </span>
        )}
      </div>
      {error ? (
        <p className="muted small">{error}</p>
      ) : !list ? (
        <p className="muted small">Loading…</p>
      ) : list.length === 0 ? (
        <p className="muted small">No containers yet.</p>
      ) : (
        <div className="cgrid">
          {list.map((c) => {
            const up = c.state === 'running';
            return (
              <div key={c.id} className={`ccard ${up ? 'up' : 'down'}`}>
                <div className="ccard-top">
                  <i className="ccard-pulse" />
                  <b className="ccard-name" title={c.name}>
                    {c.name}
                  </b>
                </div>
                <div className="ccard-image" title={c.image}>
                  {c.image}
                </div>
                <div className="ccard-status">{up ? c.status : c.status || c.state}</div>
                <div className="ccard-actions">
                  {up ? (
                    <button type="button" title={c.system ? 'System containers are protected' : 'Restart'} disabled={busy === c.id || c.system} onClick={() => void act(c, 'restart')}>
                      <Icon name="restart" size={13} />
                    </button>
                  ) : (
                    <button type="button" title="Start" disabled={busy === c.id} onClick={() => void act(c, 'start')}>
                      <Icon name="play" size={13} />
                    </button>
                  )}
                  <button
                    type="button"
                    title="Shell"
                    disabled={!up}
                    onClick={() => openApp('terminal', { title: `Terminal — ${c.name}`, props: { target: `container:${c.id}`, label: c.name }, key: `terminal:${c.id}` })}
                  >
                    <Icon name="terminal" size={13} />
                  </button>
                  <button type="button" title="Logs" onClick={() => openApp('logs', { title: `Logs — ${c.name}`, props: { id: c.id, name: c.name }, key: `logs:${c.id}` })}>
                    <Icon name="logs" size={13} />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ---------- quick script launcher ----------

export function ScriptLauncherWidget() {
  const [scripts, setScripts] = useState<Script[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const r = await scriptsApi.list();
    if (r.ok) {
      setScripts(r.data.scripts);
      setError(r.data.host.enabled ? null : 'Running scripts is turned off on this server.');
    } else setError(r.error ?? 'Could not load scripts');
  }, []);
  const { run, start, stop, clear } = useScriptRun(() => void load());

  useEffect(() => {
    void load();
  }, [load]);

  const out = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (out.current) out.current.scrollTop = out.current.scrollHeight;
  }, [run?.output]);

  const status = run ? runStatus(run) : null;

  return (
    <div className="widget glass holo scripts-widget">
      <div className="widget-head">
        <button type="button" className="link-head" onClick={() => openApp('scripts')}>
          <Icon name="fileCode" size={15} /> Quick scripts
        </button>
        <button type="button" className="ghost icon-btn" title="Manage scripts" onClick={() => openApp('scripts')}>
          <Icon name="edit" size={13} />
        </button>
      </div>
      {error && <p className="muted small">{error}</p>}
      {!error && !scripts && <p className="muted small">Loading…</p>}
      {!error && scripts?.length === 0 && (
        <button type="button" className="ghost small" onClick={() => openApp('scripts')}>
          <Icon name="plus" size={13} /> Add your first script
        </button>
      )}
      {!error && !!scripts?.length && (
        <div className="script-chips">
          {scripts.map((s) => (
            <button
              key={s.id}
              type="button"
              className={`script-chip ${run?.script_id === s.id && run.running ? 'running' : ''}`}
              title={s.description || s.name}
              disabled={!!run?.running}
              onClick={() => void start(s)}
            >
              <Icon name="play" size={11} />
              <span>{s.name}</span>
              {s.last_exit !== undefined && s.last_exit !== 0 && <i className="script-bad" title={`Last run failed (exit ${s.last_exit})`} />}
            </button>
          ))}
        </div>
      )}
      {run && status && (
        <div className="script-out">
          <div className="script-out-head">
            <b className="ellipsis">{run.name}</b>
            <span className={`chip tiny ${status.tone}`}>{status.label}</span>
            <span className="spacer" />
            {run.running ? (
              <button type="button" className="ghost small" onClick={() => void stop()}>
                <Icon name="stop" size={12} /> Stop
              </button>
            ) : (
              <button type="button" className="ghost icon-btn" title="Close output" onClick={clear}>
                <Icon name="close" size={12} />
              </button>
            )}
          </div>
          <pre ref={out}>{run.output || (run.running ? '' : '(no output)')}</pre>
        </div>
      )}
    </div>
  );
}
