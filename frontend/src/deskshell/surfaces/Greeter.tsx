// The NoCap sign-in screen (nocap-shell --greeter): a big clock, the accounts
// from greeter.users as avatar tiles, then whatever the login conversation
// asks (password, text, messages). Restart and Shut down in the corner.
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Icon } from '../../components/Icon';
import { call, send } from '../bridge';
import { fmtLongDate, fmtTime, useMinuteClock, useShellInfo } from '../hooks';
import { useT } from '../i18n';
import type { GreeterReply, GreeterUser } from '../protocol';
import { Avatar, LogoMark, Wordmark } from '../ui';

type Prompt = { kind: 'secret' | 'visible'; message: string };
type Note = { text: string; error: boolean } | null;

const nameOf = (u: GreeterUser) => u.realName || u.name;

function PowerMenu() {
  const t = useT();
  const [ask, setAsk] = useState<'reboot' | 'poweroff' | null>(null);
  if (ask) {
    return (
      <div className="nsh-greet-power ask" role="alertdialog" aria-label={t(ask === 'reboot' ? 'nsh.power.restartAsk' : 'nsh.power.offAsk')}>
        <b>{t(ask === 'reboot' ? 'nsh.power.restartAsk' : 'nsh.power.offAsk')}</b>
        <button type="button" className="ghost small" autoFocus onClick={() => setAsk(null)}>
          {t('nsh.cancel')}
        </button>
        <button type="button" className="small danger-solid" onClick={() => send('power.action', { action: ask })}>
          {t(ask === 'reboot' ? 'nsh.power.restartOk' : 'nsh.power.offOk')}
        </button>
      </div>
    );
  }
  return (
    <div className="nsh-greet-power">
      <button type="button" className="nsh-greet-action" onClick={() => setAsk('reboot')}>
        <span>
          <Icon name="restart" size={18} />
        </span>
        {t('nsh.power.restart')}
      </button>
      <button type="button" className="nsh-greet-action" onClick={() => setAsk('poweroff')}>
        <span>
          <Icon name="power" size={18} />
        </span>
        {t('nsh.power.off')}
      </button>
    </div>
  );
}

export function Greeter() {
  useShellInfo();
  const t = useT();
  const now = useMinuteClock();
  const [users, setUsers] = useState<GreeterUser[] | null>(null);
  const [user, setUser] = useState<GreeterUser | null>(null);
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [answer, setAnswer] = useState('');
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<Note>(null);
  const [shake, setShake] = useState(0);
  const [done, setDone] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  const firstTile = useRef<HTMLButtonElement>(null);
  const retries = useRef(0);

  useEffect(() => {
    call<GreeterUser[]>('greeter.users')
      .then((list) => {
        const l = Array.isArray(list) ? list : [];
        setUsers(l);
        if (l.length === 1) void start(l[0]);
      })
      .catch(() => setUsers([]));
  }, []);

  useEffect(() => {
    if (prompt && !busy) field.current?.focus();
    else if (!user && users?.length) firstTile.current?.focus();
  }, [prompt, busy, user, users, shake]);

  const fail = (text: string) => {
    setBusy(false);
    setPrompt(null);
    setUser(null);
    setNote({ text, error: true });
  };

  /** Follow the login conversation until it needs the person (a prompt) or is over. */
  const follow = async (r: GreeterReply, who: GreeterUser): Promise<void> => {
    switch (r?.state) {
      case 'secret':
      case 'visible':
        setPrompt({ kind: r.state, message: r.message });
        setAnswer('');
        setBusy(false);
        return;
      case 'info':
      case 'error':
        setNote({ text: r.message, error: r.state === 'error' });
        return follow(await call<GreeterReply>('greeter.respond', {}), who);
      case 'failed':
        // Wrong answer: say so and start over for the same account.
        if (++retries.current > 5) return fail(r.message || t('nsh.greet.failed'));
        setNote({ text: t('nsh.wrongPassword'), error: true });
        setShake((n) => n + 1);
        return follow(await call<GreeterReply>('greeter.login', { username: who.name }), who);
      case 'done':
        setDone(true);
        setBusy(false);
        return;
      default:
        return fail(t('nsh.greet.failed'));
    }
  };

  const start = async (u: GreeterUser) => {
    setUser(u);
    setPrompt(null);
    setNote(null);
    setBusy(true);
    retries.current = 0;
    try {
      await follow(await call<GreeterReply>('greeter.login', { username: u.name }), u);
    } catch (e) {
      fail(e instanceof Error ? e.message : t('nsh.greet.failed'));
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!user || busy) return;
    setBusy(true);
    setNote(null);
    try {
      await follow(await call<GreeterReply>('greeter.respond', { response: answer }), user);
    } catch (err) {
      fail(err instanceof Error ? err.message : t('nsh.greet.failed'));
    } finally {
      setAnswer('');
    }
  };

  const back = () => {
    send('greeter.cancel');
    setUser(null);
    setPrompt(null);
    setNote(null);
    setBusy(false);
  };

  return (
    <div
      className={`nsh-greeter ${done ? 'done' : ''}`}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && user && !done && (users?.length ?? 0) > 1) back();
      }}
    >
      <div className="nsh-greet-clock">
        <div className="nsh-greet-date">{fmtLongDate(now)}</div>
        <div className="nsh-greet-time">{fmtTime(now)}</div>
      </div>

      <div className="nsh-greet-main">
        {done ? (
          <div className="nsh-greet-card">
            <div className="brand-mark busy" style={{ width: 84, height: 84 }}>
              <span className="brand-mark-glow" />
              <LogoMark size={84} />
            </div>
            <div className="nsh-greet-name">{t('nsh.greet.signingIn')}</div>
            <div className="brand-bar indeterminate nsh-greet-bar">
              <div className="brand-bar-fill" />
            </div>
          </div>
        ) : !user ? (
          <div className="nsh-greet-card wide">
            <LogoMark size={64} />
            <Wordmark />
            <p className="nsh-greet-sub">{users === null ? t('nsh.loading') : users.length ? t('nsh.greet.pick') : t('nsh.greet.noUsers')}</p>
            <div
              className="nsh-greet-users"
              role="listbox"
              aria-label={t('nsh.greet.pick')}
              onKeyDown={(e) => {
                if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
                const tiles = [...e.currentTarget.querySelectorAll<HTMLButtonElement>('button')];
                const i = tiles.indexOf(document.activeElement as HTMLButtonElement);
                tiles[(i + (e.key === 'ArrowRight' ? 1 : tiles.length - 1)) % tiles.length]?.focus();
                e.preventDefault();
              }}
            >
              {users?.map((u, i) => (
                <button key={u.name} ref={i === 0 ? firstTile : undefined} type="button" role="option" aria-selected={false} className="nsh-greet-user" onClick={() => void start(u)}>
                  <Avatar name={nameOf(u)} size={76} />
                  <span className="ellipsis">{nameOf(u)}</span>
                </button>
              ))}
            </div>
            {note && <p className={`nsh-hint ${note.error ? 'err' : ''}`}>{note.text}</p>}
          </div>
        ) : (
          <form className="nsh-greet-card" onSubmit={submit}>
            <Avatar name={nameOf(user)} size={92} />
            <div className="nsh-greet-name">{nameOf(user)}</div>
            {prompt ? (
              <div key={shake} className={`nsh-pill wide ${shake ? 'shake' : ''}`}>
                <input
                  ref={field}
                  type={prompt.kind === 'secret' ? 'password' : 'text'}
                  value={answer}
                  disabled={busy}
                  autoComplete="off"
                  placeholder={prompt.message.replace(/:\s*$/, '') || t('nsh.password')}
                  aria-label={prompt.message || t('nsh.password')}
                  onChange={(e) => setAnswer(e.target.value)}
                />
                <button type="submit" className="nsh-go" disabled={busy} aria-label={t('nsh.greet.go')} title={t('nsh.greet.go')}>
                  <Icon name="arrowDown" size={16} className="nsh-go-arrow" />
                </button>
              </div>
            ) : (
              <div className="nsh-pill wide waiting">
                <span className="nsh-dots" aria-hidden="true">
                  <i />
                  <i />
                  <i />
                </span>
              </div>
            )}
            <p className={`nsh-hint ${note?.error ? 'err' : ''}`} role="status">
              {note?.text ?? ''}
            </p>
            {(users?.length ?? 0) > 1 && (
              <button type="button" className="nsh-link" onClick={back}>
                {t('nsh.greet.switch')}
              </button>
            )}
          </form>
        )}
      </div>

      {!done && <PowerMenu />}
    </div>
  );
}
