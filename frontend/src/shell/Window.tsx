import { Component, useRef, useState, type ErrorInfo, type ReactNode, type PointerEvent as RPointerEvent } from 'react';
import { APPS, canMultiWindow, openApp } from '../apps/meta';
import { APP_COMPONENTS } from '../apps/components';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon } from '../components/Icon';
import { AppIcon } from '../components/AppTile';
import { usePrefs } from '../state/prefs';
import { effectiveRect, useWM, type Rect, type WinState } from '../state/windows';

const MIN_W = 360;
const MIN_H = 240;
const EDGE = 6; // px from screen edge that triggers snapping

type Preview = 'left' | 'right' | 'max' | null;
type Dir = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';
const DIRS: Dir[] = ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'];

export function Window({ win, compact }: { win: WinState; compact: boolean }) {
  const focused = useWM((s) => s.focused === win.id);
  const wm = useWM.getState();
  const [preview, setPreview] = useState<Preview>(null);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const dragRef = useRef<{ px: number; py: number; x: number; y: number; restored: boolean } | null>(null);
  const meta = APPS[win.appId];
  const Body = APP_COMPONENTS[win.appId];
  const r = effectiveRect(win);
  const tiled = win.maximized || win.snap !== null;

  // ---- move ----
  const onTitleDown = (e: RPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest('button')) return;
    wm.focus(win.id);
    if (compact) return;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    dragRef.current = { px: e.clientX, py: e.clientY, x: r.x, y: r.y, restored: !tiled };
  };

  const onTitleMove = (e: RPointerEvent<HTMLDivElement>) => {
    const d = dragRef.current;
    if (!d) return;
    const dx = e.clientX - d.px;
    const dy = e.clientY - d.py;
    if (!d.restored) {
      if (Math.hypot(dx, dy) < 6) return;
      // Pulling a maximized/snapped window restores it under the cursor.
      const ratio = (d.px - r.x) / r.w;
      d.x = e.clientX - win.w * ratio;
      d.y = 0;
      d.px = e.clientX;
      d.py = e.clientY;
      d.restored = true;
      wm.move(win.id, d.x, d.y);
      return;
    }
    wm.move(win.id, d.x + dx, d.y + dy);
    const vw = window.innerWidth;
    setPreview(e.clientX <= EDGE ? 'left' : e.clientX >= vw - EDGE ? 'right' : e.clientY <= EDGE ? 'max' : null);
  };

  const onTitleUp = () => {
    if (!dragRef.current) return;
    dragRef.current = null;
    if (preview === 'max') wm.toggleMaximize(win.id);
    else if (preview) wm.snapTo(win.id, preview);
    setPreview(null);
  };

  // ---- resize ----
  const onResizeDown = (dir: Dir) => (e: RPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    wm.focus(win.id);
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    const start: Rect = { ...r };
    const px = e.clientX;
    const py = e.clientY;
    const move = (ev: PointerEvent) => {
      const dx = ev.clientX - px;
      const dy = ev.clientY - py;
      let { x, y, w, h } = start;
      if (dir.includes('e')) w = Math.max(MIN_W, start.w + dx);
      if (dir.includes('s')) h = Math.max(MIN_H, start.h + dy);
      if (dir.includes('w')) {
        w = Math.max(MIN_W, start.w - dx);
        x = start.x + start.w - w;
      }
      if (dir.includes('n')) {
        h = Math.max(MIN_H, start.h - dy);
        y = start.y + start.h - h;
      }
      wm.resize(win.id, { x, y, w, h });
    };
    const up = () => {
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      el.removeEventListener('pointercancel', up);
    };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
  };

  if (!meta || !Body) return null;

  const titleMenu = (): MenuItem[] => [
    ...(canMultiWindow(win.appId)
      ? [{ label: 'New window', icon: 'plus' as const, onClick: () => openApp(win.appId, { newWindow: true }) }]
      : []),
    { label: 'Minimize', icon: 'minimize', onClick: () => wm.minimize(win.id) },
    ...(!compact
      ? [{ label: tiled ? 'Restore' : 'Maximize', icon: (tiled ? 'restore' : 'maximize') as 'restore' | 'maximize', onClick: () => wm.toggleMaximize(win.id) }]
      : []),
    { label: 'Close', icon: 'close', danger: true, onClick: () => wm.close(win.id) },
  ];

  return (
    <>
      {preview && <div className={`snap-preview snap-${preview}`} />}
      <section
        className={`window ${focused ? 'focused' : ''} ${tiled ? 'tiled' : ''} ${win.minimized ? 'minimized' : ''}`}
        style={{ left: r.x, top: r.y, width: r.w, height: r.h, zIndex: win.z }}
        onPointerDownCapture={() => !focused && wm.focus(win.id)}
        role="dialog"
        aria-label={win.title}
      >
        <div
          className="titlebar"
          onPointerDown={onTitleDown}
          onPointerMove={onTitleMove}
          onPointerUp={onTitleUp}
          onPointerCancel={onTitleUp}
          onDoubleClick={(e) => {
            if ((e.target as HTMLElement).closest('button')) return;
            const action = usePrefs.getState().titleDoubleClick;
            if (action === 'minimize') wm.minimize(win.id);
            else if (action === 'maximize' && !compact) wm.toggleMaximize(win.id);
          }}
          onContextMenu={(e) => {
            e.preventDefault();
            wm.focus(win.id);
            setMenu({ x: e.clientX, y: e.clientY });
          }}
        >
          <AppIcon app={meta} size={20} />
          <span className="title">{win.title}</span>
          <div className="win-buttons">
            <button type="button" aria-label="Minimize" onClick={() => wm.minimize(win.id)}>
              <Icon name="minimize" size={14} />
            </button>
            {!compact && (
              <button type="button" aria-label={tiled ? 'Restore' : 'Maximize'} onClick={() => wm.toggleMaximize(win.id)}>
                <Icon name={tiled ? 'restore' : 'maximize'} size={13} />
              </button>
            )}
            <button type="button" className="close" aria-label="Close" onClick={() => wm.close(win.id)}>
              <Icon name="close" size={14} />
            </button>
          </div>
        </div>
        <div className="window-body">
          <ErrorBoundary title={win.title}>
            <Body win={win} />
          </ErrorBoundary>
        </div>
        {!tiled &&
          !compact &&
          DIRS.map((d) => <div key={d} className={`resize resize-${d}`} onPointerDown={onResizeDown(d)} />)}
      </section>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={titleMenu()} onClose={() => setMenu(null)} />}
    </>
  );
}

class ErrorBoundary extends Component<{ title: string; children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`${this.props.title} crashed`, error, info.componentStack);
  }
  render() {
    if (this.state.error) {
      return (
        <div className="empty">
          <Icon name="alert" size={32} />
          <h3>{this.props.title} stopped working</h3>
          <p className="muted">{this.state.error.message}</p>
          <button type="button" onClick={() => this.setState({ error: null })}>
            Reload app
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
