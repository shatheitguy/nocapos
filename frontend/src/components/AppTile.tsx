import type { AppMeta } from '../apps/meta';
import { Icon } from './Icon';

/** An app's icon: a rounded tile with its colours, a soft top light and its symbol. */
export function AppIcon({ app, size = 44 }: { app: Pick<AppMeta, 'icon' | 'tile'>; size?: number }) {
  return (
    <span
      className="app-icon"
      style={{
        width: size,
        height: size,
        borderRadius: size * 0.27,
        background: `radial-gradient(120% 90% at 30% 0%, rgba(255, 255, 255, 0.28), transparent 55%), linear-gradient(160deg, ${app.tile[0]} 0%, ${app.tile[1]} 100%)`,
      }}
    >
      <Icon name={app.icon} size={Math.round(size * 0.52)} />
    </span>
  );
}
