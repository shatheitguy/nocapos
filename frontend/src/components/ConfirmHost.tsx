import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useConfirm } from '../state/confirm';
import { Icon } from './Icon';

/** Renders the pending confirmDialog(), if any. Mount once on the desktop. */
export function ConfirmHost() {
  const req = useConfirm((s) => s.current);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const [left, setLeft] = useState<number | null>(null);
  // Optional countdown: cancels itself at zero (e.g. "Keep these settings?").
  useEffect(() => {
    if (!req?.timeoutSec) {
      setLeft(null);
      return;
    }
    const end = Date.now() + req.timeoutSec * 1000;
    setLeft(req.timeoutSec);
    const t = window.setInterval(() => {
      const s = Math.max(0, Math.ceil((end - Date.now()) / 1000));
      setLeft(s);
      if (s === 0) req.resolve(false);
    }, 250);
    return () => window.clearInterval(t);
  }, [req]);
  useEffect(() => {
    if (!req) return;
    cancelRef.current?.focus(); // safe default
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && req.resolve(false);
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [req]);
  if (!req) return null;
  return createPortal(
    <div className="confirm-backdrop" onPointerDown={(e) => e.target === e.currentTarget && req.resolve(false)}>
      <div className="confirm-dialog glass" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
        <div className={`confirm-icon ${req.danger ? 'danger' : ''}`}>
          <Icon name={req.danger ? 'power' : 'info'} size={22} />
        </div>
        <h3 id="confirm-title">{req.title}</h3>
        <p>{req.message}</p>
        {left !== null && (
          <div className="confirm-countdown">
            <span style={{ width: `${(left / (req.timeoutSec ?? 1)) * 100}%` }} />
            <b>{left}s</b>
          </div>
        )}
        <div className="confirm-actions">
          <button ref={cancelRef} type="button" className="ghost" onClick={() => req.resolve(false)}>
            {req.cancelLabel ?? 'Cancel'}
          </button>
          <button type="button" className={req.danger ? 'danger-solid' : ''} onClick={() => req.resolve(true)}>
            {req.confirmLabel ?? 'Continue'}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
