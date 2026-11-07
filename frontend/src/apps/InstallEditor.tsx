import { useEffect, useState } from 'react';
import { storeApi, type StoreApp, type TemplateService } from '../api/appstore';
import { dockerApi, type ContainerSpec, type DockerNetwork } from '../api/docker';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { ChangeList, cleanSpec, SpecForm, specDiff, validateSpec } from './SpecForm';

// App Store → "Edit before install": the app's containers as a form, one tab
// per container. Changes are checked here and on the server, then saved with
// the app so updates keep them.

/** Problems across all of an app's containers (names and ports clash). */
function validateAll(svcs: TemplateService[], specs: Record<string, ContainerSpec>): string[] {
  const errs: string[] = [];
  const names = new Map<string, string>();
  const ports = new Map<string, string>();
  for (const s of svcs) {
    const spec = specs[s.service];
    for (const e of validateSpec(spec)) errs.push(svcs.length > 1 ? `${s.service}: ${e}` : e);
    const other = names.get(spec.name);
    if (other) errs.push(`${other} and ${s.service} can’t both be called ${spec.name}.`);
    names.set(spec.name, s.service);
    for (const p of spec.ports) {
      if (!p.host) continue;
      const k = `${p.host}/${p.protocol}`;
      const o = ports.get(k);
      if (o && o !== s.service) errs.push(`Port ${k} is used by both ${o} and ${s.service}.`);
      ports.set(k, s.service);
    }
  }
  return errs;
}

export function InstallEditor({ app, onClose, onStarted }: { app: StoreApp; onClose: () => void; onStarted: () => void }) {
  const [svcs, setSvcs] = useState<TemplateService[] | null>(null);
  const [network, setNetwork] = useState<string | undefined>();
  const [specs, setSpecs] = useState<Record<string, ContainerSpec>>({});
  const [tab, setTab] = useState('');
  const [networks, setNetworks] = useState<DockerNetwork[]>([]);
  const [volumes, setVolumes] = useState<string[]>([]);
  const [review, setReview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = async (defaults: boolean) => {
    const r = await storeApi.template(app.id, defaults);
    if (!r.ok) return setError(r.error ?? 'Couldn’t load the app’s settings');
    setSvcs(r.data.services);
    setNetwork(r.data.network);
    setSpecs(Object.fromEntries(r.data.services.map((s) => [s.service, s.spec])));
    setTab((t) => t || (app.web?.service ?? r.data.services[0]?.service ?? ''));
    setError('');
  };
  useEffect(() => {
    void load(false);
    void dockerApi.networks().then((r) => r.ok && setNetworks(r.data.networks));
    void dockerApi.volumes().then((r) => r.ok && setVolumes(r.data.volumes.map((v) => v.name)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [app.id]);

  if (!svcs) {
    return (
      <div className="ce">
        <div className="ce-body">{error ? <p className="error">{error}</p> : <span className="spinner" />}</div>
      </div>
    );
  }
  const cleaned = Object.fromEntries(svcs.map((s) => [s.service, cleanSpec(specs[s.service])]));
  const problems = validateAll(svcs, cleaned);
  const current = svcs.find((s) => s.service === tab) ?? svcs[0];
  const changes = svcs.map((s) => ({ service: s.service, list: specDiff(s.spec, cleaned[s.service], s.secrets) }));
  const anySaved = svcs.some((s) => s.saved);

  const install = async () => {
    setBusy(true);
    const r = await storeApi.installWith(app.id, cleaned);
    setBusy(false);
    if (!r.ok) {
      setReview(false);
      return setError(r.error ?? 'Couldn’t start the install');
    }
    toast('info', `Installing ${app.name} your way`, 'Your settings are saved with the app, so updates keep them.');
    onStarted();
  };

  return (
    <div className="ce">
      <header className="ce-head">
        <button type="button" className="ghost" onClick={review ? () => setReview(false) : onClose}>
          <Icon name="chevronLeft" size={15} /> Back
        </button>
        <h2>{review ? `Ready to install ${app.name}?` : `Set up ${app.name}`}</h2>
        <span className="chip">Before install</span>
      </header>
      <div className="ce-body">
        {review ? (
          <div className="sf-review">
            <p className="ce-note">Here's what's different from the App Store's setup. Everything else stays default.</p>
            {changes.map((c) => (
              <section key={c.service} className="ce-sec">
                {svcs.length > 1 && <h3>{c.service}</h3>}
                <ChangeList changes={c.list} />
              </section>
            ))}
          </div>
        ) : (
          <>
            <p className="ce-note">
              Tweak ports, storage, network, variables and limits before anything runs. Your changes stick through updates.
              {anySaved && ' Showing the changes you saved last time.'}
            </p>
            {svcs.length > 1 && (
              <nav className="segmented sf-tabs" aria-label="Containers">
                {svcs.map((s) => (
                  <button key={s.service} type="button" className={s.service === current.service ? 'on' : ''} onClick={() => setTab(s.service)}>
                    <Icon name="containers" size={13} /> {s.service}
                  </button>
                ))}
              </nav>
            )}
            <SpecForm
              key={current.service}
              spec={specs[current.service]}
              onChange={(s) => setSpecs({ ...specs, [current.service]: s })}
              networks={networks}
              volumes={volumes}
              options={{ pendingNetworks: network ? [network] : [], secrets: current.secrets, webPort: current.web_port, catalogImage: current.catalog_image }}
            />
          </>
        )}
      </div>
      <footer className="ce-foot">
        {error && <p className="error">{error}</p>}
        {!error && !review && problems.length > 0 && <p className="muted small">{problems[0]}</p>}
        <span className="spacer" />
        {!review && (
          <button type="button" className="ghost" onClick={() => void load(true)} title="Start over from the App Store's setup">
            Reset to defaults
          </button>
        )}
        {review ? (
          <button type="button" disabled={busy} onClick={() => void install()}>
            {busy ? <span className="spinner sm" /> : null} {busy ? 'Starting…' : 'Install'}
          </button>
        ) : (
          <button
            type="button"
            onClick={() => {
              setError('');
              if (problems.length) return setError(problems[0]);
              setReview(true);
            }}
          >
            Review & install
          </button>
        )}
      </footer>
    </div>
  );
}
