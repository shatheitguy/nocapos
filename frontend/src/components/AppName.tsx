import { appNameKey } from '../apps/meta';
import { useT } from '../i18n';

/** An app's name in the voice in use (follows a voice switch). */
export function AppName({ id }: { id: string }) {
  const t = useT();
  return <>{t(appNameKey(id))}</>;
}
