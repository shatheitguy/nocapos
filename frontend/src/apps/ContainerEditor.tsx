import { useEffect, useState } from 'react';
import { storeApi, type TemplateService } from '../api/appstore';
import { dockerApi, emptySpec, type ContainerSpec, type DockerNetwork } from '../api/docker';
import { Icon } from '../components/Icon';
import { toast } from '../state/toasts';
import { ChangeList, cleanSpec, SpecForm, specDiff, validateSpec } from './SpecForm';

// Edit a container (or add a new one) with the shared settings form. Saving
// an existing container shows what changes first, then recreates it; its
// volumes and folders stay, and if anything fails the old one comes back.

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
  const [original, setOriginal] = useState<ContainerSpec | null>(null);
  const [meta, setMeta] = useState<{ app?: string; service?: string; stack?: string; system: boolean }>({ system: false });
  const [networks, setNetworks] = useState<DockerNetwork[]>([]);
  const [volumes, setVolumes] = useState<string[]>([]);
  const [appInfo, setAppInfo] = useState<TemplateService | null>(null);
  const [review, setReview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (containerId) {
      void dockerApi.spec(containerId).then((r) => {
        if (!r.ok) return setError(r.error ?? 'Couldn’t read the container');
        const s = { ...r.data.spec, cap_add: r.data.spec.cap_add ?? [] };
        setSpec(s);
        setOriginal(s);
        setMeta({ app: r.data.app, service: r.data.service, stack: r.data.stack, system: r.data.system });
        const { app, service } = r.data;
        if (app && service) void storeApi.template(app).then((t) => t.ok && setAppInfo(t.data.services.find((x) => x.service === service) ?? null));
      });
    }
    void dockerApi.networks().then((r) => r.ok && setNetworks(r.data.networks));
    void dockerApi.volumes().then((r) => r.ok && setVolumes(r.data.volumes.map((v) => v.name)));
  }, [containerId]);

  if (!spec) {
    return (
      <div className="ce">
        <div className="ce-body">{error ? <p className="error">{error}</p> : <span className="spinner" />}</div>
      </div>
    );
  }
  const readOnly = meta.system || !!meta.stack;
  const final = cleanSpec(spec);
  const problems = validateSpec(final);
  const changes = original ? specDiff(original, final, appInfo?.secrets) : [];

  const next = () => {
    setError('');
    if (problems.length) return setError(problems[0]);
    if (containerId) setReview(true);
    else void save();
  };

  const save = async () => {
    setError('');
    if (containerId) onApplying?.(spec.name);
    setBusy(true);
    const r = containerId ? await dockerApi.edit(containerId, final) : await dockerApi.create(final);
    setBusy(false);
    if (!r.ok) {
      setReview(false);
      return setError(r.error ?? 'Couldn’t save');
    }
    const warning = (r.data as { warning?: string }).warning;
    if (warning) toast('info', 'Changes applied', warning);
    else toast('success', containerId ? `${spec.name} updated — fresh` : `${spec.name} created`, meta.app ? 'Saved with the app, so updates keep it.' : undefined);
    onSaved();
  };

  return (
    <div className="ce">
      <header className="ce-head">
        <button type="button" className="ghost" onClick={review ? () => setReview(false) : onClose}>
          <Icon name="chevronLeft" size={15} /> Back
        </button>
        <h2>{review ? `Review changes to ${spec.name}` : containerId ? `Edit ${spec.name}` : 'Add Container'}</h2>
        {meta.app && <span className="chip">App Store: {meta.app}</span>}
      </header>
      <div className="ce-body">
        {review ? (
          <div className="sf-review">
            <p className="ce-note">NoCapOS recreates the container with these changes, so it restarts for a moment. Volumes and folders stay put. If anything goes wrong, the current container comes right back.</p>
            <ChangeList changes={changes} />
          </div>
        ) : (
          <>
            {meta.system && <p className="ce-note bad">This is one of NoCapOS's own containers; it can't be edited.</p>}
            {meta.stack && <p className="ce-note">This container belongs to the stack “{meta.stack}”. Edit the stack's compose file instead.</p>}
            {meta.app && <p className="ce-note">Changes are saved with the app, so they stick around when it updates.</p>}
            <SpecForm
              spec={spec}
              onChange={setSpec}
              networks={networks}
              volumes={volumes}
              disabled={readOnly}
              options={{ lockName: !!meta.app, catalogImage: appInfo?.catalog_image, secrets: appInfo?.secrets, webPort: appInfo?.web_port }}
            />
          </>
        )}
      </div>
      <footer className="ce-foot">
        {error && <p className="error">{error}</p>}
        {!error && !review && problems.length > 0 && <p className="muted small">{problems[0]}</p>}
        <span className="spacer" />
        <button type="button" className="ghost" onClick={review ? () => setReview(false) : onClose}>
          {review ? 'Keep editing' : 'Cancel'}
        </button>
        {review ? (
          <button type="button" disabled={busy || !changes.length} onClick={() => void save()}>
            {busy ? <span className="spinner sm" /> : null} {busy ? 'Applying…' : 'Apply changes'}
          </button>
        ) : (
          <button type="button" disabled={busy || readOnly || !spec.name.trim() || !spec.image.trim()} onClick={next}>
            {busy ? <span className="spinner sm" /> : null} {busy ? 'Creating…' : containerId ? 'Review changes' : 'Create & Start'}
          </button>
        )}
      </footer>
    </div>
  );
}
