import { APPS } from '../apps/meta';
import type { AppFolder } from '../state/folders';
import { AppIcon } from './AppTile';

/** A folder's icon: a frosted square showing the first few apps inside, like iOS. */
export function FolderIcon({ folder, size = 56 }: { folder: AppFolder; size?: number }) {
  const apps = folder.apps.filter((id) => APPS[id]).slice(0, 9);
  const per = apps.length > 4 ? 3 : 2;
  const pad = size * 0.12;
  const gap = size * 0.06;
  const mini = (size - pad * 2 - gap * (per - 1)) / per;
  return (
    <span className={`folder-icon ${mini < 16 ? 'tiny' : ''}`} style={{ width: size, height: size, borderRadius: size * 0.28, padding: pad, gap, gridTemplateColumns: `repeat(${per}, ${mini}px)` }}>
      {apps.map((id) => (
        <AppIcon key={id} app={APPS[id]} size={mini} />
      ))}
    </span>
  );
}
