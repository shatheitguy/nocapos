import { api, getAccessToken, refresh } from './client';

export type ProviderKind = 'ollama' | 'anthropic' | 'openai';

export interface Provider {
  id: string;
  name: string;
  kind: ProviderKind;
  base_url: string;
  has_key: boolean;
  default_model: string;
  keep_alive?: string;
  context_size?: number;
}

export interface ProviderInput {
  name: string;
  kind: ProviderKind;
  base_url: string;
  api_key?: string;
  clear_key?: boolean;
  default_model: string;
  keep_alive: string;
  context_size: number;
}

export interface ModelInfo {
  id: string;
  name?: string;
  size?: number;
  context_window?: number;
  loaded?: boolean;
  ram?: number;
  vram?: number;
  context_in_use?: number;
  expires_at?: string;
}

export interface Conversation {
  id: string;
  title: string;
  provider_id: string;
  model: string;
  pinned: boolean;
  created_at: string;
  updated_at: string;
  preview?: string;
}

export interface Message {
  id: number;
  role: 'user' | 'assistant';
  content: string;
  model?: string;
  error?: string;
  tokens_in?: number;
  tokens_out?: number;
  created_at: string;
}

export interface ConversationDetail extends Conversation {
  messages: Message[];
}

export interface Memory {
  id: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export const aiApi = {
  providers: () => api<Provider[]>('/api/v1/ai/providers'),
  createProvider: (p: ProviderInput) => api<Provider>('/api/v1/ai/providers', { method: 'POST', body: p }),
  updateProvider: (id: string, p: ProviderInput) => api<Provider>(`/api/v1/ai/providers/${id}`, { method: 'PUT', body: p }),
  deleteProvider: (id: string) => api(`/api/v1/ai/providers/${id}`, { method: 'DELETE' }),
  models: (provider: string) => api<ModelInfo[]>(`/api/v1/ai/models?provider=${encodeURIComponent(provider)}`),
  running: (provider: string) => api<ModelInfo[]>(`/api/v1/ai/running?provider=${encodeURIComponent(provider)}`),
  load: (provider: string, model: string) => api('/api/v1/ai/load', { method: 'POST', body: { provider, model } }),
  unload: (provider: string, model: string) => api('/api/v1/ai/unload', { method: 'POST', body: { provider, model } }),

  conversations: (q = '') => api<Conversation[]>(`/api/v1/ai/conversations${q ? `?q=${encodeURIComponent(q)}` : ''}`),
  conversation: (id: string) => api<ConversationDetail>(`/api/v1/ai/conversations/${id}`),
  createConversation: (provider: string, model: string) =>
    api<Conversation>('/api/v1/ai/conversations', { method: 'POST', body: { provider, model } }),
  patchConversation: (id: string, patch: { title?: string; pinned?: boolean }) =>
    api(`/api/v1/ai/conversations/${id}`, { method: 'PATCH', body: patch }),
  deleteConversation: (id: string) => api(`/api/v1/ai/conversations/${id}`, { method: 'DELETE' }),

  memory: () => api<{ enabled: boolean; memories: Memory[] }>('/api/v1/ai/memory'),
  addMemory: (content: string) => api<Memory>('/api/v1/ai/memory', { method: 'POST', body: { content } }),
  updateMemory: (id: string, content: string) => api(`/api/v1/ai/memory/${id}`, { method: 'PUT', body: { content } }),
  deleteMemory: (id: string) => api(`/api/v1/ai/memory/${id}`, { method: 'DELETE' }),
  setMemoryEnabled: (enabled: boolean) => api('/api/v1/ai/memory-enabled', { method: 'PUT', body: { enabled } }),
};

export interface SendCallbacks {
  onDelta: (text: string) => void;
  onDone: (message: Message) => void;
  onError: (message: string) => void;
}

/**
 * Streams a reply as newline-delimited JSON. Uses fetch with a reader rather
 * than the shared client so chunks arrive as they are generated.
 */
export async function sendMessage(conversationId: string, text: string, cb: SendCallbacks, signal: AbortSignal): Promise<void> {
  const run = async (): Promise<Response> => {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    const token = getAccessToken();
    if (token) headers.Authorization = `Bearer ${token}`;
    return fetch(`/api/v1/ai/conversations/${conversationId}/send`, {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      body: JSON.stringify({ text }),
      signal,
    });
  };

  let res: Response;
  try {
    res = await run();
    if (res.status === 401 && (await refresh())) res = await run();
  } catch (e) {
    if ((e as Error).name === 'AbortError') return;
    cb.onError('NoCapOS is not reachable');
    return;
  }

  if (!res.ok || !res.body) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      msg = j?.error?.message ?? msg;
    } catch {
      /* non-JSON */
    }
    cb.onError(msg);
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let nl: number;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        let ev: { delta?: string; done?: boolean; message?: Message; error?: string };
        try {
          ev = JSON.parse(line);
        } catch {
          continue;
        }
        if (ev.delta) cb.onDelta(ev.delta);
        else if (ev.error) cb.onError(ev.error);
        else if (ev.done && ev.message) cb.onDone(ev.message);
      }
    }
  } catch (e) {
    if ((e as Error).name !== 'AbortError') cb.onError('The connection was interrupted');
  }
}

export function kindLabel(kind: ProviderKind): string {
  return kind === 'ollama' ? 'Ollama (local)' : kind === 'anthropic' ? 'Anthropic' : 'OpenAI-compatible';
}
