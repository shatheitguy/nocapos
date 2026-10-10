import type { AppMeta } from '../apps/meta';
import { AppArt, hasArt } from './AppArt';
import { Icon } from './Icon';

type IconApp = Pick<AppMeta, 'icon' | 'tile'> & { id?: string; logo?: string; logo_mono?: boolean };

/**
 * An app's icon. Installed apps show their own icon as it is (single-colour
 * black logos turn white on dark backgrounds); NoCapOS apps show their
 * illustrated icon.
 */
export function AppIcon({ app, size = 44 }: { app: IconApp; size?: number }) {
  const radius = size * 0.234;
  if (app.logo) {
    return (
      <span className={`app-icon logo ${app.logo_mono ? 'mono' : ''}`} style={{ width: size, height: size, borderRadius: radius }}>
        <img src={app.logo} alt="" width={size} height={size} draggable={false} loading="lazy" />
      </span>
    );
  }
  if (hasArt(app.id)) {
    return (
      <span className="app-icon art" style={{ width: size, height: size, borderRadius: radius }}>
        <AppArt id={app.id} size={size} />
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
        background: `linear-gradient(160deg, ${app.tile[0]}, ${app.tile[1]})`,
      }}
    >
      <Icon name={app.icon} size={Math.round(size * 0.52)} />
    </span>
  );
}
