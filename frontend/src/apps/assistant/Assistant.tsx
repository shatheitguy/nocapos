import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { getUser } from '../../api/client';
import { aiApi, sendMessage, type Conversation, type Message, type ModelInfo, type Provider } from '../../api/ai';
import { Icon, type IconName } from '../../components/Icon';
import { useT } from '../../i18n';
import { fmtAgo } from '../../lib/format';
import { toast } from '../../state/toasts';
import { copyText, renderMarkdown } from './markdown';
import { Models } from './Models';
import { Memory } from './Memory';
import { Providers } from './Providers';

type Tab = 'chat' | 'models' | 'memory' | 'providers';
type TextSize = 'sm' | 'md' | 'lg';

/** A message on screen. Negative ids are local only (not saved, or not yet reloaded). */
interface ChatMessage extends Message {
  stopped?: boolean;
}

interface ViewPrefs {
  size: TextSize;
  dense: boolean;
  enterSends: boolean;
}

// Per-viewer display choices; nothing here needs to follow the account.
const VIEW_KEY = 'nocap.assistant.view';
const DEFAULT_VIEW: ViewPrefs = { size: 'md', dense: false, enterSends: true };

function loadView(): ViewPrefs {
  try {
    const v = JSON.parse(localStorage.getItem(VIEW_KEY) || '{}') as Partial<ViewPrefs>;
    return {
      size: v.size === 'sm' || v.size === 'lg' ? v.size : 'md',
      dense: v.dense === true,
      enterSends: v.enterSends !== false,
    };
  } catch {
    return DEFAULT_VIEW;
  }
}

function saveView(v: ViewPrefs) {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify(v));
  } catch {
    /* private mode: the choice lasts this session only */
  }
}

/** Below this width the chat list becomes a drawer over the chat. */
const NARROW = 720;
/** How close to the bottom (px) still counts as "following" the reply. */
const STICK = 48;

let localSeq = 0;
const localId = () => --localSeq;
const now = () => new Date().toISOString();

export function Assistant() {
  const t = useT();
  const isAdmin = getUser()?.role === 'admin';
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const [narrow, setNarrow] = useState(false);
  const [sidebar, setSidebar] = useState(true);
  const [tab, setTab] = useState<Tab>('chat');
  const [view, setViewState] = useState<ViewPrefs>(loadView);
  const [optionsOpen, setOptionsOpen] = useState(false);

  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerId, setProviderId] = useState('');
  const [model, setModel] = useState('');
  const [models, setModels] = useState<ModelInfo[]>([]);

  const [convs, setConvs] = useState<Conversation[]>([]);
  const [query, setQuery] = useState('');
  const queryRef = useRef('');
  const [activeId, setActiveId] = useState<string | null>(null);
  const activeRef = useRef<string | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [draft, setDraft] = useState('');
  // null: not generating; '': waiting for the first words.
  const [streaming, setStreaming] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [atBottom, setAtBottom] = useState(true);

  const abortRef = useRef<AbortController | null>(null);
  const bufRef = useRef('');
  const rafRef = useRef(0);
  const stickRef = useRef(true);

  const provider = useMemo(() => providers.find((p) => p.id === providerId) ?? providers[0] ?? null, [providers, providerId]);
  const activeConv = convs.find((c) => c.id === activeId) ?? null;

  const setView = (patch: Partial<ViewPrefs>) =>
    setViewState((v) => {
      const next = { ...v, ...patch };
      saveView(next);
      return next;
    });

  // ---- layout: the window's own width decides drawer vs side-by-side ----
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    let was: boolean | null = null;
    const apply = (w: number) => {
      const is = w > 0 && w < NARROW;
      if (is === was) return;
      was = is;
      setNarrow(is);
      setSidebar(!is);
    };
    apply(el.getBoundingClientRect().width);
    const ro = new ResizeObserver((entries) => apply(entries[0].contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // ---- data ----
  const loadProviders = useCallback(async () => {
    const r = await aiApi.providers();
    if (r.ok) {
      setProviders(r.data);
      setProviderId((cur) => (cur && r.data.some((p) => p.id === cur) ? cur : (r.data[0]?.id ?? '')));
    }
  }, []);

  const loadConvs = useCallback(async (q: string) => {
    const r = await aiApi.conversations(q);
    if (r.ok && q === queryRef.current) setConvs(r.data);
  }, []);
  const refreshConvs = useCallback(() => void loadConvs(queryRef.current), [loadConvs]);

  useEffect(() => {
    void loadProviders();
  }, [loadProviders]);

  useEffect(() => {
    queryRef.current = query;
    const h = window.setTimeout(() => void loadConvs(query), query ? 250 : 0);
    return () => window.clearTimeout(h);
  }, [query, loadConvs]);

  useEffect(() => {
    if (!provider) {
      setModels([]);
      return;
    }
    let live = true;
    void aiApi.models(provider.id).then((r) => {
      if (live) setModels(r.ok ? r.data : []);
    });
    return () => {
      live = false;
    };
  }, [provider]);

  // ---- scrolling: follow the reply only while the reader is at the bottom ----
  const scrollToBottom = useCallback((smooth = false) => {
    const el = listRef.current;
    if (!el) return;
    stickRef.current = true;
    setAtBottom(true);
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' });
  }, []);

  const onListScroll = () => {
    const el = listRef.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < STICK;
    stickRef.current = near;
    setAtBottom(near);
  };

  useLayoutEffect(() => {
    const el = listRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  }, [messages, streaming, tab]);

  // Keep the bottom in view when the list itself resizes (composer grows,
  // window resized, phone keyboard opens).
  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      if (stickRef.current) el.scrollTop = el.scrollHeight;
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [tab]);

  // Composer grows with its text, up to about a third of the window.
  useLayoutEffect(() => {
    const ta = inputRef.current;
    if (!ta) return;
    const max = Math.max(96, Math.min(220, (rootRef.current?.clientHeight ?? 600) * 0.32));
    ta.style.height = 'auto';
    ta.style.height = `${Math.min(ta.scrollHeight, max)}px`;
    ta.style.overflowY = ta.scrollHeight > max ? 'auto' : 'hidden';
  }, [draft, tab]);

  useEffect(
    () => () => {
      abortRef.current?.abort();
      cancelAnimationFrame(rafRef.current);
    },
    [],
  );

  // ---- conversations ----
  const stop = () => abortRef.current?.abort();

  const selectConversation = (id: string | null) => {
    if (busy) stop();
    activeRef.current = id;
    setActiveId(id);
    setTab('chat');
    stickRef.current = true;
    setAtBottom(true);
    if (narrow) setSidebar(false);
  };

  const openConversation = async (id: string) => {
    selectConversation(id);
    const r = await aiApi.conversation(id);
    if (!r.ok) {
      toast('error', t('ai.toastLoadFailed'), r.error);
      return;
    }
    if (activeRef.current !== id) return;
    setMessages(r.data.messages);
    if (r.data.provider_id) setProviderId(r.data.provider_id);
    setModel(r.data.model ?? '');
  };

  const newChat = () => {
    selectConversation(null);
    setMessages([]);
    setStreaming(null);
    window.setTimeout(() => inputRef.current?.focus(), 0);
  };

  const renameConv = async (id: string, title: string) => {
    const r = await aiApi.patchConversation(id, { title });
    if (!r.ok) toast('error', t('ai.toastRenameFailed'), r.error);
    refreshConvs();
  };

  const pinConv = async (c: Conversation) => {
    const r = await aiApi.patchConversation(c.id, { pinned: !c.pinned });
    if (!r.ok) toast('error', t('ai.toastActionFailed'), r.error);
    refreshConvs();
  };

  const deleteConv = async (id: string) => {
    const r = await aiApi.deleteConversation(id);
    if (!r.ok) {
      toast('error', t('ai.toastActionFailed'), r.error);
      return;
    }
    if (activeRef.current === id) newChat();
    refreshConvs();
  };

  // ---- sending ----
  /**
   * The saved messages after a turn (so every message has its real id); if
   * this turn's reply never got saved (stopped, or failed early), it stays on
   * screen after them. null: keep what is shown and add the tail.
   */
  const reloaded = async (convId: string, sinceId: number, user: ChatMessage, tail: ChatMessage | null): Promise<ChatMessage[] | null> => {
    const r = await aiApi.conversation(convId);
    if (!r.ok) return null;
    const saved: ChatMessage[] = r.data.messages;
    const fresh = saved.filter((m) => m.id > sinceId);
    if (fresh.some((m) => m.role === 'assistant') || !tail) return saved;
    return [...saved, ...(fresh.some((m) => m.role === 'user') ? [] : [user]), tail];
  };

  const run = async (convId: string, text: string, base: ChatMessage[]) => {
    const sinceId = base.reduce((max, m) => Math.max(max, m.id), 0);
    const user: ChatMessage = { id: localId(), role: 'user', content: text, created_at: now() };
    setMessages([...base, user]);
    stickRef.current = true;
    setAtBottom(true);
    setBusy(true);
    bufRef.current = '';
    setStreaming('');
    const ctrl = new AbortController();
    abortRef.current = ctrl;

    // Filled in by the stream callbacks.
    const turn: { tail: ChatMessage | null; done: boolean } = { tail: null, done: false };
    await sendMessage(
      convId,
      text,
      {
        onDelta: (d) => {
          bufRef.current += d;
          if (!rafRef.current)
            rafRef.current = requestAnimationFrame(() => {
              rafRef.current = 0;
              setStreaming(bufRef.current);
            });
        },
        onDone: () => {
          turn.done = true;
        },
        onError: (err) => {
          turn.tail = { id: localId(), role: 'assistant', content: bufRef.current, error: err, created_at: now() };
        },
      },
      ctrl.signal,
    );
    cancelAnimationFrame(rafRef.current);
    rafRef.current = 0;
    if (!turn.done && !turn.tail && ctrl.signal.aborted)
      turn.tail = bufRef.current ? { id: localId(), role: 'assistant', content: bufRef.current, stopped: true, created_at: now() } : null;
    abortRef.current = null;

    const tail = turn.tail;
    const next = activeRef.current === convId ? await reloaded(convId, sinceId, user, tail) : null;
    // Swap the live reply for the saved one in a single render: no flicker or jump.
    if (activeRef.current === convId) {
      if (next) setMessages(next);
      else if (tail) setMessages((m) => [...m, tail]);
    }
    setStreaming(null);
    setBusy(false);
    refreshConvs();
  };

  const ensureProvider = () => {
    if (provider) return true;
    toast('error', t('ai.toastNoProvider'), isAdmin ? t('ai.toastNoProviderAdmin') : t('ai.toastNoProviderUser'));
    setTab('providers');
    return false;
  };

  const send = async (raw = draft) => {
    const text = raw.trim();
    if (!text || busy || !ensureProvider() || !provider) return;
    setDraft('');
    let convId = activeId;
    if (!convId) {
      const r = await aiApi.createConversation(provider.id, model);
      if (!r.ok) {
        setDraft(raw);
        toast('error', t('ai.toastStartFailed'), r.error);
        return;
      }
      convId = r.data.id;
      activeRef.current = convId;
      setActiveId(convId);
    }
    await run(convId, text, messages);
  };

  /** Drops messages[index] and everything after it, on the server too when saved. */
  const cutFrom = async (index: number): Promise<ChatMessage[] | null> => {
    const msg = messages[index];
    if (!msg || !activeId) return null;
    const firstSaved = messages.slice(index).find((m) => m.id > 0);
    if (firstSaved) {
      const r = await aiApi.deleteMessagesFrom(activeId, firstSaved.id);
      if (!r.ok) {
        toast('error', t('ai.toastActionFailed'), r.error);
        return null;
      }
    }
    const kept = messages.slice(0, index);
    setMessages(kept);
    return kept;
  };

  const resendFrom = async (userIndex: number, text: string) => {
    if (busy || !activeId || !ensureProvider()) return;
    const convId = activeId;
    const kept = await cutFrom(userIndex);
    if (kept) await run(convId, text, kept);
  };

  const regenerate = (assistantIndex: number) => {
    for (let i = assistantIndex - 1; i >= 0; i--) {
      if (messages[i].role === 'user') return void resendFrom(i, messages[i].content);
    }
  };

  const deleteFrom = async (index: number) => {
    if (busy) return;
    await cutFrom(index);
    refreshConvs();
  };

  const chooseModel = async (pid: string, m: string) => {
    setProviderId(pid);
    setModel(m);
    if (!activeId) return;
    const r = await aiApi.patchConversation(activeId, { provider: pid, model: m });
    if (!r.ok) toast('error', t('ai.toastModelFailed'), r.error);
  };

  // ---- keys ----
  const onRootKey = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    if (optionsOpen) setOptionsOpen(false);
    else if (busy) stop();
    else if (narrow && sidebar) setSidebar(false);
    else return;
    e.stopPropagation();
  };

  const onComposerKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Enter' || e.nativeEvent.isComposing) return;
    const mod = e.ctrlKey || e.metaKey;
    if (mod || (view.enterSends && !e.shiftKey)) {
      e.preventDefault();
      void send();
    }
  };

  const tabs: { id: Tab; label: string; icon: IconName }[] = [
    { id: 'chat', label: t('ai.tabChat'), icon: 'sparkles' },
    { id: 'models', label: t('ai.tabModels'), icon: 'brain' },
    { id: 'memory', label: t('ai.tabMemory'), icon: 'pin' },
    { id: 'providers', label: t('ai.tabProviders'), icon: 'settings' },
  ];

  const pinned = convs.filter((c) => c.pinned);
  const recent = convs.filter((c) => !c.pinned);
  const lastAssistant = messages.reduce((acc, m, i) => (m.role === 'assistant' ? i : acc), -1);
  const suggestions = [t('ai.suggest1'), t('ai.suggest2'), t('ai.suggest3')];

  return (
    <div
      ref={rootRef}
      className={`assistant${narrow ? ' narrow' : ''}${view.dense ? ' dense' : ''}`}
      data-size={view.size}
      onKeyDown={onRootKey}
    >
      {sidebar && narrow && <div className="ai-scrim" onClick={() => setSidebar(false)} />}
      {sidebar && (
        <aside className={`ai-sidebar${narrow ? ' drawer' : ''}`} aria-label={t('ai.chats')}>
          <div className="ai-side-head">
            <button type="button" className="ai-new" onClick={newChat}>
              <Icon name="plus" size={16} /> {t('ai.newChat')}
            </button>
            {narrow && (
              <button type="button" className="ghost icon-btn" onClick={() => setSidebar(false)} title={t('ai.closeSidebar')} aria-label={t('ai.closeSidebar')}>
                <Icon name="close" size={16} />
              </button>
            )}
          </div>
          <label className="ai-search">
            <Icon name="search" size={14} />
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t('ai.searchChats')} aria-label={t('ai.searchChats')} />
            {query && (
              <button type="button" className="ghost icon-btn" onClick={() => setQuery('')} title={t('ai.clearSearch')} aria-label={t('ai.clearSearch')}>
                <Icon name="close" size={12} />
              </button>
            )}
          </label>
          <div className="ai-convs">
            {convs.length === 0 && <p className="muted small ai-empty-list">{query ? t('ai.noMatches') : t('ai.noChats')}</p>}
            {pinned.length > 0 && <div className="ai-conv-group">{t('ai.pinned')}</div>}
            {pinned.map((c) => (
              <ConvRow key={c.id} conv={c} active={c.id === activeId} onOpen={() => void openConversation(c.id)} onRename={(s) => void renameConv(c.id, s)} onPin={() => void pinConv(c)} onDelete={() => void deleteConv(c.id)} />
            ))}
            {pinned.length > 0 && recent.length > 0 && <div className="ai-conv-group">{t('ai.recent')}</div>}
            {recent.map((c) => (
              <ConvRow key={c.id} conv={c} active={c.id === activeId} onOpen={() => void openConversation(c.id)} onRename={(s) => void renameConv(c.id, s)} onPin={() => void pinConv(c)} onDelete={() => void deleteConv(c.id)} />
            ))}
          </div>
        </aside>
      )}

      <div className="ai-main">
        <div className="ai-topbar">
          <button type="button" className="ghost icon-btn" onClick={() => setSidebar((v) => !v)} title={t('ai.toggleSidebar')} aria-label={t('ai.toggleSidebar')}>
            <Icon name="menu" size={16} />
          </button>
          <div className="ai-tabs" role="tablist">
            {tabs.map((tb) => (
              <button key={tb.id} type="button" role="tab" aria-selected={tab === tb.id} className={tab === tb.id ? 'on' : ''} onClick={() => setTab(tb.id)} title={tb.label}>
                <Icon name={tb.icon} size={14} />
                <span className="ai-tab-label">{tb.label}</span>
              </button>
            ))}
          </div>
          <div className="ai-title" title={activeConv?.title || undefined}>
            {tab === 'chat' && (activeConv?.title || (activeId ? t('ai.untitled') : ''))}
          </div>
          <div className="ai-options-anchor">
            <button
              type="button"
              className={`ghost icon-btn${optionsOpen ? ' on' : ''}`}
              onClick={() => setOptionsOpen((v) => !v)}
              title={t('ai.viewOptions')}
              aria-label={t('ai.viewOptions')}
              aria-expanded={optionsOpen}
            >
              <Icon name="sliders" size={16} />
            </button>
            {optionsOpen && <ViewOptions view={view} onChange={setView} onClose={() => setOptionsOpen(false)} />}
          </div>
        </div>

        {tab === 'chat' && (
          <>
            <div className="ai-thread">
              <div className="ai-messages" ref={listRef} onScroll={onListScroll} role="log" aria-live="polite">
                {messages.length === 0 && streaming === null && (
                  <div className="ai-welcome">
                    <span className="ai-orb">
                      <Icon name="sparkles" size={30} />
                    </span>
                    <h2>{t('ai.welcomeTitle')}</h2>
                    <p className="muted">
                      {provider ? t('ai.welcomeBody', { name: provider.name }) : isAdmin ? t('ai.welcomeNoProviderAdmin') : t('ai.welcomeNoProvider')}
                    </p>
                    {provider && (
                      <div className="ai-suggest">
                        {suggestions.map((s) => (
                          <button key={s} type="button" className="ai-chip" onClick={() => void send(s)}>
                            {s}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
                {messages.map((m, i) => (
                  <MessageRow
                    key={m.id}
                    message={m}
                    last={i === messages.length - 1 && streaming === null}
                    busy={busy}
                    canRegenerate={m.role === 'assistant' && i === lastAssistant && streaming === null}
                    onRegenerate={() => regenerate(i)}
                    onEditSend={(text) => void resendFrom(i, text)}
                    onDelete={() => void deleteFrom(i)}
                  />
                ))}
                {streaming !== null && <MessageRow message={{ id: 0, role: 'assistant', content: streaming, created_at: '' }} streaming last busy />}
              </div>
              {!atBottom && (
                <button type="button" className="ai-jump" onClick={() => scrollToBottom(true)}>
                  <Icon name="arrowDown" size={14} /> {t('ai.jumpLatest')}
                </button>
              )}
            </div>

            <div className="ai-compose-wrap">
              <div className={`ai-compose${!provider ? ' off' : ''}`}>
                <textarea
                  ref={inputRef}
                  value={draft}
                  placeholder={provider ? t('ai.placeholder') : t('ai.placeholderNoProvider')}
                  rows={1}
                  disabled={!provider}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={onComposerKey}
                  aria-label={t('ai.placeholder')}
                />
                <div className="ai-compose-bar">
                  <ModelPicker providers={providers} provider={provider} model={model} models={models} onChange={(p, m) => void chooseModel(p, m)} />
                  <span className="ai-hint muted">{view.enterSends ? t('ai.hintEnter') : t('ai.hintCtrlEnter')}</span>
                  {busy ? (
                    <button type="button" className="ai-send stop" onClick={stop} title={t('ai.stop')} aria-label={t('ai.stop')}>
                      <Icon name="stop" size={15} />
                    </button>
                  ) : (
                    <button type="button" className="ai-send" disabled={!draft.trim() || !provider} onClick={() => void send()} title={t('ai.send')} aria-label={t('ai.send')}>
                      <Icon name="send" size={15} />
                    </button>
                  )}
                </div>
              </div>
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

// ---------- pieces ----------

function ConvRow({
  conv,
  active,
  onOpen,
  onRename,
  onPin,
  onDelete,
}: {
  conv: Conversation;
  active: boolean;
  onOpen: () => void;
  onRename: (title: string) => void;
  onPin: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const [renaming, setRenaming] = useState(false);
  const [title, setTitle] = useState('');
  const [confirm, setConfirm] = useState(false);

  useEffect(() => {
    if (!confirm) return;
    const h = window.setTimeout(() => setConfirm(false), 3000);
    return () => window.clearTimeout(h);
  }, [confirm]);

  const name = conv.title || t('ai.untitled');

  if (renaming) {
    const save = () => {
      setRenaming(false);
      const v = title.trim();
      if (v && v !== conv.title) onRename(v);
    };
    return (
      <div className={`ai-conv editing${active ? ' on' : ''}`}>
        <input
          autoFocus
          value={title}
          maxLength={200}
          aria-label={t('ai.rename')}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={save}
          onKeyDown={(e) => {
            if (e.key === 'Enter') save();
            if (e.key === 'Escape') {
              e.stopPropagation();
              setRenaming(false);
            }
          }}
        />
      </div>
    );
  }

  return (
    <div className={`ai-conv${active ? ' on' : ''}`}>
      <button type="button" className="ai-conv-open" onClick={onOpen} onDoubleClick={() => {
          setTitle(conv.title);
          setRenaming(true);
        }} title={name}>
        <b>{name}</b>
        <span className="muted small">{fmtAgo(conv.updated_at)}</span>
      </button>
      <div className="ai-conv-actions">
        <button type="button" className={`ghost icon-btn${conv.pinned ? ' on' : ''}`} title={conv.pinned ? t('ai.unpin') : t('ai.pin')} aria-label={conv.pinned ? t('ai.unpin') : t('ai.pin')} onClick={onPin}>
          <Icon name="pin" size={13} />
        </button>
        <button
          type="button"
          className="ghost icon-btn"
          title={t('ai.rename')}
          aria-label={t('ai.rename')}
          onClick={() => {
            setTitle(conv.title);
            setRenaming(true);
          }}
        >
          <Icon name="pencil" size={13} />
        </button>
        <button
          type="button"
          className={`ghost icon-btn${confirm ? ' danger' : ''}`}
          title={confirm ? t('ai.confirmDelete') : t('ai.delete')}
          aria-label={confirm ? t('ai.confirmDelete') : t('ai.delete')}
          onClick={() => (confirm ? onDelete() : setConfirm(true))}
        >
          <Icon name={confirm ? 'check' : 'trash'} size={13} />
        </button>
      </div>
    </div>
  );
}

function MessageRow({
  message,
  streaming,
  last,
  busy,
  canRegenerate,
  onRegenerate,
  onEditSend,
  onDelete,
}: {
  message: ChatMessage;
  streaming?: boolean;
  last?: boolean;
  busy: boolean;
  canRegenerate?: boolean;
  onRegenerate?: () => void;
  onEditSend?: (text: string) => void;
  onDelete?: () => void;
}) {
  const t = useT();
  const isUser = message.role === 'user';
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState('');
  const [copied, setCopied] = useState(false);
  const [confirm, setConfirm] = useState(false);

  useEffect(() => {
    if (!confirm) return;
    const h = window.setTimeout(() => setConfirm(false), 3000);
    return () => window.clearTimeout(h);
  }, [confirm]);

  const copy = () =>
    void copyText(message.content).then((ok) => {
      if (!ok) return toast('error', t('ai.toastCopyFailed'));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });

  let body: ReactNode;
  if (editing) {
    const submit = () => {
      const v = text.trim();
      if (!v) return;
      setEditing(false);
      onEditSend?.(v);
    };
    body = (
      <div className="ai-edit">
        <textarea
          autoFocus
          value={text}
          rows={Math.min(10, Math.max(2, text.split('\n').length))}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
              e.preventDefault();
              submit();
            }
            if (e.key === 'Escape') {
              e.stopPropagation();
              setEditing(false);
            }
          }}
          aria-label={t('ai.edit')}
        />
        <div className="ai-edit-bar">
          <button type="button" className="ghost small" onClick={() => setEditing(false)}>
            {t('ai.cancel')}
          </button>
          <button type="button" className="small" disabled={!text.trim() || busy} onClick={submit}>
            {t('ai.saveSend')}
          </button>
        </div>
      </div>
    );
  } else if (isUser) {
    body = <div className="ai-user-text">{message.content}</div>;
  } else {
    body = (
      <>
        {message.content ? (
          <div className="ai-md">
            {renderMarkdown(message.content)}
            {streaming && <span className="ai-caret" />}
          </div>
        ) : streaming ? (
          <div className="ai-thinking" aria-label={t('ai.thinking')}>
            <span />
            <span />
            <span />
          </div>
        ) : null}
        {message.error && (
          <div className="ai-msg-error">
            <Icon name="alert" size={15} /> <span>{message.error}</span>
          </div>
        )}
      </>
    );
  }

  const action = (icon: IconName, label: string, onClick: () => void, extra = '') => (
    <button type="button" className={`ghost icon-btn${extra}`} title={label} aria-label={label} onClick={onClick}>
      <Icon name={icon} size={13} />
    </button>
  );

  const showActions = !streaming && !editing;
  const usage = !isUser && (message.tokens_in || message.tokens_out);

  return (
    <div className={`ai-msg ${isUser ? 'user' : 'assistant'}${last ? ' last' : ''}`}>
      {!isUser && (
        <span className="ai-avatar" aria-hidden>
          <Icon name="sparkles" size={14} />
        </span>
      )}
      <div className={`ai-col${editing ? ' wide' : ''}`}>
        <div className="ai-bubble">{body}</div>
        {showActions && (
          <div className="ai-actions">
            {message.content && action(copied ? 'check' : 'copy', copied ? t('ai.copied') : t('ai.copy'), copy)}
            {isUser &&
              !busy &&
              action('pencil', t('ai.edit'), () => {
                setText(message.content);
                setEditing(true);
              })}
            {canRegenerate && !busy && action('restart', message.error || message.stopped ? t('ai.retry') : t('ai.regenerate'), () => onRegenerate?.())}
            {!busy &&
              onDelete &&
              action(confirm ? 'check' : 'trash', confirm ? t('ai.confirmDeleteMsg') : t('ai.deleteMsg'), () => (confirm ? onDelete() : setConfirm(true)), confirm ? ' danger' : '')}
            {message.stopped && <span className="ai-meta">{t('ai.stopped')}</span>}
            {usage ? (
              <span className="ai-meta" title={message.model}>
                {message.model ? `${message.model} · ` : ''}
                {t('ai.usage', { in: message.tokens_in ?? 0, out: message.tokens_out ?? 0 })}
              </span>
            ) : null}
          </div>
        )}
      </div>
    </div>
  );
}

function ModelPicker({
  providers,
  provider,
  model,
  models,
  onChange,
}: {
  providers: Provider[];
  provider: Provider | null;
  model: string;
  models: ModelInfo[];
  onChange: (providerId: string, model: string) => void;
}) {
  const t = useT();
  if (!provider) return null;
  const ids = models.map((m) => m.id);
  if (model && !ids.includes(model)) ids.unshift(model);
  const def = provider.default_model;
  return (
    <div className="ai-picker">
      {providers.length > 1 && (
        <select value={provider.id} onChange={(e) => onChange(e.target.value, '')} title={t('ai.provider')} aria-label={t('ai.provider')}>
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      )}
      <select value={model} onChange={(e) => onChange(provider.id, e.target.value)} title={t('ai.model')} aria-label={t('ai.model')}>
        <option value="">{def ? t('ai.defaultModel', { model: def }) : t('ai.defaultModelPlain')}</option>
        {ids
          .filter((id) => id !== '')
          .map((id) => (
            <option key={id} value={id}>
              {id}
            </option>
          ))}
      </select>
    </div>
  );
}

function ViewOptions({ view, onChange, onClose }: { view: ViewPrefs; onChange: (p: Partial<ViewPrefs>) => void; onClose: () => void }) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const away = (e: PointerEvent) => {
      const anchor = ref.current?.parentElement;
      if (anchor && !anchor.contains(e.target as Node)) onClose();
    };
    document.addEventListener('pointerdown', away);
    return () => document.removeEventListener('pointerdown', away);
  }, [onClose]);

  const sizes: { id: TextSize; label: string }[] = [
    { id: 'sm', label: t('ai.sizeS') },
    { id: 'md', label: t('ai.sizeM') },
    { id: 'lg', label: t('ai.sizeL') },
  ];
  return (
    <div className="ai-popover" ref={ref} role="dialog" aria-label={t('ai.viewOptions')}>
      <div className="ai-pop-label">{t('ai.textSize')}</div>
      <div className="ai-seg">
        {sizes.map((s) => (
          <button key={s.id} type="button" className={view.size === s.id ? 'on' : ''} onClick={() => onChange({ size: s.id })}>
            {s.label}
          </button>
        ))}
      </div>
      <label className="ai-pop-row">
        <input type="checkbox" checked={view.dense} onChange={(e) => onChange({ dense: e.target.checked })} />
        {t('ai.dense')}
      </label>
      <label className="ai-pop-row">
        <input type="checkbox" checked={view.enterSends} onChange={(e) => onChange({ enterSends: e.target.checked })} />
        {t('ai.enterSends')}
      </label>
      <div className="ai-pop-label">{t('ai.shortcuts')}</div>
      <dl className="ai-keys">
        <dt>{view.enterSends ? 'Enter' : 'Ctrl+Enter'}</dt>
        <dd>{t('ai.kbSend')}</dd>
        <dt>{view.enterSends ? 'Shift+Enter' : 'Enter'}</dt>
        <dd>{t('ai.kbNewline')}</dd>
        <dt>Esc</dt>
        <dd>{t('ai.kbStop')}</dd>
      </dl>
    </div>
  );
}
