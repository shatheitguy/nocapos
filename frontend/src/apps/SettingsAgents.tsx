import { tr, useT, type T } from '../i18n';
import { useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  agentsApi,
  levelRank,
  scopesAllow,
  type AgentActivity,
  type AgentToken,
  type AgentsOverview,
  type Capability,
  type Risk,
  type Scopes,
  type Secret,
  type UpdateAgent,
} from '../api/agents';
import { Icon } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Section, Toggle } from './Personalize';

// Settings → AI Agents: NoCap MCP. Each AI agent (and the built-in AI
// Assistant) has a profile: a level per capability (everything off unless
// turned on), the folders its file tools keep to, which capabilities run
// without asking, where it may connect from and until when. The server
// enforces every bit of it; this page only edits it.

const TOKEN_HOLDER = '<your agent token>';
const GROUPS: Capability['group'][] = ['data', 'system', 'apps', 'danger'];
const EXPIRY_DAYS = [0, 1, 7, 30, 90, 365];

function copy(text: string, what: string) {
  void navigator.clipboard?.writeText(text).then(() => toast('success', tr('ag.copied', { what })));
}

const emptyScopes = (): Scopes => ({ levels: {}, folders: [], auto: [] });
const cloneScopes = (s: Scopes): Scopes => ({ levels: { ...s.levels }, folders: [...s.folders], auto: [...s.auto] });

const RISK_RANK: Record<Risk, number> = { none: 0, low: 1, medium: 2, high: 3 };

/** The profile's overall risk, the way the server works it out. */
function profileRisk(caps: Capability[], s: Scopes): Risk {
  let worst: Risk = 'none';
  for (const c of caps) {
    const i = Math.max(0, levelRank(c, s.levels[c.id]));
    let r = c.levels[i].risk;
    if (r === 'medium' && s.auto.includes(c.id) && !c.never_auto) r = 'high';
    if (RISK_RANK[r] > RISK_RANK[worst]) worst = r;
  }
  return worst;
}

function sameScopes(a: Scopes, b: Scopes): boolean {
  const norm = (s: Scopes) =>
    JSON.stringify([
      Object.entries(s.levels)
        .filter(([, v]) => v && v !== 'off')
        .sort(),
      [...s.folders].sort(),
      [...s.auto].sort(),
    ]);
  return norm(a) === norm(b);
}

function RiskBadge({ risk }: { risk: Risk }) {
  const t = useT();
  return <span className={`agent-risk ${risk}`}>{t(`ag.risk.${risk}`)}</span>;
}

export function AgentsSettings() {
  const t = useT();
  const [data, setData] = useState<AgentsOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const [secret, setSecret] = useState<Secret | null>(null);

  const load = () =>
    void agentsApi.overview().then((r) => {
      if (r.ok) {
        setData(r.data);
        setError(null);
      } else setError(r.error ?? tr('ag.loadFail'));
    });
  useEffect(() => {
    load();
    const id = window.setInterval(load, 15_000);
    return () => window.clearInterval(id);
  }, []);

  const set = async (body: { enabled?: boolean; loopback_only?: boolean }) => {
    const r = await agentsApi.settings(body);
    if (!r.ok) return toast('error', tr('ag.saveFail'), r.error);
    load();
  };

  const stopAll = async () => {
    const ok = await confirmDialog({
      title: tr('ag.stopTitle'),
      message: data?.can_manage ? tr('ag.stopMsgAdmin') : tr('ag.stopMsg'),
      confirmLabel: tr('ag.stop'),
      danger: true,
    });
    if (!ok) return;
    const r = await agentsApi.stopAll();
    if (!r.ok) return toast('error', tr('ag.stopFail'), r.error);
    toast('success', tr('ag.stopped', { paused: r.data.paused, denied: r.data.denied, stopped: r.data.stopped }));
    load();
  };

  const revoke = async (a: AgentToken) => {
    const ok = await confirmDialog({
      title: tr('ag.revokeTitle', { name: a.name }),
      message: tr('ag.revokeMsg'),
      confirmLabel: tr('ag.revoke'),
      danger: true,
    });
    if (!ok) return;
    const r = await agentsApi.revoke(a.id);
    if (!r.ok && r.status !== 404) return toast('error', tr('ag.revokeFail'), r.error);
    toast('success', tr('ag.revoked', { name: a.name }));
    load();
  };

  if (error) return <p className="error">{error}</p>;
  if (!data) return <p className="muted small">{t('ag.loading')}</p>;

  return (
    <div className="stack settings-page">
      <section className="ucard dev-hero">
        <span className="dev-hero-icon">
          <Icon name="sparkles" size={26} />
        </span>
        <div className="dev-hero-text">
          <span className="dev-hero-label">NoCap MCP</span>
          <b>{t('ag.heroTitle')}</b>
          <span className="dev-hero-hint">
            {t('ag.heroHint')} {t('sp.agentsIntro')}
          </span>
        </div>
        <div className="agent-hero-actions">
          <button type="button" className="pill small" onClick={() => setAdding(true)}>
            <Icon name="plus" size={13} /> {t('ag.add')}
          </button>
          <button type="button" className="ghost small danger" onClick={() => void stopAll()}>
            {t('ag.stop')}
          </button>
        </div>
      </section>

      <Section title={t('ag.access')} hint={data.can_manage ? undefined : t('ag.accessAdminOnly')}>
        <Toggle
          label={t('ag.letIn')}
          hint={data.enabled ? t('ag.letInOn') : t('ag.letInOff')}
          checked={data.enabled}
          disabled={!data.can_manage}
          onChange={(v) => void set({ enabled: v })}
        />
        <Toggle
          label={t('ag.loopback')}
          hint={data.loopback_only ? t('ag.loopbackOn') : t('ag.loopbackOff')}
          checked={data.loopback_only}
          disabled={!data.can_manage}
          onChange={(v) => void set({ loopback_only: v })}
        />
      </Section>

      <Section title={t('ag.assistant')} hint={t('ag.assistantHint')}>
        <AgentRow
          a={data.assistant}
          data={data}
          open={open === data.assistant.id}
          onToggle={() => setOpen(open === data.assistant.id ? null : data.assistant.id)}
          onSaved={load}
          onSecret={setSecret}
        />
      </Section>

      <Section title={t('ag.yours')} hint={t('ag.yoursHint')}>
        {data.tokens.length === 0 ? (
          <div className="settings-row full">
            <p className="muted small dev-empty">{t('ag.none')}</p>
          </div>
        ) : (
          data.tokens.map((a) => (
            <AgentRow
              key={a.id}
              a={a}
              data={data}
              open={open === a.id}
              onToggle={() => setOpen(open === a.id ? null : a.id)}
              onRevoke={() => void revoke(a)}
              onSaved={load}
              onSecret={setSecret}
            />
          ))
        )}
      </Section>

      <Setup data={data} token={TOKEN_HOLDER} />

      <Activity data={data} />

      {adding && (
        <AddAgentDialog
          data={data}
          onClose={() => {
            setAdding(false);
            load();
          }}
        />
      )}
      {secret && (
        <SecretDialog
          data={data}
          secret={secret}
          onClose={() => {
            setSecret(null);
            load();
          }}
        />
      )}
    </div>
  );
}

function agentBits(t: T, a: AgentToken): string[] {
  const bits: string[] = [];
  if (a.kind !== 'assistant') bits.push(t(a.kind === 'remote' ? 'ag.kindRemote' : 'ag.kindLocal'));
  if (a.paused) bits.push(t('ag.paused'));
  if (a.expired) bits.push(t('ag.expired'));
  else if (a.expires_at) bits.push(t('ag.expiresOn', { date: new Date(a.expires_at).toLocaleDateString() }));
  if (a.pairing) bits.push(t('ag.waitingPair'));
  if (a.kind !== 'assistant') bits.push(a.last_used_at ? t('ag.used', { ago: fmtAgo(a.last_used_at) }) : t('ag.neverUsed'));
  return bits;
}

function AgentRow({
  a,
  data,
  open,
  onToggle,
  onRevoke,
  onSaved,
  onSecret,
}: {
  a: AgentToken;
  data: AgentsOverview;
  open: boolean;
  onToggle: () => void;
  onRevoke?: () => void;
  onSaved: () => void;
  onSecret: (s: Secret) => void;
}) {
  const t = useT();
  const save = async (body: UpdateAgent, done?: string) => {
    const r = await agentsApi.update(a.id, body);
    if (!r.ok) {
      toast('error', tr('ag.saveFail'), r.error);
      return false;
    }
    if (done) toast('success', done);
    onSaved();
    return true;
  };
  const rotate = async (pairing: boolean) => {
    const ok = await confirmDialog({
      title: tr(pairing ? 'ag.newCodeTitle' : 'ag.newTokenTitle', { name: a.name }),
      message: tr('ag.rotateMsg'),
      confirmLabel: tr(pairing ? 'ag.newCode' : 'ag.newToken'),
    });
    if (!ok) return;
    const r = await agentsApi.rotate(a.id, pairing);
    if (!r.ok) return toast('error', tr('ag.saveFail'), r.error);
    onSecret(r.data);
  };
  const isAssistant = a.kind === 'assistant';
  const anyOn = Object.values(a.scopes.levels).some((v) => v && v !== 'off');

  return (
    <>
      <div className={`settings-row dev-row ${a.paused || a.expired ? 'dimmed' : ''}`}>
        <span className="urow-icon dev-row-icon">
          <Icon name="sparkles" size={18} />
        </span>
        <span className="settings-row-text">
          <span className="settings-row-label">
            {isAssistant ? t('ag.assistantName') : a.name} <RiskBadge risk={a.risk} />
          </span>
          <span className="settings-row-hint">
            {isAssistant && !anyOn ? t('ag.assistantOff') : agentBits(t, a).join(' · ')}
          </span>
        </span>
        <div className="settings-row-control">
          <button type="button" className="ghost small" onClick={onToggle} aria-expanded={open}>
            {t('ag.edit')} <Icon name={open ? 'chevronUp' : 'chevronDown'} size={12} />
          </button>
          {onRevoke && (
            <button type="button" className="ghost small danger" onClick={onRevoke}>
              {t('ag.revoke')}
            </button>
          )}
        </div>
      </div>
      {open && (
        <div className="settings-row stacked agent-detail">
          <ProfileEditor
            key={a.id + JSON.stringify(a.scopes)}
            caps={data.capabilities}
            presets={data.presets}
            initial={a.scopes}
            onSave={(scopes) => void save({ scopes }, tr('ag.savedScopes', { name: isAssistant ? tr('ag.assistantName') : a.name }))}
          />
          <Toggle
            label={t('ag.pause')}
            hint={a.paused ? t('ag.pausedHint') : t('ag.pauseHint')}
            checked={a.paused}
            onChange={(v) => void save({ paused: v })}
          />
          {!isAssistant && <ConnectEditor key={[a.name, ...a.ip_allow, a.expires_at].join('|')} a={a} onSave={save} />}
          {!isAssistant && (
            <div className="agent-detail-actions">
              <button type="button" className="ghost small" onClick={() => void rotate(true)}>
                {t('ag.newCode')}
              </button>
              <button type="button" className="ghost small" onClick={() => void rotate(false)}>
                {t('ag.newToken')}
              </button>
            </div>
          )}
          <ToolList a={a} data={data} save={save} />
        </div>
      )}
    </>
  );
}

/** Name, IP allowlist and expiry of one agent. */
function ConnectEditor({ a, onSave }: { a: AgentToken; onSave: (b: UpdateAgent, done?: string) => Promise<boolean> }) {
  const t = useT();
  const [name, setName] = useState(a.name);
  const [ips, setIps] = useState(a.ip_allow.join(', '));
  const [days, setDays] = useState<string>('');
  const ipList = ips
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  const dirty = name.trim() !== a.name || ipList.join(',') !== a.ip_allow.join(',') || days !== '';
  const submit = async () => {
    const body: UpdateAgent = {};
    if (name.trim() !== a.name) body.name = name.trim();
    if (ipList.join(',') !== a.ip_allow.join(',')) body.ip_allow = ipList;
    if (days !== '') body.expires_in_days = Number(days);
    if (await onSave(body, tr('ag.saved'))) setDays('');
  };
  return (
    <div className="agent-connect">
      <label className="agent-field">
        <span>{t('ag.name')}</span>
        <input value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </label>
      {a.kind === 'remote' && (
        <label className="agent-field">
          <span>{t('ag.ipAllow')}</span>
          <input value={ips} placeholder="192.168.1.20, 10.0.0.0/8" onChange={(e) => setIps(e.target.value)} />
          <span className="muted small">{t('ag.ipAllowHint')}</span>
        </label>
      )}
      <label className="agent-field">
        <span>{t('ag.expiry')}</span>
        <select value={days} onChange={(e) => setDays(e.target.value)}>
          <option value="">{a.expires_at ? t('ag.expiryKeep', { date: new Date(a.expires_at).toLocaleDateString() }) : t('ag.expiryNever')}</option>
          {EXPIRY_DAYS.map((d) => (
            <option key={d} value={String(d)}>
              {d === 0 ? t('ag.expiryNever') : t('ag.expiryDays', { n: d })}
            </option>
          ))}
        </select>
      </label>
      <button type="button" className="small" disabled={!dirty || !name.trim()} onClick={() => void submit()}>
        {t('ag.save')}
      </button>
    </div>
  );
}

/** The tools an agent's scopes allow, which of them it may use, and which never ask. */
function ToolList({ a, data, save }: { a: AgentToken; data: AgentsOverview; save: (b: UpdateAgent) => Promise<boolean> }) {
  const t = useT();
  const usable = data.tools.filter((x) => scopesAllow(data.capabilities, a.scopes, x.capability, x.level));
  const on = (name: string) => a.tools === null || a.tools.includes(name);
  const flip = (name: string, v: boolean) => {
    const cur = usable.filter((x) => on(x.name)).map((x) => x.name);
    const next = v ? [...cur, name] : cur.filter((n) => n !== name);
    if (next.length === usable.length) void save({ all_tools: true });
    else void save({ tools: next });
  };
  const asks = (x: (typeof usable)[number]) => x.action && !a.scopes.auto.includes(x.capability) && !a.always_allow.includes(x.name);
  return (
    <details className="agent-tools-wrap">
      <summary>{t('ag.tools', { n: usable.filter((x) => on(x.name)).length, total: usable.length })}</summary>
      {usable.length === 0 ? (
        <p className="muted small">{t('ag.noTools')}</p>
      ) : (
        <div className="agent-tools">
          {usable.map((x) => (
            <label key={x.name} className="agent-tool">
              <input type="checkbox" checked={on(x.name)} onChange={(e) => flip(x.name, e.target.checked)} />
              <span>
                <b>{x.title}</b> <code>{x.name}</code>
                {asks(x) && <span className="agent-badge">{t('ag.asksFirst')}</span>}
                {a.always_allow.includes(x.name) && (
                  <button
                    type="button"
                    className="agent-badge warn"
                    title={t('ag.askAgain')}
                    onClick={(e) => {
                      e.preventDefault();
                      void save({ always_allow: a.always_allow.filter((n) => n !== x.name) });
                    }}
                  >
                    {t('ag.alwaysAllowed')} ✕
                  </button>
                )}
                <span className="muted small">{x.description}</span>
              </span>
            </label>
          ))}
        </div>
      )}
    </details>
  );
}

/** Presets, a level per capability, ask-first switches and folders. */
function ProfileEditor({
  caps,
  presets,
  initial,
  onSave,
  onChange,
}: {
  caps: Capability[];
  presets: AgentsOverview['presets'];
  initial: Scopes;
  /** With onSave the editor keeps a draft and shows Save; without it, every change goes to onChange. */
  onSave?: (s: Scopes) => void;
  onChange?: (s: Scopes) => void;
}) {
  const t = useT();
  const [s, setS] = useState<Scopes>(() => cloneScopes(initial));
  const [folder, setFolder] = useState('');
  const update = (next: Scopes) => {
    setS(next);
    onChange?.(next);
  };
  const setLevel = (c: Capability, level: string) => {
    const next = cloneScopes(s);
    if (level === 'off') {
      delete next.levels[c.id];
      next.auto = next.auto.filter((x) => x !== c.id);
    } else next.levels[c.id] = level;
    update(next);
  };
  const setAsk = (c: Capability, ask: boolean) => {
    const next = cloneScopes(s);
    next.auto = ask ? next.auto.filter((x) => x !== c.id) : [...next.auto.filter((x) => x !== c.id), c.id];
    update(next);
  };
  const addFolder = () => {
    const f = folder.trim();
    if (!/^[^/\\:]+:.*/.test(f)) return toast('error', tr('ag.folderBad'));
    if (!s.folders.includes(f)) update({ ...cloneScopes(s), folders: [...s.folders, f] });
    setFolder('');
  };
  const preset = presets.find((p) => sameScopes(p.scopes, s))?.id;
  const risk = profileRisk(caps, s);
  const filesOn = ['files', 'photos'].some((id) => s.levels[id] && s.levels[id] !== 'off');

  return (
    <div className="agent-profile">
      <div className="agent-presets" role="group" aria-label={t('ag.presets')}>
        {presets.map((p) => (
          <button
            key={p.id}
            type="button"
            className={`agent-chip ${preset === p.id ? 'on' : ''}`}
            aria-pressed={preset === p.id}
            title={t(`ag.preset.${p.id}.hint`)}
            onClick={() => update({ ...cloneScopes(p.scopes), folders: [...s.folders] })}
          >
            {t(`ag.preset.${p.id}`)}
          </button>
        ))}
        <span className="agent-profile-risk">
          {t('ag.riskLabel')} <RiskBadge risk={risk} />
        </span>
      </div>
      {GROUPS.map((g) => (
        <div key={g} className={`agent-capgroup ${g}`}>
          <h4>{t(`ag.group.${g}`)}</h4>
          {caps
            .filter((c) => c.group === g)
            .map((c) => {
              const level = s.levels[c.id] ?? 'off';
              const lv = c.levels[Math.max(0, levelRank(c, level))];
              return (
                <div key={c.id} className="agent-cap">
                  <span className="agent-cap-text">
                    <b>{t(`ag.cap.${c.id}`)}</b>
                    <span className="muted small">{t(`ag.cap.${c.id}.hint`)}</span>
                  </span>
                  <span className="agent-cap-control">
                    <span className="agent-seg" role="radiogroup" aria-label={t(`ag.cap.${c.id}`)}>
                      {c.levels.map((l) => (
                        <button
                          key={l.id}
                          type="button"
                          role="radio"
                          aria-checked={level === l.id}
                          className={`${level === l.id ? 'on' : ''} risk-${l.risk}`}
                          onClick={() => setLevel(c, l.id)}
                        >
                          {t(`ag.level.${l.id}`)}
                        </button>
                      ))}
                    </span>
                    {c.acts && level !== 'off' && (
                      <label className="agent-ask" title={c.never_auto ? t('ag.alwaysAsks') : undefined}>
                        <input
                          type="checkbox"
                          checked={c.never_auto || !s.auto.includes(c.id)}
                          disabled={c.never_auto}
                          onChange={(e) => setAsk(c, e.target.checked)}
                        />
                        {t('ag.askFirst')}
                      </label>
                    )}
                    {lv.risk === 'high' && <RiskBadge risk="high" />}
                  </span>
                </div>
              );
            })}
        </div>
      ))}
      {filesOn && (
        <div className="agent-folders">
          <b>{t('ag.folders')}</b>
          <span className="muted small">{s.folders.length ? t('ag.foldersHint') : t('ag.foldersAll')}</span>
          {s.folders.map((f) => (
            <span key={f} className="agent-chip on">
              <code>{f}</code>
              <button
                type="button"
                className="icon-btn neutral"
                aria-label={t('ag.removeFolder', { folder: f })}
                onClick={() => update({ ...cloneScopes(s), folders: s.folders.filter((x) => x !== f) })}
              >
                ✕
              </button>
            </span>
          ))}
          <span className="agent-folder-add">
            <input
              value={folder}
              placeholder="data:/Documents"
              onChange={(e) => setFolder(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), addFolder())}
            />
            <button type="button" className="ghost small" disabled={!folder.trim()} onClick={addFolder}>
              {t('ag.addFolder')}
            </button>
          </span>
        </div>
      )}
      {onSave && (
        <div className="agent-detail-actions">
          <button type="button" className="small" disabled={sameScopes(s, initial)} onClick={() => onSave(s)}>
            {t('ag.saveScopes')}
          </button>
          {!sameScopes(s, initial) && (
            <button type="button" className="ghost small" onClick={() => setS(cloneScopes(initial))}>
              {t('ag.undo')}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function Activity({ data }: { data: AgentsOverview }) {
  const t = useT();
  const [agent, setAgent] = useState('');
  const [list, setList] = useState<AgentActivity[] | null>(null);
  const [more, setMore] = useState(true);
  useEffect(() => {
    if (!agent) {
      setList(null);
      return;
    }
    void agentsApi.activity(agent).then((r) => {
      if (r.ok) {
        setList(r.data);
        setMore(r.data.length >= 100);
      }
    });
  }, [agent]);
  const shown = list ?? data.activity;
  const older = async () => {
    const last = shown[shown.length - 1];
    if (!last) return;
    const r = await agentsApi.activity(agent || undefined, last.id);
    if (!r.ok) return;
    setList([...shown, ...r.data]);
    setMore(r.data.length >= 100);
  };
  const agents = [data.assistant, ...data.tokens];
  return (
    <Section title={t('ag.activity')} hint={t('ag.activityHint')}>
      <div className="settings-row">
        <span className="settings-row-text">
          <span className="settings-row-label">{t('ag.showFor')}</span>
        </span>
        <div className="settings-row-control">
          <select value={agent} onChange={(e) => setAgent(e.target.value)}>
            <option value="">{t('ag.allAgents')}</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.kind === 'assistant' ? t('ag.assistantName') : a.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      {shown.length === 0 ? (
        <div className="settings-row full">
          <p className="muted small dev-empty">{t('ag.nothingYet')}</p>
        </div>
      ) : (
        shown.map((a) => (
          <div key={a.id} className="settings-row agent-act">
            <span className={`agent-act-dot ${a.outcome}`} />
            <span className="settings-row-text">
              <span className="settings-row-label">
                {a.summary} {a.effect && a.effect !== 'read' && <span className={`agent-effect ${a.effect}`}>{t(`ag.effect.${a.effect}`)}</span>}
              </span>
              <span className="settings-row-hint">
                {a.agent} · {a.tool} · {t(`ag.outcome.${a.outcome}`)} · {fmtAgo(a.created_at)}
                {a.detail ? ` · ${a.detail}` : ''}
              </span>
            </span>
          </div>
        ))
      )}
      {(list !== null ? more : shown.length >= 50) && shown.length > 0 && (
        <div className="settings-row full">
          <button type="button" className="ghost small" onClick={() => void older()}>
            {t('ag.older')}
          </button>
        </div>
      )}
    </Section>
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
  const yaml = `mcp_servers:
  nocap:
    command: "cap"
    args: ["mcp"]
    env:
${envYaml}`;
  return { command, http, yaml, url };
}

function Snippet({ title, hint, text }: { title: string; hint: string; text: string }) {
  const t = useT();
  return (
    <div className="settings-row stacked agent-snippet">
      <span className="settings-row-text">
        <span className="settings-row-label">{title}</span>
        <span className="settings-row-hint">{hint}</span>
      </span>
      <div className="agent-code">
        <pre>{text}</pre>
        <button type="button" className="icon-btn neutral" title={t('ag.copy')} aria-label={t('ag.copyWhat', { what: title })} onClick={() => copy(text, title)}>
          <Icon name="copy" size={14} />
        </button>
      </div>
    </div>
  );
}

function Setup({ data, token }: { data: AgentsOverview; token: string }) {
  const t = useT();
  const s = useMemo(() => snippets(data, token), [data, token]);
  return (
    <Section title={t('ag.setup')} hint={t('ag.setupHint')}>
      <Snippet title={t('ag.snipCommand')} hint={t('ag.snipCommandHint')} text={s.command} />
      <Snippet title={t('ag.snipHttp')} hint={t('ag.snipHttpHint', { url: s.url, sse: data.sse_url })} text={s.http} />
      <Snippet title={t('ag.snipYaml')} hint={t('ag.snipYamlHint')} text={s.yaml} />
    </Section>
  );
}

/** What to do with a fresh token or pairing code (shown once). */
function SecretView({ data, secret }: { data: AgentsOverview; secret: Secret }) {
  const t = useT();
  const base = secret.agent.kind === 'local' ? data.local_url.replace(/\/mcp$/, '') : data.pair_url.replace(/\/mcp\/pair$/, '');
  if (secret.code) {
    const cmd = `cap mcp pair --url ${base} --code ${secret.code}`;
    return (
      <>
        <p>{t('ag.codeIntro')}</p>
        <div className="agent-code token">
          <pre>{secret.code}</pre>
          <button type="button" className="icon-btn neutral" title={t('ag.copy')} aria-label={t('ag.copyCode')} onClick={() => copy(secret.code!, tr('ag.code'))}>
            <Icon name="copy" size={14} />
          </button>
        </div>
        <Snippet title={t('ag.pairCmd')} hint={t('ag.pairCmdHint', { url: data.pair_url })} text={cmd} />
        {!data.enabled && <p className="muted small">{t('sp.agentsOffNote')}</p>}
      </>
    );
  }
  return (
    <>
      <p>{t('ag.tokenIntro')}</p>
      <div className="agent-code token">
        <pre>{secret.token}</pre>
        <button type="button" className="icon-btn neutral" title={t('ag.copy')} aria-label={t('ag.copyToken')} onClick={() => copy(secret.token!, tr('ag.token'))}>
          <Icon name="copy" size={14} />
        </button>
      </div>
      {!data.enabled && <p className="muted small">{t('sp.agentsOffNote')}</p>}
      <div className="agent-snippets-mini">
        <Setup data={data} token={secret.token!} />
      </div>
    </>
  );
}

function useEscape(onClose: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
}

function SecretDialog({ data, secret, onClose }: { data: AgentsOverview; secret: Secret; onClose: () => void }) {
  const t = useT();
  useEscape(onClose);
  return createPortal(
    <div className="confirm-backdrop">
      <div className="confirm-dialog glass agent-add" role="dialog" aria-modal="true" aria-labelledby="agent-secret-title">
        <div className="confirm-icon">
          <Icon name="sparkles" size={22} />
        </div>
        <h3 id="agent-secret-title">{t(secret.code ? 'ag.codeTitle' : 'ag.tokenTitle')}</h3>
        <SecretView data={data} secret={secret} />
        <div className="confirm-actions">
          <button type="button" onClick={onClose}>
            {t('ag.done')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function AddAgentDialog({ data, onClose }: { data: AgentsOverview; onClose: () => void }) {
  const t = useT();
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'local' | 'remote'>('local');
  const [scopes, setScopes] = useState<Scopes>(() => cloneScopes(data.presets.find((p) => p.id === 'read_only')?.scopes ?? emptyScopes()));
  const [ips, setIps] = useState('');
  const [days, setDays] = useState(0);
  const [pairing, setPairing] = useState(true);
  const [busy, setBusy] = useState(false);
  const [secret, setSecret] = useState<Secret | null>(null);
  useEscape(onClose);

  const create = async () => {
    setBusy(true);
    const r = await agentsApi.create({
      name: name.trim(),
      kind,
      scopes,
      ip_allow: kind === 'remote' ? ips.split(/[\s,]+/).filter(Boolean) : [],
      expires_in_days: days,
      pairing,
    });
    setBusy(false);
    if (!r.ok) return toast('error', tr('ag.createFail'), r.error);
    setSecret(r.data);
  };

  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && !secret && onClose()}>
      <div className="confirm-dialog glass agent-add" role="dialog" aria-modal="true" aria-labelledby="agent-add-title">
        <div className="confirm-icon">
          <Icon name="sparkles" size={22} />
        </div>
        <h3 id="agent-add-title">{secret ? t(secret.code ? 'ag.codeTitle' : 'ag.tokenTitle') : t('ag.add')}</h3>
        {secret ? (
          <SecretView data={data} secret={secret} />
        ) : (
          <>
            <label className="agent-field">
              <span>{t('ag.name')}</span>
              <input autoFocus value={name} maxLength={64} placeholder={t('ag.namePlaceholder')} onChange={(e) => setName(e.target.value)} />
            </label>
            <div className="agent-scope">
              {(['local', 'remote'] as const).map((k) => (
                <label key={k} className={kind === k ? 'on' : ''}>
                  <input type="radio" name="agent-kind" checked={kind === k} onChange={() => setKind(k)} />
                  <span>
                    <b>{t(k === 'local' ? 'ag.kindLocal' : 'ag.kindRemote')}</b>
                    <span className="muted small">
                      {t(k === 'local' ? 'ag.kindLocalHint' : data.loopback_only ? 'ag.kindRemoteBlocked' : 'ag.kindRemoteHint')}
                    </span>
                  </span>
                </label>
              ))}
            </div>
            {kind === 'remote' && (
              <label className="agent-field">
                <span>{t('ag.ipAllow')}</span>
                <input value={ips} placeholder="192.168.1.20, 10.0.0.0/8" onChange={(e) => setIps(e.target.value)} />
                <span className="muted small">{t('ag.ipAllowHint')}</span>
              </label>
            )}
            <div className="agent-field">
              <span>{t('ag.scopes')}</span>
              <ProfileEditor caps={data.capabilities} presets={data.presets} initial={scopes} onChange={setScopes} />
            </div>
            <label className="agent-field">
              <span>{t('ag.expiry')}</span>
              <select value={String(days)} onChange={(e) => setDays(Number(e.target.value))}>
                {EXPIRY_DAYS.map((d) => (
                  <option key={d} value={String(d)}>
                    {d === 0 ? t('ag.expiryNever') : t('ag.expiryDays', { n: d })}
                  </option>
                ))}
              </select>
            </label>
            <label className="agent-ask">
              <input type="checkbox" checked={pairing} onChange={(e) => setPairing(e.target.checked)} />
              {t('ag.usePairing')}
            </label>
          </>
        )}
        <div className="confirm-actions">
          <button type="button" className="ghost" onClick={onClose}>
            {secret ? t('ag.done') : t('ag.cancel')}
          </button>
          {!secret && (
            <button type="button" disabled={busy || !name.trim()} onClick={() => void create()}>
              {t(pairing ? 'ag.makeCode' : 'ag.makeToken')}
            </button>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
