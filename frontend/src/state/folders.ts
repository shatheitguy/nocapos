// App folders (like iOS / macOS Launchpad): made by dropping one app onto
// another, shown in Launchpad and — when placed there — on the desktop as
// "folder:<id>" icons. Saved per user in this browser.
import { create } from 'zustand';
import { useDesktopIcons } from './desktopIcons';

export interface AppFolder {
  id: string;
  name: string;
  apps: string[];
}

export const FOLDER_PREFIX = 'folder:';
export const isFolderKey = (k: string) => k.startsWith(FOLDER_PREFIX);
export const folderKey = (id: string) => FOLDER_PREFIX + id;
export const folderIdOf = (k: string) => k.slice(FOLDER_PREFIX.length);

interface FolderStore {
  userId: string | null;
  folders: AppFolder[];
  restore: (userId: string) => void;
  _reset: () => void;
  /** New folder holding these apps; returns its id. */
  create: (apps: string[], name?: string) => string;
  add: (id: string, appId: string) => void;
  /** Take an app out; a folder left with one app (or none) dissolves. Returns the app left over, if any. */
  removeApp: (id: string, appId: string) => string | null;
  rename: (id: string, name: string) => void;
  remove: (id: string) => void;
  byId: (id: string) => AppFolder | undefined;
}

const keyFor = (u: string) => `alfa.folders.${u}`;
const newId = () => Math.random().toString(36).slice(2, 10);

/** A sensible default name from what's inside: a category only when every app fits it. */
export function suggestName(apps: string[], titles: (id: string) => string | undefined): string {
  const names = apps.map((id) => (titles(id) ?? '').toLowerCase());
  const all = (re: RegExp) => names.every((t) => re.test(t));
  if (all(/terminal|script|container|log|monitor|setting/)) return 'Tools';
  if (all(/brave|browser|remote/)) return 'Internet';
  if (all(/files|viewer|photo|media/)) return 'Files';
  return 'Folder';
}

export const useFolders = create<FolderStore>((set, get) => {
  const commit = (folders: AppFolder[]) => {
    set({ folders });
    const { userId } = get();
    if (!userId) return;
    try {
      localStorage.setItem(keyFor(userId), JSON.stringify(folders));
    } catch {
      /* ignore */
    }
  };
  return {
    userId: null,
    folders: [],
    restore: (userId) => {
      let folders: AppFolder[] = [];
      try {
        const v = JSON.parse(localStorage.getItem(keyFor(userId)) ?? '[]');
        if (Array.isArray(v)) folders = v.filter((f) => f && typeof f.id === 'string' && Array.isArray(f.apps));
      } catch {
        /* ignore */
      }
      set({ userId, folders });
    },
    _reset: () => set({ userId: null, folders: [] }),
    create: (apps, name = 'Folder') => {
      const id = newId();
      // An app lives in one folder at a time.
      const cleaned = get().folders.map((f) => ({ ...f, apps: f.apps.filter((a) => !apps.includes(a)) })).filter((f) => f.apps.length > 1);
      commit([...cleaned, { id, name, apps: [...new Set(apps)] }]);
      return id;
    },
    add: (id, appId) =>
      commit(
        get()
          .folders.map((f) => (f.id === id ? { ...f, apps: f.apps.includes(appId) ? f.apps : [...f.apps, appId] } : { ...f, apps: f.apps.filter((a) => a !== appId) }))
          .filter((f) => f.id === id || f.apps.length > 1),
      ),
    removeApp: (id, appId) => {
      const f = get().folders.find((x) => x.id === id);
      if (!f) return null;
      const rest = f.apps.filter((a) => a !== appId);
      if (rest.length <= 1) {
        commit(get().folders.filter((x) => x.id !== id));
        return rest[0] ?? null;
      }
      commit(get().folders.map((x) => (x.id === id ? { ...x, apps: rest } : x)));
      return null;
    },
    rename: (id, name) => commit(get().folders.map((f) => (f.id === id ? { ...f, name: name.trim() || f.name } : f))),
    remove: (id) => commit(get().folders.filter((f) => f.id !== id)),
    byId: (id) => get().folders.find((f) => f.id === id),
  };
});

/** Take an app out of a folder. If that leaves one app, the folder goes and its desktop icon becomes that app. */
export function takeOut(folderId: string, appId: string) {
  const desk = useDesktopIcons.getState();
  const key = folderKey(folderId);
  const at = desk.icons.find((i) => i.appId === key);
  const left = useFolders.getState().removeApp(folderId, appId);
  if (!useFolders.getState().byId(folderId) && at) {
    desk.remove(key);
    if (left && !desk.has(left)) desk.place(left, at);
  }
}

/** Dissolve a folder; its apps go back to Launchpad and its desktop icon is removed. */
export function ungroup(folderId: string) {
  useFolders.getState().remove(folderId);
  useDesktopIcons.getState().remove(folderKey(folderId));
}

/** The folder an app is in, if any. */
export const folderOf = (appId: string) => useFolders.getState().folders.find((f) => f.apps.includes(appId));
