// What the photo viewer can do with the picture on show, shared by the
// Photos library and the picture windows Files opens: set it as the
// wallpaper, open it in Files, send it to a linked phone, save a turn or an
// edit, and put it in the trash with an Undo.
import { baseName, fileApi, parentPath, upload } from '../api/files';
import { photosApi, type PhotoKey } from '../api/photos';
import type { MenuItem } from '../components/ContextMenu';
import { tr } from '../i18n';
import { editedName, keepsType, NEW_EDIT } from '../lib/photoEdit';
import { linkPhones, sendFilesToPhone } from '../shell/linkActions';
import { confirmDialog } from '../state/confirm';
import { saveCustomWallpaper, usePrefs } from '../state/prefs';
import { toast, toastAction } from '../state/toasts';
import { openApp } from './meta';
import { renderEdit } from './PhotoEditor';

type Pic = PhotoKey & { name: string };

/** Makes the picture at `src` the desktop wallpaper. */
export async function setAsWallpaper(src: string, name: string): Promise<boolean> {
  try {
    const res = await fetch(src);
    if (!res.ok) throw new Error(String(res.status));
    const blob = await res.blob();
    const id = await saveCustomWallpaper(new File([blob], name, { type: blob.type }));
    usePrefs.getState().set({ wallpaper: id });
    toast('success', tr('pv.wallpaperSet'));
    return true;
  } catch {
    toast('error', tr('pv.wallpaperFail'));
    return false;
  }
}

/** "…" menu entries: wallpaper, Files, linked phones. */
export function pictureMenu(p: Pic, src: string | null, opts: { video?: boolean; inFiles?: boolean } = {}): MenuItem[] {
  const items: MenuItem[] = [];
  if (src && !opts.video) items.push({ label: tr('pv.setWallpaper'), icon: 'image', onClick: () => void setAsWallpaper(src, p.name) });
  if (!opts.inFiles) items.push({ label: tr('pv.openInFiles'), icon: 'folder', onClick: () => openApp('files', { props: { root: p.root, path: parentPath(p.path) }, newWindow: true }) });
  const phones = linkPhones();
  phones.forEach((ph, i) =>
    items.push({
      label: phones.length > 1 ? tr('link.sendToNamed', { device: ph.name }) : tr('link.sendToPhone'),
      icon: 'phone',
      divider: i === 0 && items.length > 0,
      onClick: () => void sendFilesToPhone(ph, p.root, [p.path]),
    }),
  );
  return items;
}

/** Uploads an edited picture next to the original: a copy, or in its place. Returns the saved path. */
export async function saveEdited(p: Pic, file: Blob, replace: boolean): Promise<string | null> {
  const name = replace && keepsType(p.name) ? p.name : editedName(p.name);
  const r = await upload(p.root, parentPath(p.path), [new File([file], name, { type: file.type })], () => undefined, { conflict: replace ? 'replace' : 'rename' });
  const saved = r.ok ? r.data.paths?.[0] : undefined;
  if (!saved) {
    toast('error', tr('pv.edit.saveFail'), r.error);
    return null;
  }
  toast('success', replace ? tr('pv.edit.replaced') : tr('pv.edit.savedCopy', { name: baseName(saved) }));
  return saved;
}

/**
 * Saves quarter turns: losslessly through the orientation tag when the file
 * has one; otherwise (PNG, say) by saving the picture again, after asking.
 */
export async function saveTurns(p: Pic, src: string | null, turns: number): Promise<boolean> {
  const r = await photosApi.rotate(p, turns);
  if (r.ok) return true;
  if (r.status !== 409 || !src) {
    toast('error', tr('pv.rotateFail'), r.error);
    return false;
  }
  const ok = await confirmDialog({ title: tr('pv.resaveTitle'), message: tr('pv.resaveBody'), confirmLabel: tr('pv.resave') });
  if (!ok) return false;
  try {
    const file = await renderEdit(src, p.name, { ...NEW_EDIT, turns });
    return !!(await saveEdited(p, file, true));
  } catch {
    toast('error', tr('pv.rotateFail'));
    return false;
  }
}

/**
 * Puts a file from Files in its recycle bin and offers Undo (which moves it
 * back under its own name). `after` runs once it's gone and again once it's back.
 */
export async function trashWithUndo(p: Pic, after: (restored: boolean) => void): Promise<boolean> {
  const r = await fileApi.remove(p.root, [p.path]);
  if (!r.ok) {
    toast('error', tr('pv.deleteFail'), r.error);
    return false;
  }
  after(false);
  const binned = r.data?.recycled?.[0];
  if (!binned) {
    toast('success', tr('pv.deleted', { name: p.name }));
    return true;
  }
  toastAction('success', tr('pv.trashed', { name: p.name }), {
    label: tr('pv.undo'),
    run: () =>
      void (async () => {
        const back = await fileApi.transfer(p.root, [binned], parentPath(p.path), true);
        const at = back.ok ? back.data.paths[0] : undefined;
        if (!at) return toast('error', tr('pv.undoFail'), back.error);
        if (baseName(at) !== p.name) await fileApi.rename(p.root, at, p.name);
        after(true);
      })(),
  });
  return true;
}
