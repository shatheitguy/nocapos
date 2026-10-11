import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Icon, type IconName } from './Icon';

export interface MenuItem {
  label: string;
  icon?: IconName;
  danger?: boolean;
  disabled?: boolean;
  /** Draw a separator line above this item. */
  divider?: boolean;
  onClick: () => void;
}

/**
 * A lightweight right-click menu, positioned at (x, y) and clamped to the
 * viewport. With `above`, y is where the menu's bottom edge goes (menus that
 * open upwards from the taskbar).
 */
export function ContextMenu({ x, y, items, onClose, above = false }: { x: number; y: number; items: MenuItem[]; onClose: () => void; above?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x, y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const nx = Math.min(x, window.innerWidth - r.width - 8);
    const ny = above ? y - r.height : Math.min(y, window.innerHeight - r.height - 8);
    setPos({ x: Math.max(8, nx), y: Math.max(8, ny) });
  }, [x, y, above]);

  useEffect(() => {
    // Close on an outside press, but ignore presses that land inside the menu
    // (those are item clicks — let their onClick run before we unmount).
    const onDown = (e: PointerEvent) => {
      if (ref.current && e.target instanceof Node && ref.current.contains(e.target)) return;
      onClose();
    };
    window.addEventListener('pointerdown', onDown, true);
    window.addEventListener('blur', onClose);
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('blur', onClose);
      window.removeEventListener('keydown', onKey);
    };
  }, [onClose]);

  return createPortal(
    <div
      ref={ref}
      className="context-menu glass"
      style={{ left: pos.x, top: pos.y }}
      onPointerDown={(e) => e.stopPropagation()}
      role="menu"
    >
      {items.map((it, i) => [
        it.divider && i > 0 ? <hr key={`d${i}`} className="context-sep" /> : null,
        <button
          key={i}
          type="button"
          role="menuitem"
          className={`context-item ${it.danger ? 'danger' : ''}`}
          disabled={it.disabled}
          onClick={() => {
            if (it.disabled) return;
            it.onClick();
            onClose();
          }}
        >
          {it.icon && <Icon name={it.icon} size={15} />}
          <span>{it.label}</span>
        </button>,
      ])}
    </div>,
    document.body,
  );
}
