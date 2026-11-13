import { useCallback, useEffect, useMemo, useState, type CSSProperties, type MouseEvent } from 'react';
import { groupsApi, usersApi, type PeopleGroup, type PeopleList, type Person } from '../api/users';
import { ContextMenu, type MenuItem } from '../components/ContextMenu';
import { Icon } from '../components/Icon';
import { tr, useT } from '../i18n';
import { useClock } from '../lib/hooks';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import {
  AvatarStack, GroupMark, PersonAvatar, RoleBadge, devicesText, displayName, forgetPhotos, roleOf, statusText, type Role,
} from './UsersParts';
import { AddPersonSheet, EditPersonSheet, GroupSheet, PasswordSheet, PersonGroupsSheet, SessionsSheet } from './UsersSheets';

// Settings → Users & Groups (administrators): people as cards, groups as
// cards, a search and filter bar, and sheets for adding and editing.

type Tab = 'people' | 'groups';
type RoleFilter = 'all' | Role;
type Open =
  | { kind: 'add' }
  | { kind: 'edit' | 'password' | 'sessions' | 'groups'; username: string }
  | { kind: 'group'; id: number | null; back?: string }
  | null;

export function ManagePeople() {
  const t = useT();
  const [data, setData] = useState<PeopleList | null>(null);
  const [groups, setGroups] = useState<PeopleGroup[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('people');
  const [q, setQ] = useState('');
  const [roleF, setRoleF] = useState<RoleFilter>('all');
  const [groupF, setGroupF] = useState<string>('');
  const [open, setOpen] = useState<Open>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const now = useClock(30_000).getTime();

  const load = useCallback(async () => {
    const [u, g] = await Promise.all([usersApi.list(), groupsApi.list()]);
    if (u.ok) {
      setData(u.data);
      setError(null);
    } else setError(u.error ?? tr('ug.loadFailed'));
    if (g.ok) setGroups(g.data.groups);
    else setGroups((old) => old ?? []);
  }, []);
  useEffect(() => {
    forgetPhotos();
    void load();
  }, [load]);

  const linux = data?.mode === 'system';
  const people = useMemo(() => data?.users ?? [], [data]);
  const byName = useMemo(() => new Map(people.map((p) => [p.username.toLowerCase(), p])), [people]);
  const find = (name: string) => byName.get(name.toLowerCase());

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return people
      .filter((p) => roleF === 'all' || roleOf(p) === roleF)
      .filter((p) => !groupF || (p.groups ?? []).some((g) => g.toLowerCase() === groupF.toLowerCase()))
      .filter((p) => !needle || `${p.username} ${p.full_name ?? ''} ${(p.groups ?? []).join(' ')}`.toLowerCase().includes(needle))
      .sort((a, b) => Number(b.you) - Number(a.you) || Number(!!b.online) - Number(!!a.online) || displayName(a).localeCompare(displayName(b)));
  }, [people, q, roleF, groupF]);
  const shownGroups = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (groups ?? []).filter((g) => !needle || `${g.name} ${g.description} ${g.members.join(' ')}`.toLowerCase().includes(needle));
  }, [groups, q]);

  const counts = useMemo(() => {
    const c = { admin: 0, user: 0, guest: 0, online: 0 };
    for (const p of people) {
      c[roleOf(p)]++;
      if (p.online) c.online++;
    }
    return c;
  }, [people]);

  // ---------- actions ----------

  const toggleDisabled = async (p: Person) => {
    if (!p.disabled) {
      const ok = await confirmDialog({
        title: tr('ug.confirm.disableTitle', { name: p.username }),
        message: tr('ug.confirm.disableBody'),
        confirmLabel: tr('ug.menu.disable'),
        danger: true,
      });
      if (!ok) return;
    }
    const r = await usersApi.setDisabled(p.username, !p.disabled);
    if (!r.ok) return toast('error', tr('ug.toast.failed'), r.error);
    toast('success', p.disabled ? tr('ug.toast.enabled', { name: p.username }) : tr('ug.toast.disabled', { name: p.username }));
    await load();
  };

  const remove = async (p: Person) => {
    const ok = await confirmDialog({
      title: tr('ug.confirm.deleteTitle', { name: p.username }),
      message: linux ? tr('ug.confirm.deleteLinux', { name: p.username }) : tr('ug.confirm.deleteApp', { name: p.username }),
      confirmLabel: tr('ug.menu.delete'),
      danger: true,
    });
    if (!ok) return;
    let removeHome = false;
    if (linux && p.home) {
      removeHome = await confirmDialog({
        title: tr('ug.confirm.homeTitle', { home: p.home }),
        message: tr('ug.confirm.homeBody', { home: p.home }),
        confirmLabel: tr('ug.confirm.homeYes'),
        cancelLabel: tr('ug.confirm.homeKeep'),
        danger: true,
      });
    }
    const r = await usersApi.remove(p.username, removeHome);
    if (!r.ok) return toast('error', tr('ug.toast.failed'), r.error);
    toast('success', tr('ug.toast.deleted', { name: p.username }));
    setOpen(null);
    await load();
  };

  const removeGroup = async (g: PeopleGroup) => {
    const ok = await confirmDialog({
      title: tr('ug.confirm.groupTitle', { group: g.name }),
      message: tr('ug.confirm.groupBody'),
      confirmLabel: tr('ug.g.delete'),
      danger: true,
    });
    if (!ok) return;
    const r = await groupsApi.remove(g.id);
    if (!r.ok) return toast('error', tr('ug.toast.failed'), r.error);
    toast('success', tr('ug.toast.groupDeleted', { group: g.name }));
    setOpen(null);
    await load();
  };

  const personMenu = (p: Person, e: MouseEvent) => {
    e.stopPropagation();
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const locked = !!p.root || p.you;
    setMenu({
      x: r.right - 210,
      y: r.bottom + 4,
      items: [
        { label: t('ug.menu.edit'), icon: 'edit', onClick: () => setOpen({ kind: 'edit', username: p.username }) },
        { label: t('ug.menu.password'), icon: 'key', onClick: () => setOpen({ kind: 'password', username: p.username }) },
        { label: t('ses.manageTitle'), icon: 'shield', onClick: () => setOpen({ kind: 'sessions', username: p.username }) },
        { label: t('ug.menu.groups'), icon: 'users', onClick: () => setOpen({ kind: 'groups', username: p.username }) },
        { label: p.disabled ? t('ug.menu.enable') : t('ug.menu.disable'), icon: p.disabled ? 'check' : 'lock', disabled: locked, divider: true, onClick: () => void toggleDisabled(p) },
        { label: t('ug.menu.delete'), icon: 'trash', danger: true, disabled: locked, onClick: () => void remove(p) },
      ],
    });
  };

  const groupMenu = (g: PeopleGroup, e: MouseEvent) => {
    e.stopPropagation();
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setMenu({
      x: r.right - 200,
      y: r.bottom + 4,
      items: [
        { label: t('ug.g.edit'), icon: 'edit', onClick: () => setOpen({ kind: 'group', id: g.id }) },
        { label: t('ug.g.delete'), icon: 'trash', danger: true, divider: true, onClick: () => void removeGroup(g) },
      ],
    });
  };

  // ---------- render ----------

  if (error && !data) {
    return (
      <div className="ug-page">
        <div className="ug-empty">
          <span className="ug-empty-icon bad"><Icon name="alert" size={26} /></span>
          <b>{t('ug.loadFailedTitle')}</b>
          <p>{error}</p>
          <button type="button" onClick={() => void load()}><Icon name="restart" size={14} /> {t('ug.retry')}</button>
        </div>
      </div>
    );
  }

  const filtering = !!q.trim() || roleF !== 'all' || !!groupF;
  const openPerson = open && open.kind !== 'add' && open.kind !== 'group' ? find(open.username) : undefined;
  const openGroup = open?.kind === 'group' && open.id != null ? (groups ?? []).find((g) => g.id === open.id) : undefined;

  return (
    <div className="ug-page">
      <header className="ug-hero">
        <div className="ug-hero-text">
          <span className={`ug-mode ${linux ? 'linux' : ''}`} title={data ? (linux ? t('ug.mode.linuxHint') : t('ug.mode.appHint')) : undefined}>
            <Icon name={linux ? 'terminal' : 'users'} size={13} /> {data ? (linux ? t('ug.mode.linux') : t('ug.mode.app')) : '…'}
          </span>
          <div className="ug-stats-row">
            <span><b>{data ? people.length : '–'}</b> {t('ug.stat.people')}</span>
            <span><i className="ug-dot on" /><b>{data ? counts.online : '–'}</b> {t('ug.stat.online')}</span>
            <span><b>{data ? counts.admin : '–'}</b> {t('ug.stat.admins')}</span>
            <span><b>{groups ? groups.length : '–'}</b> {t('ug.stat.groups')}</span>
          </div>
        </div>
        <button type="button" className="ug-add" disabled={!data} onClick={() => setOpen(tab === 'people' ? { kind: 'add' } : { kind: 'group', id: null })}>
          <Icon name="plus" size={15} /> {tab === 'people' ? t('ug.addPerson') : t('ug.g.new')}
        </button>
      </header>

      <div className="ug-toolbar">
        <div className="ug-tabs" role="tablist">
          {(['people', 'groups'] as Tab[]).map((id) => (
            <button key={id} type="button" role="tab" aria-selected={tab === id} className={tab === id ? 'on' : ''} onClick={() => setTab(id)}>
              <Icon name={id === 'people' ? 'user' : 'users'} size={14} />
              {t(`ug.tab.${id}`)}
              <span className="ug-count">{id === 'people' ? (data ? people.length : '') : groups ? groups.length : ''}</span>
            </button>
          ))}
        </div>
        <label className="ug-search">
          <Icon name="search" size={15} />
          <input type="search" value={q} placeholder={tab === 'people' ? t('ug.searchPeople') : t('ug.searchGroups')} aria-label={t('ug.search')} onChange={(e) => setQ(e.target.value)} />
        </label>
        {tab === 'people' && (
          <div className="ug-filters">
            <select className="select ug-filter" aria-label={t('ug.filter.role')} value={roleF} onChange={(e) => setRoleF(e.target.value as RoleFilter)}>
              <option value="all">{t('ug.filter.allRoles')}</option>
              <option value="admin">{t('ug.role.admin')} ({counts.admin})</option>
              <option value="user">{t('ug.role.user')} ({counts.user})</option>
              <option value="guest">{t('ug.role.guest')} ({counts.guest})</option>
            </select>
            <select className="select ug-filter" aria-label={t('ug.filter.group')} value={groupF} disabled={!groups?.length} onChange={(e) => setGroupF(e.target.value)}>
              <option value="">{t('ug.filter.allGroups')}</option>
              {(groups ?? []).map((g) => (
                <option key={g.id} value={g.name}>{g.name} ({g.members.length})</option>
              ))}
            </select>
          </div>
        )}
      </div>

      {tab === 'people' ? (
        !data ? (
          <div className="ug-grid" aria-busy="true" aria-label={t('ug.loading')}>
            {[0, 1, 2, 3, 4, 5].map((i) => <SkeletonCard key={i} />)}
          </div>
        ) : filtered.length === 0 ? (
          <div className="ug-empty">
            <span className="ug-empty-icon"><Icon name="search" size={26} /></span>
            <b>{t('ug.empty.noMatch')}</b>
            <p>{t('ug.empty.noMatchBody')}</p>
            {filtering && (
              <button type="button" className="ghost" onClick={() => { setQ(''); setRoleF('all'); setGroupF(''); }}>
                {t('ug.empty.clear')}
              </button>
            )}
          </div>
        ) : (
          <div className="ug-grid">
            {filtered.map((p) => (
              <PersonCard key={p.username} p={p} linux={linux} now={now} groups={groups ?? []}
                onOpen={() => setOpen({ kind: 'edit', username: p.username })} onMenu={(e) => personMenu(p, e)} onGroup={(g) => { setGroupF(g); }} />
            ))}
            {!filtering && people.length < 2 && (
              <button type="button" className="ug-card ug-card-cta" onClick={() => setOpen({ kind: 'add' })}>
                <span className="ug-empty-icon"><Icon name="plus" size={22} /></span>
                <b>{t('ug.empty.alone')}</b>
                <span>{t('ug.empty.aloneBody')}</span>
              </button>
            )}
          </div>
        )
      ) : !groups ? (
        <div className="ug-grid" aria-busy="true">
          {[0, 1, 2].map((i) => <SkeletonCard key={i} />)}
        </div>
      ) : groups.length === 0 ? (
        <div className="ug-empty">
          <span className="ug-empty-icon"><Icon name="users" size={26} /></span>
          <b>{t('ug.empty.groups')}</b>
          <p>{t('ug.empty.groupsBody')}</p>
          <button type="button" onClick={() => setOpen({ kind: 'group', id: null })}><Icon name="plus" size={14} /> {t('ug.g.new')}</button>
        </div>
      ) : shownGroups.length === 0 ? (
        <div className="ug-empty">
          <span className="ug-empty-icon"><Icon name="search" size={26} /></span>
          <b>{t('ug.empty.noMatch')}</b>
          <button type="button" className="ghost" onClick={() => setQ('')}>{t('ug.empty.clear')}</button>
        </div>
      ) : (
        <div className="ug-grid groups">
          {shownGroups.map((g) => (
            <GroupCard key={g.id} g={g} members={g.members.map(find).filter((p): p is Person => !!p)}
              onOpen={() => setOpen({ kind: 'group', id: g.id })} onMenu={(e) => groupMenu(g, e)}
              onShow={() => { setGroupF(g.name); setQ(''); setRoleF('all'); setTab('people'); }} />
          ))}
        </div>
      )}

      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}

      {open?.kind === 'add' && data && (
        <AddPersonSheet linux={linux} people={people} groups={groups ?? []} onClose={() => setOpen(null)}
          onAdded={() => { setOpen(null); void load(); }} />
      )}
      {open?.kind === 'edit' && openPerson && (
        <EditPersonSheet person={openPerson} linux={linux} groups={groups ?? []} onClose={() => setOpen(null)} onChanged={load}
          onPassword={() => setOpen({ kind: 'password', username: openPerson.username })}
          onSessions={() => setOpen({ kind: 'sessions', username: openPerson.username })}
          onDisable={() => void toggleDisabled(openPerson)} onDelete={() => void remove(openPerson)} />
      )}
      {open?.kind === 'password' && openPerson && (
        <PasswordSheet person={openPerson} onClose={() => setOpen(null)} onDone={() => { setOpen(null); void load(); }} />
      )}
      {open?.kind === 'sessions' && openPerson && (
        <SessionsSheet person={openPerson} onClose={() => { setOpen(null); void load(); }} />
      )}
      {open?.kind === 'groups' && openPerson && (
        <PersonGroupsSheet person={openPerson} groups={groups ?? []} onClose={() => setOpen(null)} onChanged={load}
          onNewGroup={() => setOpen({ kind: 'group', id: null, back: openPerson.username })} />
      )}
      {open?.kind === 'group' && (open.id == null || openGroup) && (
        <GroupSheet
          group={openGroup ?? null}
          groups={groups ?? []}
          people={people}
          onClose={() => setOpen(open.back ? { kind: 'groups', username: open.back } : null)}
          onSaved={() => { setOpen(open.back ? { kind: 'groups', username: open.back } : null); void load(); }}
          onDelete={openGroup ? () => void removeGroup(openGroup) : undefined}
        />
      )}
    </div>
  );
}

function SkeletonCard() {
  return (
    <div className="ug-card skeleton" aria-hidden="true">
      <div className="ug-card-top">
        <span className="ug-skel circle" />
        <span className="ug-card-id">
          <span className="ug-skel line" />
          <span className="ug-skel line short" />
        </span>
      </div>
      <span className="ug-skel line" />
      <span className="ug-skel pill" />
    </div>
  );
}

function PersonCard({ p, linux, now, groups, onOpen, onMenu, onGroup }: {
  p: Person; linux: boolean; now: number; groups: PeopleGroup[]; onOpen: () => void; onMenu: (e: MouseEvent) => void; onGroup: (name: string) => void;
}) {
  const t = useT();
  const st = statusText(p, t, now);
  const colorOf = (name: string) => groups.find((g) => g.name.toLowerCase() === name.toLowerCase())?.color;
  return (
    <article
      className={`ug-card ${p.disabled ? 'off' : ''} ${p.you ? 'you' : ''}`}
      tabIndex={0}
      aria-label={displayName(p)}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
          e.preventDefault();
          onOpen();
        }
      }}
    >
      <div className="ug-card-top">
        <PersonAvatar person={p} size={48} online={p.online && !p.disabled} />
        <span className="ug-card-id">
          <b>{displayName(p)}</b>
          <span>@{p.username}</span>
        </span>
        <button type="button" className="ug-kebab" aria-haspopup="menu" aria-label={t('ug.menu.for', { name: p.username })} title={t('ug.menu.more')} onClick={onMenu}>
          <Icon name="moreDots" size={16} />
        </button>
      </div>

      <div className="ug-badges">
        <RoleBadge role={roleOf(p)} />
        {p.you && <span className="ug-badge you">{t('ug.badge.you')}</span>}
        {p.root && <span className="ug-badge root">root</span>}
      </div>

      <div className="ug-meta">
        <span className={`ug-status ${st.tone}`}><i className={`ug-dot ${st.tone}`} />{st.text}</span>
        <span className="ug-meta-row">
          <span title={t('ses.manageTitle')}><Icon name="phone" size={13} /> {devicesText(p.sessions ?? 0, t)}</span>
          <span className={p.totp ? 'good' : 'warn'} title={p.totp ? t('ug.edit.twofaOn') : t('ug.edit.twofaOff')}>
            <Icon name="lock" size={13} /> {p.totp ? t('ug.twofa.on') : t('ug.twofa.off')}
          </span>
        </span>
        {linux && p.uid >= 0 && <span className="ug-meta-row mono-ish">uid {p.uid} · {p.home}</span>}
      </div>

      <div className="ug-chips">
        {(p.groups ?? []).length ? (
          (p.groups ?? []).map((g) => {
            const c = colorOf(g);
            return (
              <button key={g} type="button" className="ug-chip" title={t('ug.filter.showGroup', { group: g })}
                style={c ? ({ '--ug-color': c } as CSSProperties) : undefined}
                onClick={(e) => { e.stopPropagation(); onGroup(g); }}>
                <span className="ug-gdot" />{g}
              </button>
            );
          })
        ) : (
          <span className="ug-chip none">{t('ug.noGroups')}</span>
        )}
      </div>
    </article>
  );
}

function GroupCard({ g, members, onOpen, onMenu, onShow }: { g: PeopleGroup; members: Person[]; onOpen: () => void; onMenu: (e: MouseEvent) => void; onShow: () => void }) {
  const t = useT();
  const online = members.filter((m) => m.online).length;
  return (
    <article
      className="ug-card group"
      tabIndex={0}
      aria-label={g.name}
      style={g.color ? ({ '--ug-color': g.color } as CSSProperties) : undefined}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
          e.preventDefault();
          onOpen();
        }
      }}
    >
      <div className="ug-card-top">
        <GroupMark group={g} size={44} />
        <span className="ug-card-id">
          <b>{g.name}</b>
          <span>{g.description || t('ug.g.noDesc')}</span>
        </span>
        <button type="button" className="ug-kebab" aria-haspopup="menu" aria-label={t('ug.menu.for', { name: g.name })} title={t('ug.menu.more')} onClick={onMenu}>
          <Icon name="moreDots" size={16} />
        </button>
      </div>
      <div className="ug-group-foot">
        {members.length ? <AvatarStack people={members} /> : <span className="ug-chip none">{t('ug.g.empty')}</span>}
        <span className="ug-group-count">
          <b>{t('ug.g.count', { n: members.length })}</b>
          {online > 0 && <span><i className="ug-dot on" />{t('ug.g.online', { n: online })}</span>}
        </span>
      </div>
      <button type="button" className="ug-link" onClick={(e) => { e.stopPropagation(); onShow(); }}>
        {t('ug.g.showPeople')} <Icon name="chevronRight" size={13} />
      </button>
    </article>
  );
}
