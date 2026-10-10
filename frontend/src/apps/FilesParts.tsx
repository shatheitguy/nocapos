// Phase 5 pieces for Files: RWX permission badges and the preview panel.
import { useEffect, useState } from 'react';
import { fileApi, fileKind, fileTicket, rawUrl, type FileEntry, type FileKind } from '../api/files';
import { FilesModal } from './FilesNetwork';
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

// ---------- icons and labels (grid view) ----------

const KIND_NOUN: Record<FileKind, string> = {
  folder: 'Folder',
  image: 'image',
  video: 'video',
  audio: 'audio',
  pdf: 'document',
  text: 'document',
  code: 'source',
  archive: 'archive',
  file: 'file',
};

export const extOf = (name: string) => (name.includes('.') && !name.startsWith('.') ? name.slice(name.lastIndexOf('.') + 1) : '');

/** "PNG image", "PDF document", "Folder", … */
export function kindLabel(name: string, dir = false): string {
  if (dir) return 'Folder';
  const kind = fileKind(name);
  const ext = extOf(name).toUpperCase();
  if (!ext) return kind === 'code' ? 'Config file' : 'File';
  return `${ext} ${KIND_NOUN[kind]}`;
}

/** A big folder (tinted with the accent colour) or a coloured file-type page. */
export function FileGlyph({ name, dir = false, size = 64, shared = false }: { name: string; dir?: boolean; size?: number; shared?: boolean }) {
  if (dir) {
    return (
      <span className="fglyph folder" style={{ width: size, height: size }}>
        <svg viewBox="0 0 64 64" width={size} height={size} aria-hidden="true">
          <path className="fg-back" d="M6 15a5 5 0 0 1 5-5h13.5a5 5 0 0 1 3.6 1.5l3.4 3.5H53a5 5 0 0 1 5 5v4H6z" />
          <path className="fg-front" d="M6 21a4 4 0 0 1 4-4h44a4 4 0 0 1 4 4v28a5 5 0 0 1-5 5H11a5 5 0 0 1-5-5z" />
          <path className="fg-shine" d="M10 21.5h44" />
        </svg>
        {shared && (
          <span className="fg-badge" title="Shared on the network">
            <Icon name="network" size={Math.max(10, size * 0.2)} />
          </span>
        )}
      </span>
    );
  }
  const kind = fileKind(name);
  const ext = extOf(name).toUpperCase().slice(0, 4);
  return (
    <span className={`fglyph page fk-${kind}`} style={{ width: size, height: size }}>
      <svg viewBox="0 0 64 64" width={size} height={size} aria-hidden="true">
        <path className="fg-page" d="M15 5h24l13 13v37a4 4 0 0 1-4 4H15a4 4 0 0 1-4-4V9a4 4 0 0 1 4-4z" />
        <path className="fg-fold" d="M39 5v9a4 4 0 0 0 4 4h9z" />
        {ext && size >= 28 && (
          <text x="31.5" y="47" textAnchor="middle" className="fg-ext">
            {ext}
          </text>
        )}
      </svg>
    </span>
  );
}

// ---------- Get info ----------

export function InfoDialog({ root, rootName, path, entry, unix, shared, onClose }: { root: string; rootName: string; path: string; entry: FileEntry; unix: boolean; shared: boolean; onClose: () => void }) {
  const [count, setCount] = useState<number | null>(null);
  useEffect(() => {
    if (!entry.dir) return;
    let live = true;
    void fileApi.list(root, path).then((r) => live && r.ok && setCount(r.data.entries.length));
    return () => {
      live = false;
    };
  }, [root, path, entry.dir]);
  const where = path.slice(0, path.lastIndexOf('/')) || '/';
  return (
    <FilesModal title="Info" onClose={onClose}>
      <div className="finfo">
        <div className="finfo-head">
          <FileGlyph name={entry.name} dir={entry.dir} size={72} shared={shared} />
          <div className="finfo-title">
            <b>{entry.name}</b>
            <span className="muted">{entry.dir ? (count === null ? 'Folder' : `Folder · ${count} item${count === 1 ? '' : 's'}`) : `${kindLabel(entry.name)} · ${fmtBytes(entry.size)}`}</span>
          </div>
        </div>
        <dl className="fp-meta finfo-meta">
          <dt>Kind</dt>
          <dd>{kindLabel(entry.name, entry.dir)}</dd>
          {!entry.dir && (
            <>
              <dt>Size</dt>
              <dd>
                {fmtBytes(entry.size)} <span className="muted">({entry.size.toLocaleString()} bytes)</span>
              </dd>
            </>
          )}
          <dt>Where</dt>
          <dd className="ellipsis" title={`${rootName}${where === '/' ? '' : where}`}>
            {rootName}
            {where === '/' ? '' : where}
          </dd>
          <dt>Modified</dt>
          <dd>{new Date(entry.mod_time).toLocaleString([], { dateStyle: 'full', timeStyle: 'short' })}</dd>
          {entry.mode && (
            <>
              <dt>Access</dt>
              <dd>
                <PermBadges entry={entry} unix={unix} />
              </dd>
            </>
          )}
          {entry.owner && (
            <>
              <dt>Owner</dt>
              <dd>
                {entry.owner}
                <span className="muted"> : {entry.group}</span>
              </dd>
            </>
          )}
          {shared && (
            <>
              <dt>Sharing</dt>
              <dd>Shared on the network</dd>
            </>
          )}
        </dl>
      </div>
    </FilesModal>
  );
}
