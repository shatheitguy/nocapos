import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { savedComputers, type SavedComputer, type SavedComputerInput } from '../../api/rdp';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';
import { confirmDialog } from '../../state/confirm';
import { userKey } from '../../state/prefs';
import { toast } from '../../state/toasts';
import {
  COLOR_DEPTHS,
  DEFAULT_PORT,
  EXPERIENCE,
  FIXED_SIZES,
  SCALES,
  addHistory,
  blankForm,
  optionsFor,
  sortComputers,
  speedOf,
  splitHost,
  userLabel,
  validHost,
  withSpeed,
  type ConnForm,
  type RemoteOptions,
  type RemoteProtocol,
  type SortKey,
  type Speed,
} from './rdModel';

/** What the app needs to start a session. */
export interface SessionStart {
  form: ConnForm;
  connectionId?: string;
  /** The saved computer has a remembered password: don't ask. */
  remembered: boolean;
}

type Tab = 'general' | 'display' | 'local' | 'experience' | 'advanced';

const HISTORY_KEY = 'nocap.rdp.history';
const SORT_KEY = 'nocap.rdp.sort';
const VIEW_KEY = 'nocap.rdp.view';

function readLocal<T>(key: string, fallback: T, ok: (v: unknown) => boolean): T {
  try {
    const raw = localStorage.getItem(userKey(key));
    if (raw == null) return fallback;
    const v = JSON.parse(raw);
    return ok(v) ? (v as T) : fallback;
  } catch {
    return fallback;
  }
}
function writeLocal(key: string, v: unknown) {
  try {
    localStorage.setItem(userKey(key), JSON.stringify(v));
  } catch {
    /* ignore */
  }
}

/** Remembers a computer address for the Computer field's history. */
export function rememberHost(host: string) {
  writeLocal(HISTORY_KEY, addHistory(readLocal<string[]>(HISTORY_KEY, [], Array.isArray), host));
}

export function agoLabel(ms: number, now = Date.now()): string {
  const s = Math.round((ms - now) / 1000);
  const rel = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto', style: 'short' });
  const a = Math.abs(s);
  if (a < 45) return rel.format(0, 'second');
  if (a < 3600) return rel.format(Math.round(s / 60), 'minute');
  if (a < 86_400) return rel.format(Math.round(s / 3600), 'hour');
  if (a < 7 * 86_400) return rel.format(Math.round(s / 86_400), 'day');
  return new Date(ms).toLocaleDateString(undefined, { day: 'numeric', month: 'short' });
}

/** A saved computer as the start card edits it. */
export function formFrom(c: SavedComputer): ConnForm {
  return {
    id: c.id,
    name: c.name,
    protocol: c.protocol,
    hostname: c.hostname,
    port: c.port || DEFAULT_PORT[c.protocol],
    username: c.username,
    domain: c.domain,
    password: '',
    remember: c.has_password,
    save: true,
    gatewayPassword: '',
    options: { ...blankForm(c.protocol).options, ...c.options },
  };
}

/** The form as the server saves it. */
export function inputFrom(f: ConnForm, remember: boolean, password: string): SavedComputerInput {
  return {
    name: f.name.trim() || f.hostname.trim(),
    protocol: f.protocol,
    hostname: f.hostname.trim(),
    port: f.port === DEFAULT_PORT[f.protocol] ? 0 : f.port,
    username: f.username.trim(),
    domain: f.protocol === 'rdp' ? f.domain.trim() : '',
    options: optionsFor(f.protocol, f.options),
    remember_password: remember,
    password: remember ? password : undefined,
    gateway_password: remember ? f.gatewayPassword : undefined,
  };
}

export function RemoteHome({
  draft,
  onDraft,
  onConnect,
}: {
  /** The start card's form (kept by the app across sessions). */
  draft: ConnForm;
  onDraft: (f: ConnForm) => void;
  onConnect: (s: SessionStart) => void;
}) {
  const t = useT();
  const [list, setList] = useState<SavedComputer[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [canRemember, setCanRemember] = useState(true);
  const [showOptions, setShowOptions] = useState(false);
  const [tab, setTab] = useState<Tab>('general');
  const [query, setQuery] = useState('');
  const [sort, setSortState] = useState<SortKey>(() => readLocal<SortKey>(SORT_KEY, 'recent', (v) => v === 'name' || v === 'recent' || v === 'host'));
  const [view, setViewState] = useState<'grid' | 'list'>(() => readLocal<'grid' | 'list'>(VIEW_KEY, 'grid', (v) => v === 'grid' || v === 'list'));
  const [ask, setAsk] = useState<{ form: ConnForm; saved?: SavedComputer } | null>(null);
  const [error, setError] = useState('');
  const history = useMemo(() => readLocal<string[]>(HISTORY_KEY, [], Array.isArray), []);
  const hostRef = useRef<HTMLInputElement>(null);

  const f = draft;
  const set = (p: Partial<ConnForm>) => onDraft({ ...f, ...p });
  const setOpt = (p: Partial<RemoteOptions>) => onDraft({ ...f, options: { ...f.options, ...p } });
  const rdp = f.protocol === 'rdp';

  const load = async () => {
    const r = await savedComputers.list();
    if (!r.ok) {
      setLoadError(r.error || t('rd.loadFailed'));
      setList((l) => l ?? []);
      return;
    }
    setLoadError('');
    setList(r.data.connections);
    setCanRemember(r.data.can_remember);
  };
  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const setSort = (s: SortKey) => {
    setSortState(s);
    writeLocal(SORT_KEY, s);
  };
  const setView = (v: 'grid' | 'list') => {
    setViewState(v);
    writeLocal(VIEW_KEY, v);
  };

  const setProtocol = (p: RemoteProtocol) => {
    if (p === f.protocol) return;
    const port = f.port === DEFAULT_PORT[f.protocol] ? DEFAULT_PORT[p] : f.port;
    if (p === 'vnc' && tab === 'experience') setTab('general');
    onDraft({ ...f, protocol: p, port });
  };

  const hostAll = useMemo(() => {
    const fromSaved = (list ?? []).map((c) => c.hostname);
    return [...new Set([...history, ...fromSaved])];
  }, [history, list]);

  // The Computer field may carry a port ("pc:3390").
  const normalized = (): ConnForm | null => {
    const { host, port } = splitHost(f.hostname);
    if (!validHost(host)) {
      setError(t('rd.err.host'));
      hostRef.current?.focus();
      return null;
    }
    setError('');
    return { ...f, hostname: host, port: port ?? f.port };
  };

  const savedFor = (id?: string) => (id ? list?.find((c) => c.id === id) : undefined);

  const connect = (e?: FormEvent) => {
    e?.preventDefault();
    const nf = normalized();
    if (!nf) return;
    const saved = savedFor(nf.id);
    // A remembered password for the same computer and user: straight in.
    if (saved?.has_password && saved.hostname === nf.hostname && saved.username === nf.username.trim()) {
      void start(nf, saved, true, '');
      return;
    }
    setAsk({ form: nf, saved });
  };

  // Saves (when asked) and hands over to the session.
  const start = async (nf: ConnForm, saved: SavedComputer | undefined, remembered: boolean, password: string) => {
    let id = saved?.id;
    if (nf.save) {
      const input = inputFrom(nf, nf.remember && canRemember, password);
      const r = saved ? await savedComputers.update(saved.id, input) : await savedComputers.create(input);
      if (!r.ok) {
        toast('error', t('rd.toast.saveFailed'), r.error);
      } else {
        id = r.data.id;
        setList((l) => {
          const rest = (l ?? []).filter((c) => c.id !== r.data.id);
          return [...rest, r.data];
        });
        nf = { ...nf, id };
      }
    }
    rememberHost(nf.hostname);
    onDraft({ ...nf, password: '', gatewayPassword: '' });
    onConnect({ form: { ...nf, password }, connectionId: id, remembered });
  };

  const saveOnly = async () => {
    const nf = normalized();
    if (!nf) return;
    const saved = savedFor(nf.id);
    const input = inputFrom(nf, nf.remember && canRemember, nf.password);
    const r = saved ? await savedComputers.update(saved.id, input) : await savedComputers.create(input);
    if (!r.ok) {
      toast('error', t('rd.toast.saveFailed'), r.error);
      return;
    }
    toast('success', t('rd.toast.saved', { name: r.data.name }));
    rememberHost(nf.hostname);
    setList((l) => [...(l ?? []).filter((c) => c.id !== r.data.id), r.data]);
    onDraft(blankForm(f.protocol));
    setShowOptions(false);
  };

  const edit = (c: SavedComputer) => {
    onDraft(formFrom(c));
    setShowOptions(true);
    setTab('general');
    hostRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  };

  const quickConnect = (c: SavedComputer) => {
    const nf = formFrom(c);
    onDraft(nf);
    if (c.has_password) void start(nf, c, true, '');
    else setAsk({ form: nf, saved: c });
  };

  const duplicate = async (c: SavedComputer) => {
    const r = await savedComputers.duplicate(c.id);
    if (!r.ok) {
      toast('error', t('rd.toast.saveFailed'), r.error);
      return;
    }
    setList((l) => [...(l ?? []), r.data]);
  };

  const remove = async (c: SavedComputer) => {
    const ok = await confirmDialog({
      title: t('rd.del.title', { name: c.name }),
      message: t('rd.del.body'),
      confirmLabel: t('rd.del.confirm'),
      danger: true,
    });
    if (!ok) return;
    const r = await savedComputers.remove(c.id);
    if (!r.ok) {
      toast('error', t('rd.toast.deleteFailed'), r.error);
      return;
    }
    setList((l) => (l ?? []).filter((x) => x.id !== c.id));
    if (f.id === c.id) onDraft(blankForm(f.protocol));
  };

  const shown = useMemo(() => sortComputers(list ?? [], sort, query), [list, sort, query]);
  const editing = f.id ? savedFor(f.id) : undefined;

  return (
    <div className="rd-home">
      <form className="rd-card" onSubmit={connect} noValidate>
        <div className="rd-card-head">
          <span className="rd-mark" aria-hidden>
            <Icon name="desktop" size={22} />
          </span>
          <div className="rd-card-title">
            <b>{editing ? t('rd.editing', { name: editing.name }) : t('rd.title')}</b>
            <span className="muted small">{t('rd.subtitle')}</span>
          </div>
          <div className="segmented rd-proto" role="radiogroup" aria-label={t('rd.protocol')}>
            {(['rdp', 'vnc'] as const).map((p) => (
              <button key={p} type="button" role="radio" aria-checked={f.protocol === p} className={f.protocol === p ? 'on' : ''} onClick={() => setProtocol(p)}>
                {p.toUpperCase()}
              </button>
            ))}
          </div>
        </div>

        <label className="rd-field">
          <span>{t('rd.computer')}</span>
          <input
            ref={hostRef}
            value={f.hostname}
            list="rd-history"
            placeholder={t('rd.computerHint')}
            autoComplete="off"
            spellCheck={false}
            autoCapitalize="off"
            inputMode="url"
            onChange={(e) => set({ hostname: e.target.value })}
          />
          <datalist id="rd-history">
            {hostAll.map((h) => (
              <option key={h} value={h} />
            ))}
          </datalist>
        </label>
        <label className="rd-field">
          <span>{t('rd.user')}</span>
          <input value={f.username} placeholder={rdp ? t('rd.userHint') : t('rd.userHintVnc')} autoComplete="off" autoCapitalize="off" spellCheck={false} onChange={(e) => set({ username: e.target.value })} />
        </label>
        {error && <p className="error no-margin">{error}</p>}

        {showOptions && (
          <div className="rd-options">
            <div className="rd-tabs" role="tablist">
              {(['general', 'display', 'local', 'experience', 'advanced'] as const)
                .filter((x) => rdp || x !== 'experience')
                .map((x) => (
                  <button key={x} type="button" role="tab" aria-selected={tab === x} className={tab === x ? 'on' : ''} onClick={() => setTab(x)}>
                    {t(`rd.tab.${x}`)}
                  </button>
                ))}
            </div>
            <div className="rd-tab-body" role="tabpanel">
              {tab === 'general' && (
                <>
                  {rdp && (
                    <label className="rd-field">
                      <span>{t('rd.domain')}</span>
                      <input value={f.domain} placeholder={t('rd.domainHint')} autoComplete="off" onChange={(e) => set({ domain: e.target.value })} />
                    </label>
                  )}
                  <label className="rd-field">
                    <span>{t('rd.name')}</span>
                    <input value={f.name} placeholder={f.hostname || t('rd.nameHint')} maxLength={120} onChange={(e) => set({ name: e.target.value })} />
                  </label>
                  <label className="toggle row rd-switch">
                    <span>
                      <b>{t('rd.save')}</b>
                      <span className="muted small">{t('rd.saveHint')}</span>
                    </span>
                    <input type="checkbox" checked={f.save} onChange={(e) => set({ save: e.target.checked, remember: e.target.checked && f.remember })} />
                  </label>
                  {f.save && canRemember && (
                    <label className="toggle row rd-switch">
                      <span>
                        <b>{t('rd.remember')}</b>
                        <span className="muted small">{t('rd.rememberHint')}</span>
                      </span>
                      <input type="checkbox" checked={f.remember} onChange={(e) => set({ remember: e.target.checked })} />
                    </label>
                  )}
                </>
              )}

              {tab === 'display' && (
                <>
                  <div className="rd-field">
                    <span>{t('rd.size')}</span>
                    <div className="segmented">
                      {(['fit', 'fullscreen', 'fixed'] as const).map((m) => (
                        <button key={m} type="button" className={(f.options.size_mode ?? 'fit') === m ? 'on' : ''} onClick={() => setOpt({ size_mode: m, fixed_width: f.options.fixed_width ?? 1920, fixed_height: f.options.fixed_height ?? 1080 })}>
                          {t(`rd.size.${m}`)}
                        </button>
                      ))}
                    </div>
                  </div>
                  {f.options.size_mode === 'fixed' && (
                    <label className="rd-field">
                      <span>{t('rd.resolution')}</span>
                      <select
                        className="select"
                        value={`${f.options.fixed_width}x${f.options.fixed_height}`}
                        onChange={(e) => {
                          const [w, h] = e.target.value.split('x').map(Number);
                          setOpt({ fixed_width: w, fixed_height: h });
                        }}
                      >
                        {FIXED_SIZES.map(([w, h]) => (
                          <option key={`${w}x${h}`} value={`${w}x${h}`}>
                            {w} × {h}
                          </option>
                        ))}
                      </select>
                    </label>
                  )}
                  {rdp && (
                    <label className="rd-field">
                      <span>{t('rd.scale')}</span>
                      <select className="select" value={f.options.scale ?? 100} onChange={(e) => setOpt({ scale: Number(e.target.value) })}>
                        {SCALES.map((s) => (
                          <option key={s} value={s}>
                            {s}%
                          </option>
                        ))}
                      </select>
                    </label>
                  )}
                  <label className="rd-field">
                    <span>{t('rd.colors')}</span>
                    <select className="select" value={f.options.color_depth ?? 0} onChange={(e) => setOpt({ color_depth: Number(e.target.value) })}>
                      {COLOR_DEPTHS[f.protocol].map((d) => (
                        <option key={d} value={d}>
                          {t(`rd.colors.${d}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                </>
              )}

              {tab === 'local' && (
                <>
                  <label className="rd-field">
                    <span>{t('rd.clipboard')}</span>
                    <select className="select" value={f.options.clipboard ?? 'both'} onChange={(e) => setOpt({ clipboard: e.target.value as RemoteOptions['clipboard'] })}>
                      {(['both', 'to-remote', 'from-remote', 'off'] as const).map((c) => (
                        <option key={c} value={c}>
                          {t(`rd.clipboard.${c}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                  {rdp && (
                    <label className="toggle row rd-switch">
                      <span>
                        <b>{t('rd.audio')}</b>
                        <span className="muted small">{t('rd.audioHint')}</span>
                      </span>
                      <input type="checkbox" checked={!!f.options.audio} onChange={(e) => setOpt({ audio: e.target.checked })} />
                    </label>
                  )}
                </>
              )}

              {tab === 'experience' && rdp && (
                <>
                  <label className="rd-field">
                    <span>{t('rd.speed')}</span>
                    <select className="select" value={speedOf(f.options)} onChange={(e) => onDraft({ ...f, options: withSpeed(f.options, e.target.value as Speed) })}>
                      {(['lan', 'broadband', 'low', 'custom'] as const).map((s) => (
                        <option key={s} value={s} disabled={s === 'custom'}>
                          {t(`rd.speed.${s}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                  <div className="rd-checks">
                    {EXPERIENCE.map((k) => (
                      <label key={k} className="toggle row rd-switch">
                        <span>{t(`rd.exp.${k}`)}</span>
                        <input type="checkbox" checked={!!f.options[k]} onChange={(e) => setOpt({ [k]: e.target.checked, speed: 'custom' })} />
                      </label>
                    ))}
                  </div>
                </>
              )}

              {tab === 'advanced' && (
                <>
                  <label className="rd-field rd-port">
                    <span>{t('rd.port')}</span>
                    <input type="number" min={1} max={65535} value={f.port} onChange={(e) => set({ port: Math.min(65535, Math.max(1, Number(e.target.value) || DEFAULT_PORT[f.protocol])) })} />
                  </label>
                  {rdp && (
                    <>
                      <label className="rd-field">
                        <span>{t('rd.security')}</span>
                        <select className="select" value={f.options.security ?? 'any'} onChange={(e) => setOpt({ security: e.target.value as RemoteOptions['security'] })}>
                          {(['any', 'nla', 'tls', 'rdp'] as const).map((s) => (
                            <option key={s} value={s}>
                              {t(`rd.security.${s}`)}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label className="toggle row rd-switch">
                        <span>
                          <b>{t('rd.ignoreCert')}</b>
                          <span className="muted small">{t('rd.ignoreCertHint')}</span>
                        </span>
                        <input type="checkbox" checked={!!f.options.ignore_cert} onChange={(e) => setOpt({ ignore_cert: e.target.checked })} />
                      </label>
                      {f.options.ignore_cert && (
                        <p className="rd-warn small">
                          <Icon name="alert" size={14} /> {t('rd.ignoreCertWarn')}
                        </p>
                      )}
                      <div className="rd-sub">
                        <b className="small">{t('rd.gateway')}</b>
                        <span className="muted small">{t('rd.gatewayHint')}</span>
                      </div>
                      <div className="rd-row2">
                        <label className="rd-field">
                          <span>{t('rd.gatewayHost')}</span>
                          <input value={f.options.gateway_host ?? ''} placeholder="gateway.example.com" autoComplete="off" onChange={(e) => setOpt({ gateway_host: e.target.value })} />
                        </label>
                        <label className="rd-field rd-port">
                          <span>{t('rd.port')}</span>
                          <input type="number" min={1} max={65535} value={f.options.gateway_port || 443} onChange={(e) => setOpt({ gateway_port: Number(e.target.value) || 443 })} />
                        </label>
                      </div>
                      {f.options.gateway_host && (
                        <div className="rd-row2">
                          <label className="rd-field">
                            <span>{t('rd.gatewayUser')}</span>
                            <input value={f.options.gateway_user ?? ''} autoComplete="off" onChange={(e) => setOpt({ gateway_user: e.target.value })} />
                          </label>
                          <label className="rd-field">
                            <span>{t('rd.gatewayPassword')}</span>
                            <input type="password" value={f.gatewayPassword} autoComplete="new-password" placeholder={editing?.has_gateway_password ? t('rd.keptPassword') : ''} onChange={(e) => set({ gatewayPassword: e.target.value })} />
                          </label>
                        </div>
                      )}
                    </>
                  )}
                </>
              )}
            </div>
          </div>
        )}

        <div className="rd-card-foot">
          <button type="button" className="ghost small rd-more" aria-expanded={showOptions} onClick={() => setShowOptions((v) => !v)}>
            <Icon name={showOptions ? 'chevronUp' : 'chevronDown'} size={14} /> {showOptions ? t('rd.hideOptions') : t('rd.showOptions')}
          </button>
          <span className="spacer" />
          {(editing || showOptions) && f.save && (
            <button type="button" className="ghost small" onClick={() => void saveOnly()}>
              {t('rd.saveOnly')}
            </button>
          )}
          {editing && (
            <button type="button" className="ghost small" onClick={() => onDraft(blankForm(f.protocol))}>
              {t('rd.newOne')}
            </button>
          )}
          <button type="submit" className="rd-connect">
            {t('rd.connect')}
          </button>
        </div>
      </form>

      <section className="rd-saved" aria-label={t('rd.saved')}>
        <div className="rd-saved-head">
          <b>{t('rd.saved')}</b>
          <span className="spacer" />
          <label className="rd-search">
            <Icon name="search" size={14} />
            <input value={query} placeholder={t('rd.search')} aria-label={t('rd.search')} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <select className="select small" value={sort} aria-label={t('rd.sort')} onChange={(e) => setSort(e.target.value as SortKey)}>
            <option value="recent">{t('rd.sort.recent')}</option>
            <option value="name">{t('rd.sort.name')}</option>
            <option value="host">{t('rd.sort.host')}</option>
          </select>
          <div className="segmented rd-viewpick">
            <button type="button" className={view === 'grid' ? 'on' : ''} title={t('rd.view.grid')} aria-label={t('rd.view.grid')} onClick={() => setView('grid')}>
              <Icon name="grid" size={14} />
            </button>
            <button type="button" className={view === 'list' ? 'on' : ''} title={t('rd.view.list')} aria-label={t('rd.view.list')} onClick={() => setView('list')}>
              <Icon name="list" size={14} />
            </button>
          </div>
        </div>
        {loadError && (
          <p className="error">
            {loadError}{' '}
            <button type="button" className="link" onClick={() => void load()}>
              {t('rd.retry')}
            </button>
          </p>
        )}
        {list === null ? (
          <div className="rd-empty">
            <div className="spinner" />
          </div>
        ) : list.length === 0 ? (
          <div className="rd-empty">
            <Icon name="desktop" size={28} />
            <b>{t('rd.empty')}</b>
            <span className="muted small">{t('rd.emptyHint')}</span>
          </div>
        ) : shown.length === 0 ? (
          <div className="rd-empty muted small">{t('rd.noMatches')}</div>
        ) : (
          <ul className={`rd-grid ${view}`}>
            {shown.map((c) => (
              <li key={c.id} className={`rd-pc${f.id === c.id ? ' current' : ''}`}>
                <button type="button" className="rd-pc-main" onClick={() => quickConnect(c)} title={t('rd.connectTo', { name: c.name })}>
                  <span className={`rd-pc-ico ${c.protocol}`} aria-hidden>
                    <Icon name="desktop" size={20} />
                    <i>{c.protocol.toUpperCase()}</i>
                  </span>
                  <span className="rd-pc-text">
                    <b>{c.name}</b>
                    <span className="muted small">
                      {userLabel(c.username, c.domain) ? `${userLabel(c.username, c.domain)} · ` : ''}
                      {c.hostname}
                      {c.port ? `:${c.port}` : ''}
                    </span>
                    <span className="muted tiny">{c.last_used ? t('rd.lastUsed', { when: agoLabel(c.last_used) }) : t('rd.neverUsed')}</span>
                  </span>
                </button>
                <span className="rd-pc-actions">
                  <button type="button" className="icon-btn neutral" title={t('rd.edit')} aria-label={t('rd.edit')} onClick={() => edit(c)}>
                    <Icon name="pencil" size={15} />
                  </button>
                  <button type="button" className="icon-btn neutral" title={t('rd.duplicate')} aria-label={t('rd.duplicate')} onClick={() => void duplicate(c)}>
                    <Icon name="copy" size={15} />
                  </button>
                  <button type="button" className="icon-btn" title={t('rd.delete')} aria-label={t('rd.delete')} onClick={() => void remove(c)}>
                    <Icon name="trash" size={15} />
                  </button>
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {ask && (
        <CredentialsPrompt
          form={ask.form}
          saved={ask.saved}
          canRemember={canRemember && ask.form.save}
          onCancel={() => setAsk(null)}
          onConnect={(nf, password) => {
            setAsk(null);
            void start(nf, ask.saved, false, password);
          }}
        />
      )}
    </div>
  );
}

/** Asks for the password before connecting, the way a sign-in sheet does. */
export function CredentialsPrompt({
  form,
  saved,
  canRemember,
  retry,
  onCancel,
  onConnect,
}: {
  form: ConnForm;
  saved?: SavedComputer;
  canRemember: boolean;
  /** Shown after a wrong password. */
  retry?: boolean;
  onCancel: () => void;
  onConnect: (f: ConnForm, password: string) => void;
}) {
  const t = useT();
  const [user, setUser] = useState(form.username);
  const [pw, setPw] = useState('');
  const [remember, setRemember] = useState(form.remember || !!saved?.has_password);
  const rdp = form.protocol === 'rdp';
  return (
    <div className="rd-modal" onPointerDown={(e) => e.target === e.currentTarget && onCancel()}>
      <form
        className="rd-sheet"
        onSubmit={(e) => {
          e.preventDefault();
          onConnect({ ...form, username: user.trim(), remember: canRemember && remember }, pw);
        }}
      >
        <span className="rd-mark" aria-hidden>
          <Icon name="key" size={20} />
        </span>
        <b>{t('rd.cred.title')}</b>
        <span className="muted small">{t('rd.cred.body', { name: form.name.trim() || form.hostname })}</span>
        {retry && <p className="error no-margin">{t('rd.cred.wrong')}</p>}
        {(rdp || user) && (
          <label className="rd-field">
            <span>{t('rd.user')}</span>
            <input value={user} autoComplete="off" autoCapitalize="off" spellCheck={false} onChange={(e) => setUser(e.target.value)} />
          </label>
        )}
        <label className="rd-field">
          <span>{t('rd.password')}</span>
          <input autoFocus type="password" value={pw} autoComplete="off" onChange={(e) => setPw(e.target.value)} />
        </label>
        {canRemember ? (
          <label className="toggle row rd-switch">
            <span>{t('rd.remember')}</span>
            <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          </label>
        ) : (
          <p className="muted small no-margin">{t('rd.cred.notKept')}</p>
        )}
        <div className="rd-sheet-actions">
          <button type="button" className="ghost" onClick={onCancel}>
            {t('rd.cancel')}
          </button>
          <button type="submit" className="rd-connect">
            {t('rd.connect')}
          </button>
        </div>
      </form>
    </div>
  );
}
