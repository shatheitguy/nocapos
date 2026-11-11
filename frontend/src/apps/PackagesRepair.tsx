import { tr, useT } from '../i18n';
import { api } from '../api/client';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';

// Shared by Settings → Software Update and the Packages app: the "your
// packages need a repair" card, the kernel update row, and the preview that
// lists what an update or repair would remove before anything changes.

export interface PkgHealth {
  broken: boolean;
  interrupted: boolean;
  packages?: string[];
  detail?: string;
}

interface PkgPreview {
  install: string[];
  upgrade: string[];
  remove: string[];
}

interface UpdateLike {
  name: string;
  held: boolean;
  kernel?: boolean;
  group?: string;
}

/** Splits updates into the kernel family (one "Kernel update" row) and the rest. */
export function splitKernel<T extends UpdateLike>(ups: T[]): { kernel: T[]; rest: T[] } {
  const kernel = ups.filter((u) => u.group === 'kernel' || u.kernel);
  return { kernel, rest: ups.filter((u) => !kernel.includes(u)) };
}

/**
 * Simulates the action on the server; when it would remove packages, lists
 * them and asks. Resolves to the removals to allow ([] = none), or null when
 * the person cancels. ask is the usual confirmation (null: none when
 * nothing is removed).
 */
export async function confirmChange(
  action: 'repair' | 'upgrade-all' | 'upgrade',
  names: string[],
  ask: { title: string; message: string; confirmLabel: string } | null,
): Promise<string[] | null> {
  const r = await api<{ supported: boolean; preview?: PkgPreview; error?: string }>('/api/v1/packages/preview', {
    method: 'POST',
    body: { action, names: names.length ? names : undefined },
  });
  const p = r.ok ? r.data.preview : undefined;
  const remove = p?.remove ?? [];
  if (remove.length > 0) {
    const ok = await confirmDialog({
      title: tr('pkg.removeTitle', { n: remove.length }),
      message: tr('pkg.removeMsg', { names: remove.join(', ') }),
      confirmLabel: tr('pkg.removeOk'),
      danger: true,
    });
    return ok ? remove : null;
  }
  if (!ask) return [];
  let message = ask.message;
  if (p && p.install.length > 0) message += ' ' + tr('pkg.newPkgs', { names: p.install.join(', ') });
  if (r.ok && r.data.error) message += ' ' + tr('pkg.previewFailed', { error: r.data.error });
  return (await confirmDialog({ ...ask, message })) ? [] : null;
}

/** The "Your packages need a repair" card. */
export function RepairCard({ health, busy, onRepair }: { health: PkgHealth; busy: boolean; onRepair: () => void }) {
  const t = useT();
  if (!health.broken) return null;
  const names = health.packages ?? [];
  return (
    <div className="pkg-repair" role="alert">
      <Icon name="alert" size={18} />
      <div className="pkg-repair-text">
        <b>{t('pkg.repairTitle')}</b>
        <span>{health.interrupted ? t('pkg.repairInterrupted') : t('pkg.repairDeps')}</span>
        {names.length > 0 && <small className="mono">{names.slice(0, 8).join(', ') + (names.length > 8 ? ` +${names.length - 8}` : '')}</small>}
        {health.detail && (
          <details>
            <summary>{t('pkg.repairDetail')}</summary>
            <pre className="set-logs">{health.detail}</pre>
          </details>
        )}
      </div>
      <button type="button" className="pill small" disabled={busy} onClick={onRepair}>
        <Icon name="restart" size={13} /> {t('pkg.repairBtn')}
      </button>
    </div>
  );
}

/** Asks (with the preview) before a repair; resolves to the allowed removals or null. */
export function confirmRepair(): Promise<string[] | null> {
  return confirmChange('repair', [], { title: tr('pkg.repairAskTitle'), message: tr('pkg.repairAskMsg'), confirmLabel: tr('pkg.repairBtn') });
}
