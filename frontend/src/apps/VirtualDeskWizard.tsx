import { useMemo, useState } from 'react';
import { fmtMiB, OS_LABEL, vmApi, type NetMode, type VM, type VmOS, type VmPreset, type VmStatus } from '../api/vms';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { Sheet, Working } from './StorageParts';
import { baseName, IsoShelf, OsBadge, PathField } from './VirtualDeskParts';

// New VM: what you'll install → install media → resources → name, network and
// a review. The server repeats every check (names, paths, sizes, interfaces).

const BLURB: Record<VmOS, string> = {
  windows11: 'UEFI firmware and a virtual TPM 2.0, so setup passes its hardware check.',
  windows10: 'UEFI firmware, and SATA disk + Intel network card that Windows setup knows without drivers.',
  linux: 'Ubuntu, Debian, Fedora and friends, with fast virtio disk and network.',
  android: 'Android-x86 and its relatives, with widely supported virtual hardware.',
  other: 'Anything else that boots from a CD or disk image.',
};

const NAME_RE = /^[A-Za-z0-9](?:[A-Za-z0-9 _.-]{0,46}[A-Za-z0-9_.-])?$/;

function Steps({ step, names }: { step: number; names: string[] }) {
  return (
    <ol className="st-steps">
      {names.map((n, i) => (
        <li key={n} className={i === step ? 'on' : i < step ? 'done' : ''}>
          <span>{i < step ? <Icon name="check" size={12} /> : i + 1}</span>
          {n}
        </li>
      ))}
    </ol>
  );
}

export function NewVmWizard({ status, vms, onClose, onDone }: { status: VmStatus; vms: VM[]; onClose: () => void; onDone: (name: string) => void }) {
  const host = status.host;
  const [step, setStep] = useState(0);
  const [preset, setPreset] = useState<VmPreset | null>(null);
  const [source, setSource] = useState<'iso' | 'disk'>('iso');
  const [iso, setIso] = useState('');
  const [iso2, setIso2] = useState('');
  const [image, setImage] = useState('');
  const [cpus, setCpus] = useState(2);
  const [mem, setMem] = useState(4096);
  const [disk, setDisk] = useState(32);
  const [virtio, setVirtio] = useState(true);
  const [name, setName] = useState('');
  const [net, setNet] = useState<NetMode>('nat');
  const [iface, setIface] = useState('');
  const [autostart, setAutostart] = useState(false);
  const [start, setStart] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const maxCpus = Math.max(1, host.cpus || 64);
  const maxMem = Math.max(1024, host.memory_mib || 65536);
  const bridges = host.interfaces.filter((i) => i.kind === 'bridge');
  const links = host.interfaces.filter((i) => i.kind !== 'wireless');
  const taken = useMemo(() => new Set(vms.map((v) => v.name.toLowerCase())), [vms]);
  const nameOk = NAME_RE.test(name) && !name.includes('..') && !name.includes('  ') && !taken.has(name.toLowerCase()) &&
    !['status', 'stats', 'install', 'isos'].includes(name.toLowerCase());

  const pick = (p: VmPreset) => {
    setPreset(p);
    setCpus(Math.min(p.cpus, maxCpus));
    setMem(Math.min(p.memory_mib, Math.max(1024, Math.floor(maxMem / 2 / 512) * 512)));
    setDisk(p.disk_gib);
    setVirtio(p.virtio);
    let n = p.id === 'linux' ? 'Linux' : OS_LABEL[p.id];
    for (let i = 2; taken.has(n.toLowerCase()); i++) n = `${p.id === 'linux' ? 'Linux' : OS_LABEL[p.id]} ${i}`;
    setName(n);
  };

  const ifaceFor = (mode: NetMode) => (mode === 'bridge' ? bridges[0]?.name : mode === 'direct' ? links[0]?.name : '') ?? '';
  const netOk = net === 'nat' || net === 'none' || !!iface;
  const next = [!!preset, source === 'iso' || !!image, cpus > 0 && mem > 0 && (source === 'disk' || disk > 0), nameOk && netOk][step];
  const win = preset?.id === 'windows11' || preset?.id === 'windows10';

  const create = async () => {
    if (!preset) return;
    setBusy(true);
    setError(null);
    const r = await vmApi.create({
      name, os: preset.id, cpus, memory_mib: mem, disk_gib: source === 'disk' ? 0 : disk,
      disk_image: source === 'disk' ? image : undefined, iso: source === 'iso' ? iso || undefined : undefined, iso2: iso2 || undefined,
      virtio, network: { mode: net, source: net === 'bridge' || net === 'direct' ? iface : undefined }, autostart, start,
    });
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Couldn’t create the machine');
    if (r.data.warning) toast('error', `${name} was created with a problem`, r.data.warning);
    else toast('success', `${name} is ready`, start ? 'It’s starting now. Open its screen to install.' : undefined);
    onDone(name);
    onClose();
  };

  return (
    <Sheet title="New virtual machine" icon="vm" wide onClose={onClose}
      footer={busy ? null : (
        <>
          {error && <span className="error st-foot-error">{error}</span>}
          <button type="button" className="ghost" onClick={() => (step ? (setStep(step - 1), setError(null)) : onClose())}>{step ? 'Back' : 'Cancel'}</button>
          {step < 3 ? (
            <button type="button" disabled={!next} onClick={() => setStep(step + 1)}>Continue</button>
          ) : (
            <button type="button" disabled={!next} onClick={() => void create()}>Create</button>
          )}
        </>
      )}>
      <Steps step={step} names={['System', 'Install media', 'Resources', 'Name & network']} />
      {busy ? (
        <Working text={`Creating ${name}…`} />
      ) : step === 0 ? (
        <>
          <p className="st-lead">What will you install? Virtual Desk picks virtual hardware that suits it.</p>
          <div className="vd-presets">
            {status.presets.map((p) => (
              <button key={p.id} type="button" className={`vd-preset ${preset?.id === p.id ? 'on' : ''}`} onClick={() => pick(p)}>
                <OsBadge os={p.id} size={42} />
                <span className="vd-preset-text">
                  <b>{p.title}</b>
                  <span>{BLURB[p.id]}</span>
                  <small className="muted">{p.cpus} CPUs · {fmtMiB(p.memory_mib)} · {p.disk_gib} GB{p.uefi ? ' · UEFI' : ''}{p.tpm ? ' · TPM' : ''}</small>
                </span>
              </button>
            ))}
          </div>
          {preset?.id === 'windows11' && (!status.tools.ovmf || !status.tools.swtpm) && (
            <p className="st-note warn"><Icon name="alert" size={14} />
              {!status.tools.ovmf ? 'Windows 11 needs UEFI firmware (OVMF), which isn’t installed on this server.' : 'There’s no TPM emulator (swtpm) on this server, so Windows 11 setup will refuse to continue.'} Use Install on the main page to add it.
            </p>
          )}
        </>
      ) : step === 1 ? (
        <div className="st-form">
          <div className="segmented vd-seg">
            <button type="button" className={source === 'iso' ? 'on' : ''} onClick={() => setSource('iso')}>Install from an ISO</button>
            <button type="button" className={source === 'disk' ? 'on' : ''} onClick={() => setSource('disk')}>Use an existing disk image</button>
          </div>
          {source === 'iso' ? (
            <>
              <label>
                <span>Installer ISO</span>
                <PathField kind="iso" value={iso} onChange={setIso} autoFocus />
                <small className="muted">Pick a file from Files, or put ISOs in {host.iso_dir} to see them below. You can also leave this empty and insert one later.</small>
              </label>
              <IsoShelf isos={host.isos} value={iso} onPick={setIso} />
            </>
          ) : (
            <label>
              <span>Disk image</span>
              <PathField kind="disk" value={image} onChange={setImage} autoFocus />
              <small className="muted">A .qcow2, .img, .raw, .vmdk, .vdi or .vhd(x) file. The machine uses it in place; it isn’t copied.</small>
            </label>
          )}
          <label>
            <span>Second CD drive (optional)</span>
            <PathField kind="iso" value={iso2} onChange={setIso2} placeholder={win ? 'e.g. the virtio drivers ISO' : 'Another ISO'} />
            <small className="muted">{win ? 'Handy for driver discs: Windows setup can load storage and network drivers from it.' : 'Inserted in a second drive at the same time.'}</small>
          </label>
        </div>
      ) : step === 2 ? (
        <div className="st-form">
          <label className="vd-slider">
            <span>Processors <b>{cpus}</b></span>
            <input type="range" min={1} max={maxCpus} value={cpus} onChange={(e) => setCpus(Number(e.target.value))} />
            <small className="muted">This server has {host.cpus || '?'} processor threads.</small>
          </label>
          <label className="vd-slider">
            <span>Memory <b>{fmtMiB(mem)}</b></span>
            <input type="range" min={512} max={maxMem} step={512} value={mem} onChange={(e) => setMem(Number(e.target.value))} />
            <small className={mem > maxMem * 0.75 ? 'error' : 'muted'}>
              {mem > maxMem * 0.75 ? 'That leaves little memory for NoCapOS and other machines.' : `This server has ${fmtMiB(host.memory_mib)} in total.`}
            </small>
          </label>
          {source === 'iso' && (
            <label>
              <span>Disk size</span>
              <div className="vd-unit">
                <input type="number" min={1} max={65536} value={disk} onChange={(e) => setDisk(Math.max(0, Math.floor(Number(e.target.value) || 0)))} />
                <span>GB</span>
              </div>
              <small className="muted">A new disk that only takes up the space the machine actually writes. You can grow it later.</small>
            </label>
          )}
          <label className="st-switch-row toggle">
            <span>
              <b>Fast virtio disk and network</b>
              <small className="muted">{win ? 'Faster, but Windows setup needs the virtio drivers ISO to see the disk.' : 'Recommended for Linux.'}</small>
            </span>
            <input type="checkbox" role="switch" checked={virtio} onChange={(e) => setVirtio(e.target.checked)} />
          </label>
        </div>
      ) : (
        <div className="st-form">
          <label>
            <span>Name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} maxLength={48} autoFocus />
            <small className={name && !nameOk ? 'error' : 'muted'}>
              {name && taken.has(name.toLowerCase()) ? 'There’s already a machine with that name.' : name && !nameOk ? 'Start with a letter or number; use letters, numbers, spaces and _ . - only.' : 'Shown in Virtual Desk; also the folder its disk is kept in.'}
            </small>
          </label>
          <div className="st-field">
            <span>Network</span>
            <select value={net} onChange={(e) => { const m = e.target.value as NetMode; setNet(m); setIface(ifaceFor(m)); }}>
              <option value="nat">Private network with internet (NAT)</option>
              <option value="bridge" disabled={!bridges.length}>On my network, through a bridge{bridges.length ? '' : ' (no bridge on this server)'}</option>
              <option value="direct" disabled={!links.length}>On my network, directly on a host interface</option>
              <option value="none">No network</option>
            </select>
            {(net === 'bridge' || net === 'direct') && (
              <select value={iface} onChange={(e) => setIface(e.target.value)} aria-label="Host interface">
                {(net === 'bridge' ? bridges : links).map((i) => (
                  <option key={i.name} value={i.name}>{i.name}</option>
                ))}
              </select>
            )}
            <small className="muted">
              {net === 'nat' ? 'The machine reaches the internet through this server; other devices can’t see it.' : net === 'direct'
                ? 'The machine gets its own address from your router. It can’t talk to this server itself over that link.'
                : net === 'bridge' ? 'The machine joins your network like another computer.' : 'No network card at all.'}
            </small>
          </div>
          <label className="st-switch-row toggle">
            <span><b>Start with the server</b><small className="muted">Turn the machine on whenever the server starts</small></span>
            <input type="checkbox" role="switch" checked={autostart} onChange={(e) => setAutostart(e.target.checked)} />
          </label>
          <label className="st-switch-row toggle">
            <span><b>Start it now</b><small className="muted">Then open its screen to run the installer</small></span>
            <input type="checkbox" role="switch" checked={start} onChange={(e) => setStart(e.target.checked)} />
          </label>
          <div className="st-summary">
            <div><span className="muted small">System</span><b>{preset?.title}</b></div>
            <div><span className="muted small">Resources</span><b>{cpus} CPU · {fmtMiB(mem)}</b></div>
            <div><span className="muted small">Disk</span><b className="st-wrap">{source === 'disk' ? baseName(image) : `${disk} GB, new`}</b></div>
            <div><span className="muted small">Installer</span><b className="st-wrap">{source === 'iso' ? baseName(iso) || 'None yet' : 'From the disk'}</b></div>
            <div><span className="muted small">Firmware</span><b>{preset?.uefi && status.tools.ovmf ? 'UEFI' : 'BIOS'}{preset?.tpm && status.tools.swtpm ? ' + TPM' : ''}</b></div>
          </div>
        </div>
      )}
    </Sheet>
  );
}
