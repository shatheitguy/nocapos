// The apps "Open with" offers, and how each one opens a file.
import { baseName, fileKind, fileTicket, rawUrl } from '../api/files';
import type { IconName } from '../components/Icon';
import { tr } from '../i18n';
import { defaultFor, handlersFor, setAvailableHandlers, withDefault, type HandlerId } from '../lib/openWith';
import { usePrefs } from '../state/prefs';
import { toast } from '../state/toasts';
import { openApp } from './meta';

export interface OpenTarget {
  root: string;
  path: string;
  name: string;
}

/** The icon for each app in menus and the chooser. */
export const HANDLER_ICON: Record<HandlerId, IconName> = {
  photo: 'image',
  video: 'fileVideo',
  music: 'music',
  text: 'edit',
  quicklook: 'eye',
  browser: 'globe',
  archive: 'archiveBox',
};

setAvailableHandlers(['photo', 'video', 'music', 'text', 'quicklook', 'browser', 'archive']);

export const handlerName = (id: HandlerId) => tr(`openwith.app.${id}`);
export const handlerHint = (id: HandlerId) => tr(`openwith.hint.${id}`);

/** The apps that can open this file, best first. */
export const appsFor = (name: string) => handlersFor(fileKind(name));

/** The person's chosen app for this file type, if any. */
export const defaultAppFor = (name: string) => defaultFor(name, fileKind(name), usePrefs.getState().openWith);

/** Remember (or forget, with null) the app for this file's type. */
export function setDefaultApp(name: string, id: HandlerId | null) {
  const prefs = usePrefs.getState();
  prefs.set({ openWith: withDefault(prefs.openWith, name, fileKind(name), id) });
}

/**
 * Open a file with an app. `extract` is how Files unpacks an archive here
 * (only Files can, as it knows where to put the result).
 */
export function openWith(id: HandlerId, f: OpenTarget, extract?: () => void) {
  const key = `${f.root}:${f.path}`;
  switch (id) {
    case 'photo':
      openApp('imageviewer', { title: f.name, props: { root: f.root, path: f.path }, key: `imageviewer:${key}` });
      return;
    case 'video':
      openApp('videoplayer', { title: f.name, props: { root: f.root, path: f.path }, key: `videoplayer:${key}` });
      return;
    case 'music':
      // One music player: a new song replaces what was playing.
      openApp('musicplayer', { title: f.name, props: { root: f.root, path: f.path }, key: 'musicplayer' });
      return;
    case 'text':
      openApp('viewer', { title: f.name, props: { root: f.root, path: f.path, mode: 'text' }, key: `text:${key}` });
      return;
    case 'quicklook':
      openApp('viewer', { title: f.name, props: { root: f.root, path: f.path }, key: `viewer:${key}` });
      return;
    case 'archive':
      if (extract) extract();
      return;
    case 'browser': {
      // Open the tab now (browsers block tabs opened later), then point it at the file.
      const tab = window.open('about:blank', '_blank');
      if (tab) tab.opener = null;
      void fileTicket(f.root, f.path).then((t) => {
        if (!t) {
          tab?.close();
          toast('error', tr('openwith.toast.fail', { name: baseName(f.path) }));
          return;
        }
        if (tab) tab.location.href = rawUrl(f.root, f.path, t);
        else window.open(rawUrl(f.root, f.path, t), '_blank', 'noopener');
      });
      return;
    }
    default:
      openApp('viewer', { title: f.name, props: { root: f.root, path: f.path }, key: `viewer:${key}` });
  }
}
