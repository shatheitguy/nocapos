// NoCap Desktop's file picker. Whenever a page on the screen asks for files
// (an Upload button, a wallpaper, a key file: any <input type=file>), the
// NoCap Shell host sends `files.choose` instead of opening the toolkit's
// dialog, and this shows the files the way Files does: the same sidebar
// (Home, Recents, Drops, Favorites, shared folders, drives), icons, thumbnails
// and theme. The picked files go back with `files.pick` (where each is on
// this machine, and a ticketed link for anything the account can't read
// there), or `files.cancel`. shell/host/picker.c is the other half.
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react';
import { useT } from '../i18n';
import {
  RECYCLE,
  baseName,
  fileApi,
  fileKind,
  fileTicket,
  joinPath,
  parentPath,
  rawUrl,
  type ExternalDrive,
  type Favorite,
  type FileEntry,
  type FileRoot,
} from '../api/files';
import { api, getUser } from '../api/client';
import { ContextMenu, type MenuEntry } from '../components/Dialog';
import { Icon } from '../components/Icon';
import { Modal } from '../components/Modal';
import { FileGlyph } from '../apps/FilesParts';
import { FilesSidebar } from '../apps/FilesSidebar';
import { useNetwork } from '../apps/NetworkDrives';
import type { Special } from '../apps/Files';
import { isHomeDir } from '../lib/fileRoots';
import { fmtBytes } from '../lib/format';
import { acceptEndings, acceptKind, acceptTest, acceptsEverything } from '../lib/accept';
import { hostCall, hostOn } from './platform';

/** A file request from the host. */
export interface PickRequest {
  request: number;
  multiple: boolean;
  accept: string[];
}

/** The request the host sent, checked (anything else is ignored). */
export function parsePickRequest(p: unknown): PickRequest | null {
  if (!p || typeof p !== 'object') return null;
  const o = p as Record<string, unknown>;
  if (typeof o.request !== 'number' || !Number.isInteger(o.request) || o.request < 1) return null;
  const accept = Array.isArray(o.accept) ? o.accept.filter((a): a is string => typeof a === 'string').slice(0, 64) : [];
  return { request: o.request, multiple: o.multiple === true, accept };
}

interface Item {
  key: string;
  root: string;
  path: string;
  entry: FileEntry;
  /** A label shown under the name (where a Recents or search hit is). */
  where?: string;
}

const isLocal = (r: FileRoot) => r.id !== 'system' && !r.id.startsWith('net:') && r.kind !== 'share' && r.kind !== 'homes';

/** Listens for file requests and shows the picker for each. */
export function NativeFilePicker() {
  const [req, setReq] = useState<PickRequest | null>(null);
  useEffect(
    () =>
      hostOn('files.choose', (p) => {
        const r = parsePickRequest(p);
        if (r) setReq(r);
      }),
    [],
  );
  if (!req) return null;
  return <FilePicker key={req.request} req={req} onDone={() => setReq((cur) => (cur?.request === req.request ? null : cur))} />;
}

function FilePicker({ req, onDone }: { req: PickRequest; onDone: () => void }) {
  const t = useT();
  const admin = getUser()?.role === 'admin';
  const [roots, setRoots] = useState<FileRoot[] | null>(null);
  const [favs, setFavs] = useState<Favorite[]>([]);
  const [external, setExternal] = useState<ExternalDrive[]>([]);
  const net = useNetwork(admin);
  const [at, setAt] = useState<{ root: string; path: string; special: Special }>({ root: '', path: '/', special: null });
  const [back, setBack] = useState<{ root: string; path: string; special: Special }[]>([]);
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [anchor, setAnchor] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState('');
  const [tickets, setTickets] = useState<Record<string, string>>({});
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuEntry[] } | null>(null);
  const done = useRef(false);
  const box = useRef<HTMLDivElement>(null);

  const fits = useMemo(() => acceptTest(req.accept), [req.accept]);
  const restricted = !acceptsEverything(req.accept);
  const takes = (i: Item) => !i.entry.dir && (showAll || fits(i.entry.name));

  // ---------- where things are ----------
  useEffect(() => {
    void fileApi.roots().then((r) => {
      if (!r.ok) return setError(r.error ?? t('picker.cantLoad'));
      setRoots(r.data);
      const home = r.data.find((x) => x.kind === 'home') ?? r.data.find(isLocal) ?? r.data[0];
      if (home) setAt({ root: home.id, path: '/', special: null });
    });
    void fileApi.favorites().then((r) => r.ok && setFavs(r.data.favorites));
    if (admin) void fileApi.external().then((r) => r.ok && setExternal(r.data));
  }, [admin, t]);
  const home = roots?.find((r) => r.kind === 'home') ?? roots?.find(isLocal) ?? roots?.[0];
  const rootLabel = useCallback(
    (id: string) => (id === home?.id || isHomeDir(id) ? 'Home' : (roots?.find((r) => r.id === id)?.name ?? 'Files')),
    [home?.id, roots],
  );

  // ---------- what's there ----------
  useEffect(() => {
    if (!at.root) return;
    let live = true;
    setLoading(true);
    setError('');
    setSelected([]);
    setAnchor(null);
    const where = (root: string, path: string) => `${rootLabel(root)}${parentPath(path) === '/' ? '' : parentPath(path)}`;
    const hits = (list: { root: string; path: string; name: string; dir: boolean; size: number; mod_time: string }[]): Item[] =>
      list.map((h) => ({
        key: `${h.root}:${h.path}`,
        root: h.root,
        path: h.path,
        entry: { name: h.name, dir: h.dir, size: h.size, mod_time: h.mod_time, mode: '', perm: 0 },
        where: where(h.root, h.path),
      }));
    const load = async (): Promise<Item[] | string> => {
      if (at.special === 'recents') {
        const r = await fileApi.recent(200);
        return r.ok ? hits(r.data.results) : (r.error ?? t('picker.cantLoad'));
      }
      if (at.special === 'search') {
        const r = await fileApi.search(query, 200);
        return r.ok ? hits(r.data.results) : (r.error ?? t('picker.cantLoad'));
      }
      if (at.special === 'appdata') {
        const r = await fileApi.appData();
        if (!r.ok) return r.error ?? t('picker.cantLoad');
        return r.data.apps.flatMap((a) =>
          a.folders
            .filter((f) => !f.blocked)
            .map((f) => ({
              key: `${f.id}:/`,
              root: f.id,
              path: '/',
              entry: { name: `${a.name} · ${f.target}`, dir: true, size: 0, mod_time: '', mode: '', perm: 0 },
            })),
        );
      }
      const r = await fileApi.list(at.root, at.path);
      if (!r.ok) return r.error ?? t('picker.cantLoad');
      return r.data.entries
        .filter((e) => !(at.path === '/' && e.name === RECYCLE.slice(1)))
        .map((e) => ({ key: `${at.root}:${joinPath(at.path, e.name)}`, root: at.root, path: joinPath(at.path, e.name), entry: e }));
    };
    void load().then((r) => {
      if (!live) return;
      setLoading(false);
      if (typeof r === 'string') {
        setItems([]);
        setError(r);
        return;
      }
      const byName = (a: Item, b: Item) => a.entry.name.localeCompare(b.entry.name, undefined, { numeric: true, sensitivity: 'base' });
      setItems(at.special ? r : r.sort((a, b) => (a.entry.dir === b.entry.dir ? byName(a, b) : a.entry.dir ? -1 : 1)));
    });
    return () => {
      live = false;
    };
  }, [at, query, rootLabel, t]);

  // Thumbnails for pictures (one ticket covers a folder and what's in it).
  useEffect(() => {
    let live = true;
    const want = new Set(items.filter((i) => fileKind(i.entry.name) === 'image').map((i) => `${i.root}\n${parentPath(i.path)}`));
    for (const k of want) {
      if (tickets[k]) continue;
      const [root, dir] = k.split('\n');
      void fileTicket(root, dir).then((tk) => live && tk && setTickets((m) => ({ ...m, [k]: tk })));
    }
    return () => {
      live = false;
    };
    // Tickets already fetched stay; only new folders need one.
  }, [items]); // eslint-disable-line react-hooks/exhaustive-deps

  const go = (root: string, path: string, special: Special = null) => {
    setBack((b) => [...b.slice(-49), at]);
    setFilter('');
    setAt({ root, path, special });
  };
  const goBack = () => {
    const prev = back[back.length - 1];
    if (!prev) return;
    setBack((b) => b.slice(0, -1));
    setAt(prev);
  };
  const goUp = () => {
    if (at.special || at.path === '/') return;
    go(at.root, parentPath(at.path));
  };

  const needle = filter.trim().toLowerCase();
  const shown = needle ? items.filter((i) => i.entry.name.toLowerCase().includes(needle)) : items;

  // ---------- picking ----------
  const close = useCallback(() => {
    if (done.current) return;
    done.current = true;
    void hostCall('files.cancel', { request: req.request }).catch(() => undefined);
    onDone();
  }, [req.request, onDone]);

  const choose = async (list: Item[]) => {
    const files = list.filter(takes);
    if (!files.length || busy || done.current) return;
    const pick = req.multiple ? files : files.slice(0, 1);
    setBusy(true);
    setFailed('');
    // Where each one is on this machine (grouped by location).
    const local = new Map<string, string>();
    const byRoot = new Map<string, Item[]>();
    for (const f of pick) byRoot.set(f.root, [...(byRoot.get(f.root) ?? []), f]);
    for (const [root, group] of byRoot) {
      const r = await api<{ paths: string[] }>('/api/v1/files/local', { method: 'POST', body: { root, paths: group.map((g) => g.path) } });
      if (r.ok) group.forEach((g, i) => local.set(g.key, r.data.paths[i] ?? ''));
    }
    // And a link alfad hands it out on, for files this account can't read there.
    const urls: string[] = [];
    for (const f of pick) {
      const tk = await fileTicket(f.root, parentPath(f.path));
      urls.push(tk ? rawUrl(f.root, f.path, tk, true) : '');
    }
    try {
      await hostCall('files.pick', {
        request: req.request,
        paths: pick.map((f) => local.get(f.key) ?? ''),
        urls,
        names: pick.map((f) => baseName(f.path).slice(0, 255)),
      });
      done.current = true;
      onDone();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // Big files from elsewhere take a while; the host finishes on its own.
      if (/timed out/.test(msg)) {
        done.current = true;
        onDone();
        return;
      }
      setFailed(t('picker.failed'));
      setBusy(false);
    }
  };

  const onItem = (e: MouseEvent, i: Item) => {
    if (i.entry.dir) {
      go(i.root, i.path);
      return;
    }
    if (!takes(i)) return;
    const k = i.key;
    if (req.multiple && (e.ctrlKey || e.metaKey)) {
      setSelected((s) => (s.includes(k) ? s.filter((x) => x !== k) : [...s, k]));
      setAnchor(k);
      return;
    }
    if (req.multiple && e.shiftKey && anchor) {
      const keys = shown.filter(takes).map((x) => x.key);
      const a = keys.indexOf(anchor);
      const b = keys.indexOf(k);
      if (a >= 0 && b >= 0) {
        setSelected(keys.slice(Math.min(a, b), Math.max(a, b) + 1));
        return;
      }
    }
    setSelected([k]);
    setAnchor(k);
  };

  const picked = shown.filter((i) => selected.includes(i.key) && takes(i));
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Enter' && picked.length && !(e.target instanceof HTMLInputElement)) {
      e.preventDefault();
      void choose(picked);
    } else if (e.key === 'Backspace' && !(e.target instanceof HTMLInputElement)) {
      e.preventDefault();
      goUp();
    } else if ((e.key === 'a' || e.key === 'A') && (e.ctrlKey || e.metaKey) && req.multiple && !(e.target instanceof HTMLInputElement)) {
      e.preventDefault();
      setSelected(shown.filter(takes).map((i) => i.key));
    }
  };

  // ---------- what it looks like ----------
  const kind = acceptKind(req.accept);
  const endings = acceptEndings(req.accept);
  const title = req.multiple ? t('picker.titleMany') : t('picker.title');
  const wants =
    kind === 'any' || !restricted
      ? ''
      : kind === 'other'
        ? endings.length
          ? t('picker.wantsEndings', { list: endings.join(', ') })
          : t('picker.wantsSome')
        : t(`picker.wants.${kind}`);
  const crumbs = at.special ? [] : at.path.split('/').filter(Boolean);
  const thumb = (i: Item) => {
    const tk = tickets[`${i.root}\n${parentPath(i.path)}`];
    if (!i.entry.dir && fileKind(i.entry.name) === 'image' && tk) return <img className="ft-img" src={rawUrl(i.root, i.path, tk)} alt="" loading="lazy" draggable={false} />;
    return <FileGlyph name={i.entry.name} dir={i.entry.dir} size={60} />;
  };
  const openMenu = (e: MouseEvent, entries: MenuEntry[]) => {
    e.preventDefault();
    e.stopPropagation();
    const r = box.current?.getBoundingClientRect();
    setMenu({ x: e.clientX - (r?.left ?? 0), y: e.clientY - (r?.top ?? 0), items: entries });
  };
  const nothing = () => undefined;

  const footer = (
    <div className="np-foot">
      <div className="np-foot-left">
        {wants && <span className="np-wants">{wants}</span>}
        {restricted && (
          <label className="np-all">
            <input type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} />
            {t('picker.showAll')}
          </label>
        )}
        {failed && <span className="np-failed">{failed}</span>}
      </div>
      <button type="button" className="pill ghost" onClick={close}>
        {t('common.cancel')}
      </button>
      <button type="button" className="pill primary np-open" disabled={!picked.length || busy} onClick={() => void choose(picked)}>
        {busy ? <span className="spinner small" /> : <Icon name="upload" size={14} />}
        {picked.length > 1 ? t('picker.openMany', { n: picked.length }) : t('picker.open')}
      </button>
    </div>
  );

  return (
    <Modal title={title} icon="folder" onClose={close} footer={footer} className="nocap-picker" bodyClassName="np-body">
      <div ref={box} className="files fx np-files" onKeyDown={onKey}>
        <FilesSidebar
          roots={roots ?? []}
          home={home?.id}
          current={{ root: at.root, path: at.path, special: at.special }}
          favs={favs}
          external={external}
          onEject={nothing}
          net={net}
          rootLabel={rootLabel}
          onGo={(root, path, special) => go(root, path, special ?? null)}
          onAddFavs={nothing}
          onRemoveFav={nothing}
          onDropInto={nothing}
          onDropTrash={nothing}
          onNetChanged={async () => {
            await net.load();
            const r = await fileApi.roots();
            if (r.ok) setRoots(r.data);
          }}
          onUnshare={nothing}
          openMenu={openMenu}
        />
        <div className="files-main np-main">
          <div className="toolbar files-toolbar fx-toolbar">
            <div className="fx-nav">
              <button type="button" className="ghost icon-btn" aria-label={t('picker.back')} title={t('picker.back')} disabled={!back.length} onClick={goBack}>
                <Icon name="chevronLeft" size={16} />
              </button>
            </div>
            <div className="crumbs fx-crumbs">
              {at.special ? (
                <span className="crumb fx-crumb current">
                  <Icon name={at.special === 'recents' ? 'clock' : at.special === 'appdata' ? 'appData' : 'search'} size={13} />
                  {at.special === 'recents' ? t('files.side.recents') : at.special === 'appdata' ? t('files.appdata.title') : t('picker.results', { q: query })}
                </span>
              ) : (
                <>
                  <button type="button" className={`crumb fx-crumb ${crumbs.length ? '' : 'current'}`} onClick={() => go(at.root, '/')}>
                    <Icon name={at.root === home?.id ? 'home' : 'drive'} size={13} />
                    {rootLabel(at.root)}
                  </button>
                  {crumbs.map((c, i) => {
                    const p = '/' + crumbs.slice(0, i + 1).join('/');
                    const last = i === crumbs.length - 1;
                    return (
                      <span key={p} className="crumb-wrap fx-crumb-wrap">
                        <Icon name="chevronRight" size={12} />
                        <button type="button" className={`crumb fx-crumb ${last ? 'current' : ''}`} onClick={() => go(at.root, p)}>
                          <Icon name="folder" size={13} />
                          {c}
                        </button>
                      </span>
                    );
                  })}
                </>
              )}
            </div>
            <div className="fx-actions">
              <label className="fx-search" title={t('picker.searchHint')}>
                <Icon name="search" size={14} />
                <input
                  placeholder={t('picker.search')}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && filter.trim()) {
                      setQuery(filter.trim());
                      setFilter('');
                      go(at.root || home?.id || '', '/', 'search');
                    }
                  }}
                />
              </label>
            </div>
          </div>
          <div className="files-pane fx-pane np-pane" tabIndex={-1}>
            {error ? (
              <div className="fx-empty">
                <span className="fx-empty-icon">
                  <Icon name="alert" size={30} />
                </span>
                <p className="muted">{error}</p>
              </div>
            ) : !shown.length ? (
              loading ? (
                <div className="fx-loading">
                  <span className="spinner" />
                </div>
              ) : (
                <div className="fx-empty">
                  <span className="fx-empty-icon">
                    <FileGlyph name="" dir size={64} />
                  </span>
                  <p className="muted">{t('picker.empty')}</p>
                </div>
              )
            ) : (
              <div className="fx-grid">
                {shown.map((i) => {
                  const off = !i.entry.dir && !takes(i);
                  return (
                    <div
                      key={i.key}
                      role="button"
                      tabIndex={0}
                      aria-disabled={off || undefined}
                      className={`fx-tile ${selected.includes(i.key) ? 'selected' : ''} ${off ? 'np-off' : ''}`}
                      title={i.where ? `${i.entry.name}\n${i.where}` : i.entry.name}
                      onClick={(e) => onItem(e, i)}
                      onDoubleClick={() => !i.entry.dir && takes(i) && void choose([i])}
                    >
                      <div className="fx-tile-icon">{thumb(i)}</div>
                      <span className="fx-tile-name">{i.entry.name}</span>
                      <span className="fx-tile-sub">{i.entry.dir ? t('picker.folder') : `${fmtBytes(i.entry.size)}${i.where ? ` · ${i.where}` : ''}`}</span>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
        {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
      </div>
    </Modal>
  );
}
