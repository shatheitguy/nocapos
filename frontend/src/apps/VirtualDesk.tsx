import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  fmtGiB,
  fmtMiB,
  isLive,
  OS_LABEL,
  vmApi,
  type NetMode,
  type PowerAction,
  type UpdateVm,
  type VM,
  type VmSnapshot,
  type VmStatus,
  type VmUsage,
} from '../api/vms';
import { Icon, type IconName } from '../components/Icon';
import { fmtAgo } from '../lib/format';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';
import type { WinState } from '../state/windows';
import { openApp } from './meta';
import { Sheet, TypeConfirm } from './StorageParts';
import { baseName, IsoShelf, OsBadge, PathField, StateChip, UsageBar } from './VirtualDeskParts';
import { NewVmWizard } from './VirtualDeskWizard';

// Virtual Desk: virtual machines on this server. Machines are listed as cards
// with live load; a machine's page has its overview, settings (most of them
// only while it's off), snapshots and the danger zone. Its screen opens in a
// window of its own. Admin only; the server repeats every check.

type Tab = 'overview' | 'settings' | 'snapshots';

type Modal = { kind: 'new' } | { kind: 'delete'; vm: VM } | { kind: 'clone'; vm: VM } | { kind: 'rename'; vm: VM } | null;

const NET_LABEL: Record<NetMode, string> = { nat: 'Private (NAT)', bridge: 'Bridge', direct: 'Direct', none: 'No network' };

export function openScreen(v: VM) {
  openApp('vmscreen', { title: `${v.name} — Virtual Desk`, props: { vm: v.name }, key: `vmscreen:${v.uuid}` });
}

const diskTotal = (v: VM) => v.disks.reduce((a, d) => a + d.size, 0);

export function VirtualDesk({ win }: { win: WinState }) {
  const [status, setStatus] = useState<VmStatus | null>(null);
  const [vms, setVms] = useState<VM[]>([]);
  const [stats, setStats] = useState<Record<string, VmUsage>>({});
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<string | null>(win.props?.vm ?? null);
  const [modal, setModal] = useState<Modal>(null);
  const [installing, setInstalling] = useState(false);
  const [acting, setActing] = useState<string | null>(null);

  const load = useCallback(async () => {
    const st = await vmApi.status();
    if (!st.ok) {
      setError(st.error ?? 'Couldn’t reach NoCapOS');
      setLoaded(true);
      return;
    }
    setStatus(st.data);
    if (!st.data.available) {
      setLoaded(true);
      return;
    }
    const l = await vmApi.list();
    if (l.ok) {
      setVms(l.data.vms);
      setError(null);
    } else setError(l.error ?? 'Couldn’t list the machines');
    setLoaded(true);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A notification (or another app) can ask for a specific machine.
  useEffect(() => {
    if (win.props?.vm) setSelected(win.props.vm);
  }, [win.props]);

  // Refresh: quickly while something is changing, slowly otherwise.
  const changing = vms.some((v) => v.busy || v.state === 'stopping');
  useEffect(() => {
    if (!status?.available) return;
    const t = window.setInterval(() => void load(), changing ? 3000 : 10000);
    return () => window.clearInterval(t);
  }, [load, changing, status?.available]);

  // Live CPU and memory of running machines.
  const anyLive = vms.some((v) => v.state === 'running');
  useEffect(() => {
    if (!status?.available || !anyLive) return setStats({});
    let stop = false;
    const tick = async () => {
      const r = await vmApi.stats();
      if (!stop && r.ok) setStats(r.data.stats);
    };
    void tick();
    const t = window.setInterval(() => void tick(), 3000);
    return () => {
      stop = true;
      window.clearInterval(t);
    };
  }, [status?.available, anyLive]);

  const power = async (v: VM, action: PowerAction) => {
    if (action === 'poweroff') {
      const ok = await confirmDialog({
        title: `Force ${v.name} off?`,
        message: 'Like pulling the plug: anything unsaved inside the machine is lost. Shut down lets it close properly.',
        confirmLabel: 'Force off',
        danger: true,
      });
      if (!ok) return;
    }
    setActing(v.name + action);
    const r = await vmApi.power(v.name, action);
    setActing(null);
    if (!r.ok) return toast('error', `Couldn’t ${ACTION_VERB[action]} ${v.name}`, r.error);
    if (action === 'shutdown') toast('info', `Asked ${v.name} to shut down`, 'The system inside decides when; use Force off if it doesn’t respond.');
    void load();
  };

  const install = async () => {
    setInstalling(true);
    const r = await vmApi.install();
    setInstalling(false);
    if (!r.ok) return toast('error', 'Couldn’t install the virtualization packages', r.error);
    toast('success', 'Virtualization is installed');
    void load();
  };

  const current = selected ? vms.find((v) => v.name === selected) : undefined;

  if (!loaded) {
    return (
      <div className="vd-app st-app st-center">
        <span className="st-spinner" />
      </div>
    );
  }
  if (status && !status.available) return <Unavailable status={status} installing={installing} onInstall={() => void install()} />;

  return (
    <div className="vd-app st-app app-split">
      <nav className="sidebar st-side vd-side">
        <div className="st-side-head">
          <h1 className="app-title">Virtual Desk</h1>
          {status?.demo && <span className="chip warn st-demo" title="Sample machines for trying the app; nothing real runs">Demo data</span>}
        </div>
        <button type="button" className={!current ? 'on' : ''} onClick={() => setSelected(null)} title="All machines">
          <Icon name="grid" size={16} />
          <span className="st-nav-label">All machines</span>
          <span className="st-count">{vms.length}</span>
        </button>
        {vms.length > 0 && <span className="section-label st-side-label">Machines</span>}
        {vms.map((v) => (
          <button key={v.uuid} type="button" className={`vd-side-vm ${current?.uuid === v.uuid ? 'on' : ''}`} onClick={() => setSelected(v.name)} title={v.name}>
            <span className={`vd-dot ${v.state}`} />
            <span className="st-nav-label">{v.name}</span>
          </button>
        ))}
        <span className="spacer" />
        <button type="button" className="vd-new" onClick={() => setModal({ kind: 'new' })} title="New virtual machine">
          <Icon name="plus" size={16} />
          <span className="st-nav-label">New VM</span>
        </button>
      </nav>
      <main className="app-content st-main">
        {error && (
          <div className="st-note bad">
            <Icon name="alert" size={14} /> {error}
          </div>
        )}
        {current ? (
          <VmPage key={current.uuid} vm={current} status={status!} usage={stats[current.name]} acting={acting} onPower={(a) => void power(current, a)}
            onBack={() => setSelected(null)} onChanged={() => void load()}
            onDelete={() => setModal({ kind: 'delete', vm: current })} onClone={() => setModal({ kind: 'clone', vm: current })}
            onRename={() => setModal({ kind: 'rename', vm: current })} />
        ) : (
          <AllMachines vms={vms} stats={stats} status={status!} acting={acting} onOpen={(v) => setSelected(v.name)} onPower={(v, a) => void power(v, a)}
            onNew={() => setModal({ kind: 'new' })} installing={installing} onInstall={() => void install()} />
        )}
      </main>

      {modal?.kind === 'new' && status && (
        <NewVmWizard status={status} vms={vms} onClose={() => setModal(null)} onDone={(n) => { setSelected(n); void load(); }} />
      )}
      {modal?.kind === 'delete' && <DeleteVm vm={modal.vm} onClose={() => setModal(null)} onDone={() => { setModal(null); setSelected(null); void load(); }} />}
      {(modal?.kind === 'clone' || modal?.kind === 'rename') && (
        <NameSheet kind={modal.kind} vm={modal.vm} taken={vms.map((v) => v.name)} onClose={() => setModal(null)}
          onDone={(n) => { setModal(null); setSelected(n); void load(); }} />
      )}
    </div>
  );
}

const ACTION_VERB: Record<PowerAction, string> = {
  start: 'start',
  shutdown: 'shut down',
  poweroff: 'force off',
  reboot: 'restart',
  pause: 'pause',
  resume: 'resume',
};

// ---------------- Unavailable ----------------

function Unavailable({ status, installing, onInstall }: { status: VmStatus; installing: boolean; onInstall: () => void }) {
  const native = status.native || status.demo;
  return (
    <div className="vd-app st-app st-unsupported">
      <div className="st-unsupported-card">
        <span className="st-hero-icon vd-hero"><Icon name="vm" size={34} /></span>
        <h1 className="app-title">Virtual Desk</h1>
        <p className="st-lead">
          {native
            ? status.hint ?? status.reason
            : status.reason ?? 'Virtual machines need NoCapOS on a Linux server with hardware virtualization.'}
        </p>
        {native && status.can_install && (
          <button type="button" disabled={installing} onClick={onInstall}>
            {installing ? <><span className="spinner sm" /> Installing…</> : <><Icon name="download" size={14} /> Install</>}
          </button>
        )}
        {native && status.missing?.includes('kvm') && (
          <p className="st-note warn"><Icon name="alert" size={14} /> /dev/kvm is missing, so machines can’t use the processor’s virtualization. Turn on Intel VT-x or AMD-V in the firmware settings.</p>
        )}
        <div className="ucard ulist">
          {([
            ['windows', 'Windows, Linux and Android', 'Ready-made settings for Windows 11 (with UEFI and TPM), Windows 10, Linux and Android-x86.'],
            ['desktop', 'The screen in a window', 'Install and use each machine right in NoCapOS, with Ctrl+Alt+Del and full screen.'],
            ['camera', 'Snapshots', 'Save a machine’s state before a risky change and go back in one click.'],
            ['network', 'Your network, your choice', 'Keep a machine private behind this server, or put it on your network like another PC.'],
          ] as [IconName, string, string][]).map(([icon, title, desc]) => (
            <div key={title} className="urow">
              <span className="urow-icon"><Icon name={icon} size={17} /></span>
              <span className="urow-text">
                <span className="urow-title">{title}</span>
                <span className="urow-desc">{desc}</span>
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// ---------------- All machines ----------------

function QuickActions({ vm, acting, onPower }: { vm: VM; acting: string | null; onPower: (a: PowerAction) => void }) {
  const busy = !!vm.busy || (acting?.startsWith(vm.name) ?? false);
  return (
    <span className="vd-quick" onClick={(e) => e.stopPropagation()}>
      {(vm.state === 'off' || vm.state === 'crashed') && (
        <button type="button" className="small" disabled={busy} onClick={() => onPower('start')}>
          <Icon name="play" size={13} /> Start
        </button>
      )}
      {vm.state === 'paused' && (
        <button type="button" className="small" disabled={busy} onClick={() => onPower('resume')}>
          <Icon name="play" size={13} /> Resume
        </button>
      )}
      {vm.state === 'running' && (
        <button type="button" className="small ghost" disabled={busy} onClick={() => onPower('shutdown')}>
          <Icon name="power" size={13} /> Shut down
        </button>
      )}
      {isLive(vm) && (
        <button type="button" className="small" onClick={() => openScreen(vm)}>
          <Icon name="desktop" size={13} /> Open screen
        </button>
      )}
    </span>
  );
}

function AllMachines({ vms, stats, status, acting, onOpen, onPower, onNew, installing, onInstall }: {
  vms: VM[]; stats: Record<string, VmUsage>; status: VmStatus; acting: string | null; onOpen: (v: VM) => void;
  onPower: (v: VM, a: PowerAction) => void; onNew: () => void; installing: boolean; onInstall: () => void;
}) {
  const running = vms.filter((v) => v.state === 'running');
  const cpuUsed = running.reduce((a, v) => a + v.cpus, 0);
  const memUsed = running.reduce((a, v) => a + v.memory_mib, 0);
  return (
    <div className="st-page">
      <div className="st-page-head">
        <div>
          <h2 className="st-h">Virtual machines</h2>
          <p className="muted small st-tight-top">
            {running.length} running · {cpuUsed} of {status.host.cpus} CPUs and {fmtMiB(memUsed)} of {fmtMiB(status.host.memory_mib)} given to running machines
          </p>
        </div>
        <button type="button" onClick={onNew}>
          <Icon name="plus" size={14} /> New VM
        </button>
      </div>
      {status.can_install && (!status.tools.ovmf || !status.tools.swtpm) && (
        <div className="ucard ulist">
          <div className="urow">
            <span className="urow-icon"><Icon name="windows" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">Add Windows 11 support</span>
              <span className="urow-desc">Installs {!status.tools.ovmf ? 'UEFI firmware (OVMF)' : ''}{!status.tools.ovmf && !status.tools.swtpm ? ' and ' : ''}{!status.tools.swtpm ? 'a TPM emulator (swtpm)' : ''}.</span>
            </span>
            <span className="urow-control">
              <button type="button" className="small" disabled={installing} onClick={onInstall}>{installing ? 'Installing…' : 'Install'}</button>
            </span>
          </div>
        </div>
      )}
      {!vms.length ? (
        <div className="ucard st-empty vd-empty">
          <OsBadge os="other" size={52} />
          <b>No virtual machines yet</b>
          <p className="muted small">Create one from an installer ISO, or bring an existing disk image.</p>
          <button type="button" onClick={onNew}><Icon name="plus" size={14} /> New VM</button>
        </div>
      ) : (
        <div className="vd-grid">
          {vms.map((v) => {
            const u = stats[v.name];
            return (
              <div key={v.uuid} className="ucard vd-card" role="button" tabIndex={0} onClick={() => onOpen(v)}
                onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && onOpen(v)}>
                <div className="vd-card-head">
                  <OsBadge os={v.os} size={44} />
                  <span className="vd-card-title">
                    <b>{v.name}</b>
                    <StateChip vm={v} />
                  </span>
                </div>
                <div className="vd-specs">
                  <span><Icon name="cpu" size={13} /> {v.cpus} CPU{v.cpus > 1 ? 's' : ''}</span>
                  <span><Icon name="memory" size={13} /> {fmtMiB(v.memory_mib)}</span>
                  <span><Icon name="hdd" size={13} /> {fmtGiB(diskTotal(v))}</span>
                </div>
                {v.state === 'running' && (
                  <div className="vd-live">
                    <UsageBar label="CPU" pct={u?.cpu ?? 0} value={u ? `${Math.round(u.cpu)}%` : '…'} />
                    <UsageBar label="Memory" pct={u && u.mem_total ? (u.mem_used / u.mem_total) * 100 : 0} value={u ? `${fmtGiB(u.mem_used)} of ${fmtGiB(u.mem_total)}` : '…'} />
                  </div>
                )}
                <QuickActions vm={v} acting={acting} onPower={(a) => onPower(v, a)} />
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ---------------- One machine ----------------

function VmPage({ vm, status, usage, acting, onPower, onBack, onChanged, onDelete, onClone, onRename }: {
  vm: VM; status: VmStatus; usage?: VmUsage; acting: string | null; onPower: (a: PowerAction) => void; onBack: () => void;
  onChanged: () => void; onDelete: () => void; onClone: () => void; onRename: () => void;
}) {
  const [tab, setTab] = useState<Tab>('overview');
  const busy = !!vm.busy || (acting?.startsWith(vm.name) ?? false);
  const btn = (action: PowerAction, icon: IconName, label: string, cls = 'small ghost') => (
    <button type="button" className={cls} disabled={busy} onClick={() => onPower(action)}>
      <Icon name={icon} size={14} /> {label}
    </button>
  );
  return (
    <div className="st-page">
      <div className="vd-head">
        <button type="button" className="icon-btn neutral vd-back" title="All machines" onClick={onBack}>
          <Icon name="chevronLeft" size={16} />
        </button>
        <OsBadge os={vm.os} size={52} />
        <div className="vd-head-text">
          <h2 className="st-h">{vm.name} <StateChip vm={vm} /></h2>
          <span className="muted small">
            {OS_LABEL[vm.os]} · {vm.firmware === 'uefi' ? 'UEFI' : 'BIOS'}{vm.tpm ? ' + TPM' : ''}
            {vm.state_reason && vm.state !== 'running' ? ` · ${vm.state_reason}` : ''}
          </span>
        </div>
        <div className="vd-head-actions st-actions">
          {(vm.state === 'off' || vm.state === 'crashed') && btn('start', 'play', 'Start', 'small')}
          {vm.state === 'paused' && btn('resume', 'play', 'Resume', 'small')}
          {isLive(vm) && (
            <button type="button" className="small" onClick={() => openScreen(vm)}>
              <Icon name="desktop" size={14} /> Open screen
            </button>
          )}
          {vm.state === 'running' && btn('shutdown', 'power', 'Shut down')}
          {vm.state === 'running' && btn('reboot', 'restart', 'Restart')}
          {vm.state === 'running' && btn('pause', 'pause', 'Pause')}
          {(isLive(vm) || vm.state === 'crashed') && btn('poweroff', 'stop', 'Force off', 'small ghost danger')}
        </div>
      </div>
      <div className="segmented vd-tabs">
        {(['overview', 'settings', 'snapshots'] as Tab[]).map((t) => (
          <button key={t} type="button" className={tab === t ? 'on' : ''} onClick={() => setTab(t)}>
            {t === 'overview' ? 'Overview' : t === 'settings' ? 'Settings' : 'Snapshots'}
          </button>
        ))}
      </div>
      {tab === 'overview' && <Overview vm={vm} usage={usage} />}
      {tab === 'settings' && <Settings vm={vm} status={status} onChanged={onChanged} onDelete={onDelete} onClone={onClone} onRename={onRename} />}
      {tab === 'snapshots' && <Snapshots vm={vm} onChanged={onChanged} />}
    </div>
  );
}

function Overview({ vm, usage }: { vm: VM; usage?: VmUsage }) {
  return (
    <>
      <div className="st-stats">
        <div><span>Processors</span><b>{vm.cpus}</b></div>
        <div><span>Memory</span><b>{fmtMiB(vm.memory_mib)}</b></div>
        <div><span>Disk space</span><b>{fmtGiB(diskTotal(vm))}</b></div>
        <div><span>Network</span><b>{NET_LABEL[vm.network.mode]}{vm.network.source ? ` · ${vm.network.source}` : ''}</b></div>
        <div><span>Starts with server</span><b>{vm.autostart ? 'Yes' : 'No'}</b></div>
        <div><span>Created</span><b>{vm.created ? fmtAgo(vm.created) : '—'}</b></div>
      </div>
      {vm.state === 'running' && (
        <div className="ucard pad vd-live">
          <UsageBar label="CPU" pct={usage?.cpu ?? 0} value={usage ? `${Math.round(usage.cpu)}% of ${vm.cpus} CPU${vm.cpus > 1 ? 's' : ''}` : 'Measuring…'} />
          <UsageBar label="Memory" pct={usage && usage.mem_total ? (usage.mem_used / usage.mem_total) * 100 : 0}
            value={usage ? `${fmtGiB(usage.mem_used)} of ${fmtGiB(usage.mem_total)}` : 'Measuring…'} />
        </div>
      )}
      <span className="section-label">Disks</span>
      <div className="ucard ulist">
        {vm.disks.map((d) => (
          <div key={d.target} className="urow">
            <span className="urow-icon"><Icon name="hdd" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{fmtGiB(d.size)} <span className="muted small">· {d.target} · {d.bus}{d.format ? ` · ${d.format}` : ''}</span></span>
              <span className="urow-desc mono st-wrap">{d.path}</span>
              {d.size > 0 && (
                <div className="st-meter"><span style={{ width: `${Math.min(100, (d.used / d.size) * 100)}%` }} /></div>
              )}
              <span className="urow-desc">{fmtGiB(d.used)} used on the server</span>
            </span>
          </div>
        ))}
        {!vm.disks.length && <p className="muted st-pad">No disks.</p>}
      </div>
      <span className="section-label">CD drives</span>
      <div className="ucard ulist">
        {vm.media.map((m) => (
          <div key={m.target} className="urow">
            <span className="urow-icon"><Icon name="disk" size={17} /></span>
            <span className="urow-text">
              <span className="urow-title">{m.path ? baseName(m.path) : 'Empty'}</span>
              <span className="urow-desc">{m.target}{m.path ? ` · ${m.path}` : ' · insert an ISO in Settings'}</span>
            </span>
          </div>
        ))}
        {!vm.media.length && <p className="muted st-pad">No CD drives.</p>}
      </div>
      {isLive(vm) && (
        <p className="muted small">
          <Icon name="shield" size={12} /> The screen is only reachable through NoCapOS (VNC on this server’s loopback, port {vm.vnc_port ?? '—'}).
        </p>
      )}
      {!vm.managed && <p className="st-note"><Icon name="info" size={14} /> This machine was created outside Virtual Desk. You can run and change it here too.</p>}
    </>
  );
}

// ---------------- Settings ----------------

function Settings({ vm, status, onChanged, onDelete, onClone, onRename }: {
  vm: VM; status: VmStatus; onChanged: () => void; onDelete: () => void; onClone: () => void; onRename: () => void;
}) {
  const off = !isLive(vm);
  const host = status.host;
  const [cpus, setCpus] = useState(vm.cpus);
  const [mem, setMem] = useState(vm.memory_mib);
  const [grow, setGrow] = useState<Record<string, number>>({});
  const [addDisk, setAddDisk] = useState(0);
  const [media, setMedia] = useState<Record<string, string>>(() => Object.fromEntries(vm.media.map((m) => [m.target, m.path ?? ''])));
  const [net, setNet] = useState<NetMode>(vm.network.mode);
  const [iface, setIface] = useState(vm.network.source ?? '');
  const [autostart, setAutostart] = useState(vm.autostart);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const bridges = host.interfaces.filter((i) => i.kind === 'bridge');
  const links = host.interfaces.filter((i) => i.kind !== 'wireless');
  const maxCpus = Math.max(vm.cpus, host.cpus || 64);
  const maxMem = Math.max(vm.memory_mib, host.memory_mib || 65536);

  const body = useMemo<UpdateVm>(() => {
    const b: UpdateVm = {};
    if (off && cpus !== vm.cpus) b.cpus = cpus;
    if (off && mem !== vm.memory_mib) b.memory_mib = mem;
    const resize = Object.entries(grow).filter(([t, g]) => g > Math.ceil((vm.disks.find((d) => d.target === t)?.size ?? 0) / 2 ** 30));
    if (off && resize.length) b.resize = resize.map(([target, size_gib]) => ({ target, size_gib }));
    if (off && addDisk > 0) b.add_disk_gib = addDisk;
    const mc = vm.media.filter((m) => (media[m.target] ?? '') !== (m.path ?? '')).map((m) => ({ target: m.target, path: media[m.target] ?? '' }));
    if (mc.length) b.media = mc;
    if (off && (net !== vm.network.mode || ((net === 'bridge' || net === 'direct') && iface !== (vm.network.source ?? ''))))
      b.network = { mode: net, source: net === 'bridge' || net === 'direct' ? iface : undefined };
    if (autostart !== vm.autostart) b.autostart = autostart;
    return b;
  }, [off, cpus, mem, grow, addDisk, media, net, iface, autostart, vm]);
  const dirty = Object.keys(body).length > 0;

  const save = async () => {
    setSaving(true);
    setError(null);
    const r = await vmApi.update(vm.name, body);
    setSaving(false);
    if (!r.ok) return setError(r.error ?? 'Couldn’t save');
    toast('success', 'Settings saved');
    setGrow({});
    setAddDisk(0);
    onChanged();
  };

  return (
    <>
      {!off && <p className="st-note"><Icon name="info" size={14} /> Shut {vm.name} down to change processors, memory, disks or network. Discs and autostart can change any time.</p>}
      <div className="ucard pad st-form vd-settings">
        <label className="vd-slider">
          <span>Processors <b>{cpus}</b></span>
          <input type="range" min={1} max={maxCpus} value={cpus} disabled={!off} onChange={(e) => setCpus(Number(e.target.value))} />
        </label>
        <label className="vd-slider">
          <span>Memory <b>{fmtMiB(mem)}</b></span>
          <input type="range" min={512} max={maxMem} step={256} value={mem} disabled={!off} onChange={(e) => setMem(Number(e.target.value))} />
        </label>
      </div>

      <span className="section-label">Disks</span>
      <div className="ucard ulist">
        {vm.disks.map((d) => {
          const cur = Math.ceil(d.size / 2 ** 30);
          return (
            <div key={d.target} className="urow">
              <span className="urow-icon"><Icon name="hdd" size={17} /></span>
              <span className="urow-text">
                <span className="urow-title">{d.target} · {fmtGiB(d.size)}</span>
                <span className="urow-desc mono st-wrap">{baseName(d.path)}</span>
              </span>
              <span className="urow-control vd-unit">
                <input type="number" min={cur} max={65536} value={grow[d.target] ?? cur} disabled={!off} aria-label={`Size of ${d.target}`}
                  onChange={(e) => setGrow({ ...grow, [d.target]: Math.floor(Number(e.target.value) || 0) })}
                  onBlur={() => grow[d.target] !== undefined && grow[d.target] < cur && setGrow({ ...grow, [d.target]: cur })} />
                <span>GB</span>
              </span>
            </div>
          );
        })}
        <div className="urow">
          <span className="urow-icon"><Icon name="plus" size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">Add a disk</span>
            <span className="urow-desc">A new empty disk; 0 adds none. Disks can grow later but never shrink.</span>
          </span>
          <span className="urow-control vd-unit">
            <input type="number" min={0} max={65536} value={addDisk} disabled={!off} aria-label="New disk size"
              onChange={(e) => setAddDisk(Math.max(0, Math.floor(Number(e.target.value) || 0)))} />
            <span>GB</span>
          </span>
        </div>
      </div>

      <span className="section-label">CD drives</span>
      <div className="ucard pad st-form">
        {vm.media.map((m) => (
          <label key={m.target}>
            <span>{m.target}</span>
            <div className="vd-media">
              <PathField kind="iso" value={media[m.target] ?? ''} onChange={(p) => setMedia({ ...media, [m.target]: p })} placeholder="Empty" />
              <button type="button" className="ghost small" disabled={!media[m.target]} onClick={() => setMedia({ ...media, [m.target]: '' })}>
                <Icon name="eject" size={14} /> Eject
              </button>
            </div>
            <IsoShelf isos={host.isos} value={media[m.target] ?? ''} onPick={(p) => setMedia({ ...media, [m.target]: p })} />
          </label>
        ))}
        {!vm.media.length && <p className="muted small">This machine has no CD drive.</p>}
      </div>

      <span className="section-label">Network and start-up</span>
      <div className="ucard pad st-form">
        <div className="st-field">
          <span>Network</span>
          <select value={net} disabled={!off} onChange={(e) => {
            const m = e.target.value as NetMode;
            setNet(m);
            setIface(m === 'bridge' ? (bridges[0]?.name ?? '') : m === 'direct' ? (links[0]?.name ?? '') : '');
          }}>
            <option value="nat">Private network with internet (NAT)</option>
            <option value="bridge" disabled={!bridges.length}>On my network, through a bridge</option>
            <option value="direct" disabled={!links.length}>On my network, directly on a host interface</option>
            <option value="none">No network</option>
          </select>
          {(net === 'bridge' || net === 'direct') && (
            <select value={iface} disabled={!off} onChange={(e) => setIface(e.target.value)} aria-label="Host interface">
              {(net === 'bridge' ? bridges : links).map((i) => (
                <option key={i.name} value={i.name}>{i.name}</option>
              ))}
            </select>
          )}
          {vm.network.mac && <small className="muted">Network address (MAC) {vm.network.mac} is kept when you switch.</small>}
        </div>
        <label className="st-switch-row toggle">
          <span><b>Start with the server</b><small className="muted">Turn the machine on whenever the server starts</small></span>
          <input type="checkbox" role="switch" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
        </label>
      </div>

      <div className="vd-save">
        {error && <span className="error st-foot-error">{error}</span>}
        <button type="button" className="ghost" disabled={!dirty || saving} onClick={() => {
          setCpus(vm.cpus); setMem(vm.memory_mib); setGrow({}); setAddDisk(0); setNet(vm.network.mode); setIface(vm.network.source ?? '');
          setAutostart(vm.autostart); setMedia(Object.fromEntries(vm.media.map((m) => [m.target, m.path ?? ''])));
        }}>Undo</button>
        <button type="button" disabled={!dirty || saving} onClick={() => void save()}>{saving ? 'Saving…' : 'Save changes'}</button>
      </div>

      <span className="section-label">Copies and name</span>
      <div className="ucard ulist">
        <div className="urow">
          <span className="urow-icon"><Icon name="copy" size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">Clone</span>
            <span className="urow-desc">A full copy with its own disks and network address. The machine must be off.</span>
          </span>
          <span className="urow-control"><button type="button" className="small ghost" disabled={!off || !!vm.busy} onClick={onClone}>Clone…</button></span>
        </div>
        <div className="urow">
          <span className="urow-icon"><Icon name="pencil" size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">Rename</span>
            <span className="urow-desc">Its disk files keep their place. The machine must be off.</span>
          </span>
          <span className="urow-control"><button type="button" className="small ghost" disabled={!off || !!vm.busy} onClick={onRename}>Rename…</button></span>
        </div>
      </div>

      <span className="section-label vd-danger-label">Danger zone</span>
      <div className="ucard ulist vd-danger">
        <div className="urow">
          <span className="urow-icon"><Icon name="trash" size={17} /></span>
          <span className="urow-text">
            <span className="urow-title">Delete this machine</span>
            <span className="urow-desc">Removes it from this server, optionally with its disks. This can’t be undone.</span>
          </span>
          <span className="urow-control"><button type="button" className="small danger" disabled={!!vm.busy} onClick={onDelete}>Delete…</button></span>
        </div>
      </div>
    </>
  );
}

// ---------------- Snapshots ----------------

function Snapshots({ vm, onChanged }: { vm: VM; onChanged: () => void }) {
  const [snaps, setSnaps] = useState<VmSnapshot[] | null>(null);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    const r = await vmApi.snapshots(vm.name);
    setSnaps(r.ok ? r.data.snapshots : []);
    if (!r.ok) toast('error', 'Couldn’t list snapshots', r.error);
  }, [vm.name]);
  useEffect(() => {
    void load();
  }, [load]);
  const ok = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(name);
  const create = async () => {
    setBusy(true);
    const r = await vmApi.snapshot(vm.name, name);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t take the snapshot', r.error);
    toast('success', `Snapshot ${name} saved`);
    setName('');
    void load();
  };
  const revert = async (s: VmSnapshot) => {
    const yes = await confirmDialog({
      title: `Go back to ${s.name}?`,
      message: `${vm.name} returns to how it was ${fmtAgo(s.created)}${s.state === 'running' ? ', running' : ', switched off'}. Everything since then is lost unless you take a snapshot first.`,
      confirmLabel: 'Go back',
      danger: true,
    });
    if (!yes) return;
    setBusy(true);
    const r = await vmApi.revert(vm.name, s.name);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t go back to the snapshot', r.error);
    toast('success', `${vm.name} is back at ${s.name}`);
    void load();
    onChanged();
  };
  const remove = async (s: VmSnapshot) => {
    const yes = await confirmDialog({ title: `Delete snapshot ${s.name}?`, message: 'The machine stays as it is now; you just can’t go back to this point any more.', confirmLabel: 'Delete', danger: true });
    if (!yes) return;
    setBusy(true);
    const r = await vmApi.deleteSnapshot(vm.name, s.name);
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t delete the snapshot', r.error);
    void load();
  };
  return (
    <>
      <p className="st-lead">A snapshot saves the machine’s disks{isLive(vm) ? ' and memory' : ''} as they are now, so you can come back after an update or experiment.</p>
      <div className="vd-snap-new">
        <input value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="e.g. before-update" maxLength={64}
          onKeyDown={(e) => e.key === 'Enter' && ok && !busy && void create()} />
        <button type="button" disabled={!ok || busy || !!vm.busy} onClick={() => void create()}>
          <Icon name="camera" size={14} /> Take snapshot
        </button>
      </div>
      {name && !ok && <small className="error">Use letters, numbers and _ . - (no spaces).</small>}
      <div className="ucard ulist">
        {snaps === null ? (
          <div className="st-pad"><span className="st-spinner" /></div>
        ) : !snaps.length ? (
          <p className="muted st-pad">No snapshots yet.</p>
        ) : (
          snaps.map((s) => (
            <div key={s.name} className="urow">
              <span className="urow-icon"><Icon name="camera" size={17} /></span>
              <span className="urow-text">
                <span className="urow-title">{s.name} {s.current && <span className="chip good">Current</span>}</span>
                <span className="urow-desc">{fmtAgo(s.created)} · taken while {s.state === 'running' ? 'running' : s.state === 'paused' ? 'paused' : 'off'}</span>
              </span>
              <span className="urow-control">
                <button type="button" className="small ghost" disabled={busy || !!vm.busy} onClick={() => void revert(s)}>
                  <Icon name="rewind" size={13} /> Go back
                </button>
                <button type="button" className="icon-btn" title="Delete snapshot" disabled={busy || !!vm.busy} onClick={() => void remove(s)}>
                  <Icon name="trash" size={15} />
                </button>
              </span>
            </div>
          ))
        )}
      </div>
    </>
  );
}

// ---------------- Delete, clone, rename ----------------

function DeleteVm({ vm, onClose, onDone }: { vm: VM; onClose: () => void; onDone: () => void }) {
  const [disks, setDisks] = useState(vm.managed);
  return (
    <TypeConfirm
      title={`Delete ${vm.name}?`}
      word={vm.name}
      action="Delete machine"
      message={
        <>
          <b>{vm.name} will be removed from this server{isLive(vm) ? ' (it’s turned off first)' : ''}, with its snapshots.</b>
          <label className="vd-check" onClick={(e) => e.stopPropagation()}>
            <input type="checkbox" checked={disks} onChange={(e) => setDisks(e.target.checked)} />
            <span>Also delete its disks ({fmtGiB(diskTotal(vm))}). Disk images that came from elsewhere are always kept.</span>
          </label>
        </>
      }
      onClose={onClose}
      onConfirm={async () => {
        const r = await vmApi.remove(vm.name, vm.name, disks);
        if (!r.ok) return r.error ?? 'Couldn’t delete the machine';
        toast('success', `${vm.name} deleted`);
        onDone();
        return null;
      }}
    />
  );
}

function NameSheet({ kind, vm, taken, onClose, onDone }: { kind: 'clone' | 'rename'; vm: VM; taken: string[]; onClose: () => void; onDone: (name: string) => void }) {
  const [name, setName] = useState(kind === 'clone' ? `${vm.name} copy` : vm.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const clash = taken.some((t) => t.toLowerCase() === name.toLowerCase() && (kind === 'clone' || t !== vm.name));
  const ok = /^[A-Za-z0-9](?:[A-Za-z0-9 _.-]{0,46}[A-Za-z0-9_.-])?$/.test(name) && !clash && name !== vm.name;
  const go = async () => {
    setBusy(true);
    setError(null);
    const r = kind === 'clone' ? await vmApi.clone(vm.name, name) : await vmApi.rename(vm.name, name);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'That didn’t work');
    toast('success', kind === 'clone' ? `${name} is a copy of ${vm.name}` : `Renamed to ${name}`);
    onDone(name);
  };
  return (
    <Sheet title={kind === 'clone' ? `Clone ${vm.name}` : `Rename ${vm.name}`} icon={kind === 'clone' ? 'copy' : 'pencil'} onClose={onClose}
      footer={
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={onClose}>Cancel</button>
          <button type="button" disabled={!ok || busy} onClick={() => void go()}>{busy ? (kind === 'clone' ? 'Copying disks…' : 'Renaming…') : kind === 'clone' ? 'Clone' : 'Rename'}</button>
        </>
      }>
      <div className="st-form">
        <label>
          <span>{kind === 'clone' ? 'Name of the copy' : 'New name'}</span>
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={48} autoFocus onKeyDown={(e) => e.key === 'Enter' && ok && !busy && void go()} />
          <small className={clash ? 'error' : 'muted'}>{clash ? 'There’s already a machine with that name.' : kind === 'clone' ? `Copies ${fmtGiB(diskTotal(vm))} of disks; big disks take a while.` : 'Letters, numbers, spaces and _ . -'}</small>
        </label>
      </div>
    </Sheet>
  );
}
