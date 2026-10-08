import { useState } from 'react';
import { aiApi, kindLabel, type Provider, type ProviderInput, type ProviderKind } from '../../api/ai';
import { Icon } from '../../components/Icon';
import { toast } from '../../state/toasts';

const KEEP_ALIVE = [
  { label: '30 min', value: '30m' },
  { label: '2 hours', value: '2h' },
  { label: '4 hours', value: '4h' },
  { label: 'Always', value: '-1' },
];
const CONTEXTS = [4096, 8192, 16384, 32768];

const blank: ProviderInput = { name: '', kind: 'ollama', base_url: 'http://127.0.0.1:11434', api_key: '', default_model: '', keep_alive: '4h', context_size: 8192 };

/** Admin panel: configure AI providers (local Ollama, Anthropic, OpenAI-compatible). */
export function Providers({ providers, isAdmin, onChanged }: { providers: Provider[]; isAdmin: boolean; onChanged: () => void }) {
  const [editing, setEditing] = useState<{ id: string | null; form: ProviderInput } | null>(null);

  if (!isAdmin) {
    return (
      <div className="ai-panel">
        <div className="panel">
          <b>AI providers</b>
          <p className="muted small">Only an administrator can add or change AI providers.</p>
          {providers.map((p) => (
            <div key={p.id} className="model-row">
              <div>
                <b>{p.name}</b>
                <div className="muted small">{kindLabel(p.kind)}{p.default_model ? ` · ${p.default_model}` : ''}</div>
              </div>
            </div>
          ))}
        </div>
      </div>
    );
  }

  const startAdd = () => setEditing({ id: null, form: { ...blank } });
  const startEdit = (p: Provider) =>
    setEditing({
      id: p.id,
      form: { name: p.name, kind: p.kind, base_url: p.base_url, api_key: '', default_model: p.default_model, keep_alive: p.keep_alive || '4h', context_size: p.context_size || 8192 },
    });

  const save = async () => {
    if (!editing) return;
    const f = editing.form;
    const r = editing.id ? await aiApi.updateProvider(editing.id, f) : await aiApi.createProvider(f);
    if (r.ok) {
      setEditing(null);
      onChanged();
      toast('success', editing.id ? 'Provider updated' : 'Provider added');
    } else toast('error', 'Could not save provider', r.error);
  };

  const remove = async (p: Provider) => {
    const r = await aiApi.deleteProvider(p.id);
    if (r.ok) {
      onChanged();
      toast('success', `Removed ${p.name}`);
    } else toast('error', 'Could not remove', r.error);
  };

  if (editing) {
    const f = editing.form;
    const set = (patch: Partial<ProviderInput>) => setEditing({ ...editing, form: { ...f, ...patch } });
    return (
      <div className="ai-panel">
        <div className="panel">
          <b>{editing.id ? 'Edit provider' : 'Add provider'}</b>
          <label>
            Type
            <div className="segmented wide">
              {(['ollama', 'anthropic', 'openai'] as ProviderKind[]).map((k) => (
                <button key={k} type="button" className={f.kind === k ? 'on' : ''} onClick={() => set({ kind: k })}>
                  {kindLabel(k)}
                </button>
              ))}
            </div>
          </label>
          <label>
            Name
            <input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="e.g. Local Ollama" />
          </label>
          {f.kind !== 'anthropic' && (
            <label>
              Server address
              <input
                value={f.base_url}
                onChange={(e) => set({ base_url: e.target.value })}
                placeholder={f.kind === 'ollama' ? 'http://127.0.0.1:11434' : 'https://api.openai.com/v1'}
              />
            </label>
          )}
          {f.kind !== 'ollama' && (
            <label>
              API key {editing.id && <span className="muted small">(leave blank to keep)</span>}
              <input type="password" value={f.api_key} onChange={(e) => set({ api_key: e.target.value })} placeholder="sk-…" />
            </label>
          )}
          <label>
            Default model {f.kind === 'anthropic' && <span className="muted small">(e.g. claude-opus-5-5)</span>}
            <input value={f.default_model} onChange={(e) => set({ default_model: e.target.value })} placeholder={f.kind === 'ollama' ? 'llama3.1' : ''} />
          </label>
          {f.kind === 'ollama' && (
            <>
              <label>
                Keep model loaded
                <div className="segmented wide">
                  {KEEP_ALIVE.map((k) => (
                    <button key={k.value} type="button" className={f.keep_alive === k.value ? 'on' : ''} onClick={() => set({ keep_alive: k.value })}>
                      {k.label}
                    </button>
                  ))}
                </div>
              </label>
              <label>
                Context window
                <div className="segmented wide">
                  {CONTEXTS.map((c) => (
                    <button key={c} type="button" className={f.context_size === c ? 'on' : ''} onClick={() => set({ context_size: c })}>
                      {c / 1024}K
                    </button>
                  ))}
                </div>
              </label>
            </>
          )}
          <div className="btn-group" style={{ marginTop: 14 }}>
            <button type="button" disabled={!f.name.trim()} onClick={() => void save()}>
              Save
            </button>
            <button type="button" className="ghost" onClick={() => setEditing(null)}>
              Cancel
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="ai-panel">
      <div className="panel">
        <div className="row-head">
          <b>AI providers</b>
          <button type="button" className="small" onClick={startAdd}>
            <Icon name="plus" size={14} /> Add
          </button>
        </div>
        {providers.length === 0 ? (
          <p className="muted small">
            No providers yet. Add a local Ollama server, Anthropic, or any OpenAI-compatible API.
          </p>
        ) : (
          providers.map((p) => (
            <div key={p.id} className="model-row">
              <div>
                <b>{p.name}</b>
                <div className="muted small">
                  {kindLabel(p.kind)}
                  {p.default_model ? ` · ${p.default_model}` : ''}
                  {p.kind === 'ollama' && p.keep_alive ? ` · keep ${p.keep_alive === '-1' ? 'always' : p.keep_alive}` : ''}
                  {p.has_key ? ' · key set' : ''}
                </div>
              </div>
              <div className="btn-group">
                <button type="button" className="ghost icon-btn" onClick={() => startEdit(p)} title="Edit">
                  <Icon name="pencil" size={14} />
                </button>
                <button type="button" className="ghost icon-btn danger" onClick={() => void remove(p)} title="Remove">
                  <Icon name="trash" size={14} />
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
