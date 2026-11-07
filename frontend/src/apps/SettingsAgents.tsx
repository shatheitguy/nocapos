import { tr } from '../i18n';
import { useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { agentsApi, type AgentActivity, type AgentScope, type AgentToken, type AgentsOverview } from '../api/agents';
import { Icon } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Section, Toggle } from './Personalize';

// Settings → AI Agents: NoCap MCP. AI agents on this machine (or, when
// allowed, the network) get NoCapOS's tools with their own token. Reading
// is instant; anything that changes something pops an approval card first.

const OUTCOME: Record<AgentActivity['outcome'], string> = {
  ok: 'Done',
  error: 'Failed',
  denied: 'You said no',
  timeout: 'Nobody answered',
};

const TOKEN_HOLDER = '<your agent token>';

function copy(text: string, what: string) {
  void navigator.clipboard?.writeText(text).then(() => toast('success', `${what} copied`));
}

export function AgentsSettings() {
  const [data, setData] = useState<AgentsOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const load = () =>
    void agentsApi.overview().then((r) => {
      if (r.ok) {
        setData(r.data);
        setError(null);
      } else setError(r.error ?? 'Couldn’t load AI Agents');
    });
  useEffect(() => {
    load();
    const t = window.setInterval(load, 15_000);
    return () => window.clearInterval(t);
  }, []);

  const set = async (body: { enabled?: boolean; loopback_only?: boolean }) => {
    const r = await agentsApi.settings(body);
    if (!r.ok) return toast('error', 'Couldn’t save that', r.error);
    load();
  };

  const revoke = async (t: AgentToken) => {
    const ok = await confirmDialog({
      title: `Revoke ${t.name}?`,
      message: 'Its token stops working right away. Make a new one any time.',
      confirmLabel: 'Revoke',
      danger: true,
    });
    if (!ok) return;
    const r = await agentsApi.revoke(t.id);
    if (!r.ok && r.status !== 404) return toast('error', 'Couldn’t revoke that token', r.error);
    toast('success', `${t.name} is cut off`);
    load();
  };

  if (error) return <p className="error">{error}</p>;
  if (!data) return <p className="muted small">Loading…</p>;

  return (
    <div className="stack settings-page">
      <section className="ucard dev-hero">
        <span className="dev-hero-icon">
          <Icon name="sparkles" size={26} />
        </span>
        <div className="dev-hero-text">
          <span className="dev-hero-label">NoCap MCP</span>
          <b>Let your AI agents use this server</b>
          <span className="dev-hero-hint">
            Agents that speak MCP get NoCapOS’s tools: files, apps, containers, VMs, the screen. Looking around is instant;
            {tr('sp.agentsIntro')}
          </span>
        </div>
        <button type="button" className="pill small" onClick={() => setAdding(true)}>
          <Icon name="plus" size={13} /> Add an agent
        </button>
      </section>

      <Section title="Access" hint={data.can_manage ? undefined : 'Only an administrator can flip these.'}>
        <Toggle
          label="Let AI agents in"
          hint={data.enabled ? 'On — agents with a token can connect.' : 'Off — every agent token is ignored.'}
          checked={data.enabled}
          disabled={!data.can_manage}
          onChange={(v) => void set({ enabled: v })}
        />
        <Toggle
          label="Only agents on this machine"
          hint={
            data.loopback_only
              ? 'Agents on other devices can’t connect, even with a token.'
              : 'Agents on your network can connect with a token too. Keep tokens secret.'
          }
          checked={data.loopback_only}
          disabled={!data.can_manage}
          onChange={(v) => void set({ loopback_only: v })}
        />
      </Section>

      <Section title="Your agents" hint="One token per agent. Pick what each can touch, revoke whenever.">
        {data.tokens.length === 0 ? (
          <div className="settings-row full">
            <p className="muted small dev-empty">No agents yet — hit <b>Add an agent</b> to make a token.</p>
          </div>
        ) : (
          data.tokens.map((t) => (
            <TokenRow
              key={t.id}
              t={t}
              data={data}
              open={open === t.id}
              onToggle={() => setOpen(open === t.id ? null : t.id)}
              onRevoke={() => void revoke(t)}
              onSaved={load}
            />
          ))
        )}
      </Section>

      <Setup data={data} token={TOKEN_HOLDER} />

      <Section title="Recent activity" hint="Everything your agents asked for, newest first.">
        {data.activity.length === 0 ? (
          <div className="settings-row full">
            <p className="muted small dev-empty">Nothing yet.</p>
          </div>
        ) : (
          data.activity.map((a) => (
            <div key={a.id} className="settings-row agent-act">
              <span className={`agent-act-dot ${a.outcome}`} />
              <span className="settings-row-text">
                <span className="settings-row-label">{a.summary}</span>
                <span className="settings-row-hint">
                  {a.agent} · {a.tool} · {OUTCOME[a.outcome] ?? a.outcome} · {fmtAgo(a.created_at)}
                  {a.detail ? ` · ${a.detail}` : ''}
                </span>
              </span>
            </div>
          ))
        )}
      </Section>

      {adding && (
        <AddAgentDialog
          data={data}
          onClose={() => {
            setAdding(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function TokenRow({ t, data, open, onToggle, onRevoke, onSaved }: {
  t: AgentToken;
  data: AgentsOverview;
  open: boolean;
  onToggle: () => void;
  onRevoke: () => void;
  onSaved: () => void;
}) {
  const usable = data.tools.filter((x) => t.scope === 'act' || !x.action);
  const on = (name: string) => t.tools === null || t.tools.includes(name);
  const save = async (body: { tools?: string[]; all_tools?: boolean; always_allow?: string[] }) => {
    const r = await agentsApi.setTools(t.id, body);
    if (!r.ok) return toast('error', 'Couldn’t save that', r.error);
    onSaved();
  };
  const flip = (name: string, v: boolean) => {
    const cur = usable.filter((x) => on(x.name)).map((x) => x.name);
    const next = v ? [...cur, name] : cur.filter((n) => n !== name);
    if (next.length === usable.length) void save({ all_tools: true });
    else void save({ tools: next });
  };
  const bits = [
    t.scope === 'act' ? 'Can request actions' : 'Read-only',
    t.last_used_at ? `Used ${fmtAgo(t.last_used_at)}` : 'Never used',
    t.tools === null ? 'All tools' : `${t.tools.length} tools`,
  ];
  return (
    <>
      <div className="settings-row dev-row">
        <span className="urow-icon dev-row-icon">
          <Icon name="sparkles" size={18} />
        </span>
        <span className="settings-row-text">
          <span className="settings-row-label">{t.name}</span>
          <span className="settings-row-hint">{bits.join(' · ')}</span>
        </span>
        <div className="settings-row-control">
          <button type="button" className="ghost small" onClick={onToggle} aria-expanded={open}>
            Tools <Icon name={open ? 'chevronUp' : 'chevronDown'} size={12} />
          </button>
          <button type="button" className="ghost small danger" onClick={onRevoke}>
            Revoke
          </button>
        </div>
      </div>
      {open && (
        <div className="settings-row stacked agent-tools">
          {usable.map((x) => (
            <label key={x.name} className="agent-tool">
              <input type="checkbox" checked={on(x.name)} onChange={(e) => flip(x.name, e.target.checked)} />
              <span>
                <b>{x.title}</b> <code>{x.name}</code>
                {x.action && !t.always_allow.includes(x.name) && <span className="agent-badge">asks first</span>}
                {t.always_allow.includes(x.name) && (
                  <button
                    type="button"
                    className="agent-badge warn"
                    title="Ask me again for this one"
                    onClick={(e) => {
                      e.preventDefault();
                      void save({ always_allow: t.always_allow.filter((n) => n !== x.name) });
                    }}
                  >
                    always allowed ✕
                  </button>
                )}
                <span className="muted small">{x.description}</span>
              </span>
            </label>
          ))}
        </div>
      )}
    </>
  );
}

function snippets(data: AgentsOverview, token: string) {
  const url = data.loopback_only ? data.local_url : data.network_url;
  const env: Record<string, string> = { NOCAP_MCP_TOKEN: token };
  if (data.local_url !== 'http://127.0.0.1:8780/mcp') env.NOCAP_MCP_URL = data.local_url;
  const command = JSON.stringify({ mcpServers: { nocap: { command: 'cap', args: ['mcp'], env } } }, null, 2);
  const http = JSON.stringify(
    { mcpServers: { nocap: { type: 'http', url, headers: { Authorization: `Bearer ${token}` } } } },
    null,
    2,
  );
  const envYaml = Object.entries(env)
    .map(([k, v]) => `      ${k}: "${v}"`)
    .join('\n');
  const hermes = `mcp_servers:\n  nocap:\n    command: "cap"\n    args: ["mcp"]\n    env:\n${envYaml}`;
  const claude =
    `claude mcp add nocap ${Object.entries(env).map(([k, v]) => `--env ${k}=${v}`).join(' ')} -- cap mcp\n` +
    `# or over HTTP:\nclaude mcp add --transport http nocap ${url} --header "Authorization: Bearer ${token}"`;
  return { command, http, hermes, claude, url };
}

function Snippet({ title, hint, text }: { title: string; hint: string; text: string }) {
  return (
    <div className="settings-row stacked agent-snippet">
      <span className="settings-row-text">
        <span className="settings-row-label">{title}</span>
        <span className="settings-row-hint">{hint}</span>
      </span>
      <div className="agent-code">
        <pre>{text}</pre>
        <button type="button" className="icon-btn neutral" title="Copy" aria-label={`Copy ${title}`} onClick={() => copy(text, title)}>
          <Icon name="copy" size={14} />
        </button>
      </div>
    </div>
  );
}

function Setup({ data, token }: { data: AgentsOverview; token: string }) {
  const s = useMemo(() => snippets(data, token), [data, token]);
  return (
    <Section title="Set it up" hint="Paste one of these into your agent’s MCP settings, with your token in place of the placeholder.">
      <Snippet
        title="Any MCP app (command)"
        hint="For agents on this machine: they start cap mcp, which talks to NoCapOS locally."
        text={s.command}
      />
      <Snippet title="Any MCP app (HTTP)" hint={`Streamable HTTP at ${s.url} with your token as a bearer header.`} text={s.http} />
      <Snippet title="Hermes Agent" hint="Add to ~/.hermes/config.yaml, then restart Hermes Agent." text={s.hermes} />
      <Snippet title="Claude Code" hint="Run in a terminal on this machine." text={s.claude} />
    </Section>
  );
}

function AddAgentDialog({ data, onClose }: { data: AgentsOverview; onClose: () => void }) {
  const [name, setName] = useState('');
  const [scope, setScope] = useState<AgentScope>('act');
  const [busy, setBusy] = useState(false);
  const [token, setToken] = useState<string | null>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const create = async () => {
    setBusy(true);
    const r = await agentsApi.create(name.trim(), scope);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t make a token', r.error);
    setToken(r.data.token);
  };

  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && !token && onClose()}>
      <div className="confirm-dialog glass agent-add" role="dialog" aria-modal="true" aria-labelledby="agent-add-title">
        <div className="confirm-icon">
          <Icon name="sparkles" size={22} />
        </div>
        <h3 id="agent-add-title">{token ? 'Here’s your token' : 'Add an agent'}</h3>
        {token ? (
          <>
            <p>Copy it now — it’s shown this once and NoCapOS only keeps a scrambled copy.</p>
            <div className="agent-code token">
              <pre>{token}</pre>
              <button type="button" className="icon-btn neutral" title="Copy token" aria-label="Copy token" onClick={() => copy(token, 'Token')}>
                <Icon name="copy" size={14} />
              </button>
            </div>
            {!data.enabled && <p className="muted small">{tr('sp.agentsOffNote')}</p>}
            <div className="agent-snippets-mini">
              <Setup data={data} token={token} />
            </div>
          </>
        ) : (
          <>
            <label className="agent-field">
              <span>Name</span>
              <input autoFocus value={name} maxLength={64} placeholder="Hermes on my laptop" onChange={(e) => setName(e.target.value)} />
            </label>
            <div className="agent-scope">
              <label className={scope === 'read' ? 'on' : ''}>
                <input type="radio" name="scope" checked={scope === 'read'} onChange={() => setScope('read')} />
                <span>
                  <b>Read-only</b>
                  <span className="muted small">Looks around: files, apps, system info. Can’t change anything.</span>
                </span>
              </label>
              <label className={scope === 'act' ? 'on' : ''}>
                <input type="radio" name="scope" checked={scope === 'act'} onChange={() => setScope('act')} />
                <span>
                  <b>Can request actions</b>
                  <span className="muted small">Can also ask to edit files, run commands, control apps and the screen — you approve each one.</span>
                </span>
              </label>
            </div>
          </>
        )}
        <div className="confirm-actions">
          <button type="button" className="ghost" onClick={onClose}>
            {token ? 'Done' : 'Cancel'}
          </button>
          {!token && (
            <button type="button" disabled={busy || !name.trim()} onClick={() => void create()}>
              Make token
            </button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
