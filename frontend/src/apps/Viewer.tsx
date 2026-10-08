import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { baseName, downloadFile, fileApi, fileKind, fileTicket, rawUrl } from '../api/files';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { toast } from '../state/toasts';
import { useWM, type WinState } from '../state/windows';

export function Viewer({ win }: { win: WinState }) {
  const root = win.props?.root ?? '';
  const path = win.props?.path ?? '';
  const name = baseName(path);
  const kind = fileKind(name);
  const [url, setUrl] = useState<string | null>(null);
  const [linkError, setLinkError] = useState(false);

  useEffect(() => {
    if (kind === 'text' || kind === 'code') return;
    let live = true;
    void fileTicket(root, path).then((t) => {
      if (!live) return;
      if (t) setUrl(rawUrl(root, path, t));
      else setLinkError(true);
    });
    return () => {
      live = false;
    };
  }, [root, path, kind]);

  const download = () => void downloadFile(root, path).then((ok) => !ok && toast('error', `Could not download ${name}`));

  if (kind === 'text' || kind === 'code') return <TextEditor win={win} root={root} path={path} onDownload={download} />;
  if (linkError) return <Unsupported name={name} text="The file could not be opened." onDownload={download} />;
  if (!url) return <div className="empty muted">Loading…</div>;

  return (
    <div className="viewer">
      <div className="toolbar">
        <b className="ellipsis">{name}</b>
        <span className="spacer" />
        {kind === 'pdf' && (
          <a className="btn ghost small" href={url} target="_blank" rel="noopener noreferrer">
            <Icon name="external" size={14} /> Open in new tab
          </a>
        )}
        <button type="button" className="ghost small" onClick={download}>
          <Icon name="download" size={14} /> Download
        </button>
      </div>
      <div className={`viewer-stage kind-${kind}`}>
        {kind === 'image' && <ImageView url={url} name={name} />}
        {kind === 'video' && <video src={url} controls autoPlay className="media" />}
        {kind === 'audio' && (
          <div className="audio-card">
            <Icon name="fileAudio" size={56} />
            <b>{name}</b>
            <audio src={url} controls autoPlay />
          </div>
        )}
        {kind === 'pdf' && (
          <Unsupported
            name={name}
            text="PDFs open in your browser's viewer in a separate tab."
            onDownload={download}
            action={
              <a className="btn" href={url} target="_blank" rel="noopener noreferrer">
                <Icon name="external" size={15} /> Open PDF
              </a>
            }
          />
        )}
        {(kind === 'archive' || kind === 'file') && <Unsupported name={name} text="No preview available for this file type." onDownload={download} />}
      </div>
    </div>
  );
}

function ImageView({ url, name }: { url: string; name: string }) {
  const [actual, setActual] = useState(false);
  const [dims, setDims] = useState<string | null>(null);
  return (
    <div className={`image-view ${actual ? 'actual' : ''}`} onDoubleClick={() => setActual((v) => !v)}>
      <img
        src={url}
        alt={name}
        draggable={false}
        onLoad={(e) => setDims(`${e.currentTarget.naturalWidth} × ${e.currentTarget.naturalHeight}`)}
      />
      <div className="image-info small">
        {dims} · {actual ? '100%' : 'fit'} · double-click to toggle
      </div>
    </div>
  );
}

function Unsupported({ name, text, onDownload, action }: { name: string; text: string; onDownload: () => void; action?: ReactNode }) {
  return (
    <div className="empty">
      <Icon name="file" size={44} />
      <b>{name}</b>
      <p className="muted">{text}</p>
      <div className="btn-group">
        {action}
        <button type="button" className={action ? 'ghost' : ''} onClick={onDownload}>
          <Icon name="download" size={15} /> Download
        </button>
      </div>
    </div>
  );
}

function TextEditor({ win, root, path, onDownload }: { win: WinState; root: string; path: string; onDownload: () => void }) {
  const [content, setContent] = useState<string | null>(null);
  const [saved, setSaved] = useState('');
  const [modTime, setModTime] = useState<string | undefined>();
  const [size, setSize] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [wrap, setWrap] = useState(true);
  const dirty = content !== null && content !== saved;

  useEffect(() => {
    let live = true;
    void fileApi.readText(root, path).then((r) => {
      if (!live) return;
      if (!r.ok) {
        setError(r.error ?? 'Could not open file');
        return;
      }
      setContent(r.data.content);
      setSaved(r.data.content);
      setModTime(r.data.mod_time);
      setSize(r.data.size);
    });
    return () => {
      live = false;
    };
  }, [root, path]);

  // Mark the window title while there are unsaved changes.
  useEffect(() => {
    const title = `${dirty ? '● ' : ''}${baseName(path)}`;
    if (win.title !== title) useWM.setState((s) => ({ windows: s.windows.map((w) => (w.id === win.id ? { ...w, title } : w)) }));
  }, [dirty, path, win.id, win.title]);

  const save = useCallback(async () => {
    if (content === null || saving) return;
    setSaving(true);
    const r = await fileApi.writeText(root, path, content, modTime);
    setSaving(false);
    if (!r.ok) {
      toast('error', 'Save failed', r.error);
      return;
    }
    setSaved(content);
    setModTime(r.data.mod_time);
    setSize(r.data.size);
    toast('success', `Saved ${baseName(path)}`);
  }, [content, modTime, path, root, saving]);

  if (error) return <Unsupported name={baseName(path)} text={error} onDownload={onDownload} />;
  if (content === null) return <div className="empty muted">Loading…</div>;

  return (
    <div className="editor">
      <div className="toolbar">
        <button type="button" className="small" disabled={!dirty || saving} onClick={() => void save()}>
          <Icon name="save" size={14} /> {saving ? 'Saving…' : 'Save'}
        </button>
        <label className="toggle">
          <input type="checkbox" checked={wrap} onChange={(e) => setWrap(e.target.checked)} /> Wrap lines
        </label>
        <span className="spacer" />
        <span className="muted small">
          {content.split('\n').length} lines · {fmtBytes(size)}
          {dirty ? ' · unsaved changes' : ''}
        </span>
        <button type="button" className="ghost icon-btn" title="Download" onClick={onDownload}>
          <Icon name="download" size={15} />
        </button>
      </div>
      <textarea
        className={`editor-area mono ${wrap ? 'wrap' : ''}`}
        value={content}
        spellCheck={false}
        onChange={(e) => setContent(e.target.value)}
        onKeyDown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
            e.preventDefault();
            void save();
          } else if (e.key === 'Tab') {
            e.preventDefault();
            const t = e.currentTarget;
            const { selectionStart: a, selectionEnd: b } = t;
            const next = `${content.slice(0, a)}  ${content.slice(b)}`;
            setContent(next);
            requestAnimationFrame(() => t.setSelectionRange(a + 2, a + 2));
          }
        }}
      />
    </div>
  );
}
