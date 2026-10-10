// Transfers: uploads, copies and moves with progress, conflicts, retry and
// cancel — shared by every Files window, the Transfers panel and its widget.
import { create } from 'zustand';
import { fileApi, upload, type Conflict, type TransferJob } from '../api/files';
import { choiceDialog } from './confirm';
import { toast } from './toasts';

export type TransferStatus = 'running' | 'done' | 'failed' | 'canceled';

export interface Transfer {
  id: string;
  kind: 'upload' | 'copy' | 'move' | 'compress' | 'extract';
  title: string;
  /** Where it goes, e.g. "to Photos". */
  detail: string;
  root: string;
  dest: string;
  status: TransferStatus;
  done: number;
  total: number;
  current?: string;
  error?: string;
  note?: string;
  started: number;
  ended?: number;
}

interface Store {
  list: Transfer[];
  /** Bumped whenever a transfer finishes, so Files windows can reload. */
  version: number;
}

export const useTransfers = create<Store>(() => ({ list: [], version: 0 }));

const actions = new Map<string, { cancel?: () => void; retry?: () => void }>();
let seq = 0;

function add(t: Omit<Transfer, 'id' | 'started' | 'status' | 'done' | 'total'> & { total?: number }): string {
  const id = `t${++seq}`;
  const entry: Transfer = { id, started: Date.now(), status: 'running', done: 0, ...t, total: t.total ?? 0 };
  useTransfers.setState((s) => ({ list: [entry, ...s.list].slice(0, 40) }));
  return id;
}

function update(id: string, patch: Partial<Transfer>) {
  useTransfers.setState((s) => ({ list: s.list.map((t) => (t.id === id ? { ...t, ...patch } : t)) }));
}

function finish(id: string, status: TransferStatus, patch: Partial<Transfer> = {}) {
  update(id, { status, ended: Date.now(), current: undefined, ...patch });
  useTransfers.setState((s) => ({ version: s.version + 1 }));
}

export const cancelTransfer = (id: string) => actions.get(id)?.cancel?.();
export const retryTransfer = (id: string) => {
  const a = actions.get(id);
  removeTransfer(id);
  a?.retry?.();
};
export function removeTransfer(id: string) {
  actions.delete(id);
  useTransfers.setState((s) => ({ list: s.list.filter((t) => t.id !== id) }));
}
export function clearFinished() {
  useTransfers.setState((s) => ({ list: s.list.filter((t) => t.status === 'running') }));
}

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;
const folderName = (p: string) => (p === '/' ? 'the top folder' : p.slice(p.lastIndexOf('/') + 1));

/** If some names already exist in the destination, ask what to do. null = cancel. */
export async function askConflict(names: string[]): Promise<Conflict | null> {
  if (!names.length) return 'rename';
  const what = names.length === 1 ? `“${names[0]}” already exists` : `${names.length} items already exist`;
  const id = await choiceDialog({
    title: `${what} in this folder`,
    message:
      names.length === 1
        ? 'Keep both (the new one gets a number), replace the existing item, or skip it?'
        : `${names.slice(0, 4).join(', ')}${names.length > 4 ? ', …' : ''}. Keep both, replace the existing items, or skip them?`,
    choices: [
      { id: 'skip', label: 'Skip' },
      { id: 'replace', label: 'Replace', danger: true },
      { id: 'rename', label: 'Keep both' },
    ],
  });
  return (id as Conflict | null) ?? null;
}

/** Upload files into root:dir, asking about name conflicts first. */
export async function startUpload(root: string, dir: string, files: File[], conflict?: Conflict) {
  if (!files.length) return;
  if (!conflict) {
    const r = await fileApi.list(root, dir);
    const existing = new Set(r.ok ? r.data.entries.map((e) => e.name) : []);
    const c = await askConflict(files.filter((f) => existing.has(f.name)).map((f) => f.name));
    if (!c) return;
    conflict = c;
  }
  const total = files.reduce((a, f) => a + f.size, 0);
  const id = add({
    kind: 'upload',
    title: files.length === 1 ? files[0].name : plural(files.length, 'file'),
    detail: `to ${folderName(dir)}`,
    root,
    dest: dir,
    total,
  });
  const ctl = new AbortController();
  actions.set(id, { cancel: () => ctl.abort(), retry: () => void startUpload(root, dir, files, conflict) });
  const r = await upload(root, dir, files, (p) => update(id, { done: p.loaded, total: p.total }), { conflict, signal: ctl.signal });
  if (r.ok) {
    const skipped = r.data.skipped?.length ?? 0;
    finish(id, 'done', { done: total, note: skipped ? `${skipped} skipped` : undefined });
  } else if (r.status === -1) finish(id, 'canceled');
  else finish(id, 'failed', { error: r.error });
}

/** Copy or move paths into root:dest as a background job, asking about conflicts first. */
export async function startTransfer(root: string, paths: string[], dest: string, move: boolean, conflict?: Conflict): Promise<boolean> {
  if (!paths.length) return false;
  if (!conflict) {
    const r = await fileApi.conflicts(root, paths, dest);
    const c = await askConflict(r.ok ? r.data.names : []);
    if (!c) return false;
    conflict = c;
  }
  const r = await fileApi.transferJob(root, paths, dest, move, conflict);
  if (!r.ok) {
    toast('error', move ? 'Move failed' : 'Copy failed', r.error);
    return false;
  }
  const job = r.data;
  const name = paths.length === 1 ? paths[0].slice(paths[0].lastIndexOf('/') + 1) : plural(paths.length, 'item');
  const id = add({ kind: move ? 'move' : 'copy', title: name, detail: `to ${folderName(dest)}`, root, dest });
  actions.set(id, {
    cancel: () => void fileApi.cancelJob(job.id),
    retry: () => void startTransfer(root, paths, dest, move, conflict),
  });
  return (await follow(id, job)) !== null;
}

/** Compress paths into a .zip in dest, or extract one archive into dest, as a background job. */
export async function startArchiveJob(kind: 'compress' | 'extract', root: string, paths: string[], dest: string, zipName = ''): Promise<string[] | null> {
  if (!paths.length) return null;
  const r = kind === 'compress' ? await fileApi.compress(root, paths, dest, zipName) : await fileApi.extract(root, paths[0], dest);
  if (!r.ok) {
    toast('error', kind === 'compress' ? 'Could not compress' : 'Could not extract', r.error);
    return null;
  }
  const job = r.data;
  const title = kind === 'compress' ? zipName || 'Archive.zip' : paths[0].slice(paths[0].lastIndexOf('/') + 1);
  const id = add({ kind, title, detail: `${kind === 'compress' ? 'in' : 'into'} ${folderName(dest)}`, root, dest });
  actions.set(id, {
    cancel: () => void fileApi.cancelJob(job.id),
    retry: () => void startArchiveJob(kind, root, paths, dest, zipName),
  });
  return follow(id, job);
}

/** Follow a server job until it ends; its result paths, or null if it didn't finish. */
async function follow(id: string, job: TransferJob): Promise<string[] | null> {
  for (;;) {
    await new Promise((res) => setTimeout(res, 500));
    const j = await fileApi.job(job.id);
    if (!j.ok) {
      finish(id, 'failed', { error: j.error ?? 'Lost track of the transfer' });
      return null;
    }
    const p = j.data.progress;
    update(id, { done: p.bytes_done, total: p.bytes_total, current: p.current || undefined });
    if (j.data.status !== 'running') {
      const skipped = j.data.result?.skipped.length ?? 0;
      finish(id, j.data.status, { error: j.data.error, note: skipped ? `${skipped} skipped` : undefined, done: p.bytes_total });
      return j.data.status === 'done' ? (j.data.result?.paths ?? []) : null;
    }
  }
}
