import { APPS } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { useAppDrag } from '../state/appDrag';
import { useDesktopIcons } from '../state/desktopIcons';
import { useDock } from '../state/dock';

/** The icon that follows the pointer while an app is being dragged. */
export function DragGhost() {
  const drag = useAppDrag((s) => s.drag);
  const over = useAppDrag((s) => s.over);
  const pinned = useDock((s) => (drag ? s.pins.includes(drag.appId) : false));
  const onDesktop = useDesktopIcons((s) => (drag ? s.icons.some((i) => i.appId === drag.appId) : false));
  if (!drag || !APPS[drag.appId]) return null;

  let hint = '';
  if (over?.kind === 'dock' && !(drag.source === 'dock' && pinned)) hint = pinned ? 'Move in Dock' : 'Add to Dock';
  else if (over?.kind === 'trash' && drag.source === 'desktop') hint = 'Remove from Desktop';
  else if (over?.kind === 'trash' && drag.source === 'dock') hint = 'Remove from Dock';
  else if (over?.kind === 'desktop' && drag.source !== 'desktop') hint = onDesktop ? 'Move on Desktop' : 'Add to Desktop';

  return (
    <div className="drag-ghost" style={{ left: drag.x - 28, top: drag.y - 28 }} aria-hidden>
      <AppIcon app={APPS[drag.appId]} size={56} />
      {hint && <span className="drag-hint">{hint}</span>}
    </div>
  );
}
