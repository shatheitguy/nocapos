import { useMemo, useState, type CSSProperties, type DragEvent } from 'react';
import { groupsApi, usersApi, type PeopleGroup, type Person } from '../api/users';
import { Icon } from '../components/Icon';
import { tr, useT } from '../i18n';
import { toast } from '../state/toasts';
import { PersonSessionsPanel } from './SessionsPanel';
import {
  Field, GROUP_COLORS, GroupMark, GroupToggles, PasswordInput, PersonAvatar, RoleBadge, RolePicker, Sheet, SheetSection, StrengthMeter,
  devicesText, displayName, roleOf, type Role,
} from './UsersParts';
import { PersonSpacesPanel } from './UsersSpaces';

// Settings → Users & Groups: the add / edit / password / sessions / group
// sheets. Changes in the edit sheet apply as you make them.

type Result = { ok: boolean; error?: string };
const OK: Result = { ok: true };

/** Moves a person to a role: Admin, User, or Guest (file sharing only). */
export async function applyRole(p: Person, to: Role): Promise<Result> {
  const from = roleOf(p);
  if (from === to) return OK;
  if (to === 'admin') return usersApi.setRole(p.username, 'admin');
  if (from === 'admin') {
    const r = await usersApi.setRole(p.username, 'user');
    if (!r.ok) return r;
    // Administrators always had the desktop, so they're a User now.
    return to === 'guest' ? usersApi.setAccess(p.username, 'files') : OK;
  }
  return usersApi.setAccess(p.username, to === 'guest' ? 'files' : 'desktop');
}

/** Puts username in or out of a group. */
export function setMembership(g: PeopleGroup, username: string, on: boolean) {
  const rest = g.members.filter((m) => m.toLowerCase() !== username.toLowerCase());
  return groupsApi.update(g.id, { name: g.name, description: g.description, color: g.color, members: on ? [...rest, username] : rest });
}

const nameRule = (linux: boolean) => (linux ? /^[a-z_][a-z0-9_-]{0,31}$/ : /^[a-zA-Z0-9][a-zA-Z0-9._-]{2,31}$/);

// ---------- add a person ----------

export function AddPersonSheet({ linux, people, groups, onClose, onAdded }: {
  linux: boolean; people: Person[]; groups: PeopleGroup[]; onClose: () => void; onAdded: (username: string) => void;
}) {
  const t = useT();
  const [username, setUsername] = useState('');
  const [fullName, setFullName] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [role, setRole] = useState<Role>('user');
  const [picked, setPicked] = useState<string[]>([]);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const taken = people.some((p) => p.username.toLowerCase() === username.trim().toLowerCase());
  const errors = {
    username: !username.trim()
      ? t('ug.v.nameNeeded')
      : !nameRule(linux).test(username)
        ? t(linux ? 'ug.v.nameLinux' : 'ug.v.nameApp')
        : taken
          ? t('ug.v.nameTaken')
          : null,
    pw: pw.length < 10 ? t('ug.v.pwShort') : null,
    pw2: pw2 !== pw ? t('ug.v.pwMatch') : null,
  };
  const show = (k: keyof typeof errors, typed: string) => (tried || typed ? errors[k] : null);

  const submit = async () => {
    setTried(true);
    setErr(null);
    if (errors.username || errors.pw || errors.pw2) return;
    setBusy(true);
    const r = await usersApi.create({
      username: username.trim(), full_name: fullName.trim() || undefined, password: pw,
      role: role === 'admin' ? 'admin' : 'user', access: role === 'guest' ? 'files' : 'desktop',
    });
    if (!r.ok) {
      setBusy(false);
      return setErr(r.error ?? t('ug.toast.addFailed'));
    }
    for (const g of groups.filter((g) => picked.includes(g.name))) {
      const gr = await setMembership(g, username.trim(), true);
      if (!gr.ok) toast('error', t('ug.toast.groupFailed', { group: g.name }), gr.error);
    }
    setBusy(false);
    toast('success', t('ug.toast.added', { name: username.trim() }), linux ? t('ug.toast.addedLinux') : undefined);
    onAdded(username.trim());
  };

  return (
    <Sheet
      title={t('ug.add.title')}
      sub={linux ? t('ug.add.subLinux') : t('ug.add.sub')}
      icon="plus"
      onClose={onClose}
      onSubmit={() => void submit()}
      footer={
        <>
          {err && <span className="ug-foot-error error small">{err}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('ug.cancel')}</button>
          <button type="submit" disabled={busy}>
            {busy ? <span className="spinner sm" /> : <Icon name="plus" size={14} />} {busy ? t('ug.add.busy') : t('ug.add.cta')}
          </button>
        </>
      }
    >
      <SheetSection title={t('ug.add.who')}>
        <div className="ug-fields">
          <Field label={t('ug.f.username')} hint={linux ? t('ug.f.usernameHintLinux') : t('ug.f.usernameHint')} error={show('username', username)}>
            <input value={username} autoFocus autoComplete="off" spellCheck={false} autoCapitalize="none" placeholder="alex"
              aria-invalid={!!show('username', username) || undefined} onChange={(e) => setUsername(linux ? e.target.value.toLowerCase() : e.target.value)} />
          </Field>
          {linux && (
            <Field label={t('ug.f.fullName')} hint={t('ug.f.optional')}>
              <input value={fullName} autoComplete="off" placeholder="Alex Smith" onChange={(e) => setFullName(e.target.value)} />
            </Field>
          )}
        </div>
      </SheetSection>
      <SheetSection title={t('ug.add.password')} hint={t('ug.add.passwordHint')}>
        <div className="ug-fields">
          <Field label={t('ug.f.password')} error={show('pw', pw)}>
            <PasswordInput value={pw} onChange={setPw} invalid={!!show('pw', pw)} />
          </Field>
          <Field label={t('ug.f.confirm')} error={show('pw2', pw2)}>
            <PasswordInput value={pw2} onChange={setPw2} invalid={!!show('pw2', pw2)} />
          </Field>
        </div>
        <StrengthMeter pw={pw} />
      </SheetSection>
      <SheetSection title={t('ug.add.role')}>
        <RolePicker value={role} onChange={setRole} linux={linux} />
      </SheetSection>
      {groups.length > 0 && (
        <SheetSection title={t('ug.add.groups')} hint={t('ug.add.groupsHint')}>
          <GroupToggles groups={groups} selected={picked} onToggle={(g, on) => setPicked(on ? [...picked, g.name] : picked.filter((n) => n !== g.name))} />
        </SheetSection>
      )}
    </Sheet>
  );
}

// ---------- edit a person ----------

export function EditPersonSheet({ person: p, linux, groups, onClose, onChanged, onPassword, onSessions, onDisable, onDelete }: {
  person: Person; linux: boolean; groups: PeopleGroup[]; onClose: () => void; onChanged: () => Promise<void>;
  onPassword: () => void; onSessions: () => void; onDisable: () => void; onDelete: () => void;
}) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const role = roleOf(p);
  const locked = !!p.root || p.you;

  const changeRole = async (to: Role) => {
    if (to === role) return;
    setBusy(true);
    const r = await applyRole(p, to);
    if (r.ok) toast('success', t(`ug.toast.role.${to}`, { name: p.username }));
    else toast('error', t('ug.toast.failed'), r.error);
    await onChanged();
    setBusy(false);
  };
  const toggleGroup = async (g: PeopleGroup, on: boolean) => {
    setBusy(true);
    const r = await setMembership(g, p.username, on);
    if (!r.ok) toast('error', t('ug.toast.groupFailed', { group: g.name }), r.error);
    await onChanged();
    setBusy(false);
  };

  return (
    <Sheet
      title={displayName(p)}
      sub={`@${p.username}${linux && p.uid >= 0 ? ` · uid ${p.uid}` : ''}`}
      lead={<PersonAvatar person={p} size={44} online={p.online} />}
      onClose={onClose}
      wide
      footer={<button type="button" onClick={onClose}>{t('ug.done')}</button>}
    >
      <div className="ug-edit-badges">
        <RoleBadge role={role} />
        {p.you && <span className="ug-badge you">{t('ug.badge.you')}</span>}
        {p.root && <span className="ug-badge root">root</span>}
        {p.disabled && <span className="ug-badge off">{t('ug.status.disabled')}</span>}
        <span className="ug-autosave"><Icon name="check" size={12} /> {t('ug.edit.autosave')}</span>
      </div>

      <SheetSection title={t('ug.add.role')} hint={locked ? (p.root ? t('ug.edit.rootLocked') : t('ug.edit.selfLocked')) : t('ug.edit.roleHint')}>
        <RolePicker value={role} onChange={(r) => void changeRole(r)} disabled={locked || busy} linux={linux} />
      </SheetSection>

      <SheetSection title={t('ug.edit.groups')}>
        <GroupToggles groups={groups} selected={p.groups ?? []} onToggle={(g, on) => void toggleGroup(g, on)} busy={busy} />
      </SheetSection>

      <SheetSection title={t('ug.edit.spaces')} hint={role === 'admin' ? t('ug.edit.spacesAdmin') : undefined}>
        <PersonSpacesPanel person={p} />
      </SheetSection>

      <SheetSection title={t('ug.edit.security')}>
        <div className="ug-list">
          <div className="ug-list-row">
            <Icon name="lock" size={16} />
            <span className="ug-list-text">
              <b>{t('ug.edit.twofa')}</b>
              <span>{p.totp ? t('ug.edit.twofaOn') : t('ug.edit.twofaOff')}</span>
            </span>
            <span className={`ug-pill ${p.totp ? 'good' : ''}`}>{p.totp ? t('ug.on') : t('ug.off')}</span>
          </div>
          <button type="button" className="ug-list-row action" onClick={onPassword}>
            <Icon name="key" size={16} />
            <span className="ug-list-text">
              <b>{t('ug.menu.password')}</b>
              <span>{p.you ? t('ug.edit.passwordSelf') : t('ug.edit.passwordHint')}</span>
            </span>
            <Icon name="chevronRight" size={15} />
          </button>
          <button type="button" className="ug-list-row action" onClick={onSessions}>
            <Icon name="shield" size={16} />
            <span className="ug-list-text">
              <b>{t('ses.manageTitle')}</b>
              <span>{devicesText(p.sessions ?? 0, t)}</span>
            </span>
            <Icon name="chevronRight" size={15} />
          </button>
        </div>
      </SheetSection>

      {!locked && (
        <SheetSection title={t('ug.edit.danger')}>
          <div className="ug-danger">
            <button type="button" className="ghost" onClick={onDisable}>
              <Icon name={p.disabled ? 'check' : 'lock'} size={14} /> {p.disabled ? t('ug.menu.enable') : t('ug.menu.disable')}
            </button>
            <button type="button" className="danger" onClick={onDelete}>
              <Icon name="trash" size={14} /> {t('ug.menu.delete')}
            </button>
          </div>
        </SheetSection>
      )}
    </Sheet>
  );
}

// ---------- reset password ----------

export function PasswordSheet({ person: p, onClose, onDone }: { person: Person; onClose: () => void; onDone: () => void }) {
  const t = useT();
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const e1 = pw.length < 10 ? t('ug.v.pwShort') : null;
  const e2 = pw2 !== pw ? t('ug.v.pwMatch') : null;
  const submit = async () => {
    setTried(true);
    setErr(null);
    if (e1 || e2) return;
    setBusy(true);
    const r = await usersApi.setPassword(p.username, pw);
    setBusy(false);
    if (!r.ok) return setErr(r.error ?? t('ug.toast.failed'));
    toast('success', tr('sp.passwordSet', { name: p.username }), p.you ? undefined : t('ug.pw.signedOut'));
    onDone();
  };
  return (
    <Sheet
      title={t('ug.menu.password')}
      sub={t('ug.pw.for', { name: displayName(p) })}
      icon="key"
      onClose={onClose}
      onSubmit={() => void submit()}
      footer={
        <>
          {err && <span className="ug-foot-error error small">{err}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('ug.cancel')}</button>
          <button type="submit" disabled={busy}>{busy ? <span className="spinner sm" /> : <Icon name="check" size={14} />} {t('ug.pw.save')}</button>
        </>
      }
    >
      {!p.you && <p className="ug-note"><Icon name="info" size={15} /> {t('ug.pw.note')}</p>}
      <div className="ug-fields">
        <Field label={t('ug.f.newPassword')} error={tried || pw ? e1 : null}>
          <PasswordInput value={pw} onChange={setPw} autoFocus invalid={!!(tried && e1)} />
        </Field>
        <Field label={t('ug.f.confirm')} error={tried || pw2 ? e2 : null}>
          <PasswordInput value={pw2} onChange={setPw2} invalid={!!(tried && e2)} />
        </Field>
      </div>
      <StrengthMeter pw={pw} />
    </Sheet>
  );
}

// ---------- sessions ----------

export function SessionsSheet({ person: p, onClose }: { person: Person; onClose: () => void }) {
  const t = useT();
  return (
    <Sheet title={t('ses.manageTitle')} sub={t('ses.manageFor', { name: displayName(p) })} icon="shield" onClose={onClose} wide>
      <PersonSessionsPanel username={p.username} you={p.you} />
    </Sheet>
  );
}

// ---------- one person's groups ----------

export function PersonGroupsSheet({ person: p, groups, onClose, onChanged, onNewGroup }: {
  person: Person; groups: PeopleGroup[]; onClose: () => void; onChanged: () => Promise<void>; onNewGroup: () => void;
}) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  return (
    <Sheet title={t('ug.edit.groups')} sub={t('ug.pg.sub', { name: displayName(p) })} icon="users" onClose={onClose}
      footer={
        <>
          <button type="button" className="ghost" onClick={onNewGroup}><Icon name="plus" size={14} /> {t('ug.g.new')}</button>
          <button type="button" onClick={onClose}>{t('ug.done')}</button>
        </>
      }
    >
      <GroupToggles
        groups={groups}
        selected={p.groups ?? []}
        busy={busy}
        onToggle={async (g, on) => {
          setBusy(true);
          const r = await setMembership(g, p.username, on);
          if (!r.ok) toast('error', t('ug.toast.groupFailed', { group: g.name }), r.error);
          await onChanged();
          setBusy(false);
        }}
      />
    </Sheet>
  );
}

// ---------- create / edit a group ----------

export function GroupSheet({ group, groups, people, onClose, onSaved, onDelete }: {
  group: PeopleGroup | null; groups: PeopleGroup[]; people: Person[]; onClose: () => void; onSaved: () => void; onDelete?: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(group?.name ?? '');
  const [description, setDescription] = useState(group?.description ?? '');
  const [color, setColor] = useState(group?.color ?? '');
  const [members, setMembers] = useState<string[]>(group?.members ?? []);
  const [q, setQ] = useState('');
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [dragOver, setDragOver] = useState<'in' | 'out' | null>(null);

  const trimmed = name.trim();
  const nameErr = !trimmed
    ? t('ug.v.groupName')
    : [...trimmed].length > 40
      ? t('ug.v.groupLong')
      : groups.some((g) => g.id !== group?.id && g.name.toLowerCase() === trimmed.toLowerCase())
        ? t('ug.v.groupTaken')
        : null;

  const inSet = useMemo(() => new Set(members.map((m) => m.toLowerCase())), [members]);
  const match = (p: Person) => !q.trim() || `${p.username} ${p.full_name ?? ''}`.toLowerCase().includes(q.trim().toLowerCase());
  const inside = people.filter((p) => inSet.has(p.username.toLowerCase()));
  const outside = people.filter((p) => !inSet.has(p.username.toLowerCase()) && match(p));
  const move = (username: string, on: boolean) =>
    setMembers(on ? (inSet.has(username.toLowerCase()) ? members : [...members, username]) : members.filter((m) => m.toLowerCase() !== username.toLowerCase()));

  const drop = (on: boolean) => (e: DragEvent) => {
    e.preventDefault();
    setDragOver(null);
    const u = e.dataTransfer.getData('text/x-nocap-user');
    if (u) move(u, on);
  };
  const over = (which: 'in' | 'out') => (e: DragEvent) => {
    if (!e.dataTransfer.types.includes('text/x-nocap-user')) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (dragOver !== which) setDragOver(which);
  };

  const submit = async () => {
    setTried(true);
    setErr(null);
    if (nameErr) return;
    setBusy(true);
    const body = { name: trimmed, description: description.trim(), color, members };
    const r = group ? await groupsApi.update(group.id, body) : await groupsApi.create(body);
    setBusy(false);
    if (!r.ok) return setErr(r.error ?? t('ug.toast.failed'));
    toast('success', group ? t('ug.toast.groupSaved', { group: trimmed }) : t('ug.toast.groupMade', { group: trimmed }));
    onSaved();
  };

  const personChip = (p: Person, on: boolean) => (
    <button
      key={p.username}
      type="button"
      className="ug-pick"
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData('text/x-nocap-user', p.username);
        e.dataTransfer.effectAllowed = 'move';
      }}
      onClick={() => move(p.username, !on)}
      aria-label={on ? t('ug.g.removeMember', { name: displayName(p) }) : t('ug.g.addMember', { name: displayName(p) })}
    >
      <PersonAvatar person={p} size={28} />
      <span className="ug-pick-text">
        <b>{displayName(p)}</b>
        <span>@{p.username}</span>
      </span>
      <Icon name={on ? 'close' : 'plus'} size={14} />
    </button>
  );

  return (
    <Sheet
      title={group ? t('ug.g.editTitle') : t('ug.g.newTitle')}
      sub={t('ug.g.sub')}
      lead={<GroupMark group={{ name: trimmed || '?', color }} size={40} />}
      onClose={onClose}
      onSubmit={() => void submit()}
      wide
      footer={
        <>
          {group && onDelete && (
            <button type="button" className="ghost danger ug-foot-left" onClick={onDelete}>
              <Icon name="trash" size={14} /> {t('ug.g.delete')}
            </button>
          )}
          {err && <span className="ug-foot-error error small">{err}</span>}
          <button type="button" className="ghost" onClick={onClose}>{t('ug.cancel')}</button>
          <button type="submit" disabled={busy}>{busy ? <span className="spinner sm" /> : <Icon name="check" size={14} />} {group ? t('ug.g.save') : t('ug.g.create')}</button>
        </>
      }
    >
      <SheetSection title={t('ug.g.details')}>
        <div className="ug-fields">
          <Field label={t('ug.g.name')} error={tried || name ? nameErr : null}>
            <input value={name} autoFocus maxLength={60} placeholder={t('ug.g.namePh')} aria-invalid={!!(tried && nameErr) || undefined} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label={t('ug.g.desc')} hint={t('ug.f.optional')}>
            <input value={description} maxLength={200} placeholder={t('ug.g.descPh')} onChange={(e) => setDescription(e.target.value)} />
          </Field>
        </div>
        <div className="ug-colors" role="radiogroup" aria-label={t('ug.g.color')}>
          {GROUP_COLORS.map((c) => (
            <button key={c || 'accent'} type="button" role="radio" aria-checked={color === c} className={`ug-color ${color === c ? 'on' : ''}`}
              style={c ? ({ '--ug-color': c } as CSSProperties) : undefined} aria-label={c || t('ug.g.accent')} title={c || t('ug.g.accent')} onClick={() => setColor(c)} />
          ))}
        </div>
      </SheetSection>

      <SheetSection title={t('ug.g.members', { n: members.length })} hint={t('ug.g.membersHint')}>
        <div className="ug-pickers">
          <div className={`ug-pickcol ${dragOver === 'in' ? 'over' : ''}`} onDragOver={over('in')} onDragLeave={() => setDragOver(null)} onDrop={drop(true)}>
            <h4>{t('ug.g.inGroup')}</h4>
            {inside.length ? inside.map((p) => personChip(p, true)) : <p className="ug-pick-empty">{t('ug.g.dropHere')}</p>}
          </div>
          <div className={`ug-pickcol ${dragOver === 'out' ? 'over' : ''}`} onDragOver={over('out')} onDragLeave={() => setDragOver(null)} onDrop={drop(false)}>
            <h4>{t('ug.g.everyoneElse')}</h4>
            <span className="ug-search small">
              <Icon name="search" size={14} />
              <input type="search" value={q} placeholder={t('ug.search')} aria-label={t('ug.search')} onChange={(e) => setQ(e.target.value)} />
            </span>
            {outside.length ? outside.map((p) => personChip(p, false)) : <p className="ug-pick-empty">{q ? t('ug.empty.noMatch') : t('ug.g.allIn')}</p>}
          </div>
        </div>
      </SheetSection>
    </Sheet>
  );
}
