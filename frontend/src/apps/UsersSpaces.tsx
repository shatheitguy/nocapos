import { useEffect, useState } from 'react';
import { PERM_LABEL, sharesApi, type Access, type Perm, type PersonSpaces } from '../api/shares';
import type { Person } from '../api/users';
import { toast } from '../state/toasts';

// Users & Groups → a person's access type, shared folders and apps.

export function PersonSpacesPanel({ person, onChanged }: { person: Person; onChanged: () => void }) {
  const [data, setData] = useState<PersonSpaces | null>(null);
  const [error, setError] = useState<string | null>(null);
  const load = async () => {
    const r = await sharesApi.person(person.username);
    if (r.ok) setData(r.data);
    else setError(r.error ?? 'Could not load');
  };
  useEffect(() => {
    void load();
  }, [person.username]); // eslint-disable-line react-hooks/exhaustive-deps

  const act = async (p: Promise<{ ok: boolean; error?: string }>, done: string) => {
    const r = await p;
    if (!r.ok) return toast('error', 'That didn’t work', r.error);
    toast('success', done);
    await load();
  };

  if (error) return <p className="error small">{error}</p>;
  if (!data) return <p className="muted small">Loading…</p>;
  const admin = data.admin;
  return (
    <div className="person-spaces">
      <div className="ps-line">
        <span>
          Access
          <span className="muted small"> · {admin ? 'administrators always have the desktop' : data.access === 'files' ? 'connects from a computer or phone only' : 'signs in to the desktop'}</span>
        </span>
        <select
          className="select"
          aria-label={`Access for ${person.username}`}
          value={data.access}
          disabled={admin || person.you}
          onChange={(e) => {
            const a = e.target.value as Access;
            void act(sharesApi.setAccess(person.username, a), a === 'files' ? `${person.username} now uses file sharing only` : `${person.username} can sign in to the desktop`).then(onChanged);
          }}
        >
          <option value="desktop">Desktop</option>
          <option value="files">File sharing only</option>
        </select>
      </div>

      <h4>Shared folders</h4>
      {data.folders.length === 0 && <span className="muted small">No shared folders yet (Files → File Sharing).</span>}
      {data.folders.map((f) => (
        <div key={f.id} className="ps-line">
          <span>
            {f.name}
            {f.read_only && <span className="muted small"> · read only for everyone</span>}
          </span>
          <select
            className="select"
            aria-label={`${person.username} in ${f.name}`}
            value={f.perm}
            disabled={admin}
            title={admin ? 'Administrators can always reach every shared folder' : undefined}
            onChange={(e) => void act(sharesApi.setFolderPerm(person.username, f.id, e.target.value as Perm), `${person.username}: ${PERM_LABEL[e.target.value as Perm]} in ${f.name}`)}
          >
            {(['rw', 'ro', 'none'] as Perm[]).map((p) => (
              <option key={p} value={p}>
                {PERM_LABEL[p]}
              </option>
            ))}
          </select>
        </div>
      ))}

      <h4>Apps</h4>
      {data.apps.length === 0 && <span className="muted small">No App Store apps are installed.</span>}
      {data.apps.map((a) => (
        <label key={a.id} className="ps-line">
          <span>
            {a.name}
            <span className="muted small"> · {a.everyone ? 'everyone' : 'selected people'}</span>
          </span>
          <input
            type="checkbox"
            role="switch"
            checked={a.allowed}
            disabled={admin}
            aria-label={`${person.username} can use ${a.name}`}
            onChange={(e) => void act(sharesApi.setAppAllowed(person.username, a.id, e.target.checked), e.target.checked ? `${person.username} can use ${a.name}` : `${person.username} can no longer see ${a.name}`)}
          />
        </label>
      ))}
    </div>
  );
}
