import type { AppMeta } from '../apps/meta';
import { Icon } from './Icon';

export function AppIcon({ app, size = 44 }: { app: Pick<AppMeta, 'icon' | 'tile'>; size?: number }) {
  return (
    <span
      className="app-icon"
      style={{
        width: size,
        height: size,
        borderRadius: size * 0.28,
        background: `linear-gradient(145deg, ${app.tile[0]}, ${app.tile[1]})`,
      }}
    >
      <Icon name={app.icon} size={size * 0.5} />
    </span>
  );
}
