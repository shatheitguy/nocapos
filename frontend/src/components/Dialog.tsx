import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { Icon, type IconName } from './Icon';

export type DialogSpec =
  | {
      kind: 'prompt';
      title: string;
      label?: string;
      initial?: string;
      /** Characters of `initial` to preselect (e.g. the name without extension). */
      selectTo?: number;
      confirm: string;
      onSubmit: (value: string) => void;
    }
  | {
      kind: 'confirm';
      title: string;
      body?: ReactNode;
      confirm: string;
      danger?: boolean;
      onSubmit: () => void;
    };

/** Modal scoped to the enclosing window (position: relative container). */
export function Dialog({ spec, onClose }: { spec: DialogSpec; onClose: () => void }) {
  const [value, setValue] = useState(spec.kind === 'prompt' ? (spec.initial ?? '') : '');
  const input = useRef<HTMLInputElement>(null);
  const confirmBtn = useRef<HTMLButtonElement>(null);

  useLayoutEffect(() => {
    if (spec.kind === 'prompt' && input.current) {
      input.current.focus();
      input.current.setSelectionRange(0, spec.selectTo ?? input.current.value.length);
    } else {
      confirmBtn.current?.focus();
    }
  }, [spec]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose]);

  const submit = () => {
    if (spec.kind === 'prompt') {
      if (!value.trim()) return;
      spec.onSubmit(value.trim());
    } else {
      spec.onSubmit();
    }
    onClose();
  };

  return (
    <div className="dialog-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        className="dialog"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
        onKeyDown={(e) => e.stopPropagation()}
      >
        <h3>{spec.title}</h3>
        {spec.kind === 'prompt' ? (
          <label>
            {spec.label}
            <input ref={input} value={value} onChange={(e) => setValue(e.target.value)} spellCheck={false} />
          </label>
        ) : (
          spec.body && <div className="muted">{spec.body}</div>
        )}
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose}>
            Cancel
          </button>
          <button ref={confirmBtn} type="submit" className={spec.kind === 'confirm' && spec.danger ? 'danger solid' : ''}>
            {spec.confirm}
          </button>
        </div>
      </form>
    </div>
  );
}

export interface MenuItem {
  label: string;
  icon?: IconName;
  shortcut?: string;
  danger?: boolean;
  disabled?: boolean;
  onClick: () => void;
}

export type MenuEntry = MenuItem | 'sep';

export function ContextMenu({ x, y, items, onClose }: { x: number; y: number; items: MenuEntry[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x, y });

  // Keep the menu inside its container.
  useLayoutEffect(() => {
    const el = ref.current;
    const parent = el?.offsetParent as HTMLElement | null;
    if (!el || !parent) return;
    setPos({
      x: Math.max(4, Math.min(x, parent.clientWidth - el.offsetWidth - 4)),
      y: Math.max(4, Math.min(y, parent.clientHeight - el.offsetHeight - 4)),
    });
  }, [x, y]);

  useEffect(() => {
    const onDown = (e: PointerEvent) => !ref.current?.contains(e.target as Node) && onClose();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('pointerdown', onDown, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  return (
    <div ref={ref} className="context-menu" style={{ left: pos.x, top: pos.y }} role="menu">
      {items.map((it, i) =>
        it === 'sep' ? (
          <div key={i} className="menu-sep" />
        ) : (
          <button
            key={i}
            type="button"
            role="menuitem"
            className={it.danger ? 'danger' : ''}
            disabled={it.disabled}
            onClick={() => {
              onClose();
              it.onClick();
            }}
          >
            {it.icon ? <Icon name={it.icon} size={15} /> : <span className="menu-icon-pad" />}
            <span className="menu-label">{it.label}</span>
            {it.shortcut && <span className="menu-shortcut">{it.shortcut}</span>}
          </button>
        ),
      )}
    </div>
  );
}
