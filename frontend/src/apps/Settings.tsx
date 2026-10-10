import { useEffect, useRef, useState, type ReactNode } from 'react';
import { UserAvatar } from '../components/UserAvatar';
import { Logo } from '../components/Logo';
import { api, getUser } from '../api/client';
import { hostApi, type PowerInfo } from '../api/hostctl';
import { accountApi } from '../api/account';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes, fmtUptime } from '../lib/format';
import { useClock } from '../lib/hooks';
import { powerHost } from '../lib/hostActions';
import { fmtTime, useTimePrefs } from '../lib/time';
import { LANGUAGES, LOCK_TIMEOUTS, UI_THEMES, WALLPAPERS, usePrefs } from '../state/prefs';
import { useSystem } from '../state/system';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import type { Snapshot } from '../api/types';
import { openApp } from './meta';
import { Stat } from './Monitor';
import { Appearance, DesktopSettings, DockSettings, Row, Section as Group, Wallpaper, WallpaperStrip, WindowSettings } from './Personalize';
import { FocusSettings, NetworkSettings, PowerSettings } from './SystemPages';
import { UsersSettings, YourAccount } from './UsersSettings';
import { Backup, ChangePassword, DateTime, LanguageRegion, LockScreen, Security, SoundSettings } from './SettingsSections';

// Settings, laid out like umbrelOS: an overview (device card, live usage
// tiles and a list of rows) and detail pages that slide in with a back button.

export type Section =
  | 'account'
  | 'users'
  | 'security'
  | 'appearance'
  | 'wallpaper'
  | 'desktop'
  | 'windows'
  | 'sound'
  | 'focus'
  | 'lock'
  | 'language'
  | 'datetime'
  | 'network'
  | 'storage'
  | 'backup'
  | 'troubleshoot'
  | 'power'
  | 'about';

interface Page {
  id: Section;
  label: string;
  icon: IconName;
  desc: string;
  admin?: boolean;
  /** Extra words search matches. */
  keywords?: string;
  /** The options on the page, so search can find them by name. */
  options?: string[];
}

const PAGES: Page[] = [
  { id: 'account', label: 'Account', icon: 'user', desc: 'Your photo, password and sign out', keywords: 'profile name avatar picture password sign out log out',
    options: ['Profile photo', 'Change password', 'Close other windows', 'Sign out'] },
  { id: 'users', label: 'Users', icon: 'users', admin: true, desc: 'Add people and choose who is an administrator', keywords: 'people roles admin linux accounts',
    options: ['Add user', 'Reset password', 'Delete user', 'Role'] },
  { id: 'security', label: 'Two-factor authentication', icon: 'shield', desc: 'Ask for a code from an authenticator app when you sign in', keywords: '2fa totp otp authenticator security password',
    options: ['Ask for a code when signing in', 'Change password'] },
  { id: 'appearance', label: 'Appearance', icon: 'palette', desc: 'Theme, accent color, brightness and effects', keywords: 'theme glass classic cyber deck dark light accent color colour',
    options: ['Light or dark', 'Accent color', 'Match wallpaper', 'Brightness', 'Data grid backdrop', 'Reduce transparency', 'Reduce motion', 'Square corners', 'Reset personalization'] },
  { id: 'wallpaper', label: 'Wallpaper', icon: 'image', desc: 'Photos, gradients or your own picture', keywords: 'background photo picture',
    options: ['Your photo', 'Dim wallpaper'] },
  { id: 'desktop', label: 'Dock & Desktop', icon: 'launcher', desc: 'Dock position and size, widgets and desktop icons', keywords: 'dock taskbar widgets icons grid desktop',
    options: ['Position on screen', 'Icon size', 'Magnify on hover', 'Automatically hide the Dock', 'Show recent apps', 'Show dots for open apps', 'Show widgets on the desktop', 'Show icon names', 'Snap icons to grid', 'Clean Up'] },
  { id: 'windows', label: 'Windows', icon: 'maximize', desc: 'Title bar buttons and double-click', keywords: 'title bar macos menu bar',
    options: ['Window buttons', 'Double-click a title bar to'] },
  { id: 'sound', label: 'Notifications & Sounds', icon: 'sound', desc: 'Sound effects and volume', keywords: 'volume mute clicks effects notifications',
    options: ['Play sound effects', 'Volume', 'Clicking buttons and icons', 'Opening and closing windows', 'Notifications', 'Locking and unlocking'] },
  { id: 'focus', label: 'Focus', icon: 'moon', desc: 'Hide notifications and silence their sounds', keywords: 'do not disturb dnd silence notifications', options: ['Focus'] },
  { id: 'lock', label: 'Lock screen', icon: 'lock', desc: 'Auto-lock and screen saver', keywords: 'screensaver screen saver idle auto-lock',
    options: ['Lock the screen after', 'Screen saver', 'Start after'] },
  { id: 'language', label: 'Language & Region', icon: 'globe', desc: 'Language and region formats', keywords: 'locale currency number format', options: ['NoCapOS language', 'Region formats'] },
  { id: 'datetime', label: 'Date & Time', icon: 'clock', desc: 'Time zone, 24-hour clock and calendar', keywords: 'clock time zone calendar week',
    options: ['24-hour time', 'Show seconds', 'Show the date in the Dock', 'Set time zone automatically', 'Time zone', 'First day of the week'] },
  { id: 'network', label: 'Network', icon: 'wifi', admin: true, desc: 'Wi-Fi, Ethernet and IP addresses', keywords: 'wifi wi-fi ethernet internet ip address lan dns gateway static dhcp',
    options: ['Wi-Fi', 'Networking', 'IPv4'] },
  { id: 'storage', label: 'Storage', icon: 'disk', desc: 'Disks and how full they are', keywords: 'disk drive space capacity usage' },
  { id: 'backup', label: 'Export & restore', icon: 'save', admin: true, desc: 'Download a backup of NoCapOS itself', keywords: 'backup export database restore download',
    options: ['Download a backup', 'Restore', 'Backups'] },
  { id: 'troubleshoot', label: 'Troubleshoot', icon: 'wrench', admin: true, desc: 'NoCapOS logs and diagnostic tools', keywords: 'logs debug journal problems errors support' },
  { id: 'power', label: 'Restart & shut down', icon: 'power', desc: 'Restart or shut down this machine, or sign out', keywords: 'reboot shutdown power off sign out',
    options: ['Restart', 'Shut down', 'Sign out'] },
  { id: 'about', label: 'About this device', icon: 'info', desc: 'Hardware, Docker engine and accelerators', keywords: 'version device hardware docker gpu npu model' },
];

/** Rows that open another app instead of a page. */
const APP_LINKS: { id: string; label: string; icon: IconName; desc: string; admin?: boolean; keywords?: string }[] = [
  { id: 'backups', label: 'Backups', icon: 'rewind', desc: 'Automatic encrypted backups, and Rewind', admin: true, keywords: 'backup restore snapshot rewind' },
  { id: 'monitor', label: 'Live usage', icon: 'monitor', desc: 'CPU, memory, network and storage in real time', keywords: 'resource monitor cpu memory' },
  { id: 'terminal', label: 'Terminal', icon: 'terminal', desc: 'A shell on the host or in a container', admin: true, keywords: 'shell bash command line font crt' },
  { id: 'assistant', label: 'AI assistant', icon: 'sparkles', desc: 'Models, providers and memory', keywords: 'ai llm models providers ollama openai chat' },
  { id: 'scripts', label: 'Scripts', icon: 'fileCode', desc: 'Saved shell scripts you run in one click', admin: true, keywords: 'automation shell' },
  { id: 'containers', label: 'Containers', icon: 'containers', desc: 'Docker containers, stacks, images and their logs', admin: true, keywords: 'docker compose stacks images logs' },
];

/** Every Settings page, for universal search. */
export const SETTINGS_PAGES: readonly { id: string; label: string; icon: IconName; admin?: boolean; keywords?: string }[] = PAGES.map((p) => ({
  ...p,
  keywords: `${p.desc} ${p.keywords ?? ''} ${(p.options ?? []).join(' ')}`,
}));

const isSection = (v: string | undefined): v is Section => !!v && PAGES.some((p) => p.id === v);
// Older links: "dock" lives with the desktop now.
const resolve = (v: string | undefined): Section | undefined => (v === 'dock' ? 'desktop' : isSection(v) ? v : undefined);
const themeName = (id: string) => {
  const t = UI_THEMES.find((x) => x.id === id) ?? UI_THEMES[0];
  return t.id === 'classic' ? 'NoCap classic' : t.name;
};

export function Settings({ win }: { win: WinState }) {
  const user = getUser();
  const isAdmin = user?.role === 'admin';
  const wanted = resolve(win.props?.section);
  const [page, setPage] = useState<Section | null>(wanted ?? null);
  const [start2fa, setStart2fa] = useState(false);
  const [query, setQuery] = useState('');
  const [device, setDevice] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);
  // Opening Settings at a specific page (e.g. "Change wallpaper…") while it's
  // already open switches to that page.
  useEffect(() => {
    if (wanted) setPage(wanted);
  }, [wanted, win.props]);
  useEffect(() => {
    scroller.current?.scrollTo({ top: 0 });
  }, [page]);

  const go = (id: Section | null, opts: { start2fa?: boolean } = {}) => {
    setPage(id);
    setStart2fa(!!opts.start2fa);
    setQuery('');
  };
  const current = PAGES.find((p) => p.id === page);

  return (
    <div className="set-root">
      {current && (
        <header className="set-detail-head">
          <button type="button" className="icon-btn set-back" onClick={() => go(null)} aria-label="Back to Settings" title="Back">
            <Icon name="chevronLeft" size={18} />
          </button>
          <h2 className="set-detail-title">{current.label}</h2>
        </header>
      )}
      <div className="set-scroll" ref={scroller}>
        {current ? (
          <div className="set-detail" key={current.id}>
            <PageBody id={current.id} isAdmin={isAdmin} start2fa={start2fa} go={go} />
          </div>
        ) : (
          <Overview isAdmin={isAdmin} query={query} setQuery={setQuery} go={go} openDevice={() => setDevice(true)} />
        )}
      </div>
      {device && (
        <DeviceInfo
          isAdmin={isAdmin}
          onClose={() => setDevice(false)}
          onMore={() => {
            setDevice(false);
            go('about');
          }}
        />
      )}
    </div>
  );
}

function PageBody({ id, isAdmin, start2fa, go }: { id: Section; isAdmin: boolean; start2fa: boolean; go: (id: Section | null, o?: { start2fa?: boolean }) => void }) {
  switch (id) {
    case 'account':
      return (
        <div className="stack settings-page">
          <YourAccount onSecurity={() => go('security')} />
          <ChangePassword />
        </div>
      );
    case 'users':
      return <UsersSettings isAdmin={isAdmin} onSecurity={() => go('security')} withAccount={false} />;
    case 'security':
      return <Security autoStart={start2fa} />;
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
    case 'sound':
      return (
        <div className="stack settings-page">
          <FocusSettings />
          <SoundSettings />
        </div>
      );
    case 'focus':
      return <FocusSettings />;
    case 'lock':
      return <LockScreen />;
    case 'language':
      return <LanguageRegion />;
    case 'datetime':
      return <DateTime />;
    case 'network':
      return <NetworkSettings />;
    case 'storage':
      return <StoragePage />;
    case 'backup':
      return <Backup />;
    case 'troubleshoot':
      return <Troubleshoot />;
    case 'power':
      return <PowerSettings isAdmin={isAdmin} />;
    case 'about':
      return <About />;
  }
}

// ---------------- Overview ----------------

function Overview({ isAdmin, query, setQuery, go, openDevice }: {
  isAdmin: boolean;
  query: string;
  setQuery: (q: string) => void;
  go: (id: Section | null, o?: { start2fa?: boolean }) => void;
  openDevice: () => void;
}) {
  const q = query.trim().toLowerCase();
  const pages = PAGES.filter((p) => isAdmin || !p.admin);
  const links = APP_LINKS.filter((a) => isAdmin || !a.admin);
  const hits = q
    ? [
        ...pages
          .map((p) => {
            const head = `${p.label} ${p.desc} ${p.keywords ?? ''}`.toLowerCase().includes(q);
            const opts = (p.options ?? []).filter((o) => o.toLowerCase().includes(q));
            return head || opts.length ? { key: p.id, label: p.label, icon: p.icon, desc: opts.length ? `Has: ${opts.join(', ')}` : p.desc, run: () => go(p.id) } : null;
          })
          .filter((x) => x !== null),
        ...links
          .filter((a) => `${a.label} ${a.desc} ${a.keywords ?? ''}`.toLowerCase().includes(q))
          .map((a) => ({ key: `app-${a.id}`, label: a.label, icon: a.icon, desc: a.desc, run: () => openApp(a.id) })),
      ]
    : [];

  return (
    <div className="set-overview">
      <div className="set-top">
        <Hero isAdmin={isAdmin} />
        <Tiles />
      </div>

      <label className="set-search">
        <Icon name="search" size={15} />
        <input
          value={query}
          placeholder="Search settings"
          aria-label="Search settings"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && hits[0]) hits[0].run();
            if (e.key === 'Escape') setQuery('');
          }}
        />
      </label>

      {q ? (
        <div className="ucard ulist">
          {hits.length ? (
            hits.map((h) => (
              <button key={h.key} type="button" className="urow" onClick={h.run}>
                <span className="urow-icon"><Icon name={h.icon} size={17} /></span>
                <span className="urow-text">
                  <span className="urow-title">{h.label}</span>
                  <span className="urow-desc">{h.desc}</span>
                </span>
                <span className="urow-control"><Icon name="chevronRight" size={16} /></span>
              </button>
            ))
          ) : (
            <div className="urow"><span className="urow-desc">No settings match “{query.trim()}”.</span></div>
          )}
        </div>
      ) : (
        <SettingsList isAdmin={isAdmin} go={go} openDevice={openDevice} />
      )}
    </div>
  );
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
          <h1 className="set-hero-title">Settings</h1>
          <div className="set-hero-device">
            <Icon name="desktop" size={15} /> <b>{info?.host.hostname ?? '…'}</b>
          </div>
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
      <WallpaperStrip strip />
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
      <button type="button" className="pill set-live" onClick={() => openApp('monitor')}>
        <Icon name="monitor" size={14} /> Live usage
      </button>
    </section>
  );
}

// ---------------- Rows ----------------

function NavRow({ icon, avatar, title, desc, value, onClick }: { icon?: IconName; avatar?: ReactNode; title: string; desc: string; value?: ReactNode; onClick: () => void }) {
  return (
    <button type="button" className="urow" onClick={onClick}>
      {avatar ?? <span className="urow-icon">{icon && <Icon name={icon} size={17} />}</span>}
      <span className="urow-text">
        <span className="urow-title">{title}</span>
        <span className="urow-desc">{desc}</span>
      </span>
      <span className="urow-control">
        {value != null && <span className="set-row-value">{value}</span>}
        <Icon name="chevronRight" size={16} />
      </span>
    </button>
  );
}

function AppRow({ id }: { id: string }) {
  const a = APP_LINKS.find((x) => x.id === id)!;
  return (
    <button type="button" className="urow" onClick={() => openApp(a.id)}>
      <span className="urow-icon"><Icon name={a.icon} size={17} /></span>
      <span className="urow-text">
        <span className="urow-title">{a.label}</span>
        <span className="urow-desc">{a.desc}</span>
      </span>
      <span className="urow-control"><span className="set-open">Open</span></span>
    </button>
  );
}

function CtlRow({ icon, title, desc, children }: { icon: IconName; title: string; desc: string; children: ReactNode }) {
  return (
    <div className="urow">
      <span className="urow-icon"><Icon name={icon} size={17} /></span>
      <span className="urow-text">
        <span className="urow-title">{title}</span>
        <span className="urow-desc">{desc}</span>
      </span>
      <span className="urow-control">{children}</span>
    </div>
  );
}

function Switch({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="toggle set-switch">
      <input type="checkbox" role="switch" aria-label={label} aria-checked={checked} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

function SettingsList({ isAdmin, go, openDevice }: { isAdmin: boolean; go: (id: Section | null, o?: { start2fa?: boolean }) => void; openDevice: () => void }) {
  const user = getUser();
  const prefs = usePrefs();
  const info = useSystem((s) => s.info);
  const latest = useSystem((s) => s.latest);
  const now = useClock(30000);
  const tp = useTimePrefs();
  const [totp, setTotp] = useState<boolean | null>(null);
  useEffect(() => {
    void accountApi.totpStatus().then((r) => r.ok && setTotp(r.data.enabled));
  }, []);
  const disk = storageTotals(latest);
  const wall = prefs.wallpaper === 'custom' ? 'Your photo' : (WALLPAPERS.find((w) => w.id === prefs.wallpaper)?.name ?? '');
  const lock = LOCK_TIMEOUTS.find((t) => t.value === prefs.lockTimeout)?.label ?? '';

  return (
    <>
      <div className="set-group">
        <h3 className="section-label">Account</h3>
        <div className="ucard ulist">
          {user && (
            <NavRow avatar={<UserAvatar name={user.username} />} title={user.username} desc={`${isAdmin ? 'Administrator' : 'Standard user'} · photo, password and sign out`}
              onClick={() => go('account')} />
          )}
          {isAdmin && <NavRow icon="users" title="Users" desc="Add people and choose who is an administrator" onClick={() => go('users')} />}
          <div className="urow">
            <span className="urow-icon"><Icon name="shield" size={17} /></span>
            <button type="button" className="urow-text set-row-link" onClick={() => go('security')}>
              <span className="urow-title">Two-factor authentication</span>
              <span className="urow-desc">Ask for a code from an authenticator app when you sign in</span>
            </button>
            <span className="urow-control">
              <Switch label="Two-factor authentication" checked={!!totp} disabled={totp === null}
                onChange={(on) => go('security', { start2fa: on })} />
            </span>
          </div>
        </div>
      </div>

      <div className="set-group">
        <h3 className="section-label">Look & feel</h3>
        <div className="ucard ulist">
          <NavRow icon="palette" title="Appearance" desc="Theme, accent color, brightness and effects" value={themeName(prefs.uiTheme)} onClick={() => go('appearance')} />
          <NavRow icon="image" title="Wallpaper" desc="Photos, gradients or your own picture" value={wall} onClick={() => go('wallpaper')} />
          <NavRow icon="launcher" title="Dock & Desktop" desc="Dock position and size, widgets and desktop icons" onClick={() => go('desktop')} />
          <NavRow icon="maximize" title="Windows" desc="Title bar buttons and double-click" onClick={() => go('windows')} />
        </div>
      </div>

      <div className="set-group">
        <h3 className="section-label">Notifications & sounds</h3>
        <div className="ucard ulist">
          <CtlRow icon="moon" title="Focus" desc="Hide notifications and silence their sounds; errors still show">
            <Switch label="Focus" checked={prefs.focusMode} onChange={(focusMode) => prefs.set({ focusMode })} />
          </CtlRow>
          <NavRow icon="sound" title="Sounds" desc="Sound effects and volume" value={prefs.uiSounds ? `On · ${prefs.soundVolume}%` : 'Off'} onClick={() => go('sound')} />
          <NavRow icon="lock" title="Lock screen" desc="Auto-lock and screen saver" value={lock} onClick={() => go('lock')} />
        </div>
      </div>

      <div className="set-group">
        <h3 className="section-label">System</h3>
        <div className="ucard ulist">
          <NavRow icon="globe" title="Language & Region" desc="Language and region formats" value={LANGUAGES.find((l) => l.id === prefs.language)?.name} onClick={() => go('language')} />
          <NavRow icon="clock" title="Date & Time" desc="Time zone, 24-hour clock and calendar" value={fmtTime(now, tp, false)} onClick={() => go('datetime')} />
          {isAdmin && <NavRow icon="wifi" title="Network" desc="Wi-Fi, Ethernet and IP addresses" onClick={() => go('network')} />}
          <NavRow icon="disk" title="Storage" desc={disk.total ? `${fmtBytes(disk.used)} used of ${fmtBytes(disk.total)}` : 'Disks and how full they are'} onClick={() => go('storage')} />
          {isAdmin && <AppRow id="backups" />}
          <NavRow icon="info" title="Device info" desc={info ? [info.host.board, info.host.cpu_model].filter(Boolean).join(' · ') || info.host.os : 'Model, processor, memory and addresses'}
            onClick={openDevice} />
          <CtlRow icon="download" title="Software update" desc={`You're running NoCapOS ${info?.version ?? '…'}`}>
            <span className="chip">{info ? (/^v?\d/.test(info.version) ? `v${info.version.replace(/^v/, '')}` : info.version) : '…'}</span>
          </CtlRow>
          <NavRow icon="power" title="Restart & shut down" desc="Restart or shut down this machine, or sign out" onClick={() => go('power')} />
        </div>
      </div>

      <div className="set-group">
        <h3 className="section-label">Advanced</h3>
        <div className="ucard ulist">
          {isAdmin && <NavRow icon="wrench" title="Troubleshoot" desc="NoCapOS logs and diagnostic tools" onClick={() => go('troubleshoot')} />}
          <AppRow id="assistant" />
          {isAdmin && <AppRow id="terminal" />}
          {isAdmin && <AppRow id="scripts" />}
          {isAdmin && <AppRow id="containers" />}
          {isAdmin && <NavRow icon="save" title="Export & restore" desc="Download a backup of NoCapOS itself" onClick={() => go('backup')} />}
          <NavRow icon="info" title="About this device" desc="Hardware, Docker engine and accelerators" onClick={() => go('about')} />
        </div>
      </div>
    </>
  );
}

// ---------------- Device info ----------------

function DeviceInfo({ isAdmin, onClose, onMore }: { isAdmin: boolean; onClose: () => void; onMore: () => void }) {
  const info = useSystem((s) => s.info);
  const [ips, setIps] = useState<string[] | null>(null);
  useEffect(() => {
    if (!isAdmin) return;
    void hostApi.network().then((r) => {
      if (r.ok) setIps(r.data.interfaces.flatMap((i) => i.addresses.map((a) => `${a.replace(/\/\d+$/, '')} (${i.name})`)));
    });
  }, [isAdmin]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  const h = info?.host;
  const rows: [string, string][] = h
    ? [
        ['Device name', h.hostname],
        ...(h.board ? [['Model', h.board] as [string, string]] : []),
        ['Processor', `${h.cpu_model || '–'}${h.cpu_cores ? ` (${h.cpu_cores} cores)` : ''}`],
        ['Memory', fmtBytes(h.mem_total)],
        ['Operating system', h.os || '–'],
        ['Kernel', h.kernel || '–'],
        ['Architecture', h.arch],
        ...(ips ? [['IP addresses', ips.length ? ips.join('\n') : 'None'] as [string, string]] : []),
        ['NoCapOS', info.version],
        ...(info.docker ? [['Docker', info.docker.server_version] as [string, string]] : []),
      ]
    : [];
  const copy = () => {
    void navigator.clipboard
      ?.writeText(rows.map(([k, v]) => `${k}: ${v.replace(/\n/g, ', ')}`).join('\n'))
      .then(() => toast('success', 'Device info copied'));
  };
  return (
    <div className="dialog-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog set-device" role="dialog" aria-label="Device info">
        <div className="set-device-head">
          <span className="set-device-icon"><Icon name="desktop" size={22} /></span>
          <div>
            <h3>{h?.hostname ?? 'This device'}</h3>
            <span className="muted small">Running NoCapOS {info?.version}</span>
          </div>
        </div>
        <dl className="set-device-list">
          {rows.map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={copy}>
            <Icon name="copy" size={14} /> Copy
          </button>
          <button type="button" className="ghost" onClick={onMore}>
            More details
          </button>
          <button type="button" onClick={onClose}>
            Done
          </button>
        </div>
      </div>
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
      <Group title="Manage">
        <Row label="Files" hint="Browse your drives, connect network drives and share folders">
          <button type="button" className="ghost small" onClick={() => openApp('files')}>Open</button>
        </Row>
        <Row label="Live usage" hint="Read and write speed, and every disk in real time">
          <button type="button" className="ghost small" onClick={() => openApp('monitor')}>Open</button>
        </Row>
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
      <Group title="Tools">
        <Row label="Live usage" hint="See what's using the CPU, memory, disks and network">
          <button type="button" className="ghost small" onClick={() => openApp('monitor')}>Open</button>
        </Row>
        <Row label="Containers" hint="Restart an app or read its logs">
          <button type="button" className="ghost small" onClick={() => openApp('containers')}>Open</button>
        </Row>
        <Row label="Terminal" hint="Run commands on the host, e.g. journalctl -u nocapos">
          <button type="button" className="ghost small" onClick={() => openApp('terminal')}>Open</button>
        </Row>
      </Group>
    </div>
  );
}

// ---------------- About ----------------

function About() {
  const info = useSystem((s) => s.info);
  if (!info) return <p className="muted">Loading…</p>;
  const h = info.host;
  return (
    <div className="stack settings-page">
      <div className="settings-card about-hero">
        <Logo size={84} />
        <div className="about-meta">
          <span className="chip">Version {info.version}</span>
          <span className="chip">Docker-native Agent OS</span>
        </div>
      </div>
      <Group title="Device">
        <div className="settings-row full">
          <div className="stats-row">
            <Stat label="Hostname" value={h.hostname} />
            <Stat label="Operating system" value={h.os || '–'} />
            <Stat label="Kernel" value={h.kernel || '–'} />
            <Stat label="Architecture" value={h.arch} />
          </div>
          <div className="stats-row">
            <Stat label="Processor" value={`${h.cpu_model || '–'}${h.cpu_cores ? ` (${h.cpu_cores} cores)` : ''}`} />
            <Stat label="Memory" value={fmtBytes(h.mem_total)} />
            {h.board && <Stat label="Model" value={h.board} />}
          </div>
        </div>
      </Group>
      <Group title="Docker engine">
        <div className="settings-row full">
          {info.docker ? (
            <div className="stats-row">
              <Stat label="Version" value={info.docker.server_version} />
              <Stat label="API" value={info.docker.api_version} />
              <Stat label="Storage driver" value={info.docker.storage_driver} />
              <Stat label="Runtimes" value={info.docker.runtimes.join(', ')} />
            </div>
          ) : (
            <p className="muted small">Not connected — {info.docker_error ?? 'Docker is not running'}.</p>
          )}
        </div>
      </Group>
      <Group title="Accelerators">
        <div className="settings-row full">
          {info.accelerators.length ? (
            <ul className="plain">
              {info.accelerators.map((a) => (
                <li key={a.id}>
                  {a.name} <span className="muted small">· {a.kind.toUpperCase()} · {a.vendor}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted small">None detected.</p>
          )}
        </div>
      </Group>
    </div>
  );
}
