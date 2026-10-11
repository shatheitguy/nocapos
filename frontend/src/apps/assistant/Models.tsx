import { useCallback, useEffect, useState } from 'react';
import { aiApi, type ModelInfo, type Provider } from '../../api/ai';
import { Icon } from '../../components/Icon';
import { fmtBytes } from '../../lib/format';
import { usePoll } from '../../lib/hooks';
import { toast } from '../../state/toasts';
import { Empty } from '../Monitor';

/** Models panel: catalogue, and (for local providers) what's loaded in RAM with a keep-alive countdown. */
export function Models({ provider }: { provider: Provider | null }) {
  const [models, setModels] = useState<ModelInfo[] | null>(null);
  const [running, setRunning] = useState<ModelInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const isLocal = provider?.kind === 'ollama';

  const load = useCallback(async () => {
    if (!provider) return;
    const r = await aiApi.models(provider.id);
    if (r.ok) {
      setModels(r.data);
      setError(null);
    } else {
      setError(r.error ?? 'Could not list models');
      setModels([]);
    }
    if (isLocal) {
      const rr = await aiApi.running(provider.id);
      if (rr.ok) setRunning(rr.data);
    }
  }, [provider, isLocal]);

  useEffect(() => {
    void load();
  }, [load]);
  usePoll(load, 5000, isLocal);

  if (!provider) return <Empty icon="sparkles" text="Add a provider first." />;

  const act = async (model: string, action: 'load' | 'unload') => {
    setBusy(model);
    const r = action === 'load' ? await aiApi.load(provider.id, model) : await aiApi.unload(provider.id, model);
    setBusy(null);
    if (!r.ok) toast('error', `Could not ${action} ${model}`, r.error);
    else toast('success', action === 'load' ? `Loading ${model} into memory` : `Unloaded ${model}`);
    void load();
  };

  const loaded = (id: string) => running.find((m) => m.id === id);

  return (
    <div className="ai-panel">
      {error && <div className="ai-note">{error}</div>}
      {isLocal && running.length > 0 && (
        <div className="panel">
          <b>In memory now</b>
          <p className="muted small">Models stay loaded so replies start instantly. Keep-alive is set per provider.</p>
          {running.map((m) => (
            <div key={m.id} className="model-row">
              <div>
                <b>{m.id}</b>
                <div className="muted small">
                  {fmtBytes(m.ram)} RAM{m.vram ? ` · ${fmtBytes(m.vram)} on GPU` : ''}
                  {m.context_in_use ? ` · ${(m.context_in_use / 1024).toFixed(0)}K context` : ''}
                  {' · '}
                  <KeepAlive expires={m.expires_at} />
                </div>
              </div>
              <button type="button" className="ghost small" disabled={busy === m.id} onClick={() => act(m.id, 'unload')}>
                Unload
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="panel">
        <b>Available models</b>
        {!models ? (
          <p className="muted">Loading…</p>
        ) : models.length === 0 ? (
          <p className="muted small">
            {isLocal
              ? 'No models yet. Pull one with `ollama pull llama3.1` on the host, then refresh.'
              : 'No models reported by this provider.'}
          </p>
        ) : (
          models.map((m) => (
            <div key={m.id} className="model-row">
              <div>
                <b>{m.name || m.id}</b>
                <div className="muted small">
                  {m.size ? fmtBytes(m.size) : ''}
                  {m.context_window ? `${m.size ? ' · ' : ''}${(m.context_window / 1024).toFixed(0)}K context` : ''}
                  {loaded(m.id) ? ' · in memory' : ''}
                </div>
              </div>
              {isLocal &&
                (loaded(m.id) ? (
                  <span className="chip good">loaded</span>
                ) : (
                  <button type="button" className="ghost small" disabled={busy === m.id} onClick={() => act(m.id, 'load')}>
                    <Icon name="play" size={13} /> Load
                  </button>
                ))}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function KeepAlive({ expires }: { expires?: string }) {
  if (!expires) return <span className="chip good">stays loaded</span>;
  const ms = new Date(expires).getTime() - Date.now();
  if (ms <= 0) return <span className="muted">unloading…</span>;
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  return <span>unloads in {h > 0 ? `${h}h ${m}m` : `${m}m`}</span>;
}
