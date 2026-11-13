import { useCallback, useEffect, useState } from 'react';
import { shieldApi, type Exposed, type FwEntry, type FwRule, type FwState, type Policy } from '../api/shield';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { confirmDialog } from '../state/confirm';
import { Empty } from './Monitor';
import { Card, Select, fwCall } from './ShieldCommon';

// Shield → Firewall: on/off, defaults, what's exposed, presets and rules.

export function FirewallTab({ fw, setFw }: { fw: FwState; setFw: (s: FwState) => void }) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const run = async (p: () => Promise<boolean>) => {
    setBusy(true);
    await p();
    setBusy(false);
  };

  if (!fw.supported) return <Empty icon="lock" text={fw.reason ?? t('sh.fw.unsupported')} />;
  if (!fw.installed) {
    return (
      <Empty icon="lock" text={t('sh.fw.notInstalled')}>
        {fw.can_install ? (
          <button type="button" disabled={busy} onClick={() => void run(() => fwCall(shieldApi.install(), setFw, t('sh.fw.installed')))}>
            <Icon name="download" size={14} />{busy ? t('sh.fw.installing') : t('sh.fw.install')}
          </button>
        ) : (
          <p className="muted">{t('sh.fw.installYourself')}</p>
        )}
      </Empty>
    );
  }

  const toggle = async () => {
    if (fw.active) {
      if (!(await confirmDialog({ title: t('sh.fw.offTitle'), message: t('sh.fw.offMsg'), confirmLabel: t('sh.fw.turnOff'), danger: true }))) return;
      await run(() => fwCall(shieldApi.disable(), setFw, t('sh.fw.turnedOff')));
      return;
    }
    const msg = t('sh.fw.onMsg', { client: fw.keep.client, ports: fw.keep.ports.join(', ') });
    if (!(await confirmDialog({ title: t('sh.fw.onTitle'), message: msg, confirmLabel: t('sh.fw.turnOn') }))) return;
    await run(() => fwCall(shieldApi.enable(), setFw));
  };

  const policies: { id: Policy; label: string }[] = (['deny', 'reject', 'allow'] as Policy[]).map((p) => ({ id: p, label: t(`sh.policy.${p}`) }));

  return (
    <>
      <Card className={`sh-power ${fw.active ? 'on' : ''}`}>
        <div className="sh-power-row">
          <span className="sh-power-ico"><Icon name="lock" size={22} /></span>
          <span className="sh-power-text">
            <strong>{t(fw.active ? 'sh.fw.on' : 'sh.fw.off')}</strong>
            <span className="muted">{t(fw.active ? 'sh.fw.onHint' : 'sh.fw.offHint')}</span>
          </span>
          <button type="button" className={fw.active ? 'ghost' : ''} disabled={busy || !!fw.pending} onClick={() => void toggle()}>
            {t(fw.active ? 'sh.fw.turnOff' : 'sh.fw.turnOn')}
          </button>
        </div>
        <p className="sh-safe"><Icon name="check" size={13} />{t('sh.fw.safe', { client: fw.keep.client || '?', ports: fw.keep.ports.join(', ') })}</p>
        <div className="sh-defaults">
          <label>
            <span>{t('sh.fw.incoming')}</span>
            <Select label={t('sh.fw.incoming')} value={fw.incoming} options={policies} disabled={busy} onChange={(v) => void run(() => fwCall(shieldApi.defaults({ incoming: v }), setFw))} />
          </label>
          <label>
            <span>{t('sh.fw.outgoing')}</span>
            <Select label={t('sh.fw.outgoing')} value={fw.outgoing} options={policies} disabled={busy} onChange={(v) => void run(() => fwCall(shieldApi.defaults({ outgoing: v }), setFw))} />
          </label>
        </div>
      </Card>

      <ExposedCard fw={fw} setFw={setFw} />
      <PresetsCard fw={fw} setFw={setFw} />
      <RulesCard fw={fw} setFw={setFw} />
    </>
  );
}

const KIND_ICON = { nocap: 'shield', ssh: 'terminal', container: 'containers', system: 'network' } as const;

function ExposedCard({ fw, setFw }: { fw: FwState; setFw: (s: FwState) => void }) {
  const t = useT();
  const [list, setList] = useState<Exposed[] | null>(null);
  const load = useCallback(async () => {
    const r = await shieldApi.exposed();
    if (r.ok) setList(r.data.services);
  }, []);
  useEffect(() => {
    void load();
  }, [load, fw]);

  const flip = async (e: Exposed) => {
    if (e.open && !(await confirmDialog({ title: t('sh.exp.closeTitle', { name: e.name }), message: t('sh.exp.closeMsg', { port: `${e.port}/${e.proto}` }), confirmLabel: t('sh.exp.close'), danger: true }))) return;
    await fwCall(shieldApi.setExposed(e, !e.open), setFw);
  };

  return (
    <Card title={t('sh.exp.title')} icon="eye" hint={t(fw.active ? 'sh.exp.hint' : 'sh.exp.hintOff')}>
      {!list ? (
        <p className="muted">{t('sh.loading')}</p>
      ) : list.length === 0 ? (
        <p className="muted">{t('sh.exp.none')}</p>
      ) : (
        <ul className="sh-list">
          {list.map((e) => (
            <li key={`${e.port}/${e.proto}`} className="sh-row">
              <span className={`sh-row-ico k-${e.kind}`}><Icon name={KIND_ICON[e.kind]} size={15} /></span>
              <span className="sh-row-text">
                <span className="sh-row-title">{e.name}</span>
                <span className="sh-row-sub">
                  {e.port}/{e.proto}
                  {e.process ? ` · ${e.process}` : ''}
                  {e.bypass ? ` · ${t('sh.exp.bypass')}` : ''}
                  {e.locked ? ` · ${t('sh.exp.locked')}` : ''}
                </span>
              </span>
              <span className={`chip ${e.open ? 'warn' : 'good'}`}>{t(e.open ? 'sh.exp.open' : 'sh.exp.closed')}</span>
              <input
                type="checkbox"
                role="switch"
                aria-label={t('sh.exp.toggle', { name: e.name })}
                checked={e.open}
                disabled={(e.open && e.locked) || !!fw.pending}
                onChange={() => void flip(e)}
              />
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function PresetsCard({ fw, setFw }: { fw: FwState; setFw: (s: FwState) => void }) {
  const t = useT();
  const [source, setSource] = useState('');
  const sources = [{ id: '', label: t('sh.preset.anywhere') }, ...fw.local_nets.map((n) => ({ id: n, label: t('sh.preset.lan', { net: n }) }))];
  return (
    <Card title={t('sh.preset.title')} icon="sparkles" hint={t('sh.preset.hint')}>
      <label className="sh-inline">
        <span>{t('sh.preset.from')}</span>
        <Select label={t('sh.preset.from')} value={source} options={sources} onChange={setSource} />
      </label>
      <div className="chip-row">
        {fw.presets.map((p) => (
          <button key={p.id} type="button" className="chip-btn" title={p.ports.join(', ')} onClick={() => void fwCall(shieldApi.preset(p.id, source), setFw, t('sh.preset.added', { name: p.name }))}>
            <Icon name="plus" size={12} />{p.name}
          </button>
        ))}
      </div>
    </Card>
  );
}

const EMPTY: FwRule = { action: 'allow', direction: 'in', proto: 'tcp', port: '', from: '', to: '', interface: '', app: '', comment: '' };

function describe(r: FwEntry | FwRule, any: string): string {
  const what = r.app ? r.app : r.port ? `${r.port}${r.proto !== 'any' ? `/${r.proto}` : ''}` : r.proto !== 'any' ? r.proto : any;
  return what;
}

function RulesCard({ fw, setFw }: { fw: FwState; setFw: (s: FwState) => void }) {
  const t = useT();
  const [editing, setEditing] = useState<{ raw?: string; rule: FwRule } | null>(null);
  const v4 = fw.rules.filter((r) => !r.v6);

  const remove = async (r: FwEntry) => {
    if (!(await confirmDialog({ title: t('sh.rule.deleteTitle'), message: r.raw, confirmLabel: t('sh.rule.delete'), danger: true }))) return;
    await fwCall(shieldApi.deleteRule(r.raw), setFw, t('sh.rule.deleted'));
  };
  const move = (r: FwEntry, to: number) => void fwCall(shieldApi.moveRule(r.raw, to), setFw);

  return (
    <Card
      title={t('sh.rule.title')}
      icon="list"
      hint={t('sh.rule.hint')}
      actions={<button type="button" className="small" onClick={() => setEditing({ rule: { ...EMPTY } })}><Icon name="plus" size={13} />{t('sh.rule.add')}</button>}
    >
      {editing && <RuleForm fw={fw} initial={editing.rule} raw={editing.raw} close={() => setEditing(null)} setFw={setFw} />}
      {fw.rules.length === 0 ? (
        <p className="muted">{t('sh.rule.none')}</p>
      ) : (
        <ul className="sh-list sh-rules">
          {fw.rules.map((r, i) => {
            const famIdx = r.v6 ? -1 : v4.indexOf(r);
            return (
              <li key={`${r.raw}-${i}`} className={`sh-row ${r.guard ? 'guard' : ''}`}>
                <span className={`sh-act a-${r.action}`}>{t(`sh.action.${r.action}`)}</span>
                <span className="sh-row-text">
                  <span className="sh-row-title">
                    {r.direction === 'out' ? '↑ ' : '↓ '}
                    {describe(r, t('sh.rule.everything'))}
                    {r.v6 && <span className="chip tiny">IPv6</span>}
                    {r.guard && <span className="chip tiny good">{t('sh.rule.guard')}</span>}
                  </span>
                  <span className="sh-row-sub">
                    {t('sh.rule.fromTo', { from: r.from || t('sh.rule.anywhere'), to: r.to || t('sh.rule.thisServer') })}
                    {r.interface ? ` · ${t('sh.rule.on', { iface: r.interface })}` : ''}
                    {r.comment && !r.guard ? ` · ${r.comment}` : ''}
                    {!r.editable ? ` · ${r.raw}` : ''}
                  </span>
                </span>
                <span className="sh-row-tools">
                  {famIdx >= 0 && (
                    <>
                      <button type="button" className="icon-btn" aria-label={t('sh.rule.up')} disabled={famIdx === 0 || !fw.active} onClick={() => move(r, i - 1)}><Icon name="chevronUp" size={15} /></button>
                      <button type="button" className="icon-btn" aria-label={t('sh.rule.down')} disabled={famIdx === v4.length - 1 || !fw.active} onClick={() => move(r, i + 1)}><Icon name="chevronDown" size={15} /></button>
                    </>
                  )}
                  {r.editable && (
                    <button type="button" className="icon-btn" aria-label={t('sh.rule.edit')} onClick={() => setEditing({ raw: r.raw, rule: { ...EMPTY, ...r } })}><Icon name="edit" size={15} /></button>
                  )}
                  <button type="button" className="icon-btn danger" aria-label={t('sh.rule.delete')} onClick={() => void remove(r)}><Icon name="trash" size={15} /></button>
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </Card>
  );
}

function RuleForm({ fw, initial, raw, close, setFw }: { fw: FwState; initial: FwRule; raw?: string; close: () => void; setFw: (s: FwState) => void }) {
  const t = useT();
  const [r, setR] = useState<FwRule>(initial);
  const [useApp, setUseApp] = useState(!!initial.app);
  const [saving, setSaving] = useState(false);
  const set = (p: Partial<FwRule>) => setR((x) => ({ ...x, ...p }));

  const save = async () => {
    const rule: FwRule = useApp ? { ...r, port: '', proto: 'any' } : { ...r, app: '' };
    setSaving(true);
    const ok = await fwCall(raw ? shieldApi.updateRule(raw, rule) : shieldApi.addRule(rule), setFw, t(raw ? 'sh.rule.saved' : 'sh.rule.added'));
    setSaving(false);
    if (ok) close();
  };

  return (
    <form
      className="sh-form"
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <div className="sh-form-grid">
        <label>
          <span>{t('sh.rule.action')}</span>
          <Select label={t('sh.rule.action')} value={r.action} options={(['allow', 'deny', 'reject', 'limit'] as const).map((a) => ({ id: a, label: t(`sh.action.${a}`) }))} onChange={(v) => set({ action: v })} />
        </label>
        <label>
          <span>{t('sh.rule.direction')}</span>
          <Select label={t('sh.rule.direction')} value={r.direction} options={[{ id: 'in', label: t('sh.dir.in') }, { id: 'out', label: t('sh.dir.out') }]} onChange={(v) => set({ direction: v })} />
        </label>
        {fw.apps.length > 0 && (
          <label>
            <span>{t('sh.rule.what')}</span>
            <Select label={t('sh.rule.what')} value={useApp ? 'app' : 'port'} options={[{ id: 'port', label: t('sh.rule.ports') }, { id: 'app', label: t('sh.rule.appProfile') }]} onChange={(v) => setUseApp(v === 'app')} />
          </label>
        )}
        {useApp ? (
          <label>
            <span>{t('sh.rule.appProfile')}</span>
            <Select label={t('sh.rule.appProfile')} value={r.app || ''} options={[{ id: '', label: '—' }, ...fw.apps.map((a) => ({ id: a.name, label: a.ports ? `${a.name} (${a.ports})` : a.name }))]} onChange={(v) => set({ app: v })} />
          </label>
        ) : (
          <>
            <label>
              <span>{t('sh.rule.port')}</span>
              <input value={r.port ?? ''} placeholder="22, 80,443, 8000:8100" inputMode="numeric" maxLength={100} onChange={(e) => set({ port: e.target.value })} />
            </label>
            <label>
              <span>{t('sh.rule.proto')}</span>
              <Select label={t('sh.rule.proto')} value={r.proto} options={[{ id: 'tcp', label: 'TCP' }, { id: 'udp', label: 'UDP' }, { id: 'any', label: t('sh.rule.both') }]} onChange={(v) => set({ proto: v })} />
            </label>
          </>
        )}
        <label>
          <span>{t('sh.rule.from')}</span>
          <input value={r.from ?? ''} placeholder={t('sh.rule.fromPh')} maxLength={50} onChange={(e) => set({ from: e.target.value })} />
        </label>
        <label>
          <span>{t('sh.rule.to')}</span>
          <input value={r.to ?? ''} placeholder={t('sh.rule.toPh')} maxLength={50} onChange={(e) => set({ to: e.target.value })} />
        </label>
        <label>
          <span>{t('sh.rule.iface')}</span>
          <input value={r.interface ?? ''} placeholder={t('sh.rule.ifacePh')} maxLength={15} onChange={(e) => set({ interface: e.target.value })} />
        </label>
        <label className="wide">
          <span>{t('sh.rule.comment')}</span>
          <input value={r.comment ?? ''} maxLength={80} onChange={(e) => set({ comment: e.target.value })} />
        </label>
      </div>
      {r.action === 'limit' && <p className="muted sh-small">{t('sh.rule.limitHint')}</p>}
      <div className="btn-group sh-form-actions">
        <button type="button" className="ghost" onClick={close}>{t('sh.cancel')}</button>
        <button type="submit" disabled={saving}>{t(raw ? 'sh.save' : 'sh.rule.add')}</button>
      </div>
    </form>
  );
}
