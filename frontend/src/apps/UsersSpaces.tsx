import { useEffect, useState } from 'react';
import { sharesApi, type Perm, type PersonSpaces } from '../api/shares';
import type { Person } from '../api/users';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { toast } from '../state/toasts';

// Users & Groups → a person's shared folders and apps. (Whether they sign
// in to the desktop or only use file sharing is their role: User or Guest.)

const PERMS: Perm[] = ['rw', 'ro', 'none'];

export function PersonSpacesPanel({ person }: { person: Person }) {
  const t = useT();
  const [data, setData] = useState<PersonSpaces | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = async () => {
    const r = await sharesApi.person(person.username);
    if (r.ok) setData(r.data);
    else setError(r.error ?? t('ug.sp.loadFailed'));
  };
  useEffect(() => {
    void load();
  }, [person.username, person.role, person.access]); // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (p: Promise<{ ok: boolean; error?: string }>, done: string) => {
    const r = await p;
    if (!r.ok) return toast('error', t('ug.toast.failed'), r.error);
    toast('success', done);
    await load();
  };

  if (error) return <p className="error small">{error}</p>;
  if (!data) {
    return (
      <div className="ug-list" aria-busy="true">
        <div className="ug-list-row"><span className="ug-skel line" /></div>
        <div className="ug-list-row"><span className="ug-skel line short" /></div>
      </div>
    );
  }
  const admin = data.admin;
  return (
    <div className="ug-spaces">
      <h4><Icon name="folder" size={13} /> {t('ug.sp.folders')}</h4>
      <div className="ug-list">
        {data.folders.length === 0 && <p className="ug-list-row muted small">{t('ug.sp.noFolders')}</p>}
        {data.folders.map((f) => (
          <div key={f.id} className="ug-list-row">
            <span className="ug-list-text">
              <b>{f.name}</b>
              {f.read_only && <span>{t('ug.sp.readOnlyAll')}</span>}
            </span>
            <select
              className="select"
              aria-label={t('ug.sp.permFor', { name: person.username, folder: f.name })}
              value={f.perm}
              disabled={admin}
              title={admin ? t('ug.sp.adminAll') : undefined}
              onChange={(e) => {
                const p = e.target.value as Perm;
                void act(sharesApi.setFolderPerm(person.username, f.id, p), t('ug.sp.permSet', { name: person.username, perm: t(`ug.perm.${p}`), folder: f.name }));
              }}
            >
              {PERMS.map((p) => (
                <option key={p} value={p}>
                  {t(`ug.perm.${p}`)}
                </option>
              ))}
            </select>
          </div>
        ))}
      </div>

      <h4><Icon name="store" size={13} /> {t('ug.sp.apps')}</h4>
      <div className="ug-list">
        {data.apps.length === 0 && <p className="ug-list-row muted small">{t('ug.sp.noApps')}</p>}
        {data.apps.map((a) => (
          <label key={a.id} className="ug-list-row">
            <span className="ug-list-text">
              <b>{a.name}</b>
              <span>{a.everyone ? t('ug.sp.everyone') : t('ug.sp.selected')}</span>
            </span>
            <input
              type="checkbox"
              role="switch"
              checked={a.allowed}
              disabled={admin}
              aria-label={t('ug.sp.canUse', { name: person.username, app: a.name })}
              onChange={(e) =>
                void act(
                  sharesApi.setAppAllowed(person.username, a.id, e.target.checked),
                  e.target.checked ? t('ug.sp.appOn', { name: person.username, app: a.name }) : t('ug.sp.appOff', { name: person.username, app: a.name }),
                )
              }
            />
          </label>
        ))}
      </div>
    </div>
  );
}
