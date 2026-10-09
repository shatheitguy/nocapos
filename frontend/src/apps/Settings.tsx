import { useEffect, useState } from 'react';
import { UserAvatar } from '../components/UserAvatar';
import { Logo } from '../components/Logo';
import { getUser } from '../api/client';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { useSystem } from '../state/system';
import type { WinState } from '../state/windows';
import { Stat } from './Monitor';
import { Appearance, DesktopSettings, DockSettings, Wallpaper, WindowSettings } from './Personalize';
import { FocusSettings, NetworkSettings, PowerSettings } from './SystemPages';
import { UsersSettings } from './UsersSettings';
import { Backup, DateTime, LanguageRegion, LockScreen, Security, SoundSettings } from './SettingsSections';

export type Section =
  | 'network'
  | 'appearance'
  | 'wallpaper'
  | 'dock'
  | 'desktop'
  | 'windows'
  | 'sound'
  | 'focus'
  | 'users'
  | 'security'
  | 'lock'
  | 'datetime'
  | 'language'
  | 'power'
  | 'backup'
  | 'about';

interface Item {
  id: Section;
  label: string;
  icon: IconName;
  admin?: boolean;
  /** Extra words the sidebar search matches. */
  keywords?: string;
}

// Grouped like a desktop OS: connections first, then look & feel, sound,
// people & security, and the machine itself.
const GROUPS: { title: string; items: Item[] }[] = [
  {
    title: 'Network',
    items: [{ id: 'network', label: 'Network', icon: 'wifi', admin: true, keywords: 'wifi wi-fi ethernet internet ip address interface lan' }],
  },
  {
    title: 'Personalization',
    items: [
      { id: 'appearance', label: 'Appearance', icon: 'palette', keywords: 'theme dark light accent color cyber deck brightness transparency motion' },
      { id: 'wallpaper', label: 'Wallpaper', icon: 'image', keywords: 'background photo picture dim' },
      { id: 'dock', label: 'Dock', icon: 'launcher', keywords: 'taskbar hide magnify recent position size' },
      { id: 'desktop', label: 'Desktop & Widgets', icon: 'desktop', keywords: 'icons widgets clean up grid' },
      { id: 'windows', label: 'Windows', icon: 'maximize', keywords: 'title bar buttons macos menu bar double click' },
    ],
  },
  {
    title: 'Sound & Notifications',
    items: [
      { id: 'sound', label: 'Sound', icon: 'sound', keywords: 'volume effects mute clicks' },
      { id: 'focus', label: 'Focus', icon: 'moon', keywords: 'do not disturb notifications silence' },
    ],
  },
  {
    title: 'Users & Security',
    items: [
      { id: 'users', label: 'Users & Roles', icon: 'users', keywords: 'account profile people admin linux sign out' },
      { id: 'security', label: 'Security', icon: 'shield', keywords: 'password two-factor 2fa totp authenticator' },
      { id: 'lock', label: 'Lock Screen', icon: 'lock', keywords: 'auto-lock screensaver screen saver idle' },
    ],
  },
  {
    title: 'System',
    items: [
      { id: 'datetime', label: 'Date & Time', icon: 'clock', keywords: 'clock time zone 24-hour calendar week' },
      { id: 'language', label: 'Language & Region', icon: 'globe', keywords: 'locale format currency' },
      { id: 'power', label: 'Power', icon: 'power', keywords: 'restart reboot shut down shutdown' },
      { id: 'backup', label: 'Backup', icon: 'save', keywords: 'export database restore' },
      { id: 'about', label: 'About', icon: 'info', keywords: 'version device hardware docker' },
    ],
  },
];
const ALL = GROUPS.flatMap((g) => g.items);
/** Every Settings page, for universal search. */
export const SETTINGS_PAGES: readonly { id: string; label: string; icon: IconName; admin?: boolean; keywords?: string }[] = ALL;
const isSection = (v: string | undefined): v is Section => !!v && ALL.some((i) => i.id === v);
// Older links ("account") now live in Users & Roles.
const resolve = (v: string | undefined): Section | undefined => (v === 'account' ? 'users' : isSection(v) ? v : undefined);

export function Settings({ win }: { win: WinState }) {
  const user = getUser();
  const isAdmin = user?.role === 'admin';
  const visible = (i: Item) => isAdmin || !i.admin;
  const first = ALL.find(visible)!.id;
  const wanted = resolve(win.props?.section);
  const [section, setSection] = useState<Section>(wanted ?? first);
  const [query, setQuery] = useState('');
  // Opening Settings at a specific page (e.g. "Change wallpaper…") while it's
  // already open switches to that page.
  useEffect(() => {
    if (wanted) setSection(wanted);
  }, [wanted, win.props]);

  const q = query.trim().toLowerCase();
  const results = q ? ALL.filter((i) => visible(i) && `${i.label} ${i.keywords ?? ''}`.toLowerCase().includes(q)) : [];
  const current = ALL.find((i) => i.id === section) ?? ALL[0];
  const go = (id: Section) => {
    setSection(id);
    setQuery('');
  };
  const navButton = (i: Item) => (
    <button key={i.id} type="button" className={section === i.id ? 'on' : ''} onClick={() => go(i.id)} title={i.label}>
      <Icon name={i.icon} size={16} /> {i.label}
    </button>
  );

  return (
    <div className="app-split">
      <nav className="sidebar settings-nav">
        {user && (
          <button type="button" className={`settings-profile ${section === 'users' ? 'on' : ''}`} onClick={() => go('users')} title="Your account">
            <UserAvatar name={user.username} />
            <span className="settings-profile-text">
              <b>{user.username}</b>
              <span>{isAdmin ? 'Administrator' : 'Standard user'}</span>
            </span>
          </button>
        )}
        <label className="settings-search">
          <Icon name="search" size={14} />
          <input
            value={query}
            placeholder="Search settings"
            aria-label="Search settings"
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && results[0] && go(results[0].id)}
          />
        </label>
        {q ? (
          <div className="sidebar-group">
            <span className="sidebar-heading">{results.length ? 'Results' : 'No matches'}</span>
            {results.map(navButton)}
          </div>
        ) : (
          GROUPS.map((g) => {
            const items = g.items.filter(visible);
            if (!items.length) return null;
            return (
              <div key={g.title} className="sidebar-group">
                <span className="sidebar-heading">{g.title}</span>
                {items.map(navButton)}
              </div>
            );
          })
        )}
      </nav>
      <div className="app-content">
        <h2 className="settings-title">{current.label}</h2>
        {section === 'network' && <NetworkSettings />}
        {section === 'appearance' && <Appearance />}
        {section === 'wallpaper' && <Wallpaper />}
        {section === 'dock' && <DockSettings />}
        {section === 'desktop' && <DesktopSettings />}
        {section === 'windows' && <WindowSettings />}
        {section === 'sound' && <SoundSettings />}
        {section === 'focus' && <FocusSettings />}
        {section === 'users' && <UsersSettings isAdmin={isAdmin} onSecurity={() => go('security')} />}
        {section === 'security' && <Security />}
        {section === 'lock' && <LockScreen />}
        {section === 'datetime' && <DateTime />}
        {section === 'language' && <LanguageRegion />}
        {section === 'power' && <PowerSettings isAdmin={isAdmin} />}
        {section === 'backup' && <Backup />}
        {section === 'about' && <About />}
      </div>
    </div>
  );
}

function About() {
  const info = useSystem((s) => s.info);
  if (!info) return <p className="muted">Loading…</p>;
  const h = info.host;
  return (
    <div className="stack">
      <div className="panel about-hero">
        <Logo size={84} />
        <div className="about-meta">
          <span className="chip">Version {info.version}</span>
          <span className="chip">Docker-native Agent OS</span>
        </div>
      </div>
      <div className="panel">
        <b>Device</b>
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
      <div className="panel">
        <b>Docker engine</b>
        {info.docker ? (
          <div className="stats-row">
            <Stat label="Version" value={info.docker.server_version} />
            <Stat label="API" value={info.docker.api_version} />
            <Stat label="Storage driver" value={info.docker.storage_driver} />
            <Stat label="Runtimes" value={info.docker.runtimes.join(', ')} />
          </div>
        ) : (
          <p className="muted">Not connected — {info.docker_error ?? 'Docker is not running'}.</p>
        )}
      </div>
      <div className="panel">
        <b>Accelerators</b>
        {info.accelerators.length ? (
          <ul className="plain">
            {info.accelerators.map((a) => (
              <li key={a.id}>
                {a.name} <span className="muted small">· {a.kind.toUpperCase()} · {a.vendor}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">None detected.</p>
        )}
      </div>
    </div>
  );
}
