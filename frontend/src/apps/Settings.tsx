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
import { openApp } from './meta';
import { Appearance, DesktopSettings, DockSettings, Row, Section as Group, Toggle, Wallpaper, WindowSettings } from './Personalize';
import { FocusSettings } from './SystemPages';
import { InterfacePage, NetworkHome, WifiPage } from './SettingsNetwork';
import { UsersSettings, YourAccount } from './UsersSettings';
import { DevicesSettings } from './SettingsDevices';
import { Backup, DateTime, LanguageRegion, LockScreen, Security, SoundSettings } from './SettingsSections';
import { CATS, ENTRIES, SUBS, type Cat, type Sub } from './settingsPages';

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
  { cats: ['network', 'general', 'devices'] },
  { title: 'Personalization', cats: ['appearance', 'wallpaper', 'desktop', 'windows'] },
  { title: 'Notifications & Sound', cats: ['notifications', 'sound'] },
  { title: 'Security & Users', cats: ['lock', 'security', 'users'] },
];

export function Settings({ win }: { win: WinState }) {
  const user = getUser();
  const isAdmin = user?.role === 'admin';
  const root = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const [compact, setCompact] = useState(false);
  const wanted = resolve(win.props?.section);
  const [cat, setCat] = useState<Cat | null>(wanted?.cat ?? 'general');
  const [sub, setSub] = useState<Sub | null>(wanted?.sub ?? null);
  const [start2fa, setStart2fa] = useState(false);
  const [query, setQuery] = useState('');

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
  const title = query.trim()
    ? 'Search'
    : sub
      ? (SUBS[sub]?.label ?? sub.replace(/^iface:/, ''))
      : (info?.label ?? 'Settings');
  const showSide = !compact || (!cat && !query.trim());
  const showMain = !compact || !!cat || !!query.trim();
  const back = sub ? () => setSub(null) : compact && (cat || query.trim()) ? () => go(null) : null;

  return (
    <div className={`set2 ${compact ? 'compact' : ''}`} ref={root}>
      {showSide && (
        <aside className="set2-side">
          <label className="set2-search">
            <Icon name="search" size={14} />
            <input value={query} placeholder="Search" aria-label="Search settings" onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Escape' && setQuery('')} />
          </label>
          {user && (
            <button type="button" className={`set2-account ${cat === 'account' && !query ? 'on' : ''}`} onClick={() => go('account')}>
              <UserAvatar name={user.username} />
              <span>
                <b>{user.username}</b>
                <small>{isAdmin ? 'Administrator' : 'Standard user'}</small>
              </span>
            </button>
          )}
          {SIDEBAR.map((g, i) => {
            const list = cats.filter((c) => g.cats.includes(c.id));
            return list.length ? (
              <nav key={i} className="set2-group" aria-label={g.title}>
                {g.title && <h4 className="set2-heading">{g.title}</h4>}
                {list.map((c) => (
                  <button key={c.id} type="button" className={`set2-cat ${cat === c.id && !query ? 'on' : ''}`} onClick={() => go(c.id)}>
                    <IconTile icon={c.icon} color={c.color} size={24} />
                    <span>{c.label}</span>
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
              <button type="button" className="icon-btn ghost set-back" onClick={back} aria-label="Back" title="Back">
                <Icon name="chevronLeft" size={18} />
              </button>
            )}
            <h2>{title}</h2>
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
  const q = query.trim().toLowerCase();
  const hits = ENTRIES.filter((e) => isAdmin || !e.admin)
    .map((e) => {
      const head = `${e.label} ${e.desc} ${e.keywords ?? ''}`.toLowerCase().includes(q);
      const opts = (e.options ?? []).filter((o) => o.toLowerCase().includes(q));
      return head || opts.length ? { e, desc: opts.length ? `Has: ${opts.join(', ')}` : e.desc } : null;
    })
    .filter((x) => x !== null);
  if (!hits.length) return <p className="muted set-empty">No settings match “{query.trim()}”.</p>;
  return (
    <div className="ucard ulist">
      {hits.map(({ e, desc }) => (
        <button key={e.id} type="button" className="urow" onClick={() => go(e.cat, e.sub ?? null)}>
          <IconTile icon={e.icon} color={(e.sub && SUBS[e.sub]?.color) || CATS.find((c) => c.id === e.cat)?.color || '#8e8e93'} />
          <span className="urow-text">
            <span className="urow-title">{e.label}</span>
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
  const value = (id: Sub): ReactNode => {
    switch (id) {
      case 'about': return info?.host.hostname;
      case 'update': return info ? `NoCapOS ${info.version}` : undefined;
      case 'storage': return disk.total ? `${fmtBytes(Math.max(0, disk.total - disk.used))} free` : undefined;
      case 'datetime': return fmtTime(now, tp, false);
      case 'language': return LANGUAGES.find((l) => l.id === prefs.language)?.name;
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
                    <span className="urow-title">{s.label}</span>
                    {s.desc && <span className="urow-desc">{s.desc}</span>}
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
      return <YourAccount />;
    case 'users':
      return <UsersSettings isAdmin={isAdmin} withAccount={false} />;
    case 'devices':
      return <DevicesSettings />;
    default:
      return null;
  }
}

function Hero({ isAdmin }: { isAdmin: boolean }) {
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
            Running NoCapOS {info?.version ?? '…'}
            {uptime != null && <> · Up {fmtUptime(uptime)}</>}
          </div>
        </div>
        {isAdmin && (
          <div className="set-hero-power">
            <button type="button" className="pill ghost small" disabled={!can} title={why ?? 'Restart this machine'} onClick={() => void powerHost('reboot')}>
              <Icon name="restart" size={13} /> Restart
            </button>
            <button type="button" className="pill ghost small danger" disabled={!can} title={why ?? 'Shut down this machine'} onClick={() => void powerHost('shutdown')}>
              <Icon name="power" size={13} /> Shut down
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

const NOTIFY_TOPICS: { key: keyof NotifySettings; label: string; hint: string }[] = [
  { key: 'update', label: 'NoCapOS updates', hint: 'When a new version of NoCapOS is out' },
  { key: 'app_updates', label: 'App updates', hint: 'When an App Store app you installed has a new version' },
  { key: 'app_errors', label: 'App problems', hint: 'An app stops unexpectedly, or an install or update fails' },
  { key: 'storage', label: 'Disk and pool health', hint: 'A disk is failing or a RAID pool is degraded' },
  { key: 'backups', label: 'Backups', hint: 'A backup plan fails to run' },
  { key: 'devices', label: 'USB drives', hint: 'A USB drive or memory card is plugged in or removed' },
];

function Notifications({ isAdmin }: { isAdmin: boolean }) {
  const prefs = usePrefs();
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
        toast('error', 'Could not save', r.error);
      }
    });
  };
  return (
    <div className="stack settings-page">
      {isAdmin && (
        <Group title="Notify me about" hint="Applies to every admin. Turned-off topics aren't kept in the Notification Center either.">
          {NOTIFY_TOPICS.map((t) => (
            <Toggle key={t.key} label={t.label} hint={t.hint} checked={topics?.[t.key] ?? true} disabled={!topics} onChange={(on) => setTopic(t.key, on)} />
          ))}
        </Group>
      )}
      <FocusSettings />
      <Group title="Alerts">
        <Toggle label="Play a sound for notifications" hint={prefs.uiSounds ? 'Uses the volume in Sound' : 'Turn on sound effects in Sound first'}
          checked={prefs.soundAlerts} disabled={!prefs.uiSounds} onChange={(soundAlerts) => prefs.set({ soundAlerts })} />
      </Group>
    </div>
  );
}

// ---------------- Software Update ----------------

interface UpdateInfo {
  current: string;
  latest?: string;
  available: boolean;
  dev: boolean;
  name?: string;
  notes?: string;
  url?: string;
  published?: string;
  checked: string;
  command: string;
  error?: string;
}

/** Release notes are Markdown; show them as plain, readable text. */
function plainNotes(md: string) {
  return md
    .replace(/\r/g, '')
    .split('\n')
    .filter((l) => !/^\s*```/.test(l))
    .map((l) => l.replace(/^#{1,6}\s*/, '').replace(/^\s*[-*]\s+/, '• ').replace(/\*\*(.+?)\*\*/g, '$1').replace(/`([^`]+)`/g, '$1').replace(/\[([^\]]+)\]\([^)]+\)/g, '$1'))
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

interface UpdateRunStatus {
  running: boolean;
  supported: boolean;
  version: string;
  log?: string;
}

function SoftwareUpdate() {
  const [u, setU] = useState<UpdateInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [run, setRun] = useState<UpdateRunStatus | null>(null);
  const [starting, setStarting] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);
  const check = (refresh: boolean) => {
    setBusy(true);
    void api<UpdateInfo>(`/api/v1/system/update${refresh ? '?refresh=1' : ''}`).then((r) => {
      setBusy(false);
      if (r.ok) setU(r.data);
      else toast('error', 'Could not check for updates', r.error);
    });
  };
  const pollRun = () => void api<UpdateRunStatus>('/api/v1/system/update/run').then((r) => { if (r.ok) setRun(r.data); });
  useEffect(() => {
    check(false);
    pollRun(); // in case this page reopened mid-update
  }, []);
  useEffect(() => {
    if (!run?.running) return;
    const t = window.setInterval(pollRun, 2000);
    return () => window.clearInterval(t);
  }, [run?.running]);
  useEffect(() => {
    logRef.current?.scrollTo(0, logRef.current.scrollHeight);
  }, [run?.log]);
  const copy = () => u && void navigator.clipboard?.writeText(u.command).then(() => toast('success', 'Command copied'));
  const updateNow = () => {
    setStarting(true);
    void api<{ started: boolean }>('/api/v1/system/update/run', { method: 'POST' }).then((r) => {
      setStarting(false);
      if (r.ok || r.status === 409) { pollRun(); return; }
      toast('error', 'Could not start the update', r.error);
    });
  };
  const finished = run && !run.running && run.log;
  return (
    <div className="stack settings-page">
      <div className="ucard set-update">
        <Logo size={40} />
        <div className="set-update-text">
          {!u ? (
            <b>Checking for updates…</b>
          ) : u.error ? (
            <>
              <b>Couldn't check for updates</b>
              <span>{u.error}</span>
            </>
          ) : u.available ? (
            <>
              <b>NoCapOS {u.latest} is available</b>
              <span>You have {u.current}.{u.published && ` Released ${new Date(u.published).toLocaleDateString()}.`}</span>
            </>
          ) : u.dev ? (
            <>
              <b>Development build</b>
              <span>This is {u.current}; the latest release is {u.latest ?? 'unknown'}.</span>
            </>
          ) : (
            <>
              <b>NoCapOS is up to date</b>
              <span>Version {u.current}</span>
            </>
          )}
          {u && <small>Last checked {new Date(u.checked).toLocaleTimeString()}</small>}
        </div>
        {u && (u.available || u.dev) && !u.error && run?.supported && !run.running ? (
          <button type="button" className="pill small" disabled={starting} onClick={updateNow}>
            {starting ? <span className="spinner sm" /> : <Icon name="download" size={13} />} Update now
          </button>
        ) : (
          <button type="button" className="pill ghost small" disabled={busy} onClick={() => check(true)}>
            {busy ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} Check now
          </button>
        )}
      </div>
      {run?.running && (
        <Group title="Updating NoCapOS" hint="This can take a few minutes. Keep this window open; NoCapOS will restart on its own partway through.">
          <div className="settings-row full">
            <span className="spinner sm" /> <b>Installing…</b>
            <pre ref={logRef} className="set-logs">{run.log || 'Starting…'}</pre>
          </div>
        </Group>
      )}
      {finished && (
        <Group title="Update finished">
          <div className="settings-row full">
            <pre ref={logRef} className="set-logs">{run.log}</pre>
            <button type="button" className="pill small" onClick={() => window.location.reload()}>
              <Icon name="restart" size={13} /> Reload NoCapOS
            </button>
          </div>
        </Group>
      )}
      {u && (u.available || u.dev) && !u.error && !run?.running && (
        <Group
          title="How to update"
          hint={
            run?.supported
              ? "Update now installs the latest release over this one and keeps your settings, apps and files. You can also run this on the server yourself."
              : "Updating from here needs NoCapOS installed directly on a Linux server, running as root. Run this on the server instead."
          }
        >
          <div className="settings-row full">
            <pre className="set-logs set-cmd">{u.command}</pre>
            <div className="btn-group set-logs-actions">
              <button type="button" className="ghost small" onClick={copy}>
                <Icon name="copy" size={13} /> Copy command
              </button>
              <button type="button" className="ghost small" onClick={() => openApp('terminal')}>
                <Icon name="terminal" size={13} /> Open Terminal
              </button>
            </div>
          </div>
        </Group>
      )}
      {u?.notes && !u.error && (
        <Group title={`What's new in ${u.latest}`}>
          <div className="settings-row full">
            <pre className="set-notes">{plainNotes(u.notes)}</pre>
            {u.url && (
              <a className="set-link" href={u.url} target="_blank" rel="noreferrer">
                View on GitHub <Icon name="external" size={12} />
              </a>
            )}
          </div>
        </Group>
      )}
    </div>
  );
}

// ---------------- About ----------------

function About({ isAdmin }: { isAdmin: boolean }) {
  const info = useSystem((s) => s.info);
  const [ips, setIps] = useState<string[] | null>(null);
  useEffect(() => {
    if (!isAdmin) return;
    void hostApi.network().then((r) => {
      if (r.ok) setIps(r.data.interfaces.flatMap((i) => i.addresses.filter((a) => !a.includes(':')).map((a) => `${a.replace(/\/\d+$/, '')} (${i.name})`)));
    });
  }, [isAdmin]);
  if (!info) return <p className="muted">Loading…</p>;
  const h = info.host;
  const rows: [string, string][] = [
    ['Name', h.hostname],
    ...(h.board ? [['Model', h.board] as [string, string]] : []),
    ['Processor', `${h.cpu_model || '–'}${h.cpu_cores ? ` (${h.cpu_cores} cores)` : ''}`],
    ['Memory', fmtBytes(h.mem_total)],
    ...(info.accelerators.length ? [['Accelerators', info.accelerators.map((a) => a.name).join('\n')] as [string, string]] : []),
    ['Operating system', h.os || '–'],
    ['Kernel', h.kernel || '–'],
    ['Architecture', h.arch],
    ...(ips ? [['IP addresses', ips.length ? ips.join('\n') : 'None'] as [string, string]] : []),
    ['NoCapOS', info.version],
    // Engine details are for administrators only.
    ...(info.docker || info.docker_error
      ? [['Docker', info.docker ? `${info.docker.server_version} · ${info.docker.storage_driver}` : `Not connected — ${info.docker_error}`] as [string, string]]
      : []),
  ];
  const copy = () =>
    void navigator.clipboard?.writeText(rows.map(([k, v]) => `${k}: ${v.replace(/\n/g, ', ')}`).join('\n')).then(() => toast('success', 'Device info copied'));
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
        <Icon name="copy" size={13} /> Copy details
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
  const valid = HOSTNAME_RE.test(value.trim()) && !/^[\d.]+$/.test(value.trim());
  const save = async () => {
    const next = value.trim();
    if (next.toLowerCase() === name.toLowerCase()) return setEditing(false);
    setBusy(true);
    const r = await hostApi.setHostname(next);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t rename this device', r.error);
    const info = await api<SystemInfo>('/api/v1/system/info');
    if (info.ok) useSystem.getState().setInfo(info.data);
    toast('success', `This device is now “${r.data.hostname}”`, `On your network it answers as ${r.data.hostname.split('.')[0]}.local.`);
    setEditing(false);
  };
  if (!editing) {
    return (
      <div className="host-edit">
        <h3>{name}</h3>
        <button
          type="button"
          className="icon-btn neutral"
          title="Rename this device"
          aria-label="Rename this device"
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
      <input autoFocus value={value} maxLength={63} spellCheck={false} aria-label="Device name" onChange={(e) => setValue(e.target.value)} />
      <button type="submit" className="small" disabled={!valid || busy}>
        {busy ? 'Renaming…' : 'Rename'}
      </button>
      <button type="button" className="ghost small" onClick={() => setEditing(false)}>
        Cancel
      </button>
      <span className="muted small" style={{ flexBasis: '100%', textAlign: 'center' }}>
        {valid || !value ? 'Letters, digits and hyphens, e.g. living-room-nas. It takes effect right away.' : 'Use only letters, digits and hyphens (not at the start or end).'}
      </span>
    </form>
  );
}

// ---------------- Storage ----------------

function StoragePage() {
  const latest = useSystem((s) => s.latest);
  const disks = latest?.disks ?? [];
  const total = storageTotals(latest);
  return (
    <div className="stack settings-page">
      <div className="settings-card set-storage-hero">
        <span className="set-tile-value">
          {fmtBytes(total.used)} <small>used of {fmtBytes(total.total)}</small>
        </span>
        <Meter pct={total.total ? (total.used / total.total) * 100 : 0} />
        <span className="muted small">{fmtBytes(Math.max(0, total.total - total.used))} free</span>
      </div>
      <Group title="Disks">
        {disks.length ? (
          disks.map((d) => (
            <div key={d.path} className="settings-row stacked">
              <span className="settings-row-text slider-head">
                <span className="settings-row-label set-mono">{d.path}</span>
                <span className="settings-value">
                  {fmtBytes(d.used)} of {fmtBytes(d.total)}
                </span>
              </span>
              <Meter pct={d.percent} />
            </div>
          ))
        ) : (
          <Row label="No disks reported yet">
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
  const load = () => {
    setError(null);
    void api<{ supported: boolean; reason?: string; lines: string[] }>('/api/v1/system/logs?lines=400').then((r) => {
      if (r.ok) setLogs(r.data);
      else setError(r.error ?? 'Could not read the logs');
    });
  };
  useEffect(load, []);
  useEffect(() => {
    if (box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [logs]);
  const copy = () => void navigator.clipboard?.writeText(logs?.lines.join('\n') ?? '').then(() => toast('success', 'Logs copied'));
  return (
    <div className="stack settings-page">
      <Group title="NoCapOS logs" hint="The most recent lines from the NoCapOS service. Include them when you report a problem.">
        <div className="settings-row full">
          {error ? (
            <p className="error">{error}</p>
          ) : !logs ? (
            <p className="muted small">Loading…</p>
          ) : logs.supported ? (
            <pre ref={box} className="set-logs">{logs.lines.length ? logs.lines.join('\n') : 'No log lines yet.'}</pre>
          ) : (
            <p className="muted small">{logs.reason}</p>
          )}
          <div className="btn-group set-logs-actions">
            <button type="button" className="ghost small" onClick={load}>
              <Icon name="restart" size={13} /> Refresh
            </button>
            {logs?.supported && (
              <button type="button" className="ghost small" onClick={copy}>
                <Icon name="copy" size={13} /> Copy
              </button>
            )}
          </div>
        </div>
      </Group>
    </div>
  );
}

