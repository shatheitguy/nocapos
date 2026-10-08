import { useEffect, useState, type MouseEvent as RMouseEvent } from 'react';
import { APPS, canMultiWindow, openApp } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { ContextMenu } from '../components/ContextMenu';
import { pressApp, useAppDrag } from '../state/appDrag';
import { iconCell, useDesktopIcons } from '../state/desktopIcons';
import { usePrefs } from '../state/prefs';
import { appMenuItems } from './Shelf';

/** App shortcuts on the desktop: drag anywhere, double-click to open. */
export function DesktopIcons({ isAdmin }: { isAdmin: boolean }) {
  const icons = useDesktopIcons((s) => s.icons);
  usePrefs((s) => s.iconSize); // re-render when the icon size changes
  const cell = iconCell();
  const dragging = useAppDrag((s) => (s.drag?.source === 'desktop' ? s.drag.appId : null));
  const [selected, setSelected] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; appId: string } | null>(null);

  // Clicking anywhere else clears the selection; Enter opens the selected icon.
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (!(e.target as HTMLElement).closest?.('.desktop-icon')) setSelected(null);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && selected && document.activeElement?.closest('.desktop-icon')) openApp(selected);
    };
    window.addEventListener('pointerdown', onDown, true);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('pointerdown', onDown, true);
      window.removeEventListener('keydown', onKey);
    };
  }, [selected]);

  const visible = icons.filter((i) => APPS[i.appId] && !APPS[i.appId].hidden && (isAdmin || !APPS[i.appId].adminOnly));

  const open = (e: RMouseEvent, appId: string) => {
    if (e.shiftKey && canMultiWindow(appId)) openApp(appId, { newWindow: true });
    else openApp(appId);
  };

  return (
    <div className="desktop-icons" data-drop="desktop">
      {visible.map((i) => (
        <button
          key={i.appId}
          type="button"
          className={`desktop-icon ${selected === i.appId ? 'selected' : ''} ${dragging === i.appId ? 'dragging' : ''}`}
          style={{ left: i.x, top: i.y, width: cell.w }}
          title={`${APPS[i.appId].title} — double-click to open`}
          onPointerDown={(e) => {
            setSelected(i.appId);
            pressApp(e, i.appId, 'desktop');
          }}
          onDoubleClick={(e) => open(e, i.appId)}
          onContextMenu={(e) => {
            e.preventDefault();
            e.stopPropagation();
            setSelected(i.appId);
            setMenu({ x: e.clientX, y: e.clientY, appId: i.appId });
          }}
        >
          <AppIcon app={APPS[i.appId]} size={cell.icon} />
          <span className="desktop-icon-label">{APPS[i.appId].title}</span>
        </button>
      ))}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={appMenuItems(menu.appId)} onClose={() => setMenu(null)} />}
    </div>
  );
}
