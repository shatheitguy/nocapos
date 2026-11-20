import { useCallback, useEffect, useRef, useState } from 'react';
import { sshApi, type SshKey, type SshKeys, type SshStatus, type SshWho } from '../api/ssh';
import { shieldApi } from '../api/shield';
import { Icon } from '../components/Icon';
import { copyWithToast } from '../lib/settingsKit';
import { fmtDate, useTimePrefs } from '../lib/time';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { tr, useT } from '../i18n';
import { Choice, Row, Section, Toggle } from './Personalize';
import { KeepBanner } from './ShieldCommon';

// Settings → Security: remote login (SSH) for admins, and everyone's own
// SSH keys. Everything is a switch or a button; the "ssh you@address" lines
// are there to copy, not to run here.

const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms));

/** The remote login switch and its settings (admins). */
export function RemoteLogin() {
  const t = useT();
  const [st, setSt] = useState<SshStatus | null>(null);
  const [loadFail, setLoadFail] = useState(false);
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<{ output: string; error?: string } | null>(null);
  const logRef = useRef<HTMLPreElement>(null);

  const load = useCallback(async () => {
    const r = await sshApi.status();
    if (r.ok) {
      setSt(r.data);
      setLoadFail(false);
    } else setLoadFail(true);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [job]);

  const change = async (body: Parameters<typeof sshApi.change>[0], done?: string) => {
    setBusy(true);
    const r = await sshApi.change(body);
    if (!r.ok) {
      setBusy(false);
      toast('error', tr('ssh.failed'), r.error);
      void load();
      return;
    }
    if (r.data.job) {
      // Installing the SSH server first: show its progress.
      let j = r.data.job;
      setJob({ output: j.output });
      for (let i = 0; i < 1200 && j.running; i++) {
        await sleep(1000);
        const jr = await sshApi.job(j.id);
        if (!jr.ok) break;
        j = jr.data;
        setJob({ output: j.output, error: j.error });
      }
      setBusy(false);
      if (j.ok) {
        setJob(null);
        toast('success', tr('ssh.turnedOn'));
      } else toast('error', tr('ssh.failed'), j.error);
      void load();
      return;
    }
    setBusy(false);
    setSt(r.data);
    if (done) toast('success', done);
  };

  if (!st) {
    return (
      <Section title={t('ssh.title')} hint={t('ssh.hint')}>
        <Row label={loadFail ? t('set.loadFail') : t('sp.checking')}>
          {loadFail && <button type="button" className="ghost small" onClick={() => void load()}>{t('set.retry')}</button>}
        </Row>
      </Section>
    );
  }
  if (!st.supported) {
    return (
      <Section title={t('ssh.title')} hint={t('ssh.hint')}>
        <Row label={t('ssh.unavailable')} hint={st.reason} />
      </Section>
    );
  }

  const flip = async (on: boolean) => {
    if (!on && !(await confirmDialog({ title: t('ssh.offTitle'), message: t('ssh.offMsg'), confirmLabel: t('sp.turnOff'), danger: true }))) return;
    await change({ enabled: on }, on ? t('ssh.turnedOn') : t('ssh.turnedOff'));
  };
  const setOutside = async (on: boolean) => {
    if (on && !(await confirmDialog({ title: t('ssh.outsideTitle'), message: t('ssh.outsideMsg'), confirmLabel: t('ssh.outsideConfirm'), danger: true }))) return;
    await change({ outside: on, confirm: on });
  };
  const shieldOn = async () => {
    if (!(await confirmDialog({ title: t('sh.fw.onTitle'), message: t('ssh.fw.onMsg'), confirmLabel: t('sh.fw.turnOn') }))) return;
    setBusy(true);
    const r = await shieldApi.enable();
    setBusy(false);
    if (!r.ok) toast('error', tr('sh.fw.failed'), r.error);
    void load();
  };
  const fix = async () => {
    setBusy(true);
    const r = await sshApi.fixFirewall();
    setBusy(false);
    if (!r.ok) return toast('error', tr('ssh.failed'), r.error);
    setSt(r.data);
    toast('success', tr('ssh.fw.fixed'));
  };

  const cantInstall = !st.installed && !st.can_install;
  const status = !st.installed ? t('ssh.notInstalled') : !st.enabled ? t('sp.off') : st.running ? t('ssh.running', { port: st.port }) : t('ssh.stopped');
  const portFlag = st.port !== 22 ? ` -p ${st.port}` : '';
  const fw = st.firewall;

  return (
    <Section title={t('ssh.title')} hint={t('ssh.hint')}>
      <Toggle label={t('ssh.switch')} hint={cantInstall ? (st.reason ?? t('ssh.cantInstall')) : status}
        checked={st.enabled || (busy && !!job)} disabled={busy || cantInstall} onChange={(on) => void flip(on)} />
      {job && (
        <div className="settings-row full">
          <div className="ssh-job">
            <span className="settings-row-hint">{t('ssh.installing')}</span>
            <pre ref={logRef} className="ssh-log mono">{job.output}</pre>
            {job.error && <p className="error">{job.error}</p>}
          </div>
        </div>
      )}
      {st.enabled && st.error && (
        <Row label={t('ssh.notRunning')} hint={st.error}>
          <button type="button" className="ghost small" disabled={busy} onClick={() => void change({ enabled: true }, t('ssh.turnedOn'))}>{t('ssh.tryAgain')}</button>
        </Row>
      )}
      {st.enabled && !!st.overrides?.length && <Row label={t('ssh.overridden')} hint={t('ssh.overriddenHint', { names: st.overrides.join(', ') })} />}

      {st.enabled && (
        <>
          <KeepBanner pending={fw.pending} done={() => void load()} reload={load} />
          {!fw.installed ? (
            <Row label={t('ssh.fw.none')} hint={t('ssh.fw.noneHint')} />
          ) : !fw.active ? (
            <Row label={t('ssh.fw.off')} hint={t('ssh.fw.offHint')}>
              <button type="button" className="small" disabled={busy || !!fw.pending} onClick={() => void shieldOn()}>{t('sh.fw.turnOn')}</button>
            </Row>
          ) : fw.blocked ? (
            <Row label={t('ssh.fw.blocked')} hint={t('ssh.fw.blockedHint')}>
              <button type="button" className="small" disabled={busy || !!fw.pending} onClick={() => void fix()}>{t('ssh.fw.fix')}</button>
            </Row>
          ) : (
            <Row label={t('ssh.fw.ok')} hint={t(st.outside ? 'ssh.fw.okOutside' : 'ssh.fw.okLan')} />
          )}
          <Row label={t('ssh.connect')} hint={t('ssh.connectHint')} stacked>
            {st.addresses.length === 0 ? (
              <span className="muted small">{t('ssh.noAddress')}</span>
            ) : (
              <div className="ssh-addrs">
                {st.addresses.map((a) => {
                  const line = `ssh${portFlag} ${st.user}@${a}`;
                  return (
                    <span key={a} className="ssh-addr">
                      <code>{line}</code>
                      <button type="button" className="ghost small" aria-label={t('ssh.copy')} title={t('ssh.copy')} onClick={() => void copyWithToast(line, tr('ssh.copied'))}>
                        <Icon name="copy" size={13} />
                      </button>
                    </span>
                  );
                })}
              </div>
            )}
          </Row>
        </>
      )}

      {st.installed && (
        <>
          <Row label={t('ssh.who')} hint={t('ssh.whoHint')}>
            <Choice<SshWho> value={st.who} onChange={(who) => void change({ who })}
              options={[{ id: 'admins', label: t('ssh.who.admins') }, { id: 'everyone', label: t('ssh.who.everyone') }]} />
          </Row>
          <Toggle label={t('ssh.password')} disabled={busy}
            hint={st.password && st.has_keys ? t('ssh.passwordKeys') : t(st.password ? 'ssh.passwordOn' : 'ssh.passwordOff')}
            checked={st.password} onChange={(password) => void change({ password })} />
          <Row label={t('ssh.guard')} hint={t(st.penalties ? 'ssh.guard.sshd' : 'ssh.guard.fw')} />
          <Toggle label={t('ssh.outside')} disabled={busy || (st.local_only && !st.outside)}
            hint={st.local_only && !st.outside ? t('ssh.outsideLocalOnly') : t('ssh.outsideHint')}
            checked={st.outside} onChange={(on) => void setOutside(on)} />
        </>
      )}
    </Section>
  );
}

/** Each person's SSH keys; admins also see everyone else's. */
export function SshKeysCard() {
  const t = useT();
  const tp = useTimePrefs();
  const [data, setData] = useState<SshKeys | null>(null);
  const [loadFail, setLoadFail] = useState(false);
  const [key, setKey] = useState('');
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    const r = await sshApi.keys();
    if (r.ok) {
      setData(r.data);
      setLoadFail(false);
    } else setLoadFail(true);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  const add = async () => {
    setBusy(true);
    setErr('');
    const r = await sshApi.addKey(key, name);
    setBusy(false);
    if (!r.ok) return setErr(r.error ?? tr('ssh.failed'));
    setData(r.data);
    setKey('');
    setName('');
    toast('success', tr('ssh.keys.added'));
  };
  const remove = async (user: string, k: SshKey) => {
    if (!(await confirmDialog({ title: t('ssh.keys.removeTitle', { name: k.name }), message: t('ssh.keys.removeMsg', { user }), confirmLabel: t('ssh.keys.remove'), danger: true }))) return;
    const r = await sshApi.removeKey(user, k.fingerprint);
    if (!r.ok) return toast('error', tr('ssh.failed'), r.error);
    setData(r.data);
    toast('success', tr('ssh.keys.removed'));
  };
  const upload = async (f: File | undefined) => {
    if (!f) return;
    if (f.size > 16384) return setErr(tr('ssh.keys.tooBig'));
    setKey((await f.text()).trim());
    if (!name) setName(f.name.replace(/\.pub$/i, ''));
  };

  const list = (user: string, keys: SshKey[]) =>
    keys.length === 0 ? (
      <Row label={t('ssh.keys.none')} />
    ) : (
      keys.map((k) => (
        <div key={k.fingerprint} className="settings-row">
          <span className="settings-row-text">
            <span className="settings-row-label">{k.name || t('ssh.keys.unnamed')}</span>
            <span className="settings-row-hint mono ssh-fp">{k.fingerprint}</span>
            <span className="settings-row-hint">
              {k.type}{k.bits ? ` · ${k.bits}` : ''}
              {k.added ? ` · ${t('ssh.keys.addedOn', { date: fmtDate(new Date(k.added), tp, 'short') })}` : ''}
              {k.options ? ` · ${t('ssh.keys.options')}` : ''}
            </span>
          </span>
          <div className="settings-row-control">
            <button type="button" className="ghost small danger" onClick={() => void remove(user, k)}>{t('ssh.keys.remove')}</button>
          </div>
        </div>
      ))
    );

  if (!data) {
    return (
      <Section title={t('ssh.keys.title')} hint={t('ssh.keys.hint')}>
        <Row label={loadFail ? t('set.loadFail') : t('sp.checking')}>
          {loadFail && <button type="button" className="ghost small" onClick={() => void load()}>{t('set.retry')}</button>}
        </Row>
      </Section>
    );
  }
  if (!data.supported) {
    return (
      <Section title={t('ssh.keys.title')} hint={t('ssh.keys.hint')}>
        <Row label={t('ssh.unavailable')} hint={data.reason} />
      </Section>
    );
  }
  return (
    <>
      <Section title={t('ssh.keys.title')} hint={t('ssh.keys.hint')}>
        {data.error && <Row label={t('ssh.keys.readFail')} hint={data.error} />}
        {list(data.user, data.keys)}
        <div className="settings-row full">
          <form className="ssh-add" onSubmit={(e) => { e.preventDefault(); void add(); }}>
            <label>
              {t('ssh.keys.paste')}
              <textarea className="mono" rows={3} value={key} spellCheck={false} placeholder="ssh-ed25519 AAAA…" onChange={(e) => setKey(e.target.value)} />
            </label>
            <label>
              {t('ssh.keys.name')}
              <input value={name} maxLength={64} placeholder={t('ssh.keys.namePh')} onChange={(e) => setName(e.target.value)} />
            </label>
            {err && <p className="error">{err}</p>}
            <div className="btn-group">
              <button type="submit" disabled={busy || !key.trim()}>{t('ssh.keys.add')}</button>
              <button type="button" className="ghost" onClick={() => fileRef.current?.click()}>
                <Icon name="upload" size={13} /> {t('ssh.keys.upload')}
              </button>
              <input ref={fileRef} type="file" accept=".pub,text/plain" hidden onChange={(e) => { void upload(e.target.files?.[0]); e.target.value = ''; }} />
            </div>
          </form>
        </div>
      </Section>
      {data.others && data.others.length > 0 && (
        <Section title={t('ssh.keys.others')} hint={t('ssh.keys.othersHint')}>
          {data.others.map((o) => (
            <div key={o.user} className="ssh-user">
              <div className="settings-row"><span className="settings-row-label"><Icon name="user" size={13} /> {o.user}</span></div>
              {list(o.user, o.keys)}
            </div>
          ))}
        </Section>
      )}
    </>
  );
}
