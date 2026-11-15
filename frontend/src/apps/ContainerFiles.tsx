import { useCallback, useEffect, useRef, useState } from 'react';
import type { Container } from '../api/types';
import { dockerApi, downloadWithAuth, uploadWithAuth, type FileEntry, type FileListing, type FilePreview } from '../api/docker';
import { Icon } from '../components/Icon';
import { VirtualList } from '../components/VirtualList';
import { crumbs, parentPath, validName, validPath } from '../lib/containers';
import { fmtAgo, fmtBytes } from '../lib/format';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';

// Files inside a container: browse, preview text, download (folders as a
// .tar), and upload into a folder. The server checks every path, never
// writes into /proc, /sys or /dev, and keeps NoCapOS's own containers
// read-only.

const UPLOAD_BLOCKED = /^\/(proc|sys|dev)(\/|$)/;

export function ContainerFiles({ c }: { c: Container }) {
  const t = useT();
  const [path, setPath] = useState('/');
  const [typed, setTyped] = useState('/');
  const [list, setList] = useState<FileListing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [preview, setPreview] = useState<(FilePreview & { name: string }) | null>(null);
  const [uploading, setUploading] = useState(false);
  const [filter, setFilter] = useState('');
  const picker = useRef<HTMLInputElement>(null);

  const load = useCallback(async (p: string) => {
    setLoading(true);
    setError(null);
    const r = await dockerApi.files(c.id, p);
    setLoading(false);
    if (!r.ok) {
      setError(r.error ?? tr('ctr.files.failed'));
      return;
    }
    setList(r.data);
    setPath(r.data.path);
    setTyped(r.data.path);
    setFilter('');
  }, [c.id]);

  useEffect(() => {
    void load('/');
  }, [load]);

  const open = async (e: FileEntry) => {
    if (e.dir || (e.link && !e.size)) return void load(e.path);
    const r = await dockerApi.fileContent(c.id, e.path);
    if (!r.ok) {
      if (r.data && (r.data as unknown as { error?: { code?: string } }).error?.code === 'is_dir') return void load(e.path);
      return toast('error', t('ctr.files.previewFail'), r.error);
    }
    setPreview({ ...r.data, name: e.name });
  };

  const download = async (p: string, name: string) => {
    const err = await downloadWithAuth(dockerApi.fileDownloadUrl(c.id, p), name);
    if (err) toast('error', t('ctr.files.downloadFail'), err);
  };

  const upload = async (files: FileList | null) => {
    if (!files?.length) return;
    setUploading(true);
    for (const f of Array.from(files)) {
      if (!validName(f.name)) {
        toast('error', t('ctr.files.badName', { name: f.name }));
        continue;
      }
      const err = await uploadWithAuth(dockerApi.uploadUrl(c.id, path, f.name), f);
      if (err) toast('error', t('ctr.files.uploadFail', { name: f.name }), err);
      else toast('success', t('ctr.files.uploaded', { name: f.name, path }));
    }
    setUploading(false);
    if (picker.current) picker.current.value = '';
    void load(path);
  };

  const canUpload = !c.system && !UPLOAD_BLOCKED.test(path);
  const q = filter.trim().toLowerCase();
  const entries = (list?.entries ?? []).filter((e) => !q || e.name.toLowerCase().includes(q));

  return (
    <div className="ct-files">
      <div className="ct-files-bar">
        <button type="button" className="ghost icon-btn" disabled={path === '/'} title={t('ctr.files.up')} aria-label={t('ctr.files.up')} onClick={() => void load(parentPath(path))}>
          <Icon name="arrowUp" size={15} />
        </button>
        <form
          className="ct-files-path"
          onSubmit={(e) => {
            e.preventDefault();
            if (validPath(typed)) void load(typed);
            else toast('error', t('ctr.files.badPath'));
          }}
        >
          <input className="mono" value={typed} spellCheck={false} aria-label={t('ctr.files.path')} onChange={(e) => setTyped(e.target.value)} />
        </form>
        <label className="search compact ct-files-filter">
          <Icon name="search" size={14} />
          <input placeholder={t('ctr.files.filter')} aria-label={t('ctr.files.filter')} value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
        <button type="button" className="ghost icon-btn" title={t('ctr.refresh')} aria-label={t('ctr.refresh')} onClick={() => void load(path)}>
          <Icon name="restart" size={15} />
        </button>
        <button type="button" className="ghost small" title={t('ctr.files.downloadFolder')} onClick={() => void download(path, `${path.split('/').pop() || 'root'}.tar`)}>
          <Icon name="download" size={14} /> <span className="ct-hide-narrow">.tar</span>
        </button>
        <button type="button" className="ghost small" disabled={!canUpload || uploading} title={canUpload ? t('ctr.files.uploadHint', { path }) : t('ctr.files.noUpload')} onClick={() => picker.current?.click()}>
          {uploading ? <span className="spinner sm" /> : <Icon name="upload" size={14} />} <span className="ct-hide-narrow">{t('ctr.files.upload')}</span>
        </button>
        <input ref={picker} type="file" multiple hidden onChange={(e) => void upload(e.target.files)} />
      </div>
      <nav className="ct-crumbs" aria-label={t('ctr.files.path')}>
        {crumbs(path).map(([label, p], i) => (
          <button key={p} type="button" className="ghost small" onClick={() => void load(p)}>
            {i > 0 && <Icon name="chevronRight" size={11} />} {label}
          </button>
        ))}
      </nav>
      {error && <p className="error">{error}</p>}
      {list?.truncated && <p className="muted small">{t('ctr.files.truncated')}</p>}
      {loading && !list ? (
        <p className="muted">{t('ctr.loading')}</p>
      ) : entries.length === 0 ? (
        <p className="muted">{t('ctr.files.empty')}</p>
      ) : (
        <VirtualList
          className="ct-files-list ucard"
          label={path}
          count={entries.length}
          rowHeight={40}
          render={(i) => {
            const e = entries[i];
            return (
              <div key={e.path} className="ct-file" role="listitem">
                <button type="button" className="ct-file-main" onClick={() => void open(e)} title={e.link_target ? `${e.name} → ${e.link_target}` : e.name}>
                  <Icon name={e.dir ? 'folder' : e.link ? 'external' : 'file'} size={15} />
                  <span className="ellipsis">{e.name}</span>
                  {e.link && e.link_target && <span className="muted small ellipsis ct-hide-narrow">→ {e.link_target}</span>}
                </button>
                <span className="muted small ct-file-size">{e.dir ? '' : fmtBytes(e.size)}</span>
                <span className="muted small ct-file-time ct-hide-narrow">{e.mtime && !e.mtime.startsWith('0001') ? fmtAgo(e.mtime) : ''}</span>
                <span className="mono muted small ct-hide-narrow">{e.mode}</span>
                <button type="button" className="ghost icon-btn" title={t('ctr.files.download')} aria-label={t('ctr.files.download')} onClick={() => void download(e.path, e.dir ? `${e.name}.tar` : e.name)}>
                  <Icon name="download" size={14} />
                </button>
              </div>
            );
          }}
        />
      )}
      {preview && (
        <div className="ucard ct-preview">
          <div className="ct-preview-head">
            <Icon name="fileText" size={15} />
            <b className="ellipsis">{preview.name}</b>
            <span className="muted small">{fmtBytes(preview.size)}</span>
            <span className="spacer" />
            <button type="button" className="ghost small" onClick={() => void download(preview.path, preview.name)}>
              <Icon name="download" size={13} /> {t('ctr.files.download')}
            </button>
            <button type="button" className="ghost icon-btn" aria-label={t('ctr.close')} onClick={() => setPreview(null)}>
              <Icon name="close" size={14} />
            </button>
          </div>
          {preview.binary ? (
            <p className="muted">{t('ctr.files.binary')}</p>
          ) : (
            <pre className="ct-preview-text mono">{preview.content}</pre>
          )}
          {preview.truncated && !preview.binary && <p className="muted small">{t('ctr.files.previewCut')}</p>}
        </div>
      )}
    </div>
  );
}

