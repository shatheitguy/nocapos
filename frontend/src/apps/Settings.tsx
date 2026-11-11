import { useEffect, useRef, useState, type ReactNode } from 'react';
import { UserAvatar } from '../components/UserAvatar';
import { Logo } from '../components/Logo';
import { api, getUser } from '../api/client';
import { hostApi, type PowerInfo } from '../api/hostctl';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes, fmtUptime } from '../lib/format';
import { useClock } from '../lib/hooks';
import { powerHost } from '../lib/hostActions';
import { fmtTime, useTimePrefs } from '../lib/time';
import { LANGUAGES, usePrefs } from '../state/prefs';
import { notifyApi, type NotifySettings } from '../state/notifications';
import { useSystem } from '../state/system';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import type { Snapshot, SystemInfo } from '../api/types';
import { Appearance, DesktopSettings, DockSettings, Row, Section as Group, Toggle, Wallpaper, WindowSettings } from './Personalize';
import { HostAccessPage } from './SettingsHostAccess';
import { SoftwareUpdate } from './GlowUp';
import { FocusSettings } from './SystemPages';
import { InterfacePage, NetworkHome, WifiPage } from './SettingsNetwork';
import { UsersSettings, YourAccount } from './UsersSettings';
import { DevicesSettings } from './SettingsDevices';
import { AgentsSettings } from './SettingsAgents';
import { Backup, DateTime, LockScreen, Security, SoundSettings } from './SettingsSections';
import { LanguageRegion } from './SettingsLanguage';
import { CATS, ENTRIES, SUBS, pageDesc, pageName, type Cat, type Sub } from './settingsPages';
import { tr, useGenZ, useT } from '../i18n';

// Settings, organised like a phone or Mac: a short sidebar of categories
// (Network, General, Personalization, …), each opening grouped sub-pages.
// Settings only holds settings; apps are opened from the Dock or Launchpad.


/** Older links ("dock", "focus", …) still land in the right place. */
const ALIASES: Record<string, string> = { dock: 'desktop', focus: 'notifications', password: 'security', privacy: 'security', personal: 'appearance', notify: 'notifications', 'users-accounts': 'users', apps: 'general' };
function resolve(id: string | undefined): { cat: Cat; sub: Sub | null } | null {
  if (!id) return null;
  const e = ENTRIES.find((x) => x.id === (ALIASES[id] ?? id));
  return e ? { cat: e.cat, sub: e.sub ?? null } : null;
}

function IconTile({ icon, color, size = 28 }: { icon: IconName; color: string; size?: number }) {
  return (
    <span className="set-ico" style={{ ['--c' as string]: color, width: size, height: size, borderRadius: size * 0.26 }}>
      <Icon name={icon} size={Math.round(size * 0.58)} />
    </span>
  );
}

/** Sidebar sections, with a heading each (the first has none). */
const SIDEBAR: { title?: string; cats: Cat[] }[] = [
  { cats: ['network', 'general', 'devices', 'agents'] },
  { title: 'set.group.personal', cats: ['appearance', 'wallpaper', 'desktop', 'windows'] },
  { title: 'set.group.notify', cats: ['notifications', 'sound'] },
  { title: 'set.group.security', cats: ['lock', 'security', 'users'] },
];

export function Settings({ win }: { win: WinState }) {
  const user = getUser();
  const isAdmin = user?.role === 'admin';
  const root = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [compact, setCompact] = useState(() => window.innerWidth < 700);
  const wanted = resolve(win.props?.section);
  const [cat, setCat] = useState<Cat | null>(wanted?.cat ?? 'general');
  const [sub, setSub] = useState<Sub | null>(wanted?.sub ?? null);
  const [start2fa, setStart2fa] = useState(false);
  const [query, setQuery] = useState('');
  const t = useT();
  const genz = useGenZ();

  // Opening Settings at a page while it's already open switches to it.
  useEffect(() => {
    if (wanted) {
      setCat(wanted.cat);
      setSub(wanted.sub);
    }
  }, [win.props]); // eslint-disable-line react-hooks/exhaustive-deps
  // Narrow windows show the category list, then one page at a time.
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setCompact(el.clientWidth < 700));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useEffect(() => {
    scroller.current?.scrollTo({ top: 0 });
  }, [cat, sub]);

  const go = (c: Cat | null, s: Sub | null = null, opts: { start2fa?: boolean } = {}) => {
    setCat(c);
    setSub(s);
    setStart2fa(!!opts.start2fa);
    setQuery('');
  };
  const cats = CATS.filter((c) => isAdmin || !c.admin);
  const info = cat ? CATS.find((c) => c.id === cat) : undefined;
  const pageId = sub ?? info?.id;
  const title = query.trim()
    ? t('set.searchTitle')
    : sub
      ? (SUBS[sub] ? pageName(t, sub) : sub.replace(/^iface:/, ''))
      : info
        ? pageName(t, info.id)
        : t('set.title');
  // Gen Z names always come with a plain line saying what the page is.
  const subtitle = genz && !query.trim() && pageId && (SUBS[pageId] || CATS.some((c) => c.id === pageId)) ? pageDesc(t, pageId) : null;
  const showSide = !compact || (!cat && !query.trim());
  const showMain = !compact || !!cat || !!query.trim();
  const back = sub ? () => setSub(null) : compact && (cat || query.trim()) ? () => go(null) : null;

  return (
    <div className={`set2 ${compact ? 'compact' : ''}`} ref={root}>
      {showSide && (
        <aside className="set2-side">
          <label className="set2-search">
            <Icon name="search" size={14} />
            <input value={query} placeholder={t('set.search')} aria-label={t('set.searchLabel')} onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Escape' && setQuery('')} />
          </label>
          {user && (
            <button type="button" className={`set2-account ${cat === 'account' && !query ? 'on' : ''}`} onClick={() => go('account')}>
              <UserAvatar name={user.username} />
              <span>
                <b>{user.username}</b>
                <small>{isAdmin ? t('set.admin') : t('set.standardUser')}</small>
              </span>
            </button>
          )}
          {SIDEBAR.map((g, i) => {
            const list = cats.filter((c) => g.cats.includes(c.id));
            return list.length ? (
              <nav key={i} className="set2-group" aria-label={g.title && t(g.title)}>
                {g.title && <h4 className="set2-heading">{t(g.title)}</h4>}
                {list.map((c) => (
                  <button key={c.id} type="button" className={`set2-cat ${cat === c.id && !query ? 'on' : ''}`} onClick={() => go(c.id)}
                    title={genz ? pageDesc(t, c.id) : undefined}>
                    <IconTile icon={c.icon} color={c.color} size={24} />
                    <span>{pageName(t, c.id)}</span>
                  </button>
                ))}
              </nav>
            ) : null;
          })}
        </aside>
      )}
      {showMain && (
        <main className="set2-main">
          <header className="set2-head">
            {back && (
              <button type="button" className="icon-btn ghost set-back" onClick={back} aria-label={t('common.back')} title={t('common.back')}>
                <Icon name="chevronLeft" size={18} />
              </button>
            )}
            <span className="set2-titles">
              <h2>{title}</h2>
              {subtitle && <small className="set2-subtitle">{subtitle}</small>}
            </span>
          </header>
          <div className="set2-scroll" ref={scroller}>
            <div className="set2-page" key={`${query ? 'q' : ''}${cat}-${sub}`}>
              {query.trim() ? (
                <Results query={query} isAdmin={isAdmin} go={go} />
              ) : cat ? (
                <CatBody cat={cat} sub={sub} isAdmin={isAdmin} start2fa={start2fa} open={setSub} />
              ) : null}
            </div>
          </div>
        </main>
      )}
    </div>
  );
}

function Results({ query, isAdmin, go }: { query: string; isAdmin: boolean; go: (c: Cat, s?: Sub | null) => void }) {
  const t = useT();
  const q = query.trim().toLowerCase();
  const hits = ENTRIES.filter((e) => isAdmin || !e.admin)
    .map((e) => {
      const fun = SUBS[e.id] || CATS.some((c) => c.id === e.id) ? `${pageName(t, e.id)} ${pageDesc(t, e.id)}` : '';
      const head = `${e.label} ${fun} ${e.desc} ${e.keywords ?? ''}`.toLowerCase().includes(q);
      const opts = (e.options ?? []).filter((o) => o.toLowerCase().includes(q));
      return head || opts.length ? { e, desc: opts.length ? t('set.has', { list: opts.join(', ') }) : e.desc } : null;
    })
    .filter((x) => x !== null);
  if (!hits.length) return <p className="muted set-empty">{t('set.noResults', { q: query.trim() })}</p>;
  return (
    <div className="ucard ulist">
      {hits.map(({ e, desc }) => (
        <button key={e.id} type="button" className="urow" onClick={() => go(e.cat, e.sub ?? null)}>
          <IconTile icon={e.icon} color={(e.sub && SUBS[e.sub]?.color) || CATS.find((c) => c.id === e.cat)?.color || '#8e8e93'} />
          <span className="urow-text">
            <span className="urow-title">{SUBS[e.id] || CATS.some((c) => c.id === e.id) ? pageName(t, e.id) : e.label}</span>
            <span className="urow-desc">{desc}</span>
          </span>
          <span className="urow-control"><Icon name="chevronRight" size={16} /></span>
        </button>
      ))}
    </div>
  );
}

function CatBody({ cat, sub, isAdmin, start2fa, open }: {
  cat: Cat;
  sub: Sub | null;
  isAdmin: boolean;
  start2fa: boolean;
  open: (s: Sub) => void;
}) {
  if (cat === 'network') {
    if (sub === 'wifi') return <WifiPage />;
    if (sub?.startsWith('iface:')) return <InterfacePage name={sub.slice(6)} />;
    return <NetworkHome open={(s) => open(s as Sub)} />;
  }
  if (cat !== 'general') return <SubBody sub={cat} isAdmin={isAdmin} start2fa={start2fa} />;
  if (sub) return <SubBody sub={sub} isAdmin={isAdmin} start2fa={start2fa} />;
  return <CatHome cat={cat} isAdmin={isAdmin} open={open} />;
}

/** A category's page: grouped rows that open its sub-pages, with the current value on the right. */
function CatHome({ cat, isAdmin, open }: { cat: Cat; isAdmin: boolean; open: (s: Sub) => void }) {
  const prefs = usePrefs();
  const latest = useSystem((s) => s.latest);
  const info = useSystem((s) => s.info);
  const now = useClock(30000);
  const tp = useTimePrefs();
  const disk = storageTotals(latest);
  const groups = CATS.find((c) => c.id === cat)?.groups ?? [];
  const t = useT();
  const value = (id: Sub): ReactNode => {
    switch (id) {
      case 'about': return info?.host.hostname;
      case 'update': return info ? `NoCapOS ${info.version}` : undefined;
      case 'storage': return disk.total ? t('set.free', { size: fmtBytes(Math.max(0, disk.total - disk.used)) }) : undefined;
      case 'datetime': return fmtTime(now, tp, false);
      case 'language': return `${LANGUAGES.find((l) => l.id === prefs.language)?.name ?? ''} · ${prefs.lingo === 'genz' ? t('set.lang.genz') : t('set.lang.standard')}`;
      default: return undefined;
    }
  };
  return (
    <div className="stack settings-page">
      {cat === 'general' && <Hero isAdmin={isAdmin} />}
      {groups.map((g, i) => {
        const rows = g.filter((id) => isAdmin || !SUBS[id].admin);
        if (!rows.length) return null;
        return (
          <div key={i} className="ucard ulist">
            {rows.map((id) => {
              const s = SUBS[id];
              const v = value(id);
              return (
                <button key={id} type="button" className="urow set-navrow" onClick={() => open(id)}>
                  <IconTile icon={s.icon} color={s.color} />
                  <span className="urow-text">
                    <span className="urow-title">{pageName(t, id)}</span>
                    <span className="urow-desc">{pageDesc(t, id)}</span>
                  </span>
                  <span className="urow-control">
                    {v != null && <span className="set-row-value">{v}</span>}
                    <Icon name="chevronRight" size={16} />
                  </span>
                </button>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}

function SubBody({ sub, isAdmin, start2fa }: { sub: Sub; isAdmin: boolean; start2fa: boolean }) {
  switch (sub) {
    case 'about':
      return <About isAdmin={isAdmin} />;
    case 'update':
      return <SoftwareUpdate />;
    case 'access':
      return isAdmin ? <HostAccessPage /> : null;
    case 'storage':
      return <StoragePage />;
    case 'datetime':
      return <DateTime />;
    case 'language':
      return <LanguageRegion />;
    case 'backup':
      return <Backup />;
    case 'troubleshoot':
      return <Troubleshoot />;
    case 'appearance':
      return <Appearance />;
    case 'wallpaper':
      return <Wallpaper />;
    case 'desktop':
      return (
        <div className="stack settings-page">
          <DockSettings />
          <DesktopSettings />
        </div>
      );
    case 'windows':
      return <WindowSettings />;
    case 'notifications':
      return <Notifications isAdmin={isAdmin} />;
    case 'sound':
      return <SoundSettings />;
    case 'security':
      return <Security autoStart={start2fa} />;
    case 'lock':
      return <LockScreen />;
    case 'account':
      return (
        <div className="stack settings-page">
          <YourAccount />
        </div>
      );
    case 'users':
      return <UsersSettings isAdmin={isAdmin} withAccount={false} />;
    case 'devices':
      return <DevicesSettings />;
    case 'agents':
      return <AgentsSettings />;
    default:
      return null;
  }
}

function Hero({ isAdmin }: { isAdmin: boolean }) {
  const t = useT();
  const info = useSystem((s) => s.info);
  const uptime = useSystem((s) => s.latest?.uptime);
  const [power, setPower] = useState<PowerInfo | null>(null);
  useEffect(() => {
    if (isAdmin) void hostApi.power().then((r) => r.ok && setPower(r.data));
  }, [isAdmin]);
  const can = isAdmin && !!power?.supported;
  const why = power && !power.supported ? power.reason : undefined;
  return (
    <section className="ucard set-hero">
      <div className="set-hero-head">
        <div className="set-hero-text">
          <h1 className="set-hero-title">{info?.host.hostname ?? '…'}</h1>
          <div className="set-hero-meta">
            {t('set.hero.running', { version: info?.version ?? '…' })}
            {uptime != null && <> · {t('set.hero.up', { time: fmtUptime(uptime) })}</>}
          </div>
        </div>
        {isAdmin && (
          <div className="set-hero-power">
            <button type="button" className="pill ghost small" disabled={!can} title={why ?? t('set.hero.restartTitle')} onClick={() => void powerHost('reboot')}>
              <Icon name="restart" size={13} /> {t('set.hero.restart')}
            </button>
            <button type="button" className="pill ghost small danger" disabled={!can} title={why ?? t('set.hero.shutdownTitle')} onClick={() => void powerHost('shutdown')}>
              <Icon name="power" size={13} /> {t('set.hero.shutdown')}
            </button>
          </div>
        )}
      </div>
    </section>
  );
}

/** Disks often show up once per mount; count each one once. */
function storageTotals(s: Snapshot | null) {
  const seen = new Set<string>();
  let used = 0;
  let total = 0;
  for (const d of s?.disks ?? []) {
    const k = `${d.total}-${d.used}`;
    if (seen.has(k) || !d.total) continue;
    seen.add(k);
    used += d.used;
    total += d.total;
  }
  return { used, total };
}

function Meter({ pct }: { pct: number }) {
  const v = Math.max(0, Math.min(100, pct));
  return (
    <span className={`set-meter ${v > 90 ? 'bad' : v > 75 ? 'warn' : ''}`}>
      <span style={{ width: `${v}%` }} />
    </span>
  );
}

// ---------------- Notifications ----------------

const notifyTopics = (): { key: keyof NotifySettings; label: string; hint: string }[] => [
  { key: 'update', label: tr('sp.ntUpdate'), hint: tr('sp.ntUpdateHint') },
  { key: 'app_updates', label: tr('sp.ntAppUpdates'), hint: tr('sp.ntAppUpdatesHint') },
  { key: 'app_errors', label: tr('sp.ntAppErrors'), hint: tr('sp.ntAppErrorsHint') },
  { key: 'storage', label: tr('sp.ntStorage'), hint: tr('sp.ntStorageHint') },
  { key: 'backups', label: tr('sp.ntBackups'), hint: tr('sp.ntBackupsHint') },
  { key: 'devices', label: tr('sp.ntDevices'), hint: tr('sp.ntDevicesHint') },
];

function Notifications({ isAdmin }: { isAdmin: boolean }) {
  const prefs = usePrefs();
  const t = useT();
  const [topics, setTopics] = useState<NotifySettings | null>(null);
  useEffect(() => {
    if (isAdmin) void notifyApi.settings().then((r) => r.ok && setTopics(r.data));
  }, [isAdmin]);
  const setTopic = (key: keyof NotifySettings, on: boolean) => {
    if (!topics) return;
    const next = { ...topics, [key]: on };
    setTopics(next);
    void notifyApi.saveSettings(next).then((r) => {
      if (r.ok) setTopics(r.data);
      else {
        setTopics(topics);
        toast('error', tr('sp.saveFail'), r.error);
      }
    });
  };
  return (
    <div className="stack settings-page">
      {isAdmin && (
        <Group title={t('sp.notifyAbout')} hint={t('sp.notifyAboutHint')}>
          {notifyTopics().map((n) => (
            <Toggle key={n.key} label={n.label} hint={n.hint} checked={topics?.[n.key] ?? true} disabled={!topics} onChange={(on) => setTopic(n.key, on)} />
          ))}
        </Group>
      )}
      <FocusSettings />
      <Group title={t('sp.alerts')} hint={t('sp.alertsHint')}>
        <Toggle label={t('sp.alertSound')} hint={prefs.uiSounds ? t('sp.alertSoundOn') : t('sp.alertSoundOff')}
          checked={prefs.soundAlerts} disabled={!prefs.uiSounds} onChange={(soundAlerts) => prefs.set({ soundAlerts })} />
      </Group>
    </div>
  );
}

// ---------------- About ----------------

function About({ isAdmin }: { isAdmin: boolean }) {
  const info = useSystem((s) => s.info);
  const [ips, setIps] = useState<string[] | null>(null);
  const t = useT();
  useEffect(() => {
    if (!isAdmin) return;
    void hostApi.network().then((r) => {
      if (r.ok) setIps(r.data.interfaces.flatMap((i) => i.addresses.filter((a) => !a.includes(':')).map((a) => `${a.replace(/\/\d+$/, '')} (${i.name})`)));
    });
  }, [isAdmin]);
  if (!info) return <p className="muted">{t('sp.loading')}</p>;
  const h = info.host;
  const rows: [string, string][] = [
    [t('sp.abName'), h.hostname],
    ...(h.board ? [[t('sp.abModel'), h.board] as [string, string]] : []),
    [t('sp.abCpu'), `${h.cpu_model || '–'}${h.cpu_cores ? ` (${t('sp.abCores', { n: h.cpu_cores })})` : ''}`],
    [t('sp.abMem'), fmtBytes(h.mem_total)],
    ...(info.accelerators.length ? [[t('sp.abAccel'), info.accelerators.map((a) => a.name).join('\n')] as [string, string]] : []),
    [t('sp.abOs'), h.os || '–'],
    [t('sp.abKernel'), h.kernel || '–'],
    [t('sp.abArch'), h.arch],
    ...(ips ? [[t('sp.abIps'), ips.length ? ips.join('\n') : t('sp.none')] as [string, string]] : []),
    ['NoCapOS', info.version],
    // Engine details are for administrators only.
    ...(info.docker || info.docker_error
      ? [['Docker', info.docker ? `${info.docker.server_version} · ${info.docker.storage_driver}` : t('sp.dockerOff', { error: info.docker_error })] as [string, string]]
      : []),
  ];
  const copy = () =>
    void navigator.clipboard?.writeText(rows.map(([k, v]) => `${k}: ${v.replace(/\n/g, ', ')}`).join('\n')).then(() => toast('success', tr('sp.infoCopied')));
  return (
    <div className="stack settings-page">
      <div className="set-about-hero">
        <Logo size={72} />
        {isAdmin ? <HostnameEdit name={h.hostname} /> : <h3>{h.hostname}</h3>}
        <span className="muted">NoCapOS {info.version}</span>
      </div>
      <dl className="ucard set-device-list">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
      <button type="button" className="ghost small align-start" onClick={copy}>
        <Icon name="copy" size={13} /> {t('sp.copyDetails')}
      </button>
    </div>
  );
}

// RFC 1123: letters, digits and hyphens, not at either end; dots between labels.
const HOSTNAME_RE = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/i;

/** The device name, renamed in place (administrators). It applies at once. */
function HostnameEdit({ name }: { name: string }) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(name);
  const [busy, setBusy] = useState(false);
  const t = useT();
  const valid = HOSTNAME_RE.test(value.trim()) && !/^[\d.]+$/.test(value.trim());
  const save = async () => {
    const next = value.trim();
    if (next.toLowerCase() === name.toLowerCase()) return setEditing(false);
    setBusy(true);
    const r = await hostApi.setHostname(next);
    setBusy(false);
    if (!r.ok) return toast('error', tr('sp.renameFail'), r.error);
    const info = await api<SystemInfo>('/api/v1/system/info');
    if (info.ok) useSystem.getState().setInfo(info.data);
    toast('success', tr('sp.renamed', { name: r.data.hostname }), tr('sp.renamedBody', { short: r.data.hostname.split('.')[0] }));
    setEditing(false);
  };
  if (!editing) {
    return (
      <div className="host-edit">
        <h3>{name}</h3>
        <button
          type="button"
          className="icon-btn neutral"
          title={t('sp.renameDevice')}
          aria-label={t('sp.renameDevice')}
          onClick={() => {
            setValue(name);
            setEditing(true);
          }}
        >
          <Icon name="edit" size={14} />
        </button>
      </div>
    );
  }
  return (
    <form
      className="host-edit"
      onSubmit={(e) => {
        e.preventDefault();
        if (valid) void save();
      }}
    >
      <input autoFocus value={value} maxLength={63} spellCheck={false} aria-label={t('sp.deviceName')} onChange={(e) => setValue(e.target.value)} />
      <button type="submit" className="small" disabled={!valid || busy}>
        {busy ? t('sp.renaming') : t('sp.rename')}
      </button>
      <button type="button" className="ghost small" onClick={() => setEditing(false)}>
        {t('sp.cancel')}
      </button>
      <span className="muted small" style={{ flexBasis: '100%', textAlign: 'center' }}>
        {valid || !value ? t('sp.hostHint') : t('sp.hostBad')}
      </span>
    </form>
  );
}

// ---------------- Storage ----------------

function StoragePage() {
  const latest = useSystem((s) => s.latest);
  const disks = latest?.disks ?? [];
  const total = storageTotals(latest);
  const t = useT();
  return (
    <div className="stack settings-page">
      <div className="settings-card set-storage-hero">
        <span className="set-tile-value">
          {fmtBytes(total.used)} <small>{t('sp.usedOf', { total: fmtBytes(total.total) })}</small>
        </span>
        <Meter pct={total.total ? (total.used / total.total) * 100 : 0} />
        <span className="muted small">{t('sp.free', { size: fmtBytes(Math.max(0, total.total - total.used)) })}</span>
      </div>
      <Group title={t('sp.disks')} hint={t('sp.disksHint')}>
        {disks.length ? (
          disks.map((d) => (
            <div key={d.path} className="settings-row stacked">
              <span className="settings-row-text slider-head">
                <span className="settings-row-label set-mono">{d.path}</span>
                <span className="settings-value">
                  {t('sp.diskOf', { used: fmtBytes(d.used), total: fmtBytes(d.total) })}
                </span>
              </span>
              <Meter pct={d.percent} />
            </div>
          ))
        ) : (
          <Row label={t('sp.noDisks')}>
            <span />
          </Row>
        )}
      </Group>
    </div>
  );
}

// ---------------- Troubleshoot ----------------

function Troubleshoot() {
  const [logs, setLogs] = useState<{ supported: boolean; reason?: string; lines: string[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const box = useRef<HTMLPreElement>(null);
  const t = useT();
  const load = () => {
    setError(null);
    void api<{ supported: boolean; reason?: string; lines: string[] }>('/api/v1/system/logs?lines=400').then((r) => {
      if (r.ok) setLogs(r.data);
      else setError(r.error ?? tr('sp.logsFail'));
    });
  };
  useEffect(load, []);
  useEffect(() => {
    if (box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [logs]);
  const copy = () => void navigator.clipboard?.writeText(logs?.lines.join('\n') ?? '').then(() => toast('success', tr('sp.logsCopied')));
  return (
    <div className="stack settings-page">
      <Group title={t('sp.logs')} hint={t('sp.logsHint')}>
        <div className="settings-row full">
          {error ? (
            <p className="error">{error}</p>
          ) : !logs ? (
            <p className="muted small">{t('sp.loading')}</p>
          ) : logs.supported ? (
            <pre ref={box} className="set-logs">{logs.lines.length ? logs.lines.join('\n') : t('sp.logsEmpty')}</pre>
          ) : (
            <p className="muted small">{logs.reason}</p>
          )}
          <div className="btn-group set-logs-actions">
            <button type="button" className="ghost small" onClick={load}>
              <Icon name="restart" size={13} /> {t('sp.refresh')}
            </button>
            {logs?.supported && (
              <button type="button" className="ghost small" onClick={copy}>
                <Icon name="copy" size={13} /> {t('sp.copy')}
              </button>
            )}
          </div>
        </div>
      </Group>
    </div>
  );
}

