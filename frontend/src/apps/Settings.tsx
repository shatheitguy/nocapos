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
import { useSystem } from '../state/system';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import type { Snapshot } from '../api/types';
import { openApp } from './meta';
import { Appearance, DesktopSettings, DockSettings, Row, Section as Group, Toggle, Wallpaper, WindowSettings } from './Personalize';
import { FocusSettings, PowerSettings } from './SystemPages';
import { InterfacePage, NetworkHome, WifiPage } from './SettingsNetwork';
import { UsersSettings, YourAccount } from './UsersSettings';
import { Backup, DateTime, LanguageRegion, LockScreen, Security, SoundSettings } from './SettingsSections';

// Settings, organised like a phone or Mac: a short sidebar of categories
// (Network, General, Personalization, …), each opening grouped sub-pages.
// Settings only holds settings; apps are opened from the Dock or Launchpad.

type Cat = 'network' | 'general' | 'appearance' | 'wallpaper' | 'desktop' | 'windows' | 'notifications' | 'sound' | 'lock' | 'security' | 'users' | 'account';
type Sub =
  | 'about' | 'update' | 'storage' | 'datetime' | 'language' | 'backup' | 'troubleshoot' | 'power'
  | 'appearance' | 'wallpaper' | 'desktop' | 'windows'
  | 'notifications' | 'sound'
  | 'security' | 'lock'
  | 'account' | 'users'
  | 'wifi' | `iface:${string}`;

interface CatInfo {
  id: Cat;
  label: string;
  icon: IconName;
  color: string;
  admin?: boolean;
  /** Sub-pages, in groups (each group is one card on the category page). */
  groups: Sub[][];
}

const CATS: CatInfo[] = [
  { id: 'network', label: 'Network', icon: 'wifi', color: '#0a84ff', admin: true, groups: [] },
  { id: 'general', label: 'General', icon: 'settings', color: '#8e8e93',
    groups: [['about', 'update'], ['storage'], ['datetime', 'language'], ['backup', 'troubleshoot', 'power']] },
  { id: 'appearance', label: 'Appearance', icon: 'palette', color: '#5e5ce6', groups: [] },
  { id: 'wallpaper', label: 'Wallpaper', icon: 'image', color: '#32ade6', groups: [] },
  { id: 'desktop', label: 'Desktop & Dock', icon: 'launcher', color: '#3a3a3c', groups: [] },
  { id: 'windows', label: 'Windows', icon: 'maximize', color: '#3a3a3c', groups: [] },
  { id: 'notifications', label: 'Notifications', icon: 'bell', color: '#ff3b30', groups: [] },
  { id: 'sound', label: 'Sound', icon: 'sound', color: '#ff2d55', groups: [] },
  { id: 'lock', label: 'Lock Screen', icon: 'lock', color: '#48484a', groups: [] },
  { id: 'security', label: 'Privacy & Security', icon: 'shield', color: '#30a0ff', groups: [] },
  { id: 'users', label: 'Users & Groups', icon: 'users', color: '#007aff', admin: true, groups: [] },
  { id: 'account', label: 'Your Account', icon: 'user', color: '#007aff', groups: [] },
];

const SUBS: Record<string, { label: string; icon: IconName; color: string; admin?: boolean; desc?: string }> = {
  about: { label: 'About', icon: 'info', color: '#8e8e93' },
  update: { label: 'Software Update', icon: 'download', color: '#8e8e93', admin: true },
  storage: { label: 'Storage', icon: 'disk', color: '#8e8e93' },
  datetime: { label: 'Date & Time', icon: 'clock', color: '#0a84ff' },
  language: { label: 'Language & Region', icon: 'globe', color: '#0a84ff' },
  backup: { label: 'Export & Restore', icon: 'save', color: '#8e8e93', admin: true },
  troubleshoot: { label: 'Troubleshoot', icon: 'wrench', color: '#8e8e93', admin: true },
  power: { label: 'Restart & Shut Down', icon: 'power', color: '#ff453a' },
  appearance: { label: 'Appearance', icon: 'palette', color: '#5e5ce6', desc: 'Theme, accent color, brightness and effects' },
  wallpaper: { label: 'Wallpaper', icon: 'image', color: '#32ade6', desc: 'Photos, gradients or your own picture' },
  desktop: { label: 'Desktop & Dock', icon: 'launcher', color: '#3a3a3c', desc: 'Dock, widgets and desktop icons' },
  windows: { label: 'Windows', icon: 'maximize', color: '#3a3a3c', desc: 'Title bar buttons and double-click' },
  notifications: { label: 'Notifications', icon: 'bell', color: '#ff3b30', desc: 'Focus and notification sounds' },
  sound: { label: 'Sound', icon: 'sound', color: '#ff2d55', desc: 'Sound effects and volume' },
  security: { label: 'Privacy & Security', icon: 'shield', color: '#30a0ff', desc: 'Change your password and ask for a code when you sign in' },
  lock: { label: 'Lock Screen', icon: 'lock', color: '#48484a', desc: 'Auto-lock and screen saver' },
  account: { label: 'Your Account', icon: 'user', color: '#007aff', desc: 'Photo, name and sign out' },
  users: { label: 'Users', icon: 'users', color: '#007aff', admin: true, desc: 'Add people and choose who is an administrator' },
  wifi: { label: 'Wi-Fi', icon: 'wifi', color: '#0a84ff' },
};

/** Everything searchable: deep-link ids (also used by universal search and older links). */
interface Entry {
  id: string;
  label: string;
  icon: IconName;
  desc: string;
  cat: Cat;
  sub?: Sub;
  admin?: boolean;
  keywords?: string;
  options?: string[];
}

const ENTRIES: Entry[] = [
  { id: 'network', cat: 'network', admin: true, label: 'Network', icon: 'wifi', desc: 'Wi-Fi, Ethernet and IP addresses', keywords: 'internet lan connection', options: ['Networking'] },
  { id: 'wifi', cat: 'network', sub: 'wifi', admin: true, label: 'Wi-Fi', icon: 'wifi', desc: 'Join a wireless network', keywords: 'wireless wlan ssid hotspot' },
  { id: 'ethernet', cat: 'network', admin: true, label: 'Ethernet & IP address', icon: 'ethernet', desc: 'Wired adapters, static IP, DNS and gateway', keywords: 'ip address dhcp static dns gateway router ipv4 lan cable' },
  { id: 'general', cat: 'general', label: 'General', icon: 'settings', desc: 'Device, storage, updates, date and language' },
  { id: 'about', cat: 'general', sub: 'about', label: 'About', icon: 'info', desc: 'Device name, hardware, Docker and versions', keywords: 'version device hardware docker gpu npu model processor memory serial ip' },
  { id: 'update', cat: 'general', sub: 'update', admin: true, label: 'Software Update', icon: 'download', desc: 'Check for a newer NoCapOS', keywords: 'upgrade version release new' },
  { id: 'storage', cat: 'general', sub: 'storage', label: 'Storage', icon: 'disk', desc: 'Disks and how full they are', keywords: 'disk drive space capacity usage' },
  { id: 'datetime', cat: 'general', sub: 'datetime', label: 'Date & Time', icon: 'clock', desc: 'Time zone, 24-hour clock and calendar', keywords: 'clock time zone calendar week',
    options: ['24-hour time', 'Show seconds', 'Show the date in the Dock', 'Set time zone automatically', 'Time zone', 'First day of the week'] },
  { id: 'language', cat: 'general', sub: 'language', label: 'Language & Region', icon: 'globe', desc: 'Language and region formats', keywords: 'locale currency number format', options: ['NoCapOS language', 'Region formats'] },
  { id: 'backup', cat: 'general', sub: 'backup', admin: true, label: 'Export & Restore', icon: 'save', desc: 'Download a backup of NoCapOS itself', keywords: 'backup export database restore download', options: ['Download a backup', 'Restore'] },
  { id: 'troubleshoot', cat: 'general', sub: 'troubleshoot', admin: true, label: 'Troubleshoot', icon: 'wrench', desc: 'NoCapOS logs', keywords: 'logs debug journal problems errors support' },
  { id: 'power', cat: 'general', sub: 'power', label: 'Restart & Shut Down', icon: 'power', desc: 'Restart or shut down this machine, or sign out', keywords: 'reboot shutdown power off sign out', options: ['Restart', 'Shut down', 'Sign out'] },
  { id: 'appearance', cat: 'appearance', label: 'Appearance', icon: 'palette', desc: 'Theme, accent color, brightness and effects', keywords: 'theme glass classic cyber deck dark light accent color colour',
    options: ['Light or dark', 'Accent color', 'Match wallpaper', 'Brightness', 'Reduce transparency', 'Reduce motion', 'Square corners', 'Reset personalization'] },
  { id: 'wallpaper', cat: 'wallpaper', label: 'Wallpaper', icon: 'image', desc: 'Photos, gradients or your own picture', keywords: 'background photo picture', options: ['Your photo', 'Dim wallpaper'] },
  { id: 'desktop', cat: 'desktop', label: 'Desktop & Dock', icon: 'launcher', desc: 'Dock, widgets and desktop icons', keywords: 'dock taskbar widgets icons grid desktop',
    options: ['Position on screen', 'Icon size', 'Magnify on hover', 'Automatically hide the Dock', 'Show recent apps', 'Show widgets on the desktop', 'Snap icons to grid'] },
  { id: 'windows', cat: 'windows', label: 'Windows', icon: 'maximize', desc: 'Title bar buttons and double-click', keywords: 'title bar macos menu bar', options: ['Window buttons', 'Double-click a title bar to'] },
  { id: 'notifications', cat: 'notifications', label: 'Notifications', icon: 'bell', desc: 'Focus and notification sounds', keywords: 'do not disturb dnd silence alerts', options: ['Focus', 'Play a sound for notifications'] },
  { id: 'sound', cat: 'sound', label: 'Sound', icon: 'sound', desc: 'Sound effects and volume', keywords: 'volume mute clicks effects',
    options: ['Play sound effects', 'Volume', 'Clicking buttons and icons', 'Opening and closing windows', 'Locking and unlocking'] },
  { id: 'security', cat: 'security', label: 'Password & Two-Factor', icon: 'key', desc: 'Change your password and ask for a code when you sign in', keywords: '2fa totp otp authenticator security password',
    options: ['Two-factor authentication', 'Change password'] },
  { id: 'lock', cat: 'lock', label: 'Lock Screen', icon: 'lock', desc: 'Auto-lock and screen saver', keywords: 'screensaver screen saver idle auto-lock', options: ['Lock the screen after', 'Screen saver', 'Start after'] },
  { id: 'account', cat: 'account', label: 'Your Account', icon: 'user', desc: 'Your photo, name and sign out', keywords: 'profile avatar picture sign out log out', options: ['Profile photo', 'Close other windows', 'Sign out'] },
  { id: 'users', cat: 'users', admin: true, label: 'Users & Groups', icon: 'users', desc: 'Add people and choose who is an administrator', keywords: 'people roles admin linux accounts', options: ['Add user', 'Reset password', 'Delete user', 'Role'] },
];

/** Every Settings page, for universal search. */
export const SETTINGS_PAGES: readonly { id: string; label: string; icon: IconName; admin?: boolean; keywords?: string }[] = ENTRIES.map((e) => ({
  id: e.id, label: e.label, icon: e.icon, admin: e.admin, keywords: `${e.desc} ${e.keywords ?? ''} ${(e.options ?? []).join(' ')}`,
}));

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
  { cats: ['network', 'general'] },
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
                <CatBody cat={cat} sub={sub} isAdmin={isAdmin} start2fa={start2fa} go={go} open={setSub} />
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

function CatBody({ cat, sub, isAdmin, start2fa, go, open }: {
  cat: Cat;
  sub: Sub | null;
  isAdmin: boolean;
  start2fa: boolean;
  go: (c: Cat, s?: Sub | null, o?: { start2fa?: boolean }) => void;
  open: (s: Sub) => void;
}) {
  if (cat === 'network') {
    if (sub === 'wifi') return <WifiPage />;
    if (sub?.startsWith('iface:')) return <InterfacePage name={sub.slice(6)} />;
    return <NetworkHome open={(s) => open(s as Sub)} />;
  }
  if (cat !== 'general') return <SubBody sub={cat} isAdmin={isAdmin} start2fa={start2fa} go={go} />;
  if (sub) return <SubBody sub={sub} isAdmin={isAdmin} start2fa={start2fa} go={go} />;
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
      {cat === 'general' && (
        <div className="set-top">
          <Hero isAdmin={isAdmin} />
          <Tiles />
        </div>
      )}
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

function SubBody({ sub, isAdmin, start2fa, go }: { sub: Sub; isAdmin: boolean; start2fa: boolean; go: (c: Cat, s?: Sub | null) => void }) {
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
    case 'power':
      return <PowerSettings isAdmin={isAdmin} />;
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
      return <Notifications />;
    case 'sound':
      return <SoundSettings />;
    case 'security':
      return <Security autoStart={start2fa} />;
    case 'lock':
      return <LockScreen />;
    case 'account':
      return <YourAccount onSecurity={() => go('security')} />;
    case 'users':
      return <UsersSettings isAdmin={isAdmin} onSecurity={() => go('security')} withAccount={false} />;
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

function Tiles() {
  const latest = useSystem((s) => s.latest);
  const cores = useSystem((s) => s.info?.host.cpu_cores);
  const disk = storageTotals(latest);
  const temps = [
    ...(latest?.temperatures ?? []).map((t) => t.celsius),
    ...(latest?.accelerators ?? []).map((a) => a.metrics.temp_c ?? 0),
  ].filter((c) => c > 0);
  const temp = temps.length ? Math.max(...temps) : null;
  const tiles: { label: string; icon: IconName; value: string; sub: string; pct: number }[] = [
    { label: 'Storage', icon: 'disk', value: fmtBytes(disk.used), sub: disk.total ? `of ${fmtBytes(disk.total)}` : '', pct: disk.total ? (disk.used / disk.total) * 100 : 0 },
    { label: 'Memory', icon: 'memory', value: fmtBytes(latest?.memory.used ?? 0), sub: latest ? `of ${fmtBytes(latest.memory.total)}` : '', pct: latest?.memory.percent ?? 0 },
    { label: 'CPU', icon: 'cpu', value: `${Math.round(latest?.cpu.percent ?? 0)}%`, sub: cores ? `${cores} cores` : '', pct: latest?.cpu.percent ?? 0 },
  ];
  if (temp !== null) tiles.push({ label: 'Temperature', icon: 'thermometer', value: `${Math.round(temp)}°C`, sub: temp > 80 ? 'Hot' : temp > 65 ? 'Warm' : 'Normal', pct: temp });
  return (
    <section className="set-tiles">
      {tiles.map((t) => (
        <div key={t.label} className="ucard set-tile">
          <span className="set-tile-label">
            <Icon name={t.icon} size={14} /> {t.label}
          </span>
          <span className="set-tile-value">
            {latest ? t.value : '–'} <small>{t.sub}</small>
          </span>
          <Meter pct={t.pct} />
        </div>
      ))}
    </section>
  );
}

// ---------------- Notifications ----------------

function Notifications() {
  const prefs = usePrefs();
  return (
    <div className="stack settings-page">
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

function SoftwareUpdate() {
  const [u, setU] = useState<UpdateInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const check = (refresh: boolean) => {
    setBusy(true);
    void api<UpdateInfo>(`/api/v1/system/update${refresh ? '?refresh=1' : ''}`).then((r) => {
      setBusy(false);
      if (r.ok) setU(r.data);
      else toast('error', 'Could not check for updates', r.error);
    });
  };
  useEffect(() => check(false), []);
  const copy = () => u && void navigator.clipboard?.writeText(u.command).then(() => toast('success', 'Command copied'));
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
        <button type="button" className="pill ghost small" disabled={busy} onClick={() => check(true)}>
          {busy ? <span className="spinner sm" /> : <Icon name="restart" size={13} />} Check now
        </button>
      </div>
      {u && (u.available || u.dev) && !u.error && (
        <Group title="How to update" hint="Run this on the server. It installs the latest release over this one and keeps your settings, apps and files.">
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
    ['Docker', info.docker ? `${info.docker.server_version} · ${info.docker.storage_driver}` : `Not connected${info.docker_error ? ` — ${info.docker_error}` : ''}`],
  ];
  const copy = () =>
    void navigator.clipboard?.writeText(rows.map(([k, v]) => `${k}: ${v.replace(/\n/g, ', ')}`).join('\n')).then(() => toast('success', 'Device info copied'));
  return (
    <div className="stack settings-page">
      <div className="set-about-hero">
        <Logo size={72} />
        <h3>{h.hostname}</h3>
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

