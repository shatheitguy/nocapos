import type { PointerEvent as RPointerEvent } from 'react';
import { APPS } from '../apps/meta';
import { create } from 'zustand';
import { iconCell, useDesktopIcons } from './desktopIcons';
import { useDock } from './dock';
import { folderKey, folderOf, isFolderKey, suggestName, takeOut, useFolders } from './folders';

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
// The dragged id is an app id, or "folder:<id>" for a whole folder.

export type DragSource = 'dock' | 'launchpad' | 'desktop' | 'folder';

export type DropTarget =
  | { kind: 'dock'; before: string | null }
  | { kind: 'trash' }
  | { kind: 'desktop' }
  | { kind: 'app'; appId: string; scope: 'launchpad' | 'desktop' }
  | { kind: 'folder'; folderId: string; scope: 'launchpad' | 'desktop' }
  | { kind: 'launchpad' }
  | null;

export interface ActiveDrag {
  appId: string;
  source: DragSource;
  /** Set when the app was dragged out of an open folder. */
  folderId?: string;
  x: number;
  y: number;
}

export const useAppDrag = create<{ drag: ActiveDrag | null; over: DropTarget }>(() => ({ drag: null, over: null }));

const THRESHOLD = 6;

function hitTest(x: number, y: number): DropTarget {
  const el = document.elementFromPoint(x, y) as HTMLElement | null;
  const zone = el?.closest<HTMLElement>('[data-drop]');
  switch (zone?.dataset.drop) {
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
  if (over.kind === 'app' && (folder || over.appId === appId)) return over.scope === 'desktop' ? { kind: 'desktop' } : { kind: 'launchpad' };
  if (over.kind === 'folder' && folder) return over.scope === 'desktop' ? { kind: 'desktop' } : { kind: 'launchpad' };
  if (over.kind === 'folder' && over.folderId === folderId) return null; // back into its own folder
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
      return `Add to “${useFolders.getState().byId(over.folderId)?.name ?? 'Folder'}”`;
    case 'launchpad':
      return drag.source === 'folder' ? 'Take out of Folder' : '';
    default:
      return '';
  }
}

function drop(appId: string, source: DragSource, raw: DropTarget, at: { x: number; y: number }, fromFolder?: string) {
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
    case 'launchpad':
      if (fromFolder) takeOut(fromFolder, appId);
      break;
    case 'app': {
      // Drop an app on another: both go into a new folder, which takes the target's place.
      if (fromFolder) takeOut(fromFolder, appId);
      const prev = folderOf(over.appId);
      if (prev) {
        folders.add(prev.id, appId);
        break;
      }
      const titles = (id: string) => APPS[id]?.title;
      const id = folders.create([over.appId, appId], suggestName([over.appId, appId], titles));
      if (over.scope === 'desktop') {
        const spot = desk.icons.find((i) => i.appId === over.appId);
        desk.remove(over.appId);
        if (source === 'desktop') desk.remove(appId);
        if (spot) desk.place(folderKey(id), spot);
      }
      break;
    }
    case 'folder':
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
  opts: { onStart?: () => void; folderId?: string } = {},
) {
  if (e.button !== 0 || !e.isPrimary) return;
  const startX = e.clientX;
  const startY = e.clientY;
  // Where inside the icon it was grabbed — desktop icons keep that offset.
  const r = e.currentTarget.getBoundingClientRect();
  const grab = source === 'desktop' && !opts.folderId ? { x: startX - r.left, y: startY - r.top } : { x: iconCell().w / 2, y: iconCell().icon / 2 + 8 };
  let started = false;

  const onMove = (ev: PointerEvent) => {
    if (!started) {
      if (Math.hypot(ev.clientX - startX, ev.clientY - startY) < THRESHOLD) return;
      started = true;
      document.body.classList.add('app-dragging');
      opts.onStart?.();
    }
    ev.preventDefault();
    const over = hitTest(ev.clientX, ev.clientY);
    const dock = useDock.getState();
    if (source === 'dock' && over?.kind === 'dock' && over.before && over.before !== appId && dock.has(appId)) {
      dock.moveTo(appId, over.before);
    }
    useAppDrag.setState({ drag: { appId, source, folderId: opts.folderId, x: ev.clientX, y: ev.clientY }, over });
  };

  const finish = (ev: PointerEvent, commit: boolean) => {
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
    window.removeEventListener('pointercancel', onCancel);
    if (!started) return;
    document.body.classList.remove('app-dragging');
    useAppDrag.setState({ drag: null, over: null });
    swallowNextClick();
    if (commit) drop(appId, source, hitTest(ev.clientX, ev.clientY), { x: ev.clientX - grab.x, y: ev.clientY - grab.y }, opts.folderId);
  };
  const onUp = (ev: PointerEvent) => finish(ev, true);
  const onCancel = (ev: PointerEvent) => finish(ev, false);

  window.addEventListener('pointermove', onMove, { passive: false });
  window.addEventListener('pointerup', onUp);
  window.addEventListener('pointercancel', onCancel);
}
