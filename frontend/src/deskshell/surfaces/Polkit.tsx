// The admin password prompt (full-screen overlay) for polkit.request: what
// is being asked, which account authenticates, the password, Cancel /
// Authenticate. A wrong password shakes the field (polkit.done ok=false).
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Icon } from '../../components/Icon';
import { send } from '../bridge';
import { useEvent, useShellInfo } from '../hooks';
import { useT } from '../i18n';
import type { PolkitRequest } from '../protocol';
import { Avatar, LogoMark } from '../ui';

export function Polkit() {
  useShellInfo();
  const t = useT();
  const [req, setReq] = useState<PolkitRequest | null>(null);
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [wrong, setWrong] = useState(0);
  const field = useRef<HTMLInputElement>(null);

  useEvent('polkit.request', (p) => {
    const r = p as PolkitRequest;
    if (!r?.cookie) return;
    setReq(r);
    setPassword('');
    setBusy(false);
    setWrong(0);
  });
  useEvent('polkit.done', (p) => {
    const d = (p ?? {}) as { cookie?: string; ok?: boolean };
    if (!req || d.cookie !== req.cookie) return;
    if (d.ok) {
      setReq(null);
      return;
    }
    setWrong((n) => n + 1);
    setPassword('');
    setBusy(false);
  });

  useEffect(() => {
    if (req && !busy) field.current?.focus();
  }, [req, busy, wrong]);

  if (!req) return <div className="nsh-polkit-surface" />;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!password || busy) return;
    setBusy(true);
    send('polkit.respond', { cookie: req.cookie, password });
  };
  const cancel = () => {
    send('polkit.cancel', { cookie: req.cookie });
    setReq(null);
  };

  return (
    <div className="nsh-polkit-surface" onKeyDown={(e) => e.key === 'Escape' && cancel()}>
      <form className="nsh-dialog" onSubmit={submit} aria-labelledby="nsh-polkit-title">
        <div className="nsh-dialog-icon">
          <LogoMark size={44} />
          <span className="nsh-dialog-lock">
            <Icon name="shield" size={14} />
          </span>
        </div>
        <h3 id="nsh-polkit-title">{t('nsh.polkit.title')}</h3>
        <p className="nsh-dialog-msg">{req.message || t('nsh.polkit.generic')}</p>
        <div className="nsh-dialog-user">
          <Avatar name={req.user || '?'} size={28} />
          <span>{t('nsh.polkit.as', { user: req.user || '?' })}</span>
        </div>
        <div key={wrong} className={`nsh-pill ${wrong ? 'shake' : ''}`}>
          <Icon name="key" size={15} />
          <input
            ref={field}
            type="password"
            value={password}
            autoFocus
            disabled={busy}
            placeholder={t('nsh.password')}
            aria-label={t('nsh.password')}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <p className={`nsh-hint ${wrong ? 'err' : ''}`} role="status">
          {busy ? t('nsh.polkit.checking') : wrong ? t('nsh.wrongPassword') : ''}
        </p>
        <div className="confirm-actions">
          <button type="button" className="ghost" onClick={cancel}>
            {t('nsh.cancel')}
          </button>
          <button type="submit" className="nsh-primary" disabled={!password || busy}>
            {t('nsh.polkit.ok')}
          </button>
        </div>
      </form>
    </div>
  );
}
