import { focusUnlessTouch } from '../lib/touch';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent as RMouseEvent, type PointerEvent as RPointerEvent, type WheelEvent as RWheelEvent } from 'react';
import { tr } from '../i18n';
import type { StoreApp } from '../api/appstore';
import { canMultiWindow, openApp, visibleApps, type AppMeta } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { FolderIcon } from '../components/FolderIcon';
import { Icon } from '../components/Icon';
import { dropHint, pressApp, recheckDrag, setGridSlots, TOUCH_HOLD, useAppDrag } from '../state/appDrag';
import { arrange, cellAt, preview, slotAt } from '../lib/launcherOrder';
import { useFlip } from '../lib/flip';
import { useDesktopIcons } from '../state/desktopIcons';
import { folderKey, ungroup, useFolders, type AppFolder } from '../state/folders';
import { FolderView } from './FolderView';
import { StoreTile, useWiggleEscape } from './StoreAppGrid';
import { launchStoreApp, shownOnHome, storeKey, useStoreApps } from '../state/storeApps';
import { isCompact } from '../state/prefs';
import type { NativeApp } from '../api/nativeApps';
import { launchNativeApp, nativeKey, useNativeApps } from '../state/nativeApps';
import { NativeTile } from './NativeTile';
import { appMenuItems } from './Shelf';

const OPENS_WINDOW = new Set(['Open', 'Show', 'New window']);

/** Only administrators can remove apps, so only they get wiggle mode. */
const noWiggle = () => {};

/** How long a dragged icon rests at the page edge before the page turns. */
const EDGE_MS = 600;

type Entry =
  | { kind: 'folder'; folder: AppFolder }
  | { kind: 'app'; app: AppMeta }
  | { kind: 'store'; app: StoreApp }
  | { kind: 'native'; app: NativeApp };

const entryKey = (e: Entry) =>
  e.kind === 'folder' ? folderKey(e.folder.id) : e.kind === 'store' ? storeKey(e.app.id) : e.kind === 'native' ? nativeKey(e.app.id) : e.app.id;
const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

/** Rows × columns that fit the page area, like a phone's home screen. */
function gridFor(w: number, h: number) {
  const phone = isCompact();
  const cellW = phone ? 78 : 112;
  const cellH = phone ? 96 : 112;
  return { cols: clamp(Math.floor(w / cellW), 3, 8), rows: clamp(Math.floor(h / cellH), 2, 7) };
}

export function Launcher({ isAdmin, onClose }: { isAdmin: boolean; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const [openFolder, setOpenFolder] = useState<string | null>(null);
  const folders = useFolders((s) => s.folders);
  const [jiggle, setJiggle] = useState(false);
  useWiggleEscape(jiggle, setJiggle);
  const input = useRef<HTMLInputElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const pagesRef = useRef<HTMLDivElement>(null);
  const [grid, setGrid] = useState(() => gridFor(Math.min(window.innerWidth - 32, 1080), window.innerHeight * 0.6));
  const [page, setPage] = useState(0);
  const [dx, setDx] = useState(0); // live finger / pointer offset while swiping

  // What the pointer is over while dragging, to highlight the folder-to-be.
  const target = useAppDrag((s) => {
    if (!s.drag || !dropHint(s.drag, s.over)) return null;
    return s.over?.kind === 'app' ? s.over.appId : s.over?.kind === 'folder' ? folderKey(s.over.folderId) : null;
  });

  const all = useMemo(() => visibleApps(isAdmin), [isAdmin]);
  const needle = q.trim().toLowerCase();
  // Searching lists every matching app, folders or not; otherwise folders come first.
  const matches = needle ? all.filter((a) => a.title.toLowerCase().includes(needle) || a.description.toLowerCase().includes(needle) || (a.keywords ?? '').toLowerCase().includes(needle)) : [];
  const shownFolders = needle ? [] : folders.filter((f) => f.apps.some((id) => all.some((a) => a.id === id)));
  const inFolder = new Set(folders.flatMap((f) => f.apps));
  const apps = needle ? matches : all.filter((a) => !inFolder.has(a.id));
  const storeApps = useStoreApps((s) => s.apps).filter((a) => shownOnHome(a) && (!needle || `${a.name} ${a.tagline}`.toLowerCase().includes(needle)));
  // Installed Linux apps join Launchpad by themselves (never the desktop).
  const nativeApps = useNativeApps((s) => s.apps).filter((a) => !needle || `${a.name} ${a.id}`.toLowerCase().includes(needle));

  const natural: Entry[] = [
    ...shownFolders.map((folder) => ({ kind: 'folder' as const, folder })),
    ...apps.map((app) => ({ kind: 'app' as const, app })),
    ...storeApps.map((app) => ({ kind: 'store' as const, app })),
    ...nativeApps.map((app) => ({ kind: 'native' as const, app })),
  ];

  // The owner's order: saved keys first, new apps at the end, removed ones gone.
  const order = useFolders((s) => s.order);
  // While an icon is dragged in the grid (or out of a folder), the others make room for it.
  const dragKey = useAppDrag((s) => (s.drag && (s.drag.source === 'launchpad' || s.drag.source === 'folder') ? s.drag.appId : null));
  const fromFolder = useAppDrag((s) => !!s.drag?.folderId);
  const slot = useAppDrag((s) => (s.slot?.where === 'grid' ? s.slot.before : undefined));
  const byKey = new Map(natural.map((en) => [entryKey(en), en] as const));
  const baseKeys = arrange(order, [...byKey.keys()]);
  if (dragKey && fromFolder && !byKey.has(dragKey) && !needle) {
    const app = all.find((a) => a.id === dragKey);
    if (app) byKey.set(dragKey, { kind: 'app', app });
  }
  const shownKeys = needle || !dragKey ? baseKeys : preview(baseKeys, dragKey, slot);
  const entries: Entry[] = shownKeys.map((k) => byKey.get(k)!).filter(Boolean);

  // Fixed pages sized to the screen; a new page only when the last one is full.
  const perPage = grid.cols * grid.rows;
  const pages: Entry[][] = [];
  for (let i = 0; i < entries.length; i += perPage) pages.push(entries.slice(i, i + perPage));
  if (!pages.length) pages.push([]);
  const last = pages.length - 1;
  const current = Math.min(page, last);

  // Turning pages from handlers and listeners that outlive a render.
  const nav = useRef({ current, last });
  nav.current = { current, last };
  const go = (p: number) => setPage(clamp(p, 0, nav.current.last));

  // Dragging icons between others: the slot under the pointer, from the grid's cells.
  const layout = useRef({ shownKeys, baseKeys, grid, current, needle });
  layout.current = { shownKeys, baseKeys, grid, current, needle };
  useEffect(() => {
    setGridSlots({
      slot: (x, y, dragged) => {
        const { shownKeys: shown, grid: g, current: at, needle: searching } = layout.current;
        const box = pagesRef.current?.getBoundingClientRect();
        if (searching || !box) return undefined;
        const cell = cellAt(x, y, box, g.cols, g.rows, at);
        return cell ? slotAt(shown, dragged, cell.index, cell.after) : undefined;
      },
      keys: () => [...layout.current.baseKeys],
    });
    return () => setGridSlots(null);
  }, []);

  // Icons glide to their new places while one is dragged, and as it lands.
  const track = useRef<HTMLDivElement>(null);
  const settledAt = useRef(0);
  if (dragKey) settledAt.current = Date.now() + 400;
  useFlip(track, !!dragKey || Date.now() < settledAt.current);

  useLayoutEffect(() => {
    const el = pagesRef.current;
    if (!el) return;
    const measure = () => {
      if (!el.clientWidth || !el.clientHeight) return;
      const g = gridFor(el.clientWidth, el.clientHeight);
      setGrid((old) => (old.cols === g.cols && old.rows === g.rows ? old : g));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    window.addEventListener('resize', measure);
    return () => {
      ro.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, []);

  useEffect(() => setPage(0), [needle]);

  useEffect(() => {
    focusUnlessTouch(input.current);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose(); // wiggle mode catches Esc first
        return;
      }
      if (openFolder || menu) return;
      const typing = document.activeElement === input.current && !!input.current?.value;
      const { current: at, last: end } = nav.current;
      if (e.key === 'PageDown' || (!typing && e.key === 'ArrowRight')) go(at + 1);
      else if (e.key === 'PageUp' || (!typing && e.key === 'ArrowLeft')) go(at - 1);
      else if (!typing && e.key === 'Home') go(0);
      else if (!typing && e.key === 'End') go(end);
      else return;
      e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onClose, openFolder, menu]);

  // Launchpad stays open while an app is dragged around inside it, and gets out
  // of the way once the drag heads off the grid towards the Dock or Desktop.
  // Resting a dragged icon at the left or right edge turns the page.
  useEffect(() => {
    const edge = { dir: 0, timer: 0 };
    const stopEdge = () => {
      window.clearTimeout(edge.timer);
      edge.dir = 0;
    };
    const off = useAppDrag.subscribe(({ drag }) => {
      if (!drag || openFolder) return stopEdge();
      const r = panel.current?.getBoundingClientRect();
      if (r && (drag.x < r.left - 24 || drag.x > r.right + 24 || drag.y < r.top - 24 || drag.y > r.bottom + 24)) {
        stopEdge();
        onClose();
        return;
      }
      const p = pagesRef.current?.getBoundingClientRect();
      const dir = p && drag.y > p.top && drag.y < p.bottom ? (drag.x < p.left + 44 ? -1 : drag.x > p.right - 44 ? 1 : 0) : 0;
      if (dir === edge.dir) return;
      stopEdge();
      edge.dir = dir;
      if (dir)
        edge.timer = window.setTimeout(() => {
          edge.dir = 0; // the next move at the edge waits again
          go(nav.current.current + dir);
          window.setTimeout(recheckDrag, 350); // the icons under the pointer are the new page's now
        }, EDGE_MS);
    });
    return () => {
      off();
      stopEdge();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onClose, openFolder]);

  // ---- swipe and scroll between pages ----
  const swipe = useRef<{ id: number; x: number; y: number; t: number; on: boolean; empty: boolean } | null>(null);
  const wheel = useRef({ acc: 0, at: 0, until: 0 });

  const onSwipeDown = (e: RPointerEvent<HTMLDivElement>) => {
    if (needle || e.button !== 0 || !e.isPrimary) return;
    // A mouse on an icon drags the icon; a finger, or the empty space between icons, swipes.
    if ((e.target as Element).closest('input, .search, .launcher-dots, .context-menu, .folder-backdrop')) return;
    const onIcon = !!(e.target as Element).closest('.launcher-app, .store-tile');
    if (e.pointerType === 'mouse' && onIcon) return;
    swipe.current = { id: e.pointerId, x: e.clientX, y: e.clientY, t: e.timeStamp, on: false, empty: !onIcon };
  };
  const onSwipeMove = (e: RPointerEvent<HTMLDivElement>) => {
    const s = swipe.current;
    if (!s || s.id !== e.pointerId) return;
    if (s.on && useAppDrag.getState().drag) {
      // A held icon lifted after all: it is a drag, not a swipe.
      swipe.current = null;
      setDx(0);
      return;
    }
    const mx = e.clientX - s.x;
    const my = e.clientY - s.y;
    if (!s.on) {
      if (useAppDrag.getState().drag || Math.abs(my) > 14) {
        swipe.current = null; // an icon drag, or a vertical move: not a swipe
        return;
      }
      if (Math.abs(mx) < 10 || Math.abs(mx) < Math.abs(my)) return;
      s.on = true;
      e.currentTarget.setPointerCapture(e.pointerId);
    }
    const { current: at, last: end } = nav.current;
    const pastEnd = (at === 0 && mx > 0) || (at === end && mx < 0);
    setDx(pastEnd ? mx / 3 : mx); // rubber band at either end
  };
  const onSwipeEnd = (e: RPointerEvent<HTMLDivElement>) => {
    const s = swipe.current;
    swipe.current = null;
    if (!s || s.id !== e.pointerId) return;
    if (!s.on) {
      // A click on the empty space between icons closes Launchpad (or ends wiggle mode).
      if (s.empty && e.type === 'pointerup' && !(e.target as Element).closest('.launcher-app, .store-tile')) {
        if (jiggle) setJiggle(false);
        else onClose();
      }
      return;
    }
    const w = e.currentTarget.clientWidth || 1;
    const mx = e.clientX - s.x;
    const speed = mx / Math.max(1, e.timeStamp - s.t);
    if (e.type !== 'pointercancel') {
      if (mx < -w * 0.18 || speed < -0.45) go(nav.current.current + 1);
      else if (mx > w * 0.18 || speed > 0.45) go(nav.current.current - 1);
    }
    setDx(0);
    // The click that ends a swipe must not open the icon under it.
    const stop = (ev: MouseEvent) => {
      ev.preventDefault();
      ev.stopPropagation();
    };
    window.addEventListener('click', stop, { capture: true, once: true });
    window.setTimeout(() => window.removeEventListener('click', stop, { capture: true }), 0);
  };
  // Trackpads scroll sideways; a mouse wheel scrolls down. Either turns one page per gesture.
  const onWheel = (e: RWheelEvent) => {
    if (needle || openFolder) return;
    const w = wheel.current;
    const now = e.timeStamp;
    if (now - w.at > 220) w.acc = 0;
    w.at = now;
    if (now < w.until) return;
    w.acc += Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY;
    if (Math.abs(w.acc) < 40) return;
    go(nav.current.current + Math.sign(w.acc));
    w.acc = 0;
    w.until = now + 450; // let the gesture's momentum die down
  };

  const launch = (id: string, newWindow = false) => {
    openApp(id, { newWindow: newWindow && canMultiWindow(id) });
    onClose();
  };

  // Items that put a window on screen also dismiss Launchpad.
  const appMenu = (appId: string): MenuItem[] =>
    appMenuItems(appId).map((it) =>
      OPENS_WINDOW.has(it.label)
        ? {
            ...it,
            onClick: () => {
              it.onClick();
              onClose();
            },
          }
        : it,
    );

  const folderMenu = (id: string): MenuItem[] => {
    const desk = useDesktopIcons.getState();
    const key = folderKey(id);
    return [
      { label: tr('lp.open'), icon: 'folder', onClick: () => setOpenFolder(id) },
      desk.has(key)
        ? { label: tr('dock.removeFromDesktop'), icon: 'desktop', onClick: () => desk.remove(key) }
        : { label: tr('dock.addToDesktop'), icon: 'desktop', onClick: () => desk.place(key) },
      { label: tr('lp.ungroup'), icon: 'close', danger: true, onClick: () => ungroup(id) },
    ];
  };

  const storeMenu = (e: RMouseEvent, app: StoreApp) => {
    const desk = useDesktopIcons.getState();
    const key = storeKey(app.id);
    const items: MenuItem[] = [
      {
        label: tr('lp.open'),
        icon: 'launcher',
        onClick: () => {
          launchStoreApp(app);
          onClose();
        },
      },
    ];
    if (app.installed) {
      items.push(
        desk.has(key)
          ? { label: tr('dock.removeFromDesktop'), icon: 'desktop', onClick: () => desk.remove(key) }
          : { label: tr('dock.addToDesktop'), icon: 'desktop', onClick: () => desk.place(key) },
      );
    }
    if (isAdmin) {
      items.push({
        label: tr('lp.showInStore'),
        icon: 'store',
        onClick: () => {
          openApp('appcenter', { props: { app: app.id } });
          onClose();
        },
      });
    }
    setMenu({ x: e.clientX, y: e.clientY, items });
  };

  // Each icon sits in a cell keyed by its order key, so it can glide when the order changes.
  const tile = (en: Entry, i: number) => {
    const key = entryKey(en);
    return (
      <div key={key} className={`lp-cell ${key === dragKey ? 'lifted' : ''}`} data-key={key}>
        {tileBody(en, i)}
      </div>
    );
  };

  const tileBody = (en: Entry, i: number) => {
    if (en.kind === 'folder') {
      const f = en.folder;
      return (
        <button
          key={entryKey(en)}
          type="button"
          className={`launcher-app folder ${target === folderKey(f.id) ? 'drop-into' : ''}`}
          data-drop="folder"
          data-folder={f.id}
          data-scope="launchpad"
          title={tr('lp.folderTitle')}
          onPointerDown={(e) => pressApp(e, folderKey(f.id), 'launchpad', { touchHold: TOUCH_HOLD, onStart: () => setMenu(null) })}
          onClick={() => setOpenFolder(f.id)}
          onContextMenu={(e) => {
            e.preventDefault();
            setMenu({ x: e.clientX, y: e.clientY, items: folderMenu(f.id) });
          }}
        >
          <FolderIcon folder={f} size={56} />
          <span>{f.name}</span>
        </button>
      );
    }
    if (en.kind === 'store') {
      return (
        <StoreTile key={entryKey(en)} app={en.app} index={i} jiggle={jiggle} setJiggle={isAdmin ? setJiggle : noWiggle} size={56}
          onOpened={onClose} onMenu={storeMenu} buttonClass="launcher-app" />
      );
    }
    if (en.kind === 'native') {
      return (
        <NativeTile key={entryKey(en)} app={en.app} isAdmin={isAdmin} onOpened={onClose}
          onMenu={(e, items) => setMenu({ x: e.clientX, y: e.clientY, items })} />
      );
    }
    const a = en.app;
    return (
      <button
        key={entryKey(en)}
        type="button"
        className={`launcher-app ${target === a.id ? 'drop-into' : ''}`}
        data-drop="app"
        data-app={a.id}
        data-scope="launchpad"
        title={tr('lp.appTitle')}
        onPointerDown={(e) => pressApp(e, a.id, 'launchpad', { touchHold: TOUCH_HOLD, onStart: () => setMenu(null) })}
        onClick={(e) => launch(a.id, e.shiftKey)}
        onContextMenu={(e) => {
          e.preventDefault();
          setMenu({ x: e.clientX, y: e.clientY, items: appMenu(a.id) });
        }}
      >
        <AppIcon app={a} size={56} />
        <span>{a.title}</span>
      </button>
    );
  };

  const firstApp = entries.find((en) => en.kind !== 'folder');

  return (
    <div className="launcher" onPointerDown={(e) => e.target === e.currentTarget && (jiggle ? setJiggle(false) : onClose())}>
      <div
        className="launcher-panel"
        ref={panel}
        onPointerDownCapture={(e) => jiggle && !(e.target as Element).closest('.store-tile, .dialog, .dialog-backdrop') && setJiggle(false)}
        // Swipes start anywhere on the sheet: between icons, around the grid, by the dots.
        onPointerDown={onSwipeDown}
        onPointerMove={onSwipeMove}
        onPointerUp={onSwipeEnd}
        onPointerCancel={onSwipeEnd}
        onWheel={onWheel}
      >
        <label className="search">
          <Icon name="search" size={18} />
          <input
            ref={input}
            value={q}
            placeholder={tr('lp.search')}
            aria-label={tr('lp.search')}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key !== 'Enter' || !firstApp) return;
              if (firstApp.kind === 'store') {
                launchStoreApp(firstApp.app);
                onClose();
              } else if (firstApp.kind === 'native') {
                void launchNativeApp(firstApp.app);
                onClose();
              } else if (firstApp.kind === 'app') launch(firstApp.app.id, e.shiftKey);
            }}
          />
        </label>
        <div
          ref={pagesRef}
          className={`launcher-pages ${needle ? 'searching' : ''}`}
          data-drop="launchpad"
          onScroll={(e) => {
            // Focus moving to an icon on another page must not scroll the strip itself.
            if (!needle) e.currentTarget.scrollLeft = 0;
          }}
        >
          {needle ? (
            // Search results from every page, together.
            <div className="launcher-grid launcher-results" data-no-drag>
              {entries.map(tile)}
              {!entries.length && <p className="muted">No apps match “{q}”.</p>}
            </div>
          ) : (
            <div
              ref={track}
              className={`launcher-track ${dx ? 'dragging' : ''}`}
              style={{ transform: `translateX(calc(${-current * 100}% + ${dx}px))` }}
            >
              {pages.map((items, p) => (
                <div
                  key={p}
                  className="launcher-grid launcher-page"
                  style={{ gridTemplateColumns: `repeat(${grid.cols}, minmax(0, 1fr))`, gridTemplateRows: `repeat(${grid.rows}, minmax(0, 1fr))` }}
                  aria-label={`Page ${p + 1} of ${pages.length}`}
                  inert={p !== current}
                  onFocusCapture={() => p !== current && go(p)}
                >
                  {items.map((en, i) => tile(en, p * perPage + i))}
                </div>
              ))}
            </div>
          )}
        </div>
        {!needle && pages.length > 1 && (
          <div className="launcher-dots" role="tablist" aria-label={tr('lp.pages')}>
            {pages.map((_, p) => (
              <button
                key={p}
                type="button"
                role="tab"
                aria-selected={p === current}
                aria-label={`Page ${p + 1}`}
                className={p === current ? 'on' : ''}
                onClick={() => go(p)}
              />
            ))}
          </div>
        )}
        <p className="launcher-tip muted small">
          {jiggle
            ? 'Tap ✕ to uninstall an app · Esc or click anywhere to finish'
            : tr('lp.tip')}
        </p>
      </div>
      {openFolder && (
        <FolderView folderId={openFolder} isAdmin={isAdmin} scope="launchpad" onClose={() => setOpenFolder(null)} onOpened={onClose} />
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}
