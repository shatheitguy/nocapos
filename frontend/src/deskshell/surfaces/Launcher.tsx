// The roster (full-screen overlay): search, pages of apps with dots, and
// automatic folders by category. Escape or a click on empty space hides it;
// launching an app hides it too.
import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as RKeyboardEvent,
  type MouseEvent as RMouseEvent,
  type WheelEvent as RWheelEvent,
} from 'react';
import { ContextMenu, type MenuItem } from '../../components/ContextMenu';
import { Icon } from '../../components/Icon';
import { call, send } from '../bridge';
import { useEvent, useHostValue, useShellInfo, useSurfaceShown } from '../hooks';
import { useT } from '../i18n';
import { buildRoster, moveSelection, paginate, rosterGrid, searchApps, togglePin, type FolderId, type RosterEntry } from '../logic';
import type { AppInfo } from '../protocol';
import { AppIcon } from '../ui';

const hide = () => send('surface.hide', { name: 'launcher' });
const ICON = 64;
const NAV_KEYS = ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End'];

function FolderTile({ apps, size }: { apps: AppInfo[]; size: number }) {
  const shown = apps.slice(0, 9);
  const per = shown.length > 4 ? 3 : 2;
  const pad = size * 0.12;
  const gap = size * 0.06;
  const mini = (size - pad * 2 - gap * (per - 1)) / per;
  return (
    <span className="folder-icon nsh-folder" style={{ width: size, height: size, borderRadius: size * 0.28, padding: pad, gap, gridTemplateColumns: `repeat(${per}, ${mini}px)` }}>
      {shown.map((a) => (
        <AppIcon key={a.id} icon={a.icon} name={a.name} size={mini} />
      ))}
    </span>
  );
}

function useViewport() {
  const [size, setSize] = useState({ w: window.innerWidth, h: window.innerHeight });
  useEffect(() => {
    const on = () => setSize({ w: window.innerWidth, h: window.innerHeight });
    window.addEventListener('resize', on);
    return () => window.removeEventListener('resize', on);
  }, []);
  return size;
}

export function Launcher() {
  useShellInfo();
  const t = useT();
  const [apps, , reloadApps] = useHostValue<AppInfo[]>('apps.list', null, []);
  const [pinned, setPinned] = useState<string[]>([]);
  const [q, setQ] = useState('');
  const [page, setPage] = useState(0);
  const [sel, setSel] = useState(-1);
  const [folder, setFolder] = useState<FolderId | null>(null);
  const [folderSel, setFolderSel] = useState(-1);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const { shown } = useSurfaceShown('launcher');
  const vp = useViewport();

  const loadPins = () =>
    call<{ pinned: string[] }>('dock.get')
      .then((d) => setPinned(Array.isArray(d?.pinned) ? d.pinned : []))
      .catch(() => {});

  // Every time it opens: a clean search on page one, fresh pins.
  useEffect(() => {
    setQ('');
    setPage(0);
    setSel(-1);
    setFolder(null);
    setMenu(null);
    void loadPins();
    input.current?.focus();
  }, [shown]);
  useEvent('apps.changed', () => reloadApps());

  const needle = q.trim();
  const roster = useMemo(() => buildRoster(apps), [apps]);
  const results = useMemo(() => (needle ? searchApps(apps, needle).map((app): RosterEntry => ({ kind: 'app', app })) : []), [apps, needle]);
  const list = needle ? results : roster;

  // The grid fills the space between the search field and the dots.
  const grid = rosterGrid(Math.min(vp.w - 48, 1080), vp.h - 230);
  const perPage = grid.cols * grid.rows;
  const pages = useMemo(() => paginate(list, perPage), [list, perPage]);
  const current = Math.min(page, pages.length - 1);
  const openFolder = folder ? roster.find((e): e is Extract<RosterEntry, { kind: 'folder' }> => e.kind === 'folder' && e.id === folder) : undefined;

  useLayoutEffect(() => {
    if (needle) {
      setSel(results.length ? 0 : -1);
      setPage(0);
    } else setSel(-1);
  }, [needle, results.length]);

  const launch = (app: AppInfo) => {
    send('apps.launch', { id: app.id });
    hide();
  };
  const activate = (e: RosterEntry) => (e.kind === 'folder' ? (setFolder(e.id), setFolderSel(-1)) : launch(e.app));

  const appMenu = (app: AppInfo): MenuItem[] => [
    { label: t('lp.open'), icon: 'launcher', onClick: () => launch(app) },
    {
      label: pinned.includes(app.id) ? t('dock.removeFromDock') : t('dock.addToDock'),
      icon: 'pin',
      onClick: () => {
        const next = togglePin(pinned, app.id);
        setPinned(next);
        send('dock.set', { pinned: next });
      },
    },
  ];
  const onMenu = (e: RMouseEvent, app: AppInfo) => {
    e.preventDefault();
    e.stopPropagation();
    setMenu({ x: e.clientX, y: e.clientY, items: appMenu(app) });
  };

  const onKey = (e: RKeyboardEvent) => {
    if (menu) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      if (folder) setFolder(null);
      else if (q) setQ('');
      else hide();
      return;
    }
    if (folder && openFolder) {
      const n = openFolder.apps.length;
      if (e.key === 'Enter') {
        e.preventDefault();
        const a = openFolder.apps[Math.max(0, folderSel)];
        if (a) launch(a);
        return;
      }
      const next = moveSelection(folderSel, e.key, n, Math.min(4, n), n);
      if (next !== folderSel) {
        e.preventDefault();
        setFolderSel(next);
      }
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      const target = list[sel >= 0 ? sel : 0];
      if (target && (sel >= 0 || needle)) activate(target);
      return;
    }
    if (!NAV_KEYS.includes(e.key)) return;
    // While searching, Left/Right/Home/End stay in the search text.
    if (needle && !['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown'].includes(e.key)) return;
    e.preventDefault();
    if (sel < 0) {
      // Nothing picked yet: Page keys turn pages, arrows pick the page's first icon.
      if (e.key === 'PageDown' || e.key === 'PageUp') setPage(Math.max(0, Math.min(pages.length - 1, current + (e.key === 'PageDown' ? 1 : -1))));
      else if (list.length) setSel(Math.min(list.length - 1, current * perPage));
      return;
    }
    const next = moveSelection(sel, e.key, list.length, grid.cols, perPage);
    setSel(next);
    if (!needle) setPage(Math.floor(next / perPage));
  };

  const onWheel = (e: RWheelEvent) => {
    if (needle || folder) return;
    const d = Math.abs(e.deltaY) > Math.abs(e.deltaX) ? e.deltaY : e.deltaX;
    if (Math.abs(d) < 25) return;
    setPage((p) => Math.max(0, Math.min(pages.length - 1, p + (d > 0 ? 1 : -1))));
  };

  const tile = (e: RosterEntry, i: number) => {
    const on = i === sel;
    if (e.kind === 'folder') {
      return (
        <div key={`f:${e.id}`} className="lp-cell">
          <button type="button" className={`launcher-app ${on ? 'nsh-sel' : ''}`} onClick={() => activate(e)} title={t(`nsh.folder.${e.id}`)}>
            <FolderTile apps={e.apps} size={ICON} />
            <span>{t(`nsh.folder.${e.id}`)}</span>
          </button>
        </div>
      );
    }
    return (
      <div key={e.app.id} className="lp-cell">
        <button
          type="button"
          className={`launcher-app ${on ? 'nsh-sel' : ''}`}
          title={e.app.comment || e.app.name}
          onClick={() => launch(e.app)}
          onContextMenu={(ev) => onMenu(ev, e.app)}
        >
          <AppIcon icon={e.app.icon} name={e.app.name} size={ICON} />
          <span>{e.app.name}</span>
        </button>
      </div>
    );
  };

  return (
    <div className="launcher nsh-launcher" onPointerDown={(e) => e.target === e.currentTarget && hide()} onKeyDown={onKey} onWheel={onWheel}>
      <div className="launcher-panel" onPointerDown={(e) => (e.target as Element).classList.contains('launcher-panel') && hide()}>
        <label className="search">
          <Icon name="search" size={18} />
          <input ref={input} value={q} autoFocus placeholder={t('lp.search')} aria-label={t('lp.search')} onChange={(e) => setQ(e.target.value)} />
        </label>
        <div className={`launcher-pages ${needle ? 'searching' : ''}`} onPointerDown={(e) => e.target === e.currentTarget && hide()}>
          {needle ? (
            <div className="launcher-grid launcher-results" style={{ gridTemplateColumns: `repeat(${grid.cols}, minmax(0, 1fr))` }}>
              {results.map(tile)}
              {!results.length && <p className="nsh-none">{t('nsh.lp.none', { q: needle })}</p>}
            </div>
          ) : (
            <div className="launcher-track" style={{ transform: `translateX(${-current * 100}%)` }}>
              {pages.map((items, p) => (
                <div
                  key={p}
                  className="launcher-grid launcher-page"
                  style={{ gridTemplateColumns: `repeat(${grid.cols}, minmax(0, 1fr))`, gridTemplateRows: `repeat(${grid.rows}, minmax(0, 1fr))` }}
                  inert={p !== current}
                  onPointerDown={(e) => e.target === e.currentTarget && hide()}
                >
                  {items.map((en, i) => tile(en, p * perPage + i))}
                </div>
              ))}
            </div>
          )}
        </div>
        {!needle && pages.length > 1 && (
          <div className="launcher-dots" role="tablist" aria-label={t('lp.pages')}>
            {pages.map((_, p) => (
              <button
                key={p}
                type="button"
                role="tab"
                aria-selected={p === current}
                aria-label={t('nsh.lp.page', { n: p + 1, of: pages.length })}
                className={p === current ? 'on' : ''}
                onClick={() => setPage(p)}
              />
            ))}
          </div>
        )}
        {apps.length === 0 && <p className="nsh-none">{t('nsh.loading')}</p>}
      </div>

      {openFolder && (
        <div className="folder-backdrop launchpad nsh-folder-backdrop" onPointerDown={(e) => e.target === e.currentTarget && setFolder(null)}>
          <div className="folder-view" role="dialog" aria-label={t(`nsh.folder.${openFolder.id}`)}>
            <div className="folder-name nsh-folder-name">{t(`nsh.folder.${openFolder.id}`)}</div>
            <div className="folder-grid">
              {openFolder.apps.map((a, i) => (
                <button
                  key={a.id}
                  type="button"
                  className={`launcher-app ${i === folderSel ? 'nsh-sel' : ''}`}
                  title={a.comment || a.name}
                  onClick={() => launch(a)}
                  onContextMenu={(ev) => onMenu(ev, a)}
                >
                  <AppIcon icon={a.icon} name={a.name} size={56} />
                  <span>{a.name}</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}
