import { useEffect, useState } from 'react';
import type { StoreApp } from '../api/appstore';
import { sharesApi } from '../api/shares';
import { usersApi, type Person } from '../api/users';
import { toast } from '../state/toasts';

/** App Store → app page → "Who can use it": everyone, or chosen people. Administrators always can. */
export function WhoCanUse({ app, onChanged }: { app: StoreApp; onChanged: () => void }) {
  const [people, setPeople] = useState<Person[] | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void usersApi.list().then((r) => r.ok && setPeople(r.data.users.filter((p) => p.role !== 'admin')));
  }, []);
  const everyone = !app.access || app.access.everyone;
  const chosen = new Set((app.access?.users ?? []).map((u) => u.toLowerCase()));

  const save = async (next: { everyone: boolean; users: string[] }) => {
    setBusy(true);
    const r = await sharesApi.setAppAccess(app.id, next);
    setBusy(false);
    if (!r.ok) return toast('error', 'Could not change who can use it', r.error);
    toast('success', next.everyone ? `Everyone can use ${app.name}` : `${app.name} is limited to the people you chose`);
    onChanged();
  };

  return (
    <section>
      <h3 className="as-h3">Who can use it</h3>
      <div className="ucard ulist">
        <label className="urow">
          <span className="urow-text">
            <span className="urow-title">Everyone</span>
            <span className="urow-desc">Shows on everyone’s home screen, Launchpad and search. Administrators can always use every app.</span>
          </span>
          <span className="urow-control">
            <input
              type="checkbox"
              role="switch"
              checked={everyone}
              disabled={busy}
              onChange={(e) => void save(e.target.checked ? { everyone: true, users: [] } : { everyone: false, users: (people ?? []).map((p) => p.username) })}
            />
          </span>
        </label>
        {!everyone &&
          (people ?? []).map((p) => (
            <label key={p.username} className="urow">
              <span className="urow-text">
                <span className="urow-title">{p.full_name || p.username}</span>
                {p.access === 'files' && <span className="urow-desc">File sharing only: can’t sign in to the desktop</span>}
              </span>
              <span className="urow-control">
                <input
                  type="checkbox"
                  checked={chosen.has(p.username.toLowerCase())}
                  disabled={busy}
                  aria-label={`${p.username} can use ${app.name}`}
                  onChange={(e) => {
                    const users = new Set(chosen);
                    if (e.target.checked) users.add(p.username.toLowerCase());
                    else users.delete(p.username.toLowerCase());
                    void save({ everyone: false, users: (people ?? []).filter((x) => users.has(x.username.toLowerCase())).map((x) => x.username) });
                  }}
                />
              </span>
            </label>
          ))}
        {!everyone && people?.length === 0 && (
          <div className="urow">
            <span className="urow-desc">There are no standard users yet, so only administrators can use it.</span>
          </div>
        )}
      </div>
    </section>
  );
}
