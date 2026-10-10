import type { AppMeta } from '../apps/meta';
import { appArt } from './AppArt';
import { Icon } from './Icon';

type IconApp = Pick<AppMeta, 'icon' | 'tile'> & { id?: string; logo?: string };

/**
 * An app's icon. Installed apps show their maker's logo on a light tile;
 * NoCapOS apps show their own artwork on a coloured tile with a soft top light.
 */
export function AppIcon({ app, size = 44 }: { app: IconApp; size?: number }) {
  const radius = size * 0.27;
  if (app.logo) {
    return (
      <span className="app-icon logo" style={{ width: size, height: size, borderRadius: radius }}>
        <img src={app.logo} alt="" width={Math.round(size * 0.68)} height={Math.round(size * 0.68)} draggable={false} loading="lazy" />
      </span>
    );
  }
  return (
    <span
      className="app-icon"
      style={{
        width: size,
        height: size,
        borderRadius: radius,
        background: `radial-gradient(120% 90% at 30% 0%, rgba(255, 255, 255, 0.3), transparent 55%), linear-gradient(160deg, ${app.tile[0]} 0%, ${app.tile[1]} 100%)`,
      }}
    >
      {appArt(app.id, Math.round(size * 0.62)) ?? <Icon name={app.icon} size={Math.round(size * 0.52)} />}
    </span>
  );
}
