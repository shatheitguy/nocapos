import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { getUser } from '../../api/client';
import { aiApi, sendMessage, type Conversation, type Message, type Provider } from '../../api/ai';
import { Icon } from '../../components/Icon';
import { fmtAgo } from '../../lib/format';
import { toast } from '../../state/toasts';
import { renderMarkdown } from './markdown';
import { Models } from './Models';
import { Memory } from './Memory';
import { Providers } from './Providers';

type Tab = 'chat' | 'models' | 'memory' | 'providers';

export function Assistant() {
  const isAdmin = getUser()?.role === 'admin';
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerId, setProviderId] = useState('');
  const [model, setModel] = useState('');
  const [tab, setTab] = useState<Tab>('chat');
  const [convs, setConvs] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streaming, setStreaming] = useState('');
  const [busy, setBusy] = useState(false);
  const [sidebar, setSidebar] = useState(true);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const provider = useMemo(() => providers.find((p) => p.id === providerId) ?? providers[0] ?? null, [providers, providerId]);

  const loadProviders = useCallback(async () => {
    const r = await aiApi.providers();
    if (r.ok) {
      setProviders(r.data);
      if (r.data.length && !providerId) setProviderId(r.data[0].id);
    }
  }, [providerId]);

  const loadConvs = useCallback(async () => {
    const r = await aiApi.conversations();
    if (r.ok) setConvs(r.data);
  }, []);

  useEffect(() => {
    void loadProviders();
    void loadConvs();
  }, [loadProviders, loadConvs]);

  // auto-scroll on new content
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages, streaming]);

  const openConversation = async (id: string) => {
    setActiveId(id);
    setTab('chat');
    const r = await aiApi.conversation(id);
    if (r.ok) {
      setMessages(r.data.messages);
      if (r.data.provider_id) setProviderId(r.data.provider_id);
      if (r.data.model) setModel(r.data.model);
    }
  };

  const newChat = () => {
    setActiveId(null);
    setMessages([]);
    setStreaming('');
    setTab('chat');
  };

  const send = async () => {
    const text = draft.trim();
    if (!text || busy) return;
    if (!provider) {
      toast('error', 'Add an AI provider first', isAdmin ? 'Open the Providers tab.' : 'Ask an administrator.');
      setTab('providers');
      return;
    }
    setDraft('');

    let convId = activeId;
    if (!convId) {
      const r = await aiApi.createConversation(provider.id, model);
      if (!r.ok) {
        toast('error', 'Could not start chat', r.error);
        return;
      }
      convId = r.data.id;
      setActiveId(convId);
    }

    const userMsg: Message = { id: Date.now(), role: 'user', content: text, created_at: new Date().toISOString() };
    setMessages((m) => [...m, userMsg]);
    setBusy(true);
    setStreaming('');
    const ctrl = new AbortController();
    abortRef.current = ctrl;

    await sendMessage(
      convId,
      text,
      {
        onDelta: (d) => setStreaming((s) => s + d),
        onDone: (msg) => {
          setMessages((m) => [...m, msg]);
          setStreaming('');
          void loadConvs();
        },
        onError: (err) => {
          setMessages((m) => [...m, { id: Date.now() + 1, role: 'assistant', content: '', error: err, created_at: new Date().toISOString() }]);
          setStreaming('');
        },
      },
      ctrl.signal,
    );
    setBusy(false);
    abortRef.current = null;
  };

  const stop = () => abortRef.current?.abort();

  const deleteConv = async (id: string) => {
    await aiApi.deleteConversation(id);
    if (activeId === id) newChat();
    void loadConvs();
  };

  const tabs: { id: Tab; label: string; icon: 'sparkles' | 'brain' | 'pin' | 'settings'; adminHint?: boolean }[] = [
    { id: 'chat', label: 'Chat', icon: 'sparkles' },
    { id: 'models', label: 'Models', icon: 'brain' },
    { id: 'memory', label: 'Memory', icon: 'pin' },
    { id: 'providers', label: 'Providers', icon: 'settings' },
  ];

  return (
    <div className="assistant">
      {sidebar && (
        <aside className="ai-sidebar">
          <button type="button" className="ai-new" onClick={newChat}>
            <Icon name="plus" size={16} /> New chat
          </button>
          <div className="ai-convs">
            {convs.length === 0 && <p className="muted small ai-empty-list">No conversations yet.</p>}
            {convs.map((c) => (
              <div key={c.id} className={`ai-conv ${activeId === c.id ? 'on' : ''}`} onClick={() => void openConversation(c.id)}>
                <div className="ai-conv-text">
                  <b>{c.title || 'New chat'}</b>
                  <div className="muted small">{fmtAgo(c.updated_at)}</div>
                </div>
                <button
                  type="button"
                  className="ghost icon-btn"
                  title="Delete"
                  onClick={(e) => {
                    e.stopPropagation();
                    void deleteConv(c.id);
                  }}
                >
                  <Icon name="trash" size={13} />
                </button>
              </div>
            ))}
          </div>
        </aside>
      )}

      <div className="ai-main">
        <div className="ai-topbar">
          <button type="button" className="ghost icon-btn" onClick={() => setSidebar((v) => !v)} title="Toggle sidebar">
            <Icon name="menu" size={16} />
          </button>
          <div className="ai-tabs">
            {tabs.map((t) => (
              <button key={t.id} type="button" className={tab === t.id ? 'on' : ''} onClick={() => setTab(t.id)}>
                <Icon name={t.icon} size={14} /> {t.label}
              </button>
            ))}
          </div>
          <span className="spacer" />
          {provider && (
            <span className="ai-provider-pill">
              {provider.name}
              {(model || provider.default_model) && ` · ${model || provider.default_model}`}
            </span>
          )}
        </div>

        {tab === 'chat' && (
          <>
            <div className="ai-messages" ref={scrollRef}>
              {messages.length === 0 && !streaming && (
                <div className="ai-welcome">
                  <span className="ai-orb">
                    <Icon name="sparkles" size={30} />
                  </span>
                  <h2>How can I help?</h2>
                  <p className="muted">
                    {provider
                      ? `Chatting with ${provider.name}. Ask anything, or tell me to “remember” something.`
                      : isAdmin
                        ? 'Add a provider in the Providers tab to get started.'
                        : 'No AI provider is set up yet. Ask an administrator to add one.'}
                  </p>
                </div>
              )}
              {messages.map((m) => (
                <MessageBubble key={m.id} message={m} />
              ))}
              {streaming && <MessageBubble message={{ id: -1, role: 'assistant', content: streaming, created_at: '' }} streaming />}
            </div>

            <div className="ai-compose">
              <textarea
                value={draft}
                placeholder={provider ? 'Message the assistant…' : 'Set up a provider first'}
                rows={1}
                disabled={!provider}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault();
                    void send();
                  }
                }}
              />
              {busy ? (
                <button type="button" className="ai-send stop" onClick={stop} title="Stop">
                  <Icon name="stop" size={16} />
                </button>
              ) : (
                <button type="button" className="ai-send" disabled={!draft.trim() || !provider} onClick={() => void send()} title="Send">
                  <Icon name="send" size={16} />
                </button>
              )}
            </div>
          </>
        )}

        {tab === 'models' && <Models provider={provider} />}
        {tab === 'memory' && <Memory />}
        {tab === 'providers' && <Providers providers={providers} isAdmin={isAdmin} onChanged={() => void loadProviders()} />}
      </div>
    </div>
  );
}

function MessageBubble({ message, streaming }: { message: Message; streaming?: boolean }) {
  const isUser = message.role === 'user';
  return (
    <div className={`ai-msg ${isUser ? 'user' : 'assistant'}`}>
      {!isUser && (
        <span className="ai-avatar">
          <Icon name="sparkles" size={15} />
        </span>
      )}
      <div className="ai-bubble">
        {message.error ? (
          <div className="ai-msg-error">
            <Icon name="alert" size={15} /> {message.error}
          </div>
        ) : isUser ? (
          <span className="ai-user-text">{message.content}</span>
        ) : (
          <div className="ai-md">
            {renderMarkdown(message.content)}
            {streaming && <span className="ai-caret" />}
          </div>
        )}
        {!isUser && !streaming && (message.tokens_in || message.tokens_out) ? (
          <div className="ai-usage muted">
            {message.model ? `${message.model} · ` : ''}
            {message.tokens_in ?? 0} in / {message.tokens_out ?? 0} out
          </div>
        ) : null}
      </div>
    </div>
  );
}
