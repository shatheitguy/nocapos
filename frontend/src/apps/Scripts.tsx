import { useCallback, useEffect, useRef, useState } from 'react';
import { scriptsApi, type Script, type ScriptHost, type ScriptInput } from '../api/scripts';
import { Icon } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { runStatus, useScriptRun } from '../lib/scriptRun';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Empty } from './Monitor';
import { Toggle } from './Personalize';

// Scripts: write, save and run the Quick Script Launcher's scripts.

const BLANK: ScriptInput = { name: '', description: '', body: '', confirm: false };

export function Scripts() {
  const [host, setHost] = useState<ScriptHost | null>(null);
  const [list, setList] = useState<Script[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | 'new' | null>(null);
  const [draft, setDraft] = useState<ScriptInput>(BLANK);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    const r = await scriptsApi.list();
    if (!r.ok) {
      setError(r.error ?? 'Could not load scripts');
      return;
    }
    setError(null);
    setHost(r.data.host);
    setList(r.data.scripts);
  }, []);
  const { run, start, stop, clear } = useScriptRun(() => void load());

  useEffect(() => {
    void load();
  }, [load]);

  const current = list?.find((s) => s.id === selected) ?? null;
  const dirty =
    selected === 'new' ||
    (!!current && (draft.name !== current.name || draft.description !== current.description || draft.body !== current.body || draft.confirm !== current.confirm));

  const pick = async (id: string | 'new') => {
    if (dirty && !(await confirmDialog({ title: 'Discard changes?', message: 'Your edits to this script are not saved.', confirmLabel: 'Discard', danger: true }))) return;
    setSelected(id);
    const s = list?.find((x) => x.id === id);
    setDraft(s ? { name: s.name, description: s.description, body: s.body, confirm: s.confirm } : { ...BLANK, body: host?.os === 'windows' ? 'Get-Date\n' : '#!/bin/bash\nset -e\n\n' });
  };

  const save = async (): Promise<Script | null> => {
    setSaving(true);
    const r = selected === 'new' ? await scriptsApi.create(draft) : await scriptsApi.update(selected!, draft);
    setSaving(false);
    if (!r.ok) {
      toast('error', 'Could not save the script', r.error);
      return null;
    }
    await load();
    setSelected(r.data.id);
    return r.data;
  };

  const remove = async () => {
    if (!current) return;
    if (!(await confirmDialog({ title: `Delete “${current.name}”?`, message: 'The script is removed for everyone.', confirmLabel: 'Delete', danger: true }))) return;
    const r = await scriptsApi.remove(current.id);
    if (!r.ok) toast('error', 'Could not delete the script', r.error);
    setSelected(null);
    void load();
  };

  const runIt = async () => {
    const s = dirty ? await save() : current;
    if (s) await start(s);
  };

  const out = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (out.current) out.current.scrollTop = out.current.scrollHeight;
  }, [run?.output]);

  if (error) return <Empty icon="fileCode" text={error} />;
  if (!list || !host) return <Empty icon="fileCode" text="Loading scripts…" />;

  const status = run ? runStatus(run) : null;

  return (
    <div className="scripts-app">
      <aside className="scripts-list">
        <button type="button" className="small" onClick={() => void pick('new')}>
          <Icon name="plus" size={14} /> New script
        </button>
        {list.length === 0 && <p className="muted small">No scripts yet. Saved scripts also appear in the Quick scripts widget.</p>}
        {list.map((s) => (
          <button key={s.id} type="button" className={`scripts-item ${selected === s.id ? 'on' : ''}`} onClick={() => selected !== s.id && void pick(s.id)}>
            <b className="ellipsis">{s.name}</b>
            <span className="muted small ellipsis">
              {s.last_run_at ? `Ran ${fmtAgo(s.last_run_at)} · exit ${s.last_exit}` : 'Never run'}
            </span>
          </button>
        ))}
      </aside>

      <section className="scripts-main">
        <div className={`scripts-host ${host.root ? 'root' : ''}`}>
          <Icon name={host.root ? 'alert' : 'info'} size={14} />
          {host.enabled ? (
            <span>
              Scripts run on this host with <b className="mono">{host.shell.split(/[\\/]/).pop()}</b> as <b>{host.user || 'the server account'}</b>
              {host.root && ' — that is root, so they can change anything'}. They stop after 15 minutes.
            </span>
          ) : (
            <span>Running scripts is turned off on this server (ALFA_ALLOW_HOST_TERMINAL=false). You can still edit them.</span>
          )}
        </div>

        {selected === null ? (
          <Empty icon="fileCode" text="Pick a script, or create a new one." />
        ) : (
          <div className="scripts-editor">
            <div className="scripts-fields">
              <input placeholder="Name" maxLength={60} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
              <input placeholder="What it does (optional)" maxLength={200} value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
            </div>
            <textarea
              className="scripts-body mono"
              spellCheck={false}
              value={draft.body}
              onChange={(e) => setDraft({ ...draft, body: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === 'Tab') {
                  e.preventDefault();
                  const t = e.currentTarget;
                  const { selectionStart: a, selectionEnd: b } = t;
                  const body = draft.body.slice(0, a) + '  ' + draft.body.slice(b);
                  setDraft({ ...draft, body });
                  requestAnimationFrame(() => t.setSelectionRange(a + 2, a + 2));
                } else if ((e.ctrlKey || e.metaKey) && e.key === 's') {
                  e.preventDefault();
                  void save();
                }
              }}
            />
            <Toggle label="Ask before running" hint="Show a confirmation first — useful for scripts that restart or delete things" checked={draft.confirm} onChange={(confirm) => setDraft({ ...draft, confirm })} />
            <div className="scripts-actions">
              {current && (
                <button type="button" className="ghost danger small" onClick={() => void remove()}>
                  <Icon name="trash" size={13} /> Delete
                </button>
              )}
              <span className="spacer" />
              <button type="button" className="ghost small" disabled={!dirty || saving} onClick={() => void save()}>
                <Icon name="save" size={13} /> {saving ? 'Saving…' : 'Save'}
              </button>
              <button type="button" className="small" disabled={!host.enabled || !!run?.running || saving || !draft.name.trim() || !draft.body.trim()} onClick={() => void runIt()}>
                <Icon name="play" size={13} /> {dirty ? 'Save & run' : 'Run'}
              </button>
            </div>
          </div>
        )}

        {run && status && (
          <div className="script-out big">
            <div className="script-out-head">
              <b className="ellipsis">{run.name}</b>
              <span className={`chip tiny ${status.tone}`}>{status.label}</span>
              {run.truncated && <span className="chip tiny warn">output truncated</span>}
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
      </section>
    </div>
  );
}
