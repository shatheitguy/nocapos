import { useEffect, useMemo, useState } from 'react';
import { dockerApi, emptySpec, type ContainerSpec, type DockerNetwork, type EnvVar, type NetLink, type PortMap, type SpecMount } from '../api/docker';
import { fileApi, type FileRoot } from '../api/files';
import { Icon } from '../components/Icon';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

// Edit a container (or add a new one), Portainer-style but in NoCapOS: image,
// ports, storage, networks incl. macvlan/ipvlan, environment and limits.
// Saving recreates the container; if anything fails the old one comes back.

/** Split a command line into words (quotes group words). */
function splitCmd(s: string): string[] {
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
const joinCmd = (cmd?: string[]) => (cmd ?? []).map((w) => (/\s|^$/.test(w) ? `"${w}"` : w)).join(' ');

const parseEnv = (text: string): EnvVar[] =>
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

export function ContainerEditor({
  containerId,
  onClose,
  onSaved,
  onApplying,
}: {
  /** null = a new container */
  containerId: string | null;
  onClose: () => void;
  onSaved: () => void;
  /** Called just before an existing container is recreated (it stops on purpose). */
  onApplying?: (name: string) => void;
}) {
  const [spec, setSpec] = useState<ContainerSpec | null>(containerId ? null : emptySpec());
  const [meta, setMeta] = useState<{ app?: string; stack?: string; system: boolean }>({ system: false });
  const [networks, setNetworks] = useState<DockerNetwork[]>([]);
  const [volumes, setVolumes] = useState<string[]>([]);
  const [cmd, setCmd] = useState('');
  const [envText, setEnvText] = useState<string | null>(null);
  const [picker, setPicker] = useState<number | null>(null); // mount index choosing a folder
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (containerId) {
      void dockerApi.spec(containerId).then((r) => {
        if (!r.ok) return setError(r.error ?? 'Could not read the container');
        setSpec(r.data.spec);
        setCmd(joinCmd(r.data.spec.cmd));
        setMeta({ app: r.data.app, stack: r.data.stack, system: r.data.system });
      });
    }
    void dockerApi.networks().then((r) => r.ok && setNetworks(r.data.networks));
    void dockerApi.volumes().then((r) => r.ok && setVolumes(r.data.volumes.map((v) => v.name)));
  }, [containerId]);

  const userNets = useMemo(() => networks.filter((n) => !n.builtin), [networks]);
  if (!spec) {
    return (
      <div className="ce">
        <div className="ce-body">{error ? <p className="error">{error}</p> : <span className="spinner" />}</div>
      </div>
    );
  }
  const set = <K extends keyof ContainerSpec>(k: K, v: ContainerSpec[K]) => setSpec({ ...spec, [k]: v });
  const setAt = <T,>(list: T[], i: number, v: Partial<T>) => list.map((x, j) => (j === i ? { ...x, ...v } : x));
  const readOnly = meta.system || !!meta.stack;
  const mainNet = spec.network_mode;
  const userMain = mainNet !== 'bridge' && mainNet !== 'host' && mainNet !== 'none';
  const mainLink = spec.networks.find((n) => n.name === mainNet);
  const extra = spec.networks.filter((n) => n.name !== mainNet);
  const driverOf = (name: string) => networks.find((n) => n.name === name)?.driver;

  const setMain = (name: string) => {
    const others = spec.networks.filter((n) => n.name !== mainNet && n.name !== name);
    const keep = spec.networks.find((n) => n.name === name);
    setSpec({
      ...spec,
      network_mode: name,
      networks: name === 'bridge' || name === 'host' || name === 'none' ? (name === 'bridge' ? others : []) : [keep ?? { name }, ...others],
      ports: name === 'host' || name === 'none' ? [] : spec.ports,
    });
  };
  const setMainIP = (ipv4: string) => setSpec({ ...spec, networks: spec.networks.map((n) => (n.name === mainNet ? { ...n, ipv4 } : n)) });
  const setExtra = (list: NetLink[]) => setSpec({ ...spec, networks: [...spec.networks.filter((n) => n.name === mainNet), ...list] });

  const save = async () => {
    setError('');
    const final: ContainerSpec = {
      ...spec,
      cmd: cmd.trim() ? splitCmd(cmd) : undefined,
      env: envText !== null ? parseEnv(envText) : spec.env.filter((e) => e.key.trim()),
      ports: spec.ports.filter((p) => p.container),
      mounts: spec.mounts.filter((m) => m.source && m.target),
      devices: spec.devices.filter((d) => d.trim()),
    };
    if (containerId) {
      const ok = await confirmDialog({
        title: `Apply changes to ${spec.name}?`,
        message: 'NoCapOS recreates the container with your changes; it restarts. Its data stays. If anything goes wrong, the current container is put back.',
        confirmLabel: 'Apply',
      });
      if (!ok) return;
      onApplying?.(spec.name);
    }
    setBusy(true);
    const r = containerId ? await dockerApi.edit(containerId, final) : await dockerApi.create(final);
    setBusy(false);
    if (!r.ok) return setError(r.error ?? 'Could not save');
    const warning = (r.data as { warning?: string }).warning;
    if (warning) toast('info', 'Changes applied', warning);
    else toast('success', containerId ? `${spec.name} updated` : `${spec.name} created`, meta.app ? 'Kept for future app updates too.' : undefined);
    onSaved();
  };

  return (
    <div className="ce">
      <header className="ce-head">
        <button type="button" className="ghost" onClick={onClose}>
          <Icon name="chevronLeft" size={15} /> Back
        </button>
        <h2>{containerId ? `Edit ${spec.name}` : 'Add Container'}</h2>
        {meta.app && <span className="chip">App Store: {meta.app}</span>}
      </header>
      <div className="ce-body">
        {meta.system && <p className="ce-note bad">This is one of NoCapOS's own containers; it can't be edited.</p>}
        {meta.stack && <p className="ce-note">This container belongs to the stack “{meta.stack}”. Edit the stack's compose file instead.</p>}
        {meta.app && <p className="ce-note">Changes are saved with the app, so they stay when the app updates. The image always follows the App Store version.</p>}

        <fieldset disabled={readOnly} className="ce-fields">
          <section className="ce-sec">
            <h3>General</h3>
            <div className="ce-grid">
              <label>
                Name
                <input value={spec.name} disabled={!!meta.app} spellCheck={false} onChange={(e) => set('name', e.target.value)} placeholder="my-app" />
              </label>
              <label>
                Image
                <input value={spec.image} disabled={!!meta.app} spellCheck={false} onChange={(e) => set('image', e.target.value)} placeholder="nginx:latest" />
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
              <label>
                Command <span className="muted">(optional)</span>
                <input value={cmd} spellCheck={false} onChange={(e) => setCmd(e.target.value)} placeholder="Image default" />
              </label>
            </div>
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
                    <input type="number" min={0} max={65535} value={p.host || ''} placeholder="auto" onChange={(e) => set('ports', setAt<PortMap>(spec.ports, i, { host: Number(e.target.value) || 0 }))} aria-label="Server port" />
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
                      <>
                        <input list="ce-volumes" value={m.source} spellCheck={false} placeholder="volume name" onChange={(e) => set('mounts', setAt<SpecMount>(spec.mounts, i, { source: e.target.value }))} aria-label="Volume" />
                      </>
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
                      {n.name} ({n.driver}
                      {n.subnet ? ` ${n.subnet}` : ''})
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
              Environment
              <button type="button" className="ghost ce-add" onClick={() => setEnvText(envText === null ? spec.env.map((e) => `${e.key}=${e.value}`).join('\n') : null)}>
                {envText === null ? 'Edit as text' : 'Edit as list'}
              </button>
              {envText === null && (
                <button type="button" className="ghost ce-add" onClick={() => set('env', [...spec.env, { key: '', value: '' }])}>
                  <Icon name="plus" size={13} /> Add
                </button>
              )}
            </h3>
            {envText !== null ? (
              <textarea className="ce-envtext" value={envText} spellCheck={false} rows={8} onChange={(e) => setEnvText(e.target.value)} placeholder={'KEY=value\nTZ=Asia/Dubai'} />
            ) : !spec.env.length ? (
              <p className="muted small">No variables.</p>
            ) : (
              <div className="ce-rows">
                {spec.env.map((e, i) => (
                  <div key={i} className="ce-row ce-env">
                    <input value={e.key} spellCheck={false} placeholder="NAME" onChange={(ev) => set('env', setAt<EnvVar>(spec.env, i, { key: ev.target.value }))} aria-label="Variable name" />
                    <input value={e.value} spellCheck={false} placeholder="value" onChange={(ev) => set('env', setAt<EnvVar>(spec.env, i, { value: ev.target.value }))} aria-label="Value" />
                    <button type="button" className="ghost icon-btn" aria-label="Remove variable" onClick={() => set('env', spec.env.filter((_, j) => j !== i))}>
                      <Icon name="close" size={13} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section className="ce-sec">
            <h3>Resources</h3>
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
        </fieldset>
      </div>
      <footer className="ce-foot">
        {error && <p className="error">{error}</p>}
        <span className="spacer" />
        <button type="button" className="ghost" onClick={onClose}>
          Cancel
        </button>
        <button type="button" disabled={busy || readOnly || !spec.name.trim() || !spec.image.trim()} onClick={() => void save()}>
          {busy ? <span className="spinner sm" /> : null} {busy ? (containerId ? 'Applying…' : 'Creating…') : containerId ? 'Apply Changes' : 'Create & Start'}
        </button>
      </footer>
      {picker !== null && (
        <FolderPicker
          onClose={() => setPicker(null)}
          onPick={(p) => {
            set('mounts', setAt<SpecMount>(spec.mounts, picker, { source: p }));
            setPicker(null);
          }}
        />
      )}
    </div>
  );
}

/** Choose a folder in a storage location; returns its path on the server. */
function FolderPicker({ onClose, onPick }: { onClose: () => void; onPick: (hostPath: string) => void }) {
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
  const load = (rt: string, p: string) => {
    setDirs(null);
    void fileApi.list(rt, p).then((r) => setDirs(r.ok ? r.data.entries.filter((e) => e.dir && e.name !== '.recycle').map((e) => e.name) : []));
  };
  useEffect(() => {
    if (root) load(root, path);
  }, [root, path]);
  const crumbs = path === '/' ? [] : path.slice(1).split('/');
  const choose = async () => {
    let target = path;
    if (newName.trim()) {
      const m = await fileApi.mkdir(root, path, newName.trim());
      if (!m.ok) return toast('error', 'Could not create the folder', m.error);
      target = (path === '/' ? '' : path) + '/' + newName.trim();
    }
    const r = await dockerApi.hostPath(root, target);
    if (!r.ok) return toast('error', 'Could not use that folder', r.error);
    onPick(r.data.path);
  };
  return (
    <div className="dialog-backdrop" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog ce-picker" role="dialog" aria-label="Choose a folder">
        <h3>Choose a folder</h3>
        <select value={root} onChange={(e) => {
          setRoot(e.target.value);
          setPath('/');
        }} aria-label="Location">
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
