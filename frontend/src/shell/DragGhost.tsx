import { APPS } from '../apps/meta';
import { AppIcon } from '../components/AppTile';
import { FolderIcon } from '../components/FolderIcon';
import { dropHint, useAppDrag } from '../state/appDrag';
import { folderIdOf, isFolderKey, useFolders } from '../state/folders';

/** The icon that follows the pointer while an app (or folder) is being dragged. */
export function DragGhost() {
  const drag = useAppDrag((s) => s.drag);
  const over = useAppDrag((s) => s.over);
  const folder = useFolders((s) => (drag && isFolderKey(drag.appId) ? s.folders.find((f) => f.id === folderIdOf(drag.appId)) : undefined));
  if (!drag || !(folder || APPS[drag.appId])) return null;
  const hint = dropHint(drag, over);

  return (
    <div className="drag-ghost" style={{ left: drag.x - 28, top: drag.y - 28 }} aria-hidden>
      {folder ? <FolderIcon folder={folder} size={56} /> : <AppIcon app={APPS[drag.appId]} size={56} />}
      {hint && <span className="drag-hint">{hint}</span>}
    </div>
  );
}
