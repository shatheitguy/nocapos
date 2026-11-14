import type { PointerEvent as RPointerEvent } from 'react';
import { APPS } from '../apps/meta';
import { create } from 'zustand';
import { iconCell, useDesktopIcons } from './desktopIcons';
import { useDock } from './dock';
import { folderKey, folderOf, isFolderKey, suggestName, takeOut, useFolders } from './folders';
import { isStoreKey, useStoreApps } from './storeApps';
import { isNativeKey, useNativeApps } from './nativeApps';
import { moveBefore, replaceKey, toSave } from '../lib/launcherOrder';

// One drag-and-drop system for app icons in the dock, Launchpad and desktop.
//
// Drop targets are found by hit-testing the DOM for [data-drop]:
//   data-drop="dock"     the dock (pinned items carry data-dock-pin=<appId>)
//   data-drop="trash"    the Recycle Bin in the dock
//   data-drop="desktop"  the empty desktop
//   data-drop="app"      an app icon in Launchpad (data-app, data-scope="launchpad")
//                        or on the desktop (data-scope="desktop"): drop to make a folder
//   data-drop="folder"   a folder tile or icon (data-folder, data-scope): drop to add to it
//   data-drop="launchpad" the Launchpad grid: an app dragged out of a folder leaves it
//
// In Launchpad only an icon's picture is a folder target; the rest of its cell
// and the gaps are slots between icons, which rearrange them. The open
// Launchpad registers its grid (setGridSlots) to turn a point into a slot.
// Inside an open folder, its apps (data-key) rearrange the same way.
//
// The dragged id is an app id, "folder:<id>" for a whole folder, or
// "store:<id>" for an installed app or "native:<id>" for a Linux app
// (desktop shortcuts only: no Dock, no folders).

export type DragSource = 'dock' | 'launchpad' | 'desktop' | 'folder';

export type DropTarget =
  | { kind: 'dock'; before: string | null }
  | { kind: 'trash' }
  | { kind: 'desktop' }
  | { kind: 'app'; appId: string; scope: 'launchpad' | 'desktop' }
  | { kind: 'folder'; folderId: string; scope: 'launchpad' | 'desktop'; before?: string | null }
  | { kind: 'launchpad'; before?: string | null }
  | null;

/** Where a rearranged icon will land: in front of `before` (null: the end), in the Launchpad grid or its own open folder. */
export interface Slot {
  where: 'grid' | 'folder';
  before: string | null;
}

export interface ActiveDrag {
  appId: string;
  source: DragSource;
  /** Set when the app was dragged out of an open folder. */
  folderId?: string;
  x: number;
  y: number;
}

export const useAppDrag = create<{ drag: ActiveDrag | null; over: DropTarget; slot: Slot | null }>(() => ({ drag: null, over: null, slot: null }));

const THRESHOLD = 6;
/** Touch: a finger rests this long on an icon before it lifts (a quick move swipes or scrolls). */
export const TOUCH_HOLD = 350;
/** The pointer rests this long between icons before they make room, so passing over one to make a folder doesn't shove it away. */
const SETTLE_MS = { grid: 160, folder: 60 };

/** The open Launchpad's grid. */
export interface GridSlots {
  /** The slot under a point: a key to land before, null for the end, undefined for none (the dragged icon's own spot). */
  slot: (x: number, y: number, dragged: string) => string | null | undefined;
  /** The keys in the grid now, in order, without any drag preview. */
  keys: () => string[];
}
let grid: GridSlots | null = null;
let recheck: (() => void) | null = null;
/** Look again at what is under a resting pointer (the Launchpad page turned under it). */
export const recheckDrag = () => recheck?.();
export function setGridSlots(g: GridSlots | null) {
  grid = g;
}

/** Keys of apps whose list hasn't loaded yet keep their saved place. */
const stillLoading = (k: string) => (isStoreKey(k) ? !useStoreApps.getState().loaded : isNativeKey(k) ? !useNativeApps.getState().loaded : false);

/** Save a new Launchpad order made from the current one. */
function saveGrid(keys: string[]) {
  const f = useFolders.getState();
  f.setOrder(toSave(f.order, keys, stillLoading));
}

function hitTest(x: number, y: number, dragged: string, source: DragSource): DropTarget {
  const el = document.elementFromPoint(x, y) as HTMLElement | null;
  const zone = el?.closest<HTMLElement>('[data-drop]');
  const kind = zone?.dataset.drop;
  const openFolder = !!zone?.classList.contains('folder-view');

  if (grid && zone && (source === 'launchpad' || source === 'folder') && !openFolder &&
    (kind === 'launchpad' || ((kind === 'app' || kind === 'folder') && zone.dataset.scope === 'launchpad'))) {
    // An icon's picture makes (or fills) a folder, for icons that can go in one.
    const pic = el?.closest('.app-icon, .folder-icon');
    const foldable = !isFolderKey(dragged) && !isStoreKey(dragged) && !isNativeKey(dragged);
    if (!(foldable && kind !== 'launchpad' && pic && zone.contains(pic))) {
      const before = grid.slot(x, y, dragged);
      return before === undefined ? { kind: 'launchpad' } : { kind: 'launchpad', before };
    }
  }

  if (openFolder && source === 'folder' && zone?.dataset.folder) {
    // Inside the dragged app's own folder: between which apps.
    const folderId = zone.dataset.folder;
    const scope = zone.dataset.scope === 'desktop' ? 'desktop' : 'launchpad';
    const cell = el?.closest<HTMLElement>('[data-key]');
    if (!cell || !zone.contains(cell)) return { kind: 'folder', folderId, scope };
    const keys = [...zone.querySelectorAll<HTMLElement>('[data-key]')].map((c) => c.dataset.key ?? '');
    const i = keys.indexOf(cell.dataset.key ?? '');
    if (keys[i] === dragged) return { kind: 'folder', folderId, scope };
    const r = cell.getBoundingClientRect();
    const before = x < r.left + r.width / 2 ? keys[i] : (keys.slice(i + 1).find((k) => k !== dragged) ?? null);
    return { kind: 'folder', folderId, scope, before };
  }

  switch (kind) {
    case 'trash':
      return { kind: 'trash' };
    case 'dock':
      return { kind: 'dock', before: el?.closest<HTMLElement>('[data-dock-pin]')?.dataset.dockPin ?? null };
    case 'desktop':
      return { kind: 'desktop' };
    case 'app':
      return { kind: 'app', appId: zone!.dataset.app ?? '', scope: zone!.dataset.scope === 'desktop' ? 'desktop' : 'launchpad' };
    case 'folder':
      return { kind: 'folder', folderId: zone!.dataset.folder ?? '', scope: zone!.dataset.scope === 'desktop' ? 'desktop' : 'launchpad' };
    case 'launchpad':
      return { kind: 'launchpad' };
    default:
      return null;
  }
}

/** The click that ends a drag must not also open the app. */
function swallowNextClick() {
  const stop = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };
  window.addEventListener('click', stop, { capture: true, once: true });
  window.setTimeout(() => window.removeEventListener('click', stop, { capture: true }), 0);
}

/** Where a drop would really go: dropping an icon on itself (or a folder on an app) is just a move. */
function effective(appId: string, over: DropTarget, folderId?: string): DropTarget {
  if (!over) return null;
  const folder = isFolderKey(appId);
  if (isStoreKey(appId) || isNativeKey(appId)) {
    if (over.kind === 'app' || over.kind === 'folder') return over.scope === 'desktop' ? { kind: 'desktop' } : { kind: 'launchpad' };
    if (over.kind === 'dock') return null;
    return over;
  }
  if (over.kind === 'app' && (folder || over.appId === appId)) return over.scope === 'desktop' ? { kind: 'desktop' } : { kind: 'launchpad' };
  if (over.kind === 'folder' && folder) return over.scope === 'desktop' ? { kind: 'desktop' } : { kind: 'launchpad' };
  if (over.kind === 'folder' && over.folderId === folderId) return over; // rearranging its own folder
  if (over.kind === 'dock' && folder) return null; // folders don't go in the Dock
  return over;
}

/** What a drop will do, for the hint under the dragged icon. */
export function dropHint(drag: ActiveDrag, raw: DropTarget): string {
  const over = effective(drag.appId, raw, drag.folderId);
  const dock = useDock.getState();
  const desk = useDesktopIcons.getState();
  switch (over?.kind) {
    case 'dock':
      if (drag.source === 'dock' && dock.has(drag.appId)) return '';
      return dock.has(drag.appId) ? 'Move in Dock' : 'Add to Dock';
    case 'trash':
      return drag.source === 'desktop' ? 'Remove from Desktop' : drag.source === 'dock' ? 'Remove from Dock' : '';
    case 'desktop':
      if (drag.source === 'folder') return 'Take out of Folder';
      if (drag.source === 'desktop') return '';
      return desk.has(drag.appId) ? 'Move on Desktop' : 'Add to Desktop';
    case 'app':
      return 'Create Folder';
    case 'folder':
      if (over.folderId === drag.folderId) return '';
      return `Add to “${useFolders.getState().byId(over.folderId)?.name ?? 'Folder'}”`;
    case 'launchpad':
      return drag.source === 'folder' ? 'Take out of Folder' : '';
    default:
      return '';
  }
}

/** Take an app out of its folder, returning the grid keys with a dissolved folder replaced by the app left in it. */
function outOfFolder(fromFolder: string, appId: string, keys: string[]): string[] {
  const rest = useFolders.getState().byId(fromFolder)?.apps.filter((a) => a !== appId) ?? [];
  takeOut(fromFolder, appId);
  return rest.length <= 1 ? replaceKey(keys, folderKey(fromFolder), rest) : keys;
}

function drop(appId: string, source: DragSource, raw: DropTarget, at: { x: number; y: number }, fromFolder?: string, slot?: Slot | null) {
  const dock = useDock.getState();
  const desk = useDesktopIcons.getState();
  const folders = useFolders.getState();
  const over = effective(appId, raw, fromFolder);
  if (!over) return;
  switch (over.kind) {
    case 'dock':
      // Dock icons were already reordered live while hovering.
      if (!(source === 'dock' && dock.has(appId))) dock.place(appId, over.before);
      break;
    case 'trash':
      if (source === 'desktop') desk.remove(appId);
      else if (source === 'dock') dock.remove(appId);
      break;
    case 'desktop':
      if (fromFolder) takeOut(fromFolder, appId);
      desk.place(appId, at);
      break;
    case 'launchpad': {
      // Dropped between icons, or anywhere on the grid once the icons made room.
      const before = over.before !== undefined ? over.before : slot?.where === 'grid' ? slot.before : undefined;
      const keys = grid?.keys();
      if (fromFolder) {
        const left = outOfFolder(fromFolder, appId, keys ?? []);
        if (keys) saveGrid(moveBefore(left, appId, before ?? null));
      } else if (keys && before !== undefined) saveGrid(moveBefore(keys, appId, before));
      break;
    }
    case 'app': {
      // Drop an app on another: both go into a new folder, which takes the target's place.
      const keys = over.scope === 'launchpad' ? grid?.keys() : undefined;
      const left = fromFolder ? outOfFolder(fromFolder, appId, keys ?? []) : keys;
      const prev = folderOf(over.appId);
      if (prev) {
        folders.add(prev.id, appId);
        break;
      }
      const titles = (id: string) => APPS[id]?.title;
      const id = folders.create([over.appId, appId], suggestName([over.appId, appId], titles));
      if (keys && left) saveGrid(replaceKey(left.filter((k) => k !== appId), over.appId, [folderKey(id)]));
      if (over.scope === 'desktop') {
        const spot = desk.icons.find((i) => i.appId === over.appId);
        desk.remove(over.appId);
        if (source === 'desktop') desk.remove(appId);
        if (spot) desk.place(folderKey(id), spot);
      }
      break;
    }
    case 'folder':
      if (fromFolder && over.folderId === fromFolder) {
        const before = over.before !== undefined ? over.before : slot?.where === 'folder' ? slot.before : undefined;
        if (before !== undefined) folders.moveIn(fromFolder, appId, before);
        break;
      }
      if (fromFolder) takeOut(fromFolder, appId);
      folders.add(over.folderId, appId);
      if (source === 'desktop') desk.remove(appId);
      break;
  }
}

/**
 * Call from an app icon's onPointerDown. Nothing happens until the pointer
 * moves past a small threshold, so a plain click still opens the app.
 */
export function pressApp(
  e: RPointerEvent<HTMLElement>,
  appId: string,
  source: DragSource,
  opts: { onStart?: () => void; folderId?: string; touchHold?: number } = {},
) {
  if (e.button !== 0 || !e.isPrimary) return;
  // Search results are a fixed list.
  if (e.currentTarget.closest('[data-no-drag]')) return;
  const startX = e.clientX;
  const startY = e.clientY;
  // On touch screens a quick swipe should scroll (e.g. Launchpad pages), so
  // the icon only lifts once the finger has rested on it for a moment.
  const holdUntil = opts.touchHold && e.pointerType !== 'mouse' ? e.timeStamp + opts.touchHold : 0;
  // Where inside the icon it was grabbed — desktop icons keep that offset.
  const r = e.currentTarget.getBoundingClientRect();
  const grab = source === 'desktop' && !opts.folderId ? { x: startX - r.left, y: startY - r.top } : { x: iconCell().w / 2, y: iconCell().icon / 2 + 8 };
  let started = false;

  // A slot the pointer rests on becomes the shown one once it has settled.
  let pending: { slot: Slot; timer: number } | null = null;
  const clearPending = () => {
    if (pending) window.clearTimeout(pending.timer);
    pending = null;
  };
  const settle = (over: DropTarget) => {
    const before = over?.kind === 'launchpad' || over?.kind === 'folder' ? over.before : undefined;
    if (before === undefined) return clearPending(); // over an icon, or off the grid: keep what is shown
    const next: Slot = { where: over!.kind === 'folder' ? 'folder' : 'grid', before };
    const same = (s: Slot | null | undefined) => !!s && s.where === next.where && s.before === next.before;
    if (same(useAppDrag.getState().slot)) return clearPending();
    if (same(pending?.slot)) return;
    clearPending();
    pending = {
      slot: next,
      timer: window.setTimeout(() => {
        pending = null;
        if (useAppDrag.getState().drag) useAppDrag.setState({ slot: next });
      }, SETTLE_MS[next.where]),
    };
  };

  // After the page turns under a resting pointer, what is under it changed.
  let lastAt = { x: startX, y: startY };
  recheck = () => {
    if (!started) return;
    const over = hitTest(lastAt.x, lastAt.y, appId, source);
    useAppDrag.setState({ over });
    settle(over);
  };

  const onMove = (ev: PointerEvent) => {
    if (!started) {
      if (Math.hypot(ev.clientX - startX, ev.clientY - startY) < THRESHOLD) return;
      if (holdUntil && ev.timeStamp < holdUntil) {
        finish(ev, false);
        return;
      }
      started = true;
      document.body.classList.add('app-dragging');
      opts.onStart?.();
    }
    ev.preventDefault();
    lastAt = { x: ev.clientX, y: ev.clientY };
    const over = hitTest(ev.clientX, ev.clientY, appId, source);
    const dock = useDock.getState();
    if (source === 'dock' && over?.kind === 'dock' && over.before && over.before !== appId && dock.has(appId)) {
      dock.moveTo(appId, over.before);
    }
    useAppDrag.setState({ drag: { appId, source, folderId: opts.folderId, x: ev.clientX, y: ev.clientY }, over });
    settle(over);
  };

  const finish = (ev: PointerEvent, commit: boolean) => {
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', onCancel);
    clearPending();
    recheck = null;
    if (!started) return;
    document.body.classList.remove('app-dragging');
    const { slot } = useAppDrag.getState();
    const over = commit ? hitTest(ev.clientX, ev.clientY, appId, source) : null;
    useAppDrag.setState({ drag: null, over: null, slot: null });
    swallowNextClick();
    if (commit) drop(appId, source, over, { x: ev.clientX - grab.x, y: ev.clientY - grab.y }, opts.folderId, slot);
  };
  const onUp = (ev: PointerEvent) => finish(ev, true);
  const onCancel = (ev: PointerEvent) => finish(ev, false);

  window.addEventListener('pointermove', onMove, { passive: false });
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onCancel);
}
