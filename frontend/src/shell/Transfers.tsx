import { useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Icon } from '../components/Icon';
import { fmtBytes } from '../lib/format';
import { useDismiss } from '../lib/hooks';
import { cancelTransfer, clearFinished, removeTransfer, retryTransfer, useTransfers, type Transfer } from '../state/transfers';

// Transfers: a menu-bar / tray button (only while there are transfers) with a
// panel listing uploads, copies and moves, plus a desktop widget.

const VERB = { upload: 'Upload', copy: 'Copy', move: 'Move' } as const;

function pct(t: Transfer) {
  if (t.status === 'done') return 100;
  return t.total ? Math.min(100, (t.done / t.total) * 100) : 0;
}

function Row({ t }: { t: Transfer }) {
  const running = t.status === 'running';
  const status =
    t.status === 'running'
      ? t.total
        ? `${fmtBytes(t.done)} of ${fmtBytes(t.total)}${t.current && t.kind !== 'upload' ? ` · ${t.current}` : ''}`
        : 'Starting…'
      : t.status === 'done'
        ? `Done${t.note ? ` · ${t.note}` : ''}`
        : t.status === 'canceled'
          ? 'Canceled'
          : (t.error ?? 'Failed');
  return (
    <div className={`xfer-row ${t.status}`}>
      <span className="xfer-icon">
        <Icon name={t.kind === 'upload' ? 'upload' : t.kind === 'move' ? 'swap' : 'copy'} size={15} />
      </span>
      <div className="xfer-main">
        <div className="xfer-title">
          <b className="ellipsis">{t.title}</b>
          <span className="muted small">
            {VERB[t.kind]} {t.detail}
          </span>
        </div>
        <div className="xfer-bar">
          <span style={{ width: `${pct(t)}%` }} />
        </div>
        <span className="xfer-status small">{status}</span>
      </div>
      {running ? (
        <button type="button" className="ghost icon-btn" title="Cancel" onClick={() => cancelTransfer(t.id)}>
          <Icon name="close" size={13} />
        </button>
      ) : t.status === 'failed' || t.status === 'canceled' ? (
        <button type="button" className="ghost icon-btn" title="Try again" onClick={() => retryTransfer(t.id)}>
          <Icon name="restart" size={13} />
        </button>
      ) : (
        <button type="button" className="ghost icon-btn" title="Remove from list" onClick={() => removeTransfer(t.id)}>
          <Icon name="check" size={13} />
        </button>
      )}
    </div>
  );
}

export function TransferList({ compact = false }: { compact?: boolean }) {
  const list = useTransfers((s) => s.list);
  if (!list.length) return <p className="muted small xfer-empty">No transfers. Uploads, copies and moves show up here.</p>;
  const finished = list.some((t) => t.status !== 'running');
  return (
    <div className={`xfer-list ${compact ? 'compact' : ''}`}>
      {list.map((t) => (
        <Row key={t.id} t={t} />
      ))}
      {finished && (
        <button type="button" className="ghost small xfer-clear" onClick={clearFinished}>
          Clear finished
        </button>
      )}
    </div>
  );
}

/** Ring showing overall progress of running transfers. */
function Ring({ value }: { value: number }) {
  const c = 2 * Math.PI * 8;
  return (
    <svg className="xfer-ring" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true">
      <circle cx="10" cy="10" r="8" className="track" />
      <circle cx="10" cy="10" r="8" className="value" strokeDasharray={`${(value / 100) * c} ${c}`} transform="rotate(-90 10 10)" />
    </svg>
  );
}

export function TransfersButton({ className }: { className: string }) {
  const list = useTransfers((s) => s.list);
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  useDismiss(open, () => setOpen(false), panel, btn);
  if (!list.length) return null;
  const running = list.filter((t) => t.status === 'running');
  const failed = list.some((t) => t.status === 'failed');
  const total = running.reduce((a, t) => a + t.total, 0);
  const done = running.reduce((a, t) => a + t.done, 0);
  return (
    <>
      <button
        ref={btn}
        type="button"
        className={`${className} xfer-btn ${open ? 'open' : ''} ${failed ? 'failed' : ''}`}
        title={running.length ? `${running.length} transfer${running.length > 1 ? 's' : ''} running` : 'Transfers'}
        aria-label="Transfers"
        onClick={() => setOpen((v) => !v)}
      >
        {running.length ? <Ring value={total ? (done / total) * 100 : 0} /> : <Icon name={failed ? 'alert' : 'check'} size={15} />}
      </button>
      {open &&
        createPortal(
        <div ref={panel} className="popover quick-settings xfer-panel" role="dialog" aria-label="Transfers">
          <div className="xfer-head">
            <b>Transfers</b>
            {running.length > 0 && <span className="muted small">{running.length} running</span>}
          </div>
          <TransferList />
        </div>,
          document.body,
        )}
    </>
  );
}

export function TransfersWidget() {
  const running = useTransfers((s) => s.list.filter((t) => t.status === 'running').length);
  return (
    <div className="widget glass xfer-widget">
      <div className="widget-head">
        <span>
          <Icon name="swap" size={15} /> Transfers
        </span>
        {running > 0 && <span className="chip tiny warn">{running} running</span>}
      </div>
      <TransferList compact />
    </div>
  );
}
