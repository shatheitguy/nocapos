import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { openStoreApp, storeApi, type StoreApp } from '../api/appstore';
import { api, logout } from '../api/client';
import { fileKind } from '../api/files';
import { openApp, visibleApps } from '../apps/meta';
import { SETTINGS_PAGES } from '../apps/Settings';
import { AppIcon } from '../components/AppTile';
import { Icon, type IconName } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { powerHost } from '../lib/hostActions';
import { usePrefs } from '../state/prefs';
import { useRecents } from '../state/recents';
import { useSpotlight } from '../state/spotlight';

// Universal search: apps, settings, files, App Store apps and actions in one
// box (Ctrl+Space). Arrow keys move, Enter opens, Esc closes.

interface FileHit {
  root: string;
  path: string;
  name: string;
  dir: boolean;
  size: number;
  mod_time: string;
}

type Group = 'Top hit' | 'Apps' | 'Settings' | 'Actions' | 'App Store' | 'Files';

interface Result {
  key: string;
  group: Group;
  title: string;
  subtitle: string;
  icon: ReactNode;
  big: ReactNode;
  details?: [string, string][];
  score: number;
  run: () => void;
}

/** How well `text` matches the query: 0 = no match. */
function match(q: string, ...texts: (string | undefined)[]): number {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  let best = 0;
  for (const raw of texts) {
    if (!raw) continue;
    const t = raw.toLowerCase();
    if (!words.every((w) => t.includes(w))) continue;
    let s = 10;
    const full = words.join(' ');
    if (t === full) s = 100;
    else if (t.startsWith(full)) s = 70;
    else if (words.every((w) => new RegExp(`(^|[\\s\\-_./(])${w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`).test(t))) s = 40;
    best = Math.max(best, s - (texts.indexOf(raw) > 0 ? 15 : 0)); // title beats description/keywords
  }
  return best;
}

const glyph = (name: IconName, tone = '') => (
  <span className={`sl-glyph ${tone}`}>
    <Icon name={name} size={16} />
  </span>
);

export function Spotlight({ isAdmin, onLock }: { isAdmin: boolean; onLock: () => void }) {
  const close = () => useSpotlight.getState().set(false);
  const [q, setQ] = useState('');
  const [sel, setSel] = useState(0);
  const [files, setFiles] = useState<FileHit[]>([]);
  const [filesInfo, setFilesInfo] = useState<{ building: boolean; indexed: number } | null>(null);
  const [store, setStore] = useState<StoreApp[]>([]);
  const recents = useRecents((s) => s.recents);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);

  useEffect(() => {
    input.current?.focus();
    if (isAdmin) void storeApi.list().then((r) => r.ok && setStore(r.data.apps));
  }, [isAdmin]);

  // File names come from the server's index (debounced).
  useEffect(() => {
    const needle = q.trim();
    if (!isAdmin || needle.length < 2) {
      setFiles([]);
      return;
    }
    let live = true;
    const t = window.setTimeout(async () => {
      const r = await api<{ results: FileHit[]; building: boolean; indexed: number }>(`/api/v1/files/search?${new URLSearchParams({ q: needle, limit: '12' })}`);
      if (live && r.ok) {
        setFiles(r.data.results);
        setFilesInfo({ building: r.data.building, indexed: r.data.indexed });
      }
    }, 140);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [q, isAdmin]);

  const results = useMemo(() => {
    const needle = q.trim();
    const out: Result[] = [];
    const go = (fn: () => void) => () => {
      fn();
      close();
    };

    // Apps
    const apps = visibleApps(isAdmin);
    for (const a of apps) {
      const s = needle ? match(needle, a.title, a.description) : recents.includes(a.id) ? 50 - recents.indexOf(a.id) : 0;
      if (!s) continue;
      out.push({
        key: `app:${a.id}`,
        group: 'Apps',
        title: a.title,
        subtitle: a.description,
        icon: <AppIcon app={a} size={22} />,
        big: <AppIcon app={a} size={64} />,
        details: [['Kind', 'App']],
        score: s + 6,
        run: go(() => openApp(a.id)),
      });
    }
    if (!needle) return out.slice(0, 8);

    // Settings pages
    for (const p of SETTINGS_PAGES) {
      if (p.admin && !isAdmin) continue;
      const s = match(needle, p.label, p.keywords);
      if (!s) continue;
      out.push({
        key: `set:${p.id}`,
        group: 'Settings',
        title: p.label,
        subtitle: 'Settings',
        icon: glyph(p.icon),
        big: <span className="sl-big-glyph">{<Icon name={p.icon} size={34} />}</span>,
        details: [['Opens', `Settings → ${p.label}`]],
        score: s,
        run: go(() => openApp('settings', { props: { section: p.id } })),
      });
    }

    // Actions
    const prefs = usePrefs.getState();
    const actions: { id: string; title: string; words: string; icon: IconName; admin?: boolean; run: () => void }[] = [
      { id: 'lock', title: 'Lock Screen', words: 'lock away', icon: 'lock', run: onLock },
      { id: 'logout', title: 'Sign Out', words: 'log out logout sign out exit', icon: 'logout', run: () => void logout() },
      {
        id: 'theme',
        title: prefs.theme === 'light' ? 'Switch to Dark Mode' : 'Switch to Light Mode',
        words: 'dark light mode theme appearance night',
        icon: prefs.theme === 'light' ? 'moon' : 'sun',
        run: () => usePrefs.getState().set({ theme: prefs.theme === 'light' ? 'dark' : 'light' }),
      },
      {
        id: 'focus',
        title: prefs.focusMode ? 'Turn Off Focus' : 'Turn On Focus',
        words: 'focus do not disturb notifications quiet',
        icon: 'moon',
        run: () => usePrefs.getState().set({ focusMode: !prefs.focusMode }),
      },
      { id: 'newterm', title: 'New Terminal Window', words: 'terminal shell console bash root', icon: 'terminal', admin: true, run: () => openApp('terminal', { newWindow: true }) },
      { id: 'newfiles', title: 'New Files Window', words: 'files finder explorer folder', icon: 'folder', admin: true, run: () => openApp('files', { newWindow: true }) },
      { id: 'restart', title: 'Restart…', words: 'restart reboot', icon: 'restart', admin: true, run: () => void powerHost('reboot') },
      { id: 'shutdown', title: 'Shut Down…', words: 'shut down shutdown power off', icon: 'power', admin: true, run: () => void powerHost('shutdown') },
    ];
    for (const a of actions) {
      if (a.admin && !isAdmin) continue;
      const s = match(needle, a.title, a.words);
      if (!s) continue;
      out.push({
        key: `act:${a.id}`,
        group: 'Actions',
        title: a.title,
        subtitle: 'Action',
        icon: glyph(a.icon, 'action'),
        big: <span className="sl-big-glyph">{<Icon name={a.icon} size={34} />}</span>,
        details: [['Kind', 'Action']],
        score: s - 2,
        run: go(a.run),
      });
    }

    // App Store
    for (const a of store) {
      const s = match(needle, a.name, `${a.tagline} ${a.category} ${a.developer}`);
      if (!s) continue;
      const running = a.installed?.status === 'running';
      out.push({
        key: `store:${a.id}`,
        group: 'App Store',
        title: a.name,
        subtitle: running ? 'Installed · open in a new tab' : a.installed ? 'Installed' : `App Store · ${a.tagline}`,
        icon: (
          <AppIcon app={a} size={24} />
        ),
        big: (
          <AppIcon app={a} size={64} />
        ),
        details: [
          ['Category', a.category],
          ['Status', a.installed ? a.installed.status : 'Not installed'],
        ],
        score: s - 4,
        run: go(() => (running ? openStoreApp(a) : openApp('appcenter', { key: 'appcenter', props: { app: a.id } }))),
      });
    }

    // Files
    for (const f of files) {
      const kind = fileKind(f.name, f.dir);
      const parent = f.path.slice(0, f.path.lastIndexOf('/')) || '/';
      out.push({
        key: `file:${f.root}:${f.path}`,
        group: 'Files',
        title: f.name,
        subtitle: parent,
        icon: glyph(f.dir ? 'folder' : kind === 'image' ? 'fileImage' : kind === 'code' ? 'fileCode' : kind === 'pdf' ? 'filePdf' : 'file', f.dir ? 'folder' : 'file'),
        big: <span className="sl-big-glyph">{<Icon name={f.dir ? 'folder' : 'file'} size={34} />}</span>,
        details: [
          ['Where', parent],
          ...(f.dir ? [] : ([['Size', fmtBytes(f.size)]] as [string, string][])),
          ['Modified', new Date(f.mod_time).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })],
        ],
        score: 0,
        run: go(() =>
          f.dir
            ? openApp('files', { props: { root: f.root, path: f.path }, newWindow: true })
            : openApp('viewer', { title: f.name, props: { root: f.root, path: f.path }, key: `viewer:${f.root}:${f.path}` }),
        ),
      });
    }

    // Best non-file match becomes the Top hit.
    const ranked = out.filter((r) => r.group !== 'Files').sort((a, b) => b.score - a.score);
    if (ranked[0]) ranked[0] = { ...ranked[0], group: 'Top hit' };
    const order: Group[] = ['Top hit', 'Apps', 'Settings', 'Actions', 'App Store', 'Files'];
    const top = ranked[0];
    const rest = [...ranked.slice(1), ...out.filter((r) => r.group === 'Files')];
    const grouped = order.flatMap((g) => (g === 'Top hit' ? (top ? [top] : []) : rest.filter((r) => r.group === g).slice(0, g === 'Files' ? 12 : 6)));
    return grouped;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, files, store, isAdmin, recents]);

  useEffect(() => setSel(0), [q]);
  useEffect(() => {
    list.current?.querySelector('.sl-row.on')?.scrollIntoView({ block: 'nearest' });
  }, [sel]);

  const current = results[Math.min(sel, results.length - 1)];

  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape') close();
    else if (e.key === 'ArrowDown') setSel((s) => Math.min(s + 1, results.length - 1));
    else if (e.key === 'ArrowUp') setSel((s) => Math.max(s - 1, 0));
    else if (e.key === 'Enter') current?.run();
    else return;
    e.preventDefault();
  };

  let lastGroup = '';
  return (
    <div className="spotlight-backdrop" onPointerDown={(e) => e.target === e.currentTarget && close()}>
      <div className="spotlight glass" role="dialog" aria-label="Search">
        <label className="sl-input">
          <Icon name="search" size={20} />
          <input
            ref={input}
            value={q}
            placeholder="Search apps, settings, files and actions"
            aria-label="Search"
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onKey}
          />
          {q && (
            <button type="button" className="ghost icon-btn" aria-label="Clear" onClick={() => setQ('')}>
              <Icon name="close" size={13} />
            </button>
          )}
        </label>
        {(results.length > 0 || q.trim()) && (
          <div className="sl-body">
            <div ref={list} className="sl-list" role="listbox">
              {!q.trim() && results.length > 0 && <div className="sl-group">Recent apps</div>}
              {results.map((r, i) => {
                const head = q.trim() && r.group !== lastGroup ? r.group : null;
                lastGroup = r.group;
                return (
                  <div key={r.key}>
                    {head && <div className="sl-group">{head}</div>}
                    <button
                      type="button"
                      role="option"
                      aria-selected={i === sel}
                      className={`sl-row ${i === sel ? 'on' : ''}`}
                      onMouseMove={() => i !== sel && setSel(i)}
                      onClick={r.run}
                    >
                      <span className="sl-row-icon">{r.icon}</span>
                      <span className="sl-row-text">
                        <b>{r.title}</b>
                        <span>{r.subtitle}</span>
                      </span>
                    </button>
                  </div>
                );
              })}
              {q.trim() && results.length === 0 && (
                <p className="sl-empty">
                  No results for “{q.trim()}”.
                  {filesInfo?.building ? ' Files are still being indexed.' : ''}
                </p>
              )}
            </div>
            {current && (
              <aside className="sl-preview">
                {current.big}
                <b>{current.title}</b>
                <span className="muted small">{current.subtitle}</span>
                {current.details && (
                  <dl>
                    {current.details.map(([k, v]) => (
                      <div key={k}>
                        <dt>{k}</dt>
                        <dd>{v}</dd>
                      </div>
                    ))}
                  </dl>
                )}
                <span className="sl-hint">↵ to open · ↑↓ to move · Esc to close</span>
              </aside>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

