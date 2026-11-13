import { useState } from 'react';
import { shieldApi, type DnsRecord, type DnsSettings, type DnsStats, type DnsStatus } from '../api/shield';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import { Card, Select, fmtNum, pct, useSecondsLeft } from './ShieldCommon';

// Shield → DNS blocker: on/off, pause, port 53, setup help, lists,
// allow/block lists and local records.

/** Saves new DNS settings; the new status on success, a toast on failure. */
export function useDnsSave(setDns: (s: DnsStatus) => void) {
  const t = useT();
  return async (s: DnsSettings, done?: string): Promise<boolean> => {
    const r = await shieldApi.saveDns(s);
    if (!r.ok) {
      toast('error', t('sh.dns.failed'), r.error);
      return false;
    }
    setDns(r.data);
    if (done) toast('success', done);
    return true;
  };
}

export function DnsTab({ dns, setDns, stats }: { dns: DnsStatus; setDns: (s: DnsStatus) => void; stats: DnsStats }) {
  const t = useT();
  const save = useDnsSave(setDns);
  const s = dns.settings;
  const paused = useSecondsLeft(dns.paused_until);
  const [busy, setBusy] = useState(false);

  const toggle = async () => {
    if (!s.enabled && !(await confirmDialog({ title: t('sh.dns.onTitle'), message: t('sh.dns.onMsg'), confirmLabel: t('sh.dns.turnOn') }))) return;
    setBusy(true);
    await save({ ...s, enabled: !s.enabled }, t(s.enabled ? 'sh.dns.turnedOff' : 'sh.dns.turnedOn'));
    setBusy(false);
  };
  const pause = async (m: number) => {
    const r = await shieldApi.pause(m);
    if (!r.ok) return toast('error', t('sh.dns.failed'), r.error);
    setDns(r.data);
  };
  const port53 = async (free: boolean) => {
    if (free && !(await confirmDialog({ title: t('sh.p53.freeTitle'), message: t('sh.p53.freeMsg'), confirmLabel: t('sh.p53.free') }))) return;
    setBusy(true);
    const r = await shieldApi.port53(free);
    setBusy(false);
    if (!r.ok) return toast('error', t('sh.dns.failed'), r.error);
    setDns(r.data);
  };
  const useHere = async (on: boolean) => {
    if (on && !(await confirmDialog({ title: t('sh.dns.useTitle'), message: t('sh.dns.useMsg'), confirmLabel: t('sh.dns.useOn') }))) return;
    await save({ ...s, use_on_server: on });
  };

  return (
    <>
      <Card className={`sh-power ${s.enabled ? 'on' : ''}`}>
        <div className="sh-power-row">
          <span className="sh-power-ico"><Icon name="globe" size={22} /></span>
          <span className="sh-power-text">
            <strong>{t(s.enabled ? 'sh.dns.on' : 'sh.dns.off')}</strong>
            <span className="muted">{s.enabled ? (dns.running ? t('sh.dns.listening', { addrs: dns.listening.join(', ') }) : t('sh.dns.notListening')) : t('sh.dns.offHint')}</span>
          </span>
          <button type="button" className={s.enabled ? 'ghost' : ''} disabled={busy} onClick={() => void toggle()}>{t(s.enabled ? 'sh.dns.turnOff' : 'sh.dns.turnOn')}</button>
        </div>
        {dns.listen_error && <p className="sh-warn"><Icon name="alert" size={14} />{t('sh.dns.listenError', { err: dns.listen_error })}</p>}
        {s.enabled && dns.running && (
          <div className="btn-group">
            {paused ? (
              <>
                <span className="chip warn">{t('sh.dns.pausedFor', { n: Math.ceil(paused / 60) })}</span>
                <button type="button" className="small" onClick={() => void pause(0)}><Icon name="play" size={13} />{t('sh.dns.resume')}</button>
              </>
            ) : (
              [5, 30, 60].map((m) => (
                <button key={m} type="button" className="ghost small" onClick={() => void pause(m)}><Icon name="pause" size={13} />{t('sh.dns.pauseN', { n: m })}</button>
              ))
            )}
          </div>
        )}
      </Card>

      {(dns.port53.stub || dns.port53.freed || dns.listen_note === 'lan') && (
        <Card title={t('sh.p53.title')} icon="network" hint={t(dns.port53.freed ? 'sh.p53.freedHint' : 'sh.p53.hint')}>
          {dns.listen_note === 'lan' && <p className="muted sh-small">{t('sh.p53.lanNote')}</p>}
          {dns.port53.can_manage && (
            <div className="btn-group">
              {dns.port53.freed ? (
                <button type="button" className="ghost small" disabled={busy} onClick={() => void port53(false)}>{t('sh.p53.restore')}</button>
              ) : (
                <button type="button" className="small" disabled={busy} onClick={() => void port53(true)}>{t('sh.p53.free')}</button>
              )}
            </div>
          )}
        </Card>
      )}

      {s.enabled && (
        <Card title={t('sh.setup.title')} icon="info" hint={t('sh.setup.hint')}>
          <ol className="sh-steps">
            <li>{t('sh.setup.router')}</li>
            <li>{t('sh.setup.address')} {dns.addresses.length ? dns.addresses.map((a) => <code key={a} className="sh-code">{a}</code>) : <span className="muted">{t('sh.setup.noAddr')}</span>}</li>
            <li>{t('sh.setup.devices')}</li>
            <li>{t('sh.setup.check')}</li>
          </ol>
          <label className="settings-row toggle row">
            <span className="settings-row-text">
              <span className="settings-row-label">{t('sh.dns.useHere')}</span>
              <span className="settings-row-hint">{t('sh.dns.useHereHint')}</span>
            </span>
            <input type="checkbox" role="switch" checked={s.use_on_server} disabled={!dns.running} onChange={(e) => void useHere(e.target.checked)} />
          </label>
        </Card>
      )}

      <Card title={t('sh.lists.title')} icon="list" hint={t('sh.lists.hint', { n: fmtNum(dns.domains) })} actions={
        <button type="button" className="ghost small" disabled={!s.enabled || dns.refreshing} onClick={async () => { const r = await shieldApi.refresh(); if (r.ok) setDns(r.data); }}>
          <Icon name="restart" size={13} />{t(dns.refreshing ? 'sh.lists.refreshing' : 'sh.lists.refresh')}
        </button>
      }>
        <Lists dns={dns} save={save} />
      </Card>

      <div className="sh-grid">
        <Card title={t('sh.allow.title')} icon="check" hint={t('sh.allow.hint')}>
          <DomainBox value={s.allow} onSave={(v) => save({ ...s, allow: v }, t('sh.saved'))} />
        </Card>
        <Card title={t('sh.block.title')} icon="alert" hint={t('sh.block.hint')}>
          <DomainBox value={s.block} onSave={(v) => save({ ...s, block: v }, t('sh.saved'))} />
        </Card>
      </div>

      <Card title={t('sh.rec.title')} icon="globe" hint={t('sh.rec.hint')}>
        <Records records={s.records} onSave={(v) => save({ ...s, records: v }, t('sh.saved'))} />
      </Card>

      {stats.log_enabled && stats.top_blocked.length > 0 && (
        <Card title={t('sh.ov.topBlocked')} icon="alert" hint={t('sh.dns.todayPct', { pct: pct(stats.blocked_today, stats.today) })}>
          <ol className="sh-top">
            {stats.top_blocked.map((c) => (
              <li key={c.name}>
                <span className="ellipsis">{c.name}</span>
                <span className="muted">{fmtNum(c.count)}</span>
                <button type="button" className="ghost small" onClick={() => void save({ ...s, allow: [...s.allow, c.name] }, t('sh.allow.added', { name: c.name }))}>{t('sh.allow.add')}</button>
              </li>
            ))}
          </ol>
        </Card>
      )}
    </>
  );
}

function Lists({ dns, save }: { dns: DnsStatus; save: (s: DnsSettings, done?: string) => Promise<boolean> }) {
  const t = useT();
  const s = dns.settings;
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const set = (id: string, enabled: boolean) => void save({ ...s, lists: s.lists.map((l) => (l.id === id ? { ...l, enabled } : l)) });
  const remove = async (id: string, n: string) => {
    if (!(await confirmDialog({ title: t('sh.lists.removeTitle', { name: n }), message: t('sh.lists.removeMsg'), confirmLabel: t('sh.lists.remove'), danger: true }))) return;
    await save({ ...s, lists: s.lists.filter((l) => l.id !== id) });
  };
  const add = async () => {
    if (await save({ ...s, lists: [...s.lists, { id: '', name: name.trim(), url: url.trim(), enabled: true }] }, t('sh.lists.added'))) {
      setName('');
      setUrl('');
    }
  };
  return (
    <>
      <ul className="sh-list">
        {dns.lists.map((l) => (
          <li key={l.id} className="sh-row">
            <span className="sh-row-text">
              <span className="sh-row-title">{l.name}</span>
              <span className="sh-row-sub ellipsis">
                {l.error ? <span className="sh-err">{t('sh.lists.error', { err: l.error })}</span> : l.updated ? t('sh.lists.count', { n: fmtNum(l.domains ?? 0), when: new Date(l.updated).toLocaleString() }) : t('sh.lists.never')}
              </span>
            </span>
            <button type="button" className="icon-btn danger" aria-label={t('sh.lists.remove')} onClick={() => void remove(l.id, l.name)}><Icon name="trash" size={14} /></button>
            <input type="checkbox" role="switch" aria-label={l.name} checked={l.enabled} onChange={(e) => set(l.id, e.target.checked)} />
          </li>
        ))}
      </ul>
      <form
        className="sh-addline"
        onSubmit={(e) => {
          e.preventDefault();
          void add();
        }}
      >
        <input value={name} placeholder={t('sh.lists.namePh')} maxLength={80} onChange={(e) => setName(e.target.value)} />
        <input value={url} placeholder="https://…" maxLength={2048} type="url" onChange={(e) => setUrl(e.target.value)} />
        <button type="submit" className="small" disabled={!name.trim() || !url.trim()}><Icon name="plus" size={13} />{t('sh.lists.add')}</button>
      </form>
    </>
  );
}

function DomainBox({ value, onSave }: { value: string[]; onSave: (v: string[]) => Promise<boolean> }) {
  const t = useT();
  const [text, setText] = useState(value.join('\n'));
  const dirty = text.trim() !== value.join('\n').trim();
  return (
    <>
      <textarea className="sh-textarea" rows={6} value={text} spellCheck={false} placeholder={'example.com\nads.example.net'} onChange={(e) => setText(e.target.value)} />
      <div className="btn-group">
        <button type="button" className="small" disabled={!dirty} onClick={() => void onSave(text.split(/[\s,]+/).filter(Boolean))}>{t('sh.save')}</button>
        <span className="muted sh-small">{t('sh.domains.count', { n: value.length })}</span>
      </div>
    </>
  );
}

function Records({ records, onSave }: { records: DnsRecord[]; onSave: (v: DnsRecord[]) => Promise<boolean> }) {
  const t = useT();
  const [draft, setDraft] = useState<DnsRecord>({ name: '', type: 'A', value: '' });
  const add = async () => {
    if (await onSave([...records, draft])) setDraft({ name: '', type: 'A', value: '' });
  };
  return (
    <>
      {records.length > 0 && (
        <ul className="sh-list">
          {records.map((r, i) => (
            <li key={`${r.name}-${r.type}-${r.value}`} className="sh-row">
              <span className="sh-row-text">
                <span className="sh-row-title">{r.name}</span>
                <span className="sh-row-sub">{r.type} → {r.value}</span>
              </span>
              <button type="button" className="icon-btn danger" aria-label={t('sh.rec.remove')} onClick={() => void onSave(records.filter((_, j) => j !== i))}><Icon name="trash" size={14} /></button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="sh-addline"
        onSubmit={(e) => {
          e.preventDefault();
          void add();
        }}
      >
        <input value={draft.name} placeholder={t('sh.rec.namePh')} maxLength={253} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
        <Select label={t('sh.rec.type')} value={draft.type} options={[{ id: 'A', label: 'A' }, { id: 'AAAA', label: 'AAAA' }, { id: 'CNAME', label: 'CNAME' }]} onChange={(v) => setDraft({ ...draft, type: v })} />
        <input value={draft.value} placeholder={draft.type === 'CNAME' ? 'nas.home' : draft.type === 'A' ? '192.168.1.10' : 'fd00::10'} maxLength={253} onChange={(e) => setDraft({ ...draft, value: e.target.value })} />
        <button type="submit" className="small" disabled={!draft.name.trim() || !draft.value.trim()}><Icon name="plus" size={13} />{t('sh.rec.add')}</button>
      </form>
    </>
  );
}
