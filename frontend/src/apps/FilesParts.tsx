// Phase 5 pieces for Files: RWX permission badges and the preview panel.
import { useEffect, useState } from 'react';
import { fileApi, fileKind, fileTicket, rawUrl, type FileEntry } from '../api/files';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { CodeView, langName } from '../lib/highlight';

const WHO = ['Owner', 'Group', 'Others'];

/** "rwxr-xr-x" as three colour-coded owner / group / others badges. */
export function PermBadges({ entry, compact = false, unix = true }: { entry: FileEntry; compact?: boolean; unix?: boolean }) {
  const m = entry.mode ?? '';
  if (m.length < 10) return null;
  const triplets = [m.slice(1, 4), m.slice(4, 7), m.slice(7, 10)];
  const octal = (entry.perm & 0o7777).toString(8).padStart(3, '0');
  const title = unix
    ? `${m}  (${octal})${entry.owner ? `\nOwner ${entry.owner} · Group ${entry.group}` : ''}`
    : `${m}\nWindows doesn't use Unix permissions; this only reflects the read-only flag.`;
  return (
    <span className={`perm-badges ${compact ? 'compact' : ''} ${unix ? '' : 'synthetic'}`} title={title}>
      {triplets.map((t, i) => (
        <span key={i} className="perm-trip" aria-label={`${WHO[i]}: ${describe(t)}`}>
          {[...t].map((c, j) => (
            <i key={j} className={c === '-' ? 'off' : c === 'r' ? 'r' : c === 'w' ? 'w' : 'x'}>
              {c}
            </i>
          ))}
        </span>
      ))}
    </span>
  );
}

function describe(t: string) {
  const parts = [t[0] === 'r' && 'read', t[1] === 'w' && 'write', t[2] !== '-' && 'execute'].filter(Boolean);
  return parts.length ? parts.join(', ') : 'no access';
}

const MAX_PREVIEW = 512 * 1024;

/** Right-hand preview: details, permissions and a highlighted / image preview. */
export function FilePreview({ root, path, entry, unix, onClose }: { root: string; path: string; entry: FileEntry | null; unix: boolean; onClose: () => void }) {
  const kind = entry ? fileKind(entry.name, entry.dir) : null;
  const [text, setText] = useState<{ content: string; error?: string } | null>(null);
  const [ticket, setTicket] = useState<string | null>(null);

  useEffect(() => {
    setText(null);
    setTicket(null);
    if (!entry || entry.dir) return;
    let live = true;
    if (kind === 'text' || kind === 'code') {
      if (entry.size > MAX_PREVIEW) {
        setText({ content: '', error: `Too large to preview here (${fmtBytes(entry.size)}). Open it in the Viewer.` });
        return;
      }
      void fileApi.readText(root, path).then((r) => live && setText(r.ok ? { content: r.data.content } : { content: '', error: r.error ?? 'Could not read the file' }));
    } else if (kind === 'image' || kind === 'video' || kind === 'audio') {
      void fileTicket(root, path).then((t) => live && setTicket(t));
    }
    return () => {
      live = false;
    };
  }, [root, path, entry, kind]);

  return (
    <aside className="files-preview">
      <div className="fp-head">
        <b className="ellipsis">{entry ? entry.name : 'Preview'}</b>
        <button type="button" className="ghost icon-btn" aria-label="Close preview" onClick={onClose}>
          <Icon name="close" size={13} />
        </button>
      </div>
      {!entry ? (
        <p className="muted small fp-empty">Select a file to preview it here.</p>
      ) : (
        <>
          <dl className="fp-meta">
            <dt>Kind</dt>
            <dd>{entry.dir ? 'Folder' : kind === 'code' || kind === 'text' ? langName(entry.name) : kind}</dd>
            {!entry.dir && (
              <>
                <dt>Size</dt>
                <dd>{fmtBytes(entry.size)}</dd>
              </>
            )}
            <dt>Modified</dt>
            <dd>{new Date(entry.mod_time).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })}</dd>
            <dt>Access</dt>
            <dd>
              <PermBadges entry={entry} unix={unix} />
              {unix && <span className="mono fp-octal">{(entry.perm & 0o7777).toString(8).padStart(4, '0')}</span>}
            </dd>
            {entry.owner && (
              <>
                <dt>Owner</dt>
                <dd>
                  {entry.owner}
                  <span className="muted"> : {entry.group}</span>
                </dd>
              </>
            )}
          </dl>
          <div className="fp-body">
            {entry.dir ? (
              <p className="muted small">Double-click to open the folder.</p>
            ) : kind === 'text' || kind === 'code' ? (
              text === null ? (
                <p className="muted small">Loading…</p>
              ) : text.error ? (
                <p className="muted small">{text.error}</p>
              ) : (
                <CodeView text={text.content} name={entry.name} />
              )
            ) : kind === 'image' && ticket ? (
              <img className="fp-media" src={rawUrl(root, path, ticket)} alt={entry.name} />
            ) : kind === 'video' && ticket ? (
              <video className="fp-media" src={rawUrl(root, path, ticket)} controls />
            ) : kind === 'audio' && ticket ? (
              <audio className="fp-audio" src={rawUrl(root, path, ticket)} controls />
            ) : (
              <p className="muted small">No preview for this kind of file.</p>
            )}
          </div>
        </>
      )}
    </aside>
  );
}
