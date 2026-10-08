import { useRef, type PointerEvent as RPointerEvent } from 'react';
import { TransfersWidget } from './Transfers';
import { Icon } from '../components/Icon';
import { WIDGETS, useWidgets, widgetDef, type WidgetInstance, type WidgetType } from '../state/widgets';
import { AcceleratorWidget, ClockWidget, NetworkWidget, SystemWidget } from './Widgets';
import { AnalogClock, CalendarWidget, ContainersWidget, NotesWidget, StorageWidget, UptimeWidget } from './WidgetExtras';
import { ContainerGridWidget, ScriptLauncherWidget } from './OpsWidgets';
import { getUser } from '../api/client';
import { CoreHeatmapWidget, MemoryRingsWidget, NetWaveWidget, StorageGaugesWidget } from './HoloWidgets';

export function WidgetLayer({ username, compact }: { username: string; compact: boolean }) {
  const all = useWidgets((s) => s.widgets);
  const isAdmin = getUser()?.role === 'admin';
  const widgets = isAdmin ? all : all.filter((w) => !widgetDef(w.type).adminOnly);
  const editing = useWidgets((s) => s.editing);

  // On phones, widgets stack in a simple column (no free positioning).
  if (compact) {
    return (
      <div className="widget-stack">
        {widgets.map((w) => (
          <div key={w.type} className="widget-static">
            <WidgetBody type={w.type} username={username} />
          </div>
        ))}
      </div>
    );
  }

  return (
    <>
      {editing && <EditToolbar />}
      <div className={`widget-layer ${editing ? 'editing' : ''}`}>
        {widgets.map((w) => (
          <WidgetFrame key={w.type} w={w} username={username} editing={editing} />
        ))}
      </div>
    </>
  );
}

function WidgetFrame({ w, username, editing }: { w: WidgetInstance; username: string; editing: boolean }) {
  const wm = useWidgets.getState();
  const drag = useRef<{ px: number; py: number; x: number; y: number } | null>(null);

  const onMoveDown = (e: RPointerEvent<HTMLDivElement>) => {
    if (!editing || e.button !== 0 || (e.target as HTMLElement).closest('.widget-remove, .widget-resize')) return;
    e.preventDefault();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    drag.current = { px: e.clientX, py: e.clientY, x: w.x, y: w.y };
  };
  const onMoveMove = (e: RPointerEvent<HTMLDivElement>) => {
    if (!drag.current) return;
    wm.move(w.type, drag.current.x + (e.clientX - drag.current.px), drag.current.y + (e.clientY - drag.current.py));
  };
  const onMoveUp = () => {
    drag.current = null;
  };

  const onResizeDown = (e: RPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    const start = { px: e.clientX, py: e.clientY, w: w.w, h: w.h };
    const move = (ev: PointerEvent) => wm.resize(w.type, start.w + (ev.clientX - start.px), start.h + (ev.clientY - start.py));
    const up = () => {
      el.removeEventListener('pointermove', move);
      el.removeEventListener('pointerup', up);
      el.removeEventListener('pointercancel', up);
    };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
  };

  return (
    <div
      className={`widget-frame ${editing ? 'editing' : ''}`}
      style={{ left: w.x, top: w.y, width: w.w, height: w.h }}
      onPointerDown={onMoveDown}
      onPointerMove={onMoveMove}
      onPointerUp={onMoveUp}
      onPointerCancel={onMoveUp}
    >
      {editing && (
        <>
          <div className="widget-grip">
            <Icon name="menu" size={14} /> {widgetDef(w.type).name}
          </div>
          <button type="button" className="widget-remove" title="Remove" onClick={() => wm.remove(w.type)}>
            <Icon name="close" size={13} />
          </button>
        </>
      )}
      <div className="widget-content">
        <WidgetBody type={w.type} username={username} />
      </div>
      {editing && <div className="widget-resize" onPointerDown={onResizeDown} />}
    </div>
  );
}

function WidgetBody({ type, username }: { type: WidgetType; username: string }) {
  switch (type) {
    case 'clock':
      return <ClockWidget username={username} />;
    case 'analog':
      return <AnalogClock />;
    case 'calendar':
      return <CalendarWidget />;
    case 'notes':
      return <NotesWidget />;
    case 'system':
      return <SystemWidget />;
    case 'storage':
      return <StorageWidget />;
    case 'network':
      return <NetworkWidget />;
    case 'uptime':
      return <UptimeWidget />;
    case 'containers':
      return <ContainersWidget />;
    case 'accelerators':
      return <AcceleratorWidget />;
    case 'cores':
      return <CoreHeatmapWidget />;
    case 'memrings':
      return <MemoryRingsWidget />;
    case 'netwave':
      return <NetWaveWidget />;
    case 'gauges':
      return <StorageGaugesWidget />;
    case 'containerGrid':
      return <ContainerGridWidget />;
    case 'scripts':
      return <ScriptLauncherWidget />;
    case 'transfers':
      return <TransfersWidget />;
  }
}

function EditToolbar() {
  const widgets = useWidgets((s) => s.widgets);
  const wm = useWidgets.getState();
  const present = new Set(widgets.map((w) => w.type));
  const isAdmin = getUser()?.role === 'admin';
  const addable = WIDGETS.filter((d) => !present.has(d.type) && (isAdmin || !d.adminOnly));

  return (
    <div className="widget-edit-toolbar glass">
      <b>Editing widgets</b>
      <span className="muted small">Drag to move, drag the corner to resize.</span>
      <span className="spacer" />
      {addable.length > 0 && (
        <div className="widget-add">
          <Icon name="plus" size={14} />
          {addable.map((d) => (
            <button key={d.type} type="button" className="chip-btn" onClick={() => wm.add(d.type)}>
              {d.name}
            </button>
          ))}
        </div>
      )}
      <button type="button" className="ghost small" onClick={() => wm.resetLayout()}>
        Reset
      </button>
      <button type="button" className="small" onClick={() => wm.setEditing(false)}>
        Done
      </button>
    </div>
  );
}
