import { useEffect, useState } from 'react';
import { dockerApi, type ContainerSpec, type DockerNetwork, type EnvVar, type NetLink, type PortMap, type SpecMount } from '../api/docker';
import { fileApi, type FileRoot } from '../api/files';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';

// The container settings form, shared by "Edit before install" (App Store),
// editing an installed container, and adding a new one: image and tag, name,
// network, ports, storage, environment, restart policy, limits, capabilities
// and devices. It only edits a ContainerSpec; saving is up to the caller.

/** Split a command line into words (quotes group words). */
export function splitCmd(s: string): string[] {
  const out: string[] = [];
  let cur = '';
  let quote: string | null = null;
  let has = false;
  for (const ch of s) {
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      has = true;
    } else if (/\s/.test(ch)) {
      if (has || cur) out.push(cur);
      cur = '';
      has = false;
    } else {
      cur += ch;
    }
  }
  if (has || cur) out.push(cur);
  return out;
}
export const joinCmd = (cmd?: string[]) => (cmd ?? []).map((w) => (/\s|^$/.test(w) ? `"${w}"` : w)).join(' ');

/** "ghcr.io/a/b:tag" → ["ghcr.io/a/b", "tag"]; no tag = latest. */
export function splitImage(ref: string): [string, string] {
  const slash = ref.lastIndexOf('/');
  const colon = ref.lastIndexOf(':');
  if (colon > slash) return [ref.slice(0, colon), ref.slice(colon + 1)];
  return [ref, 'latest'];
}
const joinImage = (name: string, tag: string) => (tag.trim() ? `${name.trim()}:${tag.trim()}` : name.trim());

const NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/;
const ENV_RE = /^[A-Za-z_][A-Za-z0-9_.-]*$/;
const CAP_RE = /^(CAP_)?[A-Z][A-Z0-9_]{1,39}$/i;
const TAG_RE = /^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/;
const DEV_RE = /^\/dev\/[A-Za-z0-9_./-]+$/;
const SECRET_RE = /\{\{secret:[a-z0-9_]{1,32}\}\}/;
const SECRETISH = /PASS|SECRET|TOKEN|KEY|PRIVATE/i;

const isBuiltinNet = (n: string) => n === 'bridge' || n === 'host' || n === 'none';

/** Problems with one container's settings (empty = good to go). */
export function validateSpec(s: ContainerSpec): string[] {
  const errs: string[] = [];
  if (!NAME_RE.test(s.name.trim())) errs.push('Name: letters, numbers, . _ and - only (start with a letter or number).');
  const [img, tag] = splitImage(s.image.trim());
  if (!img || /\s/.test(s.image)) errs.push('Image: enter one, e.g. nginx.');
  else if (!TAG_RE.test(tag)) errs.push(`Tag “${tag}” isn't valid.`);
  const seenPort = new Set<string>();
  for (const p of s.ports) {
    if (!p.container || p.container < 1 || p.container > 65535) errs.push('Ports: the container port must be 1–65535.');
    if (p.host < 0 || p.host > 65535) errs.push('Ports: the server port must be 1–65535 (or empty for auto).');
    if (p.host) {
      const k = `${p.host}/${p.protocol}`;
      if (seenPort.has(k)) errs.push(`Port ${k} is used twice.`);
      seenPort.add(k);
    }
  }
  if ((s.network_mode === 'host' || s.network_mode === 'none') && s.ports.length) errs.push('Ports can’t be published with host or no networking.');
  for (const m of s.mounts) {
    if (!m.target.startsWith('/')) errs.push(`Storage: “${m.target || '(empty)'}” inside the container must start with /.`);
    if (m.type === 'bind' && !m.source.startsWith('/')) errs.push(`Storage: “${m.source || '(empty)'}” must be a full path on the server, like /srv/data.`);
    if (m.type === 'volume' && !NAME_RE.test(m.source)) errs.push(`Storage: volume name “${m.source || '(empty)'}” isn't valid.`);
  }
  const targets = new Set<string>();
  for (const m of s.mounts) {
    if (targets.has(m.target)) errs.push(`Storage: ${m.target} is mounted twice.`);
    targets.add(m.target);
  }
  const keys = new Set<string>();
  for (const e of s.env) {
    if (!ENV_RE.test(e.key.trim())) errs.push(`Variable name “${e.key}” isn't valid.`);
    if (keys.has(e.key.trim())) errs.push(`Variable ${e.key} is set twice.`);
    keys.add(e.key.trim());
  }
  for (const c of s.cap_add ?? []) if (c.trim() && !CAP_RE.test(c.trim())) errs.push(`Capability “${c}” isn't valid (e.g. NET_ADMIN).`);
  for (const d of s.devices) {
    const [h, c] = d.split(':');
    if (!DEV_RE.test(h) || h.includes('..') || (c !== undefined && (!DEV_RE.test(c) || c.includes('..')))) errs.push(`Device “${d}” must be under /dev.`);
  }
  if (s.memory_mb && s.memory_mb < 6) errs.push('A memory limit must be at least 6 MB.');
  for (const n of s.networks) if (n.ipv4 && !/^\d{1,3}(\.\d{1,3}){3}$/.test(n.ipv4)) errs.push(`“${n.ipv4}” isn't an IPv4 address.`);
  return Array.from(new Set(errs));
}

/** The spec as it will be sent: blank rows dropped, values trimmed. */
export function cleanSpec(s: ContainerSpec): ContainerSpec {
  return {
    ...s,
    name: s.name.trim(),
    image: s.image.trim(),
    env: s.env.filter((e) => e.key.trim()).map((e) => ({ key: e.key.trim(), value: e.value })),
    ports: s.ports.filter((p) => p.container),
    mounts: s.mounts.filter((m) => m.source.trim() || m.target.trim()).map((m) => ({ ...m, source: m.source.trim(), target: m.target.trim() })),
    devices: s.devices.map((d) => d.trim()).filter(Boolean),
    cap_add: (s.cap_add ?? []).map((c) => c.trim().toUpperCase().replace(/^CAP_/, '')).filter(Boolean),
  };
}

export interface Change {
  kind: 'add' | 'remove' | 'change';
  text: string;
}

const portText = (p: PortMap) => `${p.host || 'auto'} → ${p.container}/${p.protocol}`;
const mountText = (m: SpecMount) => `${m.source} → ${m.target}${m.read_only ? ' (read only)' : ''}`;

/** What changed between two specs, in plain words (secret values never shown). */
export function specDiff(a: ContainerSpec, b: ContainerSpec, secrets: string[] = []): Change[] {
  const out: Change[] = [];
  const field = (label: string, x: unknown, y: unknown, fmt: (v: unknown) => string = (v) => String(v ?? '') || '—') => {
    if (JSON.stringify(x ?? null) !== JSON.stringify(y ?? null)) out.push({ kind: 'change', text: `${label}: ${fmt(x)} → ${fmt(y)}` });
  };
  field('Image', a.image, b.image);
  field('Name', a.name, b.name);
  field('Restart', a.restart, b.restart);
  field('Network', a.network_mode, b.network_mode);
  field('Networks', a.networks, b.networks, (v) => (v as NetLink[]).map((n) => n.name + (n.ipv4 ? ` @ ${n.ipv4}` : '')).join(', ') || '—');
  field('Command', joinCmd(a.cmd), joinCmd(b.cmd));
  field('Hostname', a.hostname, b.hostname);
  field('Memory limit', a.memory_mb, b.memory_mb, (v) => (v ? `${v} MB` : 'none'));
  field('CPU limit', a.cpus, b.cpus, (v) => (v ? `${v} cores` : 'none'));
  field('Privileged', a.privileged, b.privileged, (v) => (v ? 'on' : 'off'));
  const sets = (label: string, x: string[], y: string[]) => {
    const xs = new Set(x);
    const ys = new Set(y);
    for (const v of y) if (!xs.has(v)) out.push({ kind: 'add', text: `${label} ${v}` });
    for (const v of x) if (!ys.has(v)) out.push({ kind: 'remove', text: `${label} ${v}` });
  };
  sets('Port', a.ports.map(portText), b.ports.map(portText));
  sets('Storage', a.mounts.map(mountText), b.mounts.map(mountText));
  sets('Device', a.devices, b.devices);
  sets('Capability', a.cap_add ?? [], b.cap_add ?? []);
  const env = (l: EnvVar[]) => new Map(l.map((e) => [e.key, e.value]));
  const ea = env(a.env);
  const eb = env(b.env);
  const hidden = (k: string, v: string) => (secrets.includes(k) || SECRET_RE.test(v) || SECRETISH.test(k) ? '••••' : v);
  for (const [k, v] of eb) {
    if (!ea.has(k)) out.push({ kind: 'add', text: `Variable ${k}=${hidden(k, v)}` });
    else if (ea.get(k) !== v) out.push({ kind: 'change', text: `Variable ${k}: ${hidden(k, ea.get(k)!)} → ${hidden(k, v)}` });
  }
  for (const k of ea.keys()) if (!eb.has(k)) out.push({ kind: 'remove', text: `Variable ${k}` });
  return out;
}

/** A list of changes, for review steps. */
export function ChangeList({ changes }: { changes: Change[] }) {
  if (!changes.length) return <p className="muted small">No changes — same as before.</p>;
  return (
    <ul className="sf-changes">
      {changes.map((c, i) => (
        <li key={i} className={c.kind}>
          <span className="sf-change-mark">{c.kind === 'add' ? '+' : c.kind === 'remove' ? '−' : '~'}</span>
          <span>{c.text}</span>
        </li>
      ))}
    </ul>
  );
}

export interface SpecFormOptions {
  /** Name can't change (installed App Store containers keep theirs). */
  lockName?: boolean;
  /** Networks that don't exist yet but will (the app's own network). */
  pendingNetworks?: string[];
  /** Variables that hold generated secrets (shown masked). */
  secrets?: string[];
  /** The web UI's container port (its server port may be left on auto). */
  webPort?: number;
  /** The App Store's image: changing it pins your own. */
  catalogImage?: string;
}

export function SpecForm({
  spec,
  onChange,
  networks,
  volumes,
  disabled,
  options = {},
}: {
  spec: ContainerSpec;
  onChange: (s: ContainerSpec) => void;
  networks: DockerNetwork[];
  volumes: string[];
  disabled?: boolean;
  options?: SpecFormOptions;
}) {
  const [cmd, setCmd] = useState(joinCmd(spec.cmd));
  const [envText, setEnvText] = useState<string | null>(null);
  const [picker, setPicker] = useState<number | null>(null);
  const [shown, setShown] = useState<Set<number>>(new Set());
  const [capDraft, setCapDraft] = useState('');
  useEffect(() => setCmd(joinCmd(spec.cmd)), [spec.cmd]);

  const set = <K extends keyof ContainerSpec>(k: K, v: ContainerSpec[K]) => onChange({ ...spec, [k]: v });
  const setAt = <T,>(list: T[], i: number, v: Partial<T>) => list.map((x, j) => (j === i ? { ...x, ...v } : x));
  const userNets = [
    ...(options.pendingNetworks ?? []).filter((n) => !networks.some((x) => x.name === n)).map((n) => ({ name: n, driver: 'bridge', subnet: undefined as string | undefined, pending: true })),
    ...networks.filter((n) => !n.builtin).map((n) => ({ name: n.name, driver: n.driver, subnet: n.subnet, pending: false })),
  ];
  const mainNet = spec.network_mode;
  const userMain = !isBuiltinNet(mainNet);
  const mainLink = spec.networks.find((n) => n.name === mainNet);
  const extra = spec.networks.filter((n) => n.name !== mainNet);
  const driverOf = (name: string) => userNets.find((n) => n.name === name)?.driver;
  const [imgName, imgTag] = splitImage(spec.image);
  const pinned = !!options.catalogImage && spec.image.trim() !== options.catalogImage;

  const setMain = (name: string) => {
    const others = spec.networks.filter((n) => n.name !== mainNet && n.name !== name);
    const keep = spec.networks.find((n) => n.name === name);
    onChange({
      ...spec,
      network_mode: name,
      networks: isBuiltinNet(name) ? (name === 'bridge' ? others : []) : [keep ?? { name }, ...others],
      ports: name === 'host' || name === 'none' ? [] : spec.ports,
    });
  };
  const setMainIP = (ipv4: string) => onChange({ ...spec, networks: spec.networks.map((n) => (n.name === mainNet ? { ...n, ipv4 } : n)) });
  const setExtra = (list: NetLink[]) => onChange({ ...spec, networks: [...spec.networks.filter((n) => n.name === mainNet), ...list] });
  const masked = (e: EnvVar, i: number) => !shown.has(i) && ((options.secrets ?? []).includes(e.key) || SECRET_RE.test(e.value) || (SECRETISH.test(e.key) && !!e.value));
  const parseEnvText = (text: string): EnvVar[] =>
    text
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l && !l.startsWith('#'))
      .map((l) => {
        const i = l.indexOf('=');
        const key = (i < 0 ? l : l.slice(0, i)).replace(/^export\s+/, '').trim();
        let value = i < 0 ? '' : l.slice(i + 1).trim();
        if (/^(['"]).*\1$/.test(value)) value = value.slice(1, -1);
        return { key, value };
      });
  const caps = spec.cap_add ?? [];
  const addCap = (c: string) => {
    const v = c.trim().toUpperCase().replace(/^CAP_/, '');
    if (v && !caps.includes(v)) set('cap_add', [...caps, v]);
    setCapDraft('');
  };

  return (
    <fieldset disabled={disabled} className="ce-fields">
      <section className="ce-sec">
        <h3>General</h3>
        <div className="ce-grid">
          <label>
            Image
            <input value={imgName} spellCheck={false} onChange={(e) => set('image', joinImage(e.target.value, imgTag))} placeholder="nginx" />
          </label>
          <label>
            Tag
            <input value={imgTag} spellCheck={false} onChange={(e) => set('image', joinImage(imgName, e.target.value))} placeholder="latest" />
          </label>
          <label>
            Container name
            <input value={spec.name} disabled={options.lockName} spellCheck={false} onChange={(e) => set('name', e.target.value)} placeholder="my-app" />
          </label>
          <label>
            Restart
            <select value={spec.restart} onChange={(e) => set('restart', e.target.value as ContainerSpec['restart'])}>
              <option value="unless-stopped">Unless stopped</option>
              <option value="always">Always</option>
              <option value="on-failure">On failure</option>
              <option value="no">Never</option>
            </select>
          </label>
          <label className="ce-wide">
            Command <span className="muted">(optional)</span>
            <input
              value={cmd}
              spellCheck={false}
              onChange={(e) => setCmd(e.target.value)}
              onBlur={() => set('cmd', cmd.trim() ? splitCmd(cmd) : undefined)}
              placeholder="Image default"
            />
          </label>
        </div>
        {options.catalogImage && (
          <p className="muted small">
            {pinned ? (
              <>
                Pinned to your own image — App Store updates won't change it.{' '}
                <button type="button" className="as-link sf-inline" onClick={() => set('image', options.catalogImage!)}>
                  Follow the App Store again
                </button>
              </>
            ) : (
              'Follows the App Store: updates bring new versions. Change the image or tag to pin your own.'
            )}
          </p>
        )}
      </section>

      <section className="ce-sec">
        <h3>Network</h3>
        <div className="ce-grid">
          <label>
            Network
            <select value={mainNet} onChange={(e) => setMain(e.target.value)}>
              <option value="bridge">Default (bridge)</option>
              <option value="host">Host (share the server's network)</option>
              <option value="none">None (no network)</option>
              {userNets.map((n) => (
                <option key={n.name} value={n.name}>
                  {n.name} ({n.pending ? 'the app’s own, made on install' : `${n.driver}${n.subnet ? ` ${n.subnet}` : ''}`})
                </option>
              ))}
            </select>
          </label>
          {userMain && (
            <label>
              Fixed IP address <span className="muted">(optional)</span>
              <input value={mainLink?.ipv4 ?? ''} spellCheck={false} placeholder={networks.find((n) => n.name === mainNet)?.subnet ?? 'automatic'} onChange={(e) => setMainIP(e.target.value.trim())} />
            </label>
          )}
        </div>
        {userMain && (driverOf(mainNet) === 'macvlan' || driverOf(mainNet) === 'ipvlan') && (
          <p className="muted small">On a {driverOf(mainNet)} network the container gets its own address on your LAN. The server itself can't reach it directly (a Linux limit); other devices can.</p>
        )}
        {!userNets.some((n) => n.driver === 'macvlan' || n.driver === 'ipvlan') && (
          <p className="muted small">Want it on your LAN with its own IP? Make a macvlan network in Containers → Networks first.</p>
        )}
        {mainNet !== 'host' && mainNet !== 'none' && (
          <>
            {extra.map((n, i) => (
              <div key={i} className="ce-row ce-net">
                <select value={n.name} onChange={(e) => setExtra(extra.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} aria-label="Also on network">
                  {userNets.map((u) => (
                    <option key={u.name} value={u.name}>
                      {u.name} ({u.driver})
                    </option>
                  ))}
                </select>
                <input value={n.ipv4 ?? ''} placeholder="IP (optional)" spellCheck={false} onChange={(e) => setExtra(extra.map((x, j) => (j === i ? { ...x, ipv4: e.target.value.trim() } : x)))} aria-label="IP on that network" />
                <button type="button" className="ghost icon-btn" aria-label="Remove network" onClick={() => setExtra(extra.filter((_, j) => j !== i))}>
                  <Icon name="close" size={13} />
                </button>
              </div>
            ))}
            {userNets.some((u) => u.name !== mainNet) && (
              <button type="button" className="ghost ce-add-line" onClick={() => setExtra([...extra, { name: userNets.find((u) => u.name !== mainNet)!.name }])}>
                <Icon name="plus" size={13} /> Also join another network
              </button>
            )}
          </>
        )}
      </section>

      <section className="ce-sec">
        <h3>
          Ports
          <button type="button" className="ghost ce-add" disabled={mainNet === 'host' || mainNet === 'none'} onClick={() => set('ports', [...spec.ports, { host: 0, container: 0, protocol: 'tcp' }])}>
            <Icon name="plus" size={13} /> Add
          </button>
        </h3>
        {mainNet === 'host' ? (
          <p className="muted small">With host networking the container uses the server's ports directly.</p>
        ) : !spec.ports.length ? (
          <p className="muted small">No ports published.</p>
        ) : (
          <div className="ce-rows">
            <div className="ce-row ce-hdr">
              <span>On the server</span>
              <span>In the container</span>
              <span>Protocol</span>
              <span />
            </div>
            {spec.ports.map((p, i) => (
              <div key={i} className="ce-row">
                <input
                  type="number"
                  min={0}
                  max={65535}
                  value={p.host || ''}
                  placeholder={options.webPort === p.container ? 'auto (web page)' : 'auto'}
                  onChange={(e) => set('ports', setAt<PortMap>(spec.ports, i, { host: Number(e.target.value) || 0 }))}
                  aria-label="Server port"
                />
                <input type="number" min={1} max={65535} value={p.container || ''} onChange={(e) => set('ports', setAt<PortMap>(spec.ports, i, { container: Number(e.target.value) || 0 }))} aria-label="Container port" />
                <select value={p.protocol} onChange={(e) => set('ports', setAt<PortMap>(spec.ports, i, { protocol: e.target.value as 'tcp' | 'udp' }))} aria-label="Protocol">
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                </select>
                <button type="button" className="ghost icon-btn" aria-label="Remove port" onClick={() => set('ports', spec.ports.filter((_, j) => j !== i))}>
                  <Icon name="close" size={13} />
                </button>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="ce-sec">
        <h3>
          Storage
          <button type="button" className="ghost ce-add" onClick={() => set('mounts', [...spec.mounts, { type: 'bind', source: '', target: '', read_only: false }])}>
            <Icon name="plus" size={13} /> Add
          </button>
        </h3>
        {!spec.mounts.length ? (
          <p className="muted small">Nothing is kept outside the container: its data is lost when it's recreated. Add a folder for anything worth keeping.</p>
        ) : (
          <div className="ce-rows">
            {spec.mounts.map((m, i) => (
              <div key={i} className="ce-mount">
                <select value={m.type} onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { type: e.target.value as SpecMount['type'], source: '' }))} aria-label="Storage type">
                  <option value="bind">Folder on the server</option>
                  <option value="volume">Docker volume</option>
                </select>
                {m.type === 'bind' ? (
                  <div className="ce-src">
                    <input value={m.source} spellCheck={false} placeholder="/srv/nocapos/app-data" onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { source: e.target.value }))} aria-label="Folder on the server" />
                    <button type="button" className="ghost" onClick={() => setPicker(i)}>
                      <Icon name="folder" size={13} /> Choose
                    </button>
                  </div>
                ) : (
                  <input list="ce-volumes" value={m.source} spellCheck={false} placeholder="volume name" onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { source: e.target.value }))} aria-label="Volume" />
                )}
                <span className="ce-arrow">→</span>
                <input value={m.target} spellCheck={false} placeholder="/data (in the container)" onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { target: e.target.value }))} aria-label="Folder in the container" />
                <label className="ce-ro">
                  <input type="checkbox" checked={m.read_only} onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { read_only: e.target.checked }))} /> Read only
                </label>
                <button type="button" className="ghost icon-btn" aria-label="Remove" onClick={() => set('mounts', spec.mounts.filter((_, j) => j !== i))}>
                  <Icon name="close" size={13} />
                </button>
              </div>
            ))}
            <datalist id="ce-volumes">
              {volumes.map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
          </div>
        )}
      </section>

      <section className="ce-sec">
        <h3>
          Environment
          <button
            type="button"
            className="ghost ce-add"
            onClick={() => {
              if (envText === null) setEnvText(spec.env.map((e) => `${e.key}=${e.value}`).join('\n'));
              else {
                set('env', parseEnvText(envText));
                setEnvText(null);
              }
            }}
          >
            {envText === null ? 'Edit as text' : 'Done'}
          </button>
          {envText === null && (
            <button type="button" className="ghost ce-add" onClick={() => set('env', [...spec.env, { key: '', value: '' }])}>
              <Icon name="plus" size={13} /> Add
            </button>
          )}
        </h3>
        {envText !== null ? (
          <textarea
            className="ce-envtext"
            value={envText}
            spellCheck={false}
            rows={8}
            onChange={(e) => {
              setEnvText(e.target.value);
              onChange({ ...spec, env: parseEnvText(e.target.value) });
            }}
            placeholder={'KEY=value\nTZ=Asia/Dubai'}
          />
        ) : !spec.env.length ? (
          <p className="muted small">No variables.</p>
        ) : (
          <div className="ce-rows">
            {spec.env.map((e, i) => {
              const hide = masked(e, i);
              const secretish = (options.secrets ?? []).includes(e.key) || SECRET_RE.test(e.value) || SECRETISH.test(e.key);
              return (
                <div key={i} className="ce-row ce-env sf-env">
                  <input value={e.key} spellCheck={false} placeholder="NAME" onChange={(ev) => set('env', setAt<EnvVar>(spec.env, i, { key: ev.target.value }))} aria-label="Variable name" />
                  <input
                    type={hide ? 'password' : 'text'}
                    value={e.value}
                    spellCheck={false}
                    autoComplete="off"
                    placeholder="value"
                    onChange={(ev) => set('env', setAt<EnvVar>(spec.env, i, { value: ev.target.value }))}
                    aria-label="Value"
                  />
                  {secretish ? (
                    <button
                      type="button"
                      className="ghost icon-btn"
                      title={hide ? 'Show' : 'Hide'}
                      onClick={() => setShown((s) => {
                        const n = new Set(s);
                        if (n.has(i)) n.delete(i);
                        else n.add(i);
                        return n;
                      })}
                    >
                      <Icon name="eye" size={13} />
                    </button>
                  ) : (
                    <span />
                  )}
                  <button type="button" className="ghost icon-btn" aria-label="Remove variable" onClick={() => set('env', spec.env.filter((_, j) => j !== i))}>
                    <Icon name="close" size={13} />
                  </button>
                </div>
              );
            })}
          </div>
        )}
        {spec.env.some((e) => SECRET_RE.test(e.value)) && <p className="muted small">Values like {'{{secret:…}}'} become strong random passwords on install. Type your own to replace one.</p>}
      </section>

      <section className="ce-sec">
        <h3>Resources & access</h3>
        <div className="ce-grid">
          <label>
            Memory limit (MB) <span className="muted">0 = none</span>
            <input type="number" min={0} value={spec.memory_mb || 0} onChange={(e) => set('memory_mb', Math.max(0, Number(e.target.value) || 0))} />
          </label>
          <label>
            CPU limit (cores) <span className="muted">0 = none</span>
            <input type="number" min={0} step={0.25} value={spec.cpus || 0} onChange={(e) => set('cpus', Math.max(0, Number(e.target.value) || 0))} />
          </label>
          <label>
            Hostname <span className="muted">(optional)</span>
            <input value={spec.hostname ?? ''} disabled={mainNet === 'host'} spellCheck={false} onChange={(e) => set('hostname', e.target.value.trim())} />
          </label>
          <label className="ce-check">
            <input type="checkbox" checked={spec.privileged} onChange={(e) => set('privileged', e.target.checked)} /> Privileged <span className="muted">(full access to the server; only if the app needs it)</span>
          </label>
        </div>
        <div className="ce-devices">
          <span className="small">Extra capabilities</span>
          <div className="sf-chips">
            {caps.map((c) => (
              <span key={c} className="chip sf-chip">
                {c}
                <button type="button" className="ghost icon-btn" aria-label={`Remove ${c}`} onClick={() => set('cap_add', caps.filter((x) => x !== c))}>
                  <Icon name="close" size={11} />
                </button>
              </span>
            ))}
            <input
              className="sf-cap-input"
              value={capDraft}
              list="sf-caps"
              spellCheck={false}
              placeholder="e.g. NET_ADMIN"
              onChange={(e) => setCapDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  addCap(capDraft);
                }
              }}
              onBlur={() => capDraft.trim() && addCap(capDraft)}
              aria-label="Add a capability"
            />
            <datalist id="sf-caps">
              {['NET_ADMIN', 'NET_RAW', 'SYS_ADMIN', 'SYS_TIME', 'SYS_NICE', 'SYS_PTRACE', 'MKNOD', 'IPC_LOCK', 'DAC_READ_SEARCH'].map((c) => (
                <option key={c} value={c} />
              ))}
            </datalist>
          </div>
        </div>
        <div className="ce-devices">
          <span className="small">Devices</span>
          {spec.devices.map((d, i) => (
            <div key={i} className="ce-row ce-net">
              <input value={d} spellCheck={false} placeholder="/dev/dri" onChange={(e) => set('devices', spec.devices.map((x, j) => (j === i ? e.target.value : x)))} aria-label="Device" />
              <button type="button" className="ghost icon-btn" aria-label="Remove device" onClick={() => set('devices', spec.devices.filter((_, j) => j !== i))}>
                <Icon name="close" size={13} />
              </button>
            </div>
          ))}
          <div className="ce-inline">
            {!spec.devices.includes('/dev/dri') && (
              <button type="button" className="ghost ce-add-line" onClick={() => set('devices', [...spec.devices, '/dev/dri'])}>
                <Icon name="plus" size={13} /> GPU (/dev/dri)
              </button>
            )}
            <button type="button" className="ghost ce-add-line" onClick={() => set('devices', [...spec.devices, ''])}>
              <Icon name="plus" size={13} /> Other device
            </button>
          </div>
        </div>
      </section>
      {picker !== null && (
        <FolderPicker
          onClose={() => setPicker(null)}
          onPick={(p) => {
            set('mounts', setAt<SpecMount>(spec.mounts, picker, { source: p }));
            setPicker(null);
          }}
        />
      )}
    </fieldset>
  );
}

/** Choose a folder in a storage location; returns its path on the server. */
export function FolderPicker({ onClose, onPick }: { onClose: () => void; onPick: (hostPath: string) => void }) {
  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [root, setRoot] = useState('');
  const [path, setPath] = useState('/');
  const [dirs, setDirs] = useState<string[] | null>(null);
  const [newName, setNewName] = useState('');
  useEffect(() => {
    void fileApi.roots().then((r) => {
      if (!r.ok) return;
      setRoots(r.data);
      setRoot(r.data[0]?.id ?? '');
    });
  }, []);
  useEffect(() => {
    if (!root) return;
    setDirs(null);
    void fileApi.list(root, path).then((r) => setDirs(r.ok ? r.data.entries.filter((e) => e.dir && e.name !== '.recycle').map((e) => e.name) : []));
  }, [root, path]);
  const crumbs = path === '/' ? [] : path.slice(1).split('/');
  const choose = async () => {
    let target = path;
    if (newName.trim()) {
      const m = await fileApi.mkdir(root, path, newName.trim());
      if (!m.ok) return toast('error', 'Couldn’t create the folder', m.error);
      target = (path === '/' ? '' : path) + '/' + newName.trim();
    }
    const r = await dockerApi.hostPath(root, target);
    if (!r.ok) return toast('error', 'Couldn’t use that folder', r.error);
    onPick(r.data.path);
  };
  return (
    <div className="dialog-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog ce-picker" role="dialog" aria-label="Choose a folder">
        <h3>Choose a folder</h3>
        <select
          value={root}
          onChange={(e) => {
            setRoot(e.target.value);
            setPath('/');
          }}
          aria-label="Location"
        >
          {roots.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
        <div className="ci-crumbs">
          <button type="button" className="ghost" onClick={() => setPath('/')}>
            {roots.find((r) => r.id === root)?.name ?? 'Location'}
          </button>
          {crumbs.map((c, i) => (
            <span key={i}>
              <Icon name="chevronRight" size={12} />
              <button type="button" className="ghost" onClick={() => setPath('/' + crumbs.slice(0, i + 1).join('/'))}>
                {c}
              </button>
            </span>
          ))}
        </div>
        <div className="ci-folders">
          {dirs === null ? (
            <span className="spinner sm" />
          ) : !dirs.length ? (
            <p className="muted small">No folders here.</p>
          ) : (
            dirs.map((d) => (
              <button key={d} type="button" className="ci-folder" onClick={() => setPath((path === '/' ? '' : path) + '/' + d)}>
                <Icon name="folder" size={14} /> {d}
              </button>
            ))
          )}
        </div>
        <input value={newName} placeholder="New folder here (optional)" onChange={(e) => setNewName(e.target.value)} />
        <div className="dialog-actions">
          <button type="button" className="ghost" onClick={onClose}>
            Cancel
          </button>
          <button type="button" disabled={!root} onClick={() => void choose()}>
            Use {newName.trim() ? `“${newName.trim()}”` : 'This Folder'}
          </button>
        </div>
      </div>
    </div>
  );
}
