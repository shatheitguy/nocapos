import { useCallback, useEffect, useRef, useState } from 'react';
import { stacksApi, type ComposeInfo, type StackAction, type StackDetail, type StackOp, type StackSummary } from '../api/stacks';
import type { Container } from '../api/types';
import { Icon } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Choice } from './Personalize';

// Stacks: multi-container apps from a compose file, deployed with Docker's
// own Compose. Stacks started elsewhere show up too (read-only).

const STARTER = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html:ro
`;

const ACTION_LABEL: Record<string, string> = {
  up: 'Deploy',
  redeploy: 'Update & redeploy',
  pull: 'Pull images',
  start: 'Start',
  stop: 'Stop',
  restart: 'Restart',
  down: 'Take down',
};

function counts(cs: Container[]) {
  const running = cs.filter((c) => c.state === 'running').length;
  return { running, total: cs.length };
}

function stateChip(cs: Container[]) {
  const { running, total } = counts(cs);
  if (!total) return <span className="chip">not deployed</span>;
  return <span className={`chip ${running === total ? 'good' : running ? 'warn' : 'bad'}`}>{running === total ? 'running' : running ? 'partly running' : 'stopped'} {running}/{total}</span>;
}

export function Stacks() {
  const [data, setData] = useState<{ compose: ComposeInfo; stacks: StackSummary[] } | null>(null);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState<string | null>(null); // stack name or 'new'
  const [installing, setInstalling] = useState(false);

  const load = useCallback(async () => {
    const r = await stacksApi.list();
    if (!r.ok) return setError(r.error ?? 'Could not load stacks');
    setError('');
    setData(r.data);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  // Follow running tasks.
  const busy = data?.stacks.some((s) => s.op?.running);
  useEffect(() => {
    if (!busy) return;
    const t = window.setInterval(() => void load(), 1500);
    return () => window.clearInterval(t);
  }, [busy, load]);

  const run = async (st: StackSummary, action: StackAction) => {
    const r = await stacksApi.run(st.name, action);
    if (!r.ok) return toast('error', `Could not ${ACTION_LABEL[action].toLowerCase()} ${st.name}`, r.error);
    void load();
  };
  const remove = async (st: StackSummary) => {
    const ok = await confirmDialog({
      title: `Delete the stack “${st.name}”?`,
      message: 'Its containers are removed along with its compose file. Folders on the server stay. Named volumes stay unless you remove them in the next step.',
      confirmLabel: 'Delete',
      danger: true,
    });
    if (!ok) return;
    const volumes =
      st.containers.length > 0 &&
      (await confirmDialog({ title: 'Also delete its named volumes?', message: 'Data kept in the stack’s Docker volumes is erased for good. Choose Keep to leave them.', confirmLabel: 'Delete volumes', cancelLabel: 'Keep', danger: true }));
    const r = await stacksApi.remove(st.name, volumes);
    if (!r.ok) return toast('error', `Could not delete ${st.name}`, r.error);
    toast('success', `Stack ${st.name} deleted`);
    void load();
  };
  const install = async () => {
    setInstalling(true);
    const r = await stacksApi.installCompose();
    setInstalling(false);
    if (!r.ok) return toast('error', 'Could not install Docker Compose', r.error);
    toast('success', 'Docker Compose installed');
    void load();
  };

  if (editing) {
    return (
      <StackEditor
        name={editing === 'new' ? null : editing}
        compose={data?.compose}
        onClose={() => {
          setEditing(null);
          void load();
        }}
      />
    );
  }
  if (error) return <p className="error dn-pad">{error}</p>;
  if (!data) return <span className="spinner" />;
  const { compose, stacks } = data;

  return (
    <div className="dn">
      <div className="dn-head">
        <p className="muted small">
          A stack is an app made of one or more containers, described in a compose file.{' '}
          {compose.installed ? <span className="chip tiny">Compose {compose.version}</span> : null}
        </p>
        <button type="button" disabled={!compose.installed} onClick={() => setEditing('new')}>
          <Icon name="plus" size={14} /> Create Stack
        </button>
      </div>
      {!compose.installed && (
        <div className="ce-note st-install">
          <span>Docker Compose isn't installed on this server, so stacks can't be deployed.</span>
          {compose.can_install ? (
            <button type="button" disabled={installing} onClick={() => void install()}>
              {installing ? <span className="spinner sm" /> : <Icon name="download" size={14} />} {installing ? 'Installing…' : 'Install Compose'}
            </button>
          ) : (
            <span className="muted small">Install the docker-compose plugin with your package manager.</span>
          )}
        </div>
      )}
      {stacks.length === 0 ? (
        <div className="st-empty muted">No stacks yet. Create one from a compose file, or paste one you already use.</div>
      ) : (
        <div className="st-list">
          {stacks.map((st) => (
            <div key={st.name} className="st-card">
              <div className="st-top">
                <Icon name="containers" size={18} />
                <b className="st-name">{st.name}</b>
                {stateChip(st.containers)}
                {!st.managed && <span className="chip tiny" title="This stack was started outside NoCapOS; its compose file isn't here">external</span>}
                <span className="spacer" />
                {st.op?.running ? (
                  <span className="muted small st-op">
                    <span className="spinner sm" /> {ACTION_LABEL[st.op.action] ?? st.op.action}…
                  </span>
                ) : st.op?.error ? (
                  <span className="bad small" title={st.op.output}>
                    {ACTION_LABEL[st.op.action] ?? st.op.action} failed
                  </span>
                ) : st.updated ? (
                  <span className="muted small">edited {fmtAgo(st.updated)}</span>
                ) : null}
              </div>
              {st.containers.length > 0 && (
                <div className="st-services">
                  {st.containers.map((c) => (
                    <span key={c.id} className={`st-svc ${c.state === 'running' ? 'on' : ''}`} title={`${c.image} · ${c.status}`}>
                      <i />
                      {c.labels?.['com.docker.compose.service'] ?? c.name}
                      {[...new Set(c.ports.map((p) => p.public).filter(Boolean))].slice(0, 2).map((port) => (
                        <span key={port} className="muted">
                          :{port}
                        </span>
                      ))}
                    </span>
                  ))}
                </div>
              )}
              {st.managed && (
                <div className="st-actions">
                  <button type="button" className="small" onClick={() => setEditing(st.name)}>
                    <Icon name="edit" size={13} /> Edit
                  </button>
                  {counts(st.containers).running ? (
                    <>
                      <button type="button" className="ghost small" disabled={st.op?.running} onClick={() => void run(st, 'restart')}>
                        <Icon name="restart" size={13} /> Restart
                      </button>
                      <button type="button" className="ghost small" disabled={st.op?.running} onClick={() => void run(st, 'stop')}>
                        <Icon name="stop" size={13} /> Stop
                      </button>
                    </>
                  ) : (
                    <button type="button" className="ghost small" disabled={st.op?.running} onClick={() => void run(st, st.containers.length ? 'start' : 'up')}>
                      <Icon name="play" size={13} /> {st.containers.length ? 'Start' : 'Deploy'}
                    </button>
                  )}
                  <button type="button" className="ghost small" disabled={st.op?.running} title="Download newer images and recreate what changed" onClick={() => void run(st, 'redeploy')}>
                    <Icon name="download" size={13} /> Update
                  </button>
                  <span className="spacer" />
                  <button type="button" className="ghost small danger" disabled={st.op?.running} onClick={() => void remove(st)}>
                    <Icon name="trash" size={13} /> Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function OpOutput({ op }: { op: StackOp }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [op.output]);
  return (
    <div className="st-out">
      <div className="st-out-head">
        {op.running ? <span className="spinner sm" /> : <Icon name={op.error ? 'alert' : 'check'} size={14} />}
        <b>{ACTION_LABEL[op.action] ?? op.action}</b>
        <span className={op.error ? 'bad' : 'muted'}>{op.running ? 'running…' : op.error ? op.error : 'done'}</span>
      </div>
      <pre ref={ref} className="log-view st-log">
        {op.output || ' '}
      </pre>
    </div>
  );
}

function StackEditor({ name, compose, onClose }: { name: string | null; compose?: ComposeInfo; onClose: () => void }) {
  const [stackName, setStackName] = useState(name ?? '');
  const [detail, setDetail] = useState<StackDetail | null>(null);
  const [yaml, setYaml] = useState(name ? '' : STARTER);
  const [env, setEnv] = useState('');
  const [view, setView] = useState<'compose' | 'env' | 'logs'>('compose');
  const [logs, setLogs] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(name);
  const dirty = !detail || yaml !== detail.compose || env !== detail.env;

  const load = useCallback(async (n: string, files: boolean) => {
    const r = await stacksApi.get(n);
    if (!r.ok) return setError(r.error ?? 'Could not load the stack');
    setDetail(r.data);
    if (files) {
      setYaml(r.data.compose);
      setEnv(r.data.env);
    }
  }, []);
  useEffect(() => {
    if (name) void load(name, true);
  }, [name, load]);
  const running = !!detail?.op?.running;
  useEffect(() => {
    if (!running || !saved) return;
    const t = window.setInterval(() => void load(saved, false), 1200);
    return () => window.clearInterval(t);
  }, [running, saved, load]);
  useEffect(() => {
    if (view !== 'logs' || !saved) return;
    let live = true;
    const get = () => void stacksApi.logs(saved).then((r) => live && setLogs(r.ok ? r.data.logs || 'No log lines yet.' : (r.error ?? 'Could not read the logs')));
    get();
    const t = window.setInterval(get, 4000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [view, saved]);

  const save = async (deploy: boolean) => {
    setBusy(true);
    setError('');
    const n = saved ?? stackName.trim();
    const r = saved ? await stacksApi.update(n, yaml, env, deploy) : await stacksApi.create(n, yaml, env, deploy);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not save');
    toast('success', deploy ? `Deploying ${n}…` : `${n} saved`);
    setSaved(n);
    await load(n, false);
    setDetail((d) => (d ? { ...d, compose: yaml, env } : d));
  };
  const run = async (action: StackAction) => {
    if (!saved) return;
    if (action === 'down') {
      const ok = await confirmDialog({ title: `Take ${saved} down?`, message: 'Its containers are stopped and removed. The compose file, folders and volumes stay, so you can deploy it again.', confirmLabel: 'Take down', danger: true });
      if (!ok) return;
    }
    const r = await stacksApi.run(saved, action);
    if (!r.ok) return toast('error', `Could not ${ACTION_LABEL[action].toLowerCase()}`, r.error);
    void load(saved, false);
  };
  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>, value: string, set: (v: string) => void) => {
    if (e.key === 'Tab') {
      e.preventDefault();
      const t = e.currentTarget;
      const { selectionStart: a, selectionEnd: b } = t;
      set(value.slice(0, a) + '  ' + value.slice(b));
      requestAnimationFrame(() => t.setSelectionRange(a + 2, a + 2));
    } else if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      void save(false);
    }
  };
  const validName = /^[a-z0-9][a-z0-9_-]{0,62}$/.test(stackName.trim());

  return (
    <div className="ce">
      <header className="ce-head">
        <button type="button" className="ghost" onClick={onClose}>
          <Icon name="chevronLeft" size={15} /> Back
        </button>
        <h2>{saved ? `Stack ${saved}` : 'Create Stack'}</h2>
        {detail && stateChip(detail.containers)}
      </header>
      <div className="ce-body st-body">
        {!saved && (
          <label className="st-namefield">
            Name <span className="muted">(lowercase letters, numbers, - and _)</span>
            <input value={stackName} autoFocus spellCheck={false} placeholder="my-stack" onChange={(e) => setStackName(e.target.value.toLowerCase())} />
          </label>
        )}
        <div className="st-tabs">
          <Choice
            value={view}
            options={[
              { id: 'compose', label: 'compose.yaml' },
              { id: 'env', label: 'Variables (.env)' },
              ...(saved ? [{ id: 'logs' as const, label: 'Logs' }] : []),
            ]}
            onChange={setView}
          />
          {detail && <span className="muted small mono st-dir" title="Relative paths like ./data are inside this folder">{detail.dir}</span>}
        </div>
        {view === 'compose' && <textarea className="scripts-body st-yaml" spellCheck={false} value={yaml} onChange={(e) => setYaml(e.target.value)} onKeyDown={(e) => onKey(e, yaml, setYaml)} />}
        {view === 'env' && (
          <>
            <p className="muted small st-hint">
              One <span className="mono">KEY=value</span> per line. Use them in the compose file as <span className="mono">${'{KEY}'}</span>. Kept private on the server.
            </p>
            <textarea className="scripts-body st-yaml" spellCheck={false} placeholder={'TZ=Asia/Dubai\nDB_PASSWORD=change-me'} value={env} onChange={(e) => setEnv(e.target.value)} onKeyDown={(e) => onKey(e, env, setEnv)} />
          </>
        )}
        {view === 'logs' && <pre className="log-view st-log st-logs">{logs ?? 'Loading…'}</pre>}
        {detail?.op && <OpOutput op={detail.op} />}
      </div>
      <footer className="ce-foot">
        {error && <p className="error">{error}</p>}
        {saved && (
          <div className="st-run">
            <button type="button" className="ghost small" disabled={running || dirty} title={dirty ? 'Save first' : 'Download newer images and recreate what changed'} onClick={() => void run('redeploy')}>
              <Icon name="download" size={13} /> Update
            </button>
            <button type="button" className="ghost small" disabled={running} onClick={() => void run('restart')}>
              <Icon name="restart" size={13} /> Restart
            </button>
            <button type="button" className="ghost small" disabled={running} onClick={() => void run('stop')}>
              <Icon name="stop" size={13} /> Stop
            </button>
            <button type="button" className="ghost small danger" disabled={running || !detail?.containers.length} onClick={() => void run('down')}>
              Take down
            </button>
          </div>
        )}
        <span className="spacer" />
        <button type="button" className="ghost" disabled={busy || running || (!saved && !validName) || !yaml.trim() || (!!saved && !dirty)} onClick={() => void save(false)}>
          <Icon name="save" size={14} /> Save
        </button>
        <button type="button" disabled={busy || running || (!saved && !validName) || !yaml.trim() || compose?.installed === false} onClick={() => void save(true)}>
          {busy ? <span className="spinner sm" /> : <Icon name="play" size={14} />} {saved ? 'Save & Deploy' : 'Create & Deploy'}
        </button>
      </footer>
    </div>
  );
}
