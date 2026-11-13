import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { getAccessToken } from '../api/client';
import { usersApi, type PeopleGroup, type Person } from '../api/users';
import { Icon, type IconName } from '../components/Icon';
import { WinClose } from '../components/WinClose';
import { useT, type T } from '../i18n';
import { useAvatar } from '../lib/avatar';

// Shared pieces of Settings → Users & Groups: avatars, badges, the sheet
// (a centered card on wide windows, a bottom sheet on phones) and helpers.

export type Role = 'admin' | 'user' | 'guest';

/** Admin, User, or Guest (a standard account that only uses file sharing). */
export const roleOf = (p: Pick<Person, 'role' | 'access'>): Role => (p.role === 'admin' ? 'admin' : p.access === 'files' ? 'guest' : 'user');

export const displayName = (p: Person) => p.full_name || p.username;

/** One or two letters for an avatar: "Alex Smith" → AS, "mia.k" → MK, "alex" → A. */
export function initials(name: string): string {
  const parts = name.trim().split(/[\s._-]+/).filter(Boolean);
  if (parts.length === 0) return '?';
  const two = parts.length > 1 ? parts[0][0] + parts[1][0] : parts[0][0];
  return two.toUpperCase();
}

/** A stable hue per name, so every initials avatar gets its own shade. */
export function hueOf(name: string): number {
  let h = 0;
  for (const c of name.toLowerCase()) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

// ---------- photos ----------

const photos = new Map<string, Promise<string | null>>();

/** Forget cached photos (after the list reloads, a photo may have changed). */
export function forgetPhotos() {
  for (const p of photos.values()) void p.then((u) => u && URL.revokeObjectURL(u));
  photos.clear();
}

function fetchPhoto(username: string): Promise<string | null> {
  let p = photos.get(username);
  if (!p) {
    p = fetch(usersApi.avatarPath(username), { headers: { Authorization: `Bearer ${getAccessToken()}` }, credentials: 'same-origin' })
      .then((r) => (r.ok ? r.blob() : null))
      .then((b) => (b ? URL.createObjectURL(b) : null))
      .catch(() => null);
    photos.set(username, p);
  }
  return p;
}

/** A person's profile photo URL, or null to show their initials. */
export function usePersonPhoto(p: Pick<Person, 'username' | 'avatar' | 'you'> | undefined): string | null {
  const mine = useAvatar((s) => s.url);
  const [url, setUrl] = useState<string | null>(null);
  const name = p?.username ?? '';
  const has = !!p?.avatar && !p?.you;
  useEffect(() => {
    if (!has) {
      setUrl(null);
      return;
    }
    let live = true;
    void fetchPhoto(name).then((u) => live && setUrl(u));
    return () => {
      live = false;
    };
  }, [name, has]);
  return p?.you ? mine : url;
}

export function PersonAvatar({ person, size = 44, online, ring }: { person: Person; size?: number; online?: boolean; ring?: boolean }) {
  const url = usePersonPhoto(person);
  const style = { '--ug-size': `${size}px`, '--ug-hue': hueOf(person.username) } as CSSProperties;
  return (
    <span className={`ug-avatar ${url ? 'photo' : ''} ${ring ? 'ring' : ''}`} style={style} aria-hidden="true">
      {url ? <img src={url} alt="" draggable={false} /> : initials(displayName(person))}
      {online && <span className="ug-online" />}
    </span>
  );
}

/** Up to `max` overlapping avatars, then "+N". */
export function AvatarStack({ people, max = 5, size = 28 }: { people: Person[]; max?: number; size?: number }) {
  const shown = people.slice(0, max);
  const more = people.length - shown.length;
  return (
    <span className="ug-stack">
      {shown.map((p) => (
        <PersonAvatar key={p.username} person={p} size={size} ring />
      ))}
      {more > 0 && (
        <span className="ug-avatar ug-more-count ring" style={{ '--ug-size': `${size}px` } as CSSProperties}>
          +{more}
        </span>
      )}
    </span>
  );
}

export function RoleBadge({ role }: { role: Role }) {
  const t = useT();
  const icon: IconName = role === 'admin' ? 'shield' : role === 'guest' ? 'folder' : 'user';
  return (
    <span className={`ug-badge role-${role}`} title={t(`ug.role.${role}.hint`)}>
      <Icon name={icon} size={11} /> {t(`ug.role.${role}`)}
    </span>
  );
}

/** The group's color tile with its first letter. */
export function GroupMark({ group, size = 40 }: { group: Pick<PeopleGroup, 'name' | 'color'>; size?: number }) {
  const style = { '--ug-size': `${size}px`, ...(group.color ? { '--ug-color': group.color } : {}) } as CSSProperties;
  return (
    <span className="ug-gmark" style={style} aria-hidden="true">
      {initials(group.name || '?')}
    </span>
  );
}

export const GROUP_COLORS = ['', '#ff9f0a', '#30d158', '#0a84ff', '#5e5ce6', '#bf5af2', '#ff375f', '#64d2ff'];

/** "Online now", "Seen 5 min ago", "Never signed in" or "Disabled". */
export function statusText(p: Person, t: T, now = Date.now()): { text: string; tone: 'on' | 'off' | 'idle' } {
  if (p.disabled) return { text: t('ug.status.disabled'), tone: 'off' };
  if (p.online) return { text: t('ug.status.online'), tone: 'on' };
  if (!p.last_seen) return { text: t('ug.status.never'), tone: 'idle' };
  const s = Math.max(0, (now - new Date(p.last_seen).getTime()) / 1000);
  const ago =
    s < 90
      ? t('ses.ago.now')
      : s < 3600
        ? t('ses.ago.min', { n: Math.round(s / 60) })
        : s < 86400
          ? t('ses.ago.hour', { n: Math.floor(s / 3600) })
          : s < 86400 * 60
            ? t('ses.ago.day', { n: Math.floor(s / 86400) })
            : new Date(p.last_seen).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
  return { text: t('ug.status.seen', { ago }), tone: 'idle' };
}

// ---------- sheet ----------

/** The Settings window the page sits in, so sheets cover just that window. */
function useHost(anchor: React.RefObject<HTMLElement | null>) {
  const [host, setHost] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => {
    setHost((anchor.current?.closest('.window') as HTMLElement | null) ?? document.body);
  }, [anchor]);
  return host;
}

/**
 * A form sheet: a centered card in the window, or a bottom sheet when the
 * window is phone-narrow. Escape or a press outside closes it.
 */
export function Sheet({ title, sub, icon, lead, onClose, onSubmit, footer, children, wide }: {
  title: string;
  sub?: string;
  icon?: IconName;
  lead?: ReactNode;
  onClose: () => void;
  onSubmit?: () => void;
  footer?: ReactNode;
  children: ReactNode;
  wide?: boolean;
}) {
  const t = useT();
  const anchor = useRef<HTMLSpanElement>(null);
  const host = useHost(anchor);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || document.querySelector('.confirm-backdrop')) return;
      e.stopPropagation();
      onClose();
    };
    // Capture: text fields and the window manager handle keys on the way up.
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose]);

  const body = (
    <>
      <header className="ug-sheet-head">
        <span className="ug-grabber" aria-hidden="true" />
        {lead ?? (icon && <span className="ug-sheet-icon"><Icon name={icon} size={18} /></span>)}
        <span className="ug-sheet-titles">
          <h2>{title}</h2>
          {sub && <span>{sub}</span>}
        </span>
        <WinClose onClick={onClose} label={t('common.close')} />
      </header>
      <div className="ug-sheet-body">{children}</div>
      {footer && <footer className="ug-sheet-foot">{footer}</footer>}
    </>
  );
  const submit = (e: FormEvent) => {
    e.preventDefault();
    onSubmit?.();
  };
  const sheet = (
    <div className={`ug-backdrop ${host === document.body ? 'fixed' : ''}`} onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="ug-sheet-wrap" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
        {onSubmit ? (
          <form className={`ug-sheet ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title} onSubmit={submit} noValidate>
            {body}
          </form>
        ) : (
          <div className={`ug-sheet ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
            {body}
          </div>
        )}
      </div>
    </div>
  );
  return (
    <>
      <span ref={anchor} hidden />
      {host && createPortal(sheet, host)}
    </>
  );
}

/** A titled block inside a sheet. */
export function SheetSection({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="ug-sec">
      <h3>{title}</h3>
      {hint && <p className="ug-sec-hint">{hint}</p>}
      {children}
    </section>
  );
}

/** A labelled field with its validation message. */
export function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string | null; children: ReactNode }) {
  return (
    <label className={`ug-field ${error ? 'invalid' : ''}`}>
      <span className="ug-field-label">{label}</span>
      {children}
      {error ? <span className="ug-field-error" role="alert">{error}</span> : hint && <span className="ug-field-hint">{hint}</span>}
    </label>
  );
}

/** Admin / User / Guest as three big pickable cards. */
export function RolePicker({ value, onChange, disabled, linux }: { value: Role; onChange: (r: Role) => void; disabled?: boolean; linux: boolean }) {
  const t = useT();
  const roles: { id: Role; icon: IconName }[] = [
    { id: 'user', icon: 'user' },
    { id: 'admin', icon: 'shield' },
    { id: 'guest', icon: 'folder' },
  ];
  return (
    <div className="ug-roles" role="radiogroup">
      {roles.map((r) => (
        <button
          key={r.id}
          type="button"
          role="radio"
          aria-checked={value === r.id}
          className={`ug-role ${value === r.id ? 'on' : ''}`}
          disabled={disabled}
          onClick={() => onChange(r.id)}
        >
          <Icon name={r.icon} size={18} />
          <b>{t(`ug.role.${r.id}`)}</b>
          <span>{t(r.id === 'admin' && linux ? 'ug.role.admin.hintLinux' : `ug.role.${r.id}.hint`)}</span>
        </button>
      ))}
    </div>
  );
}

/** Group chips you can tap on and off. */
export function GroupToggles({ groups, selected, onToggle, busy }: { groups: PeopleGroup[]; selected: string[]; onToggle: (g: PeopleGroup, on: boolean) => void; busy?: boolean }) {
  const t = useT();
  if (!groups.length) return <p className="ug-sec-hint">{t('ug.groups.noneYet')}</p>;
  const has = new Set(selected.map((s) => s.toLowerCase()));
  return (
    <div className="ug-gtoggles">
      {groups.map((g) => {
        const on = has.has(g.name.toLowerCase());
        return (
          <button key={g.id} type="button" className={`ug-gtoggle ${on ? 'on' : ''}`} aria-pressed={on} disabled={busy} onClick={() => onToggle(g, !on)}
            style={g.color ? ({ '--ug-color': g.color } as CSSProperties) : undefined}>
            <span className="ug-gdot" />
            {g.name}
            {on && <Icon name="check" size={12} />}
          </button>
        );
      })}
    </div>
  );
}

/** Password strength 0–4, for the meter under new passwords. */
export function strength(pw: string): number {
  if (!pw) return 0;
  let s = pw.length >= 10 ? 1 : 0;
  if (pw.length >= 14) s++;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) s++;
  if (/\d/.test(pw) && /[^A-Za-z0-9]/.test(pw)) s++;
  return Math.min(4, s);
}

export function StrengthMeter({ pw }: { pw: string }) {
  const t = useT();
  const s = strength(pw);
  if (!pw) return null;
  return (
    <span className={`ug-strength s${s}`}>
      <span className="ug-strength-bar">
        {[1, 2, 3, 4].map((i) => (
          <i key={i} className={i <= s ? 'on' : ''} />
        ))}
      </span>
      {t(`ug.pw.strength${s}`)}
    </span>
  );
}

/** A password input with a show/hide eye. */
export function PasswordInput({ value, onChange, autoFocus, placeholder, invalid }: { value: string; onChange: (v: string) => void; autoFocus?: boolean; placeholder?: string; invalid?: boolean }) {
  const t = useT();
  const [show, setShow] = useState(false);
  return (
    <span className="ug-pw">
      <input type={show ? 'text' : 'password'} autoComplete="new-password" spellCheck={false} autoFocus={autoFocus} placeholder={placeholder}
        aria-invalid={invalid || undefined} value={value} onChange={(e) => onChange(e.target.value)} />
      <button type="button" className="ug-pw-eye" aria-label={show ? t('ug.pw.hide') : t('ug.pw.show')} title={show ? t('ug.pw.hide') : t('ug.pw.show')} onClick={() => setShow(!show)}>
        <Icon name="eye" size={15} />
      </button>
    </span>
  );
}

/** "Not signed in anywhere", "On 1 device", "On 3 devices". */
export const devicesText = (n: number, t: T) => (n === 0 ? t('ug.devices.none') : n === 1 ? t('ug.devices.one') : t('ug.devices.many', { n }));
