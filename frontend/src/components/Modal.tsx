import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { tr } from '../i18n';
import { Icon, type IconName } from './Icon';
import { WinClose } from './WinClose';
import { startDialogDrag } from './dialogDrag';

// The one frame for dialogs and sheets that have a title: the title, and the
// close button windows have, placed the way window title bars place it. With
// the round buttons on the right the title sits left and the close right;
// with the left-button (traffic-light) style the red light comes first, with
// the title beside it. On phones the close is the same 40px round button as
// in window title bars. Escape and the close button do the same thing.

// Open dialogs, newest last: Escape closes only the one on top (a picker
// opened from a sheet closes before the sheet does).
const stack: { current: () => void }[] = [];
let listening = false;

function onEscape(e: KeyboardEvent) {
  if (e.key !== 'Escape' || e.defaultPrevented || !stack.length) return;
  // A confirm asked from inside the dialog answers Escape first.
  if (document.querySelector('.confirm-backdrop')) return;
  e.stopPropagation();
  e.preventDefault();
  stack[stack.length - 1].current();
}

/** Escape closes the topmost dialog, exactly like its close button. */
export function useModalEscape(onClose: () => void, enabled = true) {
  const latest = useRef(onClose);
  latest.current = onClose;
  useEffect(() => {
    if (!enabled) return;
    const entry = latest;
    stack.push(entry);
    if (!listening) {
      // Capture: text fields and the window manager handle keys on the way up.
      window.addEventListener('keydown', onEscape, true);
      listening = true;
    }
    return () => {
      const i = stack.lastIndexOf(entry);
      if (i >= 0) stack.splice(i, 1);
      if (!stack.length && listening) {
        window.removeEventListener('keydown', onEscape, true);
        listening = false;
      }
    };
  }, [enabled]);
}

/** For tests: how many dialogs answer Escape right now. */
export const openModalCount = () => stack.length;

/**
 * Where a dialog opened from deep inside an app is drawn: over the whole
 * window body, so styles of the place it was opened from (a sidebar, a list
 * row) never reach into it. Falls back to the page for the desktop.
 */
export function useModalHost(appRoot?: string) {
  const anchor = useRef<HTMLSpanElement>(null);
  const [host, setHost] = useState<Element | null>(null);
  const [top, setTop] = useState(0);
  useLayoutEffect(() => {
    const el = anchor.current;
    if (!el) return;
    // An app whose dialogs use its own scoped styles names its root element.
    const app = appRoot ? el.closest(appRoot) : null;
    if (app) {
      setTop(0);
      setHost(app);
      return;
    }
    // The window itself, not its (scrolling) body, with the backdrop starting
    // under the title bar: the window can still be moved or closed.
    const win = el.closest('.window');
    const body = win?.querySelector(':scope > .window-body') as HTMLElement | null;
    setTop(body ? body.offsetTop : 0);
    setHost(win ?? document.body);
  }, [appRoot]);
  const anchorEl = <span ref={anchor} hidden />;
  const style = host && host !== document.body && top ? { top } : undefined;
  return { anchorEl, host, style, fixed: host === document.body };
}

/** A dialog's title row: optional icon, the title (and a line under it), extra buttons, and the window close button. Dragging it moves the dialog. */
export function ModalHead({
  title,
  sub,
  icon,
  lead,
  titleId,
  onClose,
  closeLabel,
  className = '',
  children,
}: {
  title: ReactNode;
  sub?: ReactNode;
  icon?: IconName;
  /** Drawn before the title instead of the icon (an avatar, a colored tile). */
  lead?: ReactNode;
  titleId?: string;
  onClose: () => void;
  closeLabel?: string;
  className?: string;
  /** Extra buttons between the title and the close button. */
  children?: ReactNode;
}) {
  return (
    <header className={`modal-head ${className}`} onPointerDown={startDialogDrag}>
      <span className="modal-grabber" aria-hidden="true" />
      {lead ?? (icon && <span className="modal-head-icon"><Icon name={icon} size={18} /></span>)}
      <span className="modal-head-titles">
        <h2 id={titleId}>{title}</h2>
        {sub && <span className="modal-head-sub">{sub}</span>}
      </span>
      {children}
      <WinClose onClick={onClose} label={closeLabel ?? tr('common.close')} />
    </header>
  );
}

/**
 * A titled dialog over the window it was opened in: backdrop (a press on it
 * closes), the shared title row, a body and an optional footer. Becomes a
 * bottom sheet on phones.
 */
export function Modal({
  title,
  sub,
  icon,
  onClose,
  footer,
  className = '',
  bodyClassName = '',
  wide,
  children,
}: {
  title: string;
  sub?: ReactNode;
  icon?: IconName;
  onClose: () => void;
  footer?: ReactNode;
  className?: string;
  bodyClassName?: string;
  wide?: boolean;
  children: ReactNode;
}) {
  useModalEscape(onClose);
  const { anchorEl, host, style, fixed } = useModalHost();
  const dialog = (
    <div className={`dialog-backdrop modal-backdrop ${fixed ? 'fixed' : ''}`} style={style} onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog modal ${wide ? 'wide' : ''} ${className}`} role="dialog" aria-modal="true" aria-label={title} onKeyDown={(e) => e.stopPropagation()}>
        <ModalHead title={title} sub={sub} icon={icon} onClose={onClose} />
        <div className={`modal-body ${bodyClassName}`}>{children}</div>
        {footer && <footer className="modal-foot">{footer}</footer>}
      </div>
    </div>
  );
  return (
    <>
      {anchorEl}
      {host && createPortal(dialog, host)}
    </>
  );
}
