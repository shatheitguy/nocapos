import type { Container } from '../api/types';
import { Icon } from '../components/Icon';
import { fmtUptime } from '../lib/format';
import { useT } from '../i18n';
import { openApp } from './meta';

// Small pieces shared by the Containers app's list and container page.

export const isApp = (c: Container) => !!c.labels?.['nocapos.app'];
export const isRunning = (c: Container) => c.state === 'running';

export function stateTone(state: string) {
  return state === 'running' ? 'good' : state === 'paused' || state === 'restarting' || state === 'partial' ? 'warn' : state === 'exited' || state === 'dead' ? 'bad' : '';
}

/** Where "Open" goes: the server's pick (own address / macvlan IP), else this
 *  server on the first published TCP port. */
export function containerLink(c: Pick<Container, 'web_url' | 'ports'>): string | null {
  if (c.web_url) return c.web_url;
  const p = c.ports.find((x) => x.public && (x.protocol === 'tcp' || !x.protocol));
  return p ? `http://${window.location.hostname}:${p.public}/` : null;
}

export function uptime(c: Container): string | null {
  if (!c.started_at || !isRunning(c)) return null;
  return fmtUptime(Math.max(0, (Date.now() - new Date(c.started_at).getTime()) / 1000));
}

export function openTerminal(c: Pick<Container, 'id' | 'name'>) {
  openApp('terminal', { title: `Terminal — ${c.name}`, props: { target: `container:${c.id}`, label: c.name }, key: `terminal:${c.id}` });
}

export function StateDot({ state }: { state: string }) {
  return <span className={`ct-dot ${stateTone(state)}`} title={state} />;
}

export function Badges({ c }: { c: Container }) {
  const t = useT();
  return (
    <>
      {c.system && <span className="chip tiny">{t('ctr.badge.system')}</span>}
      {isApp(c) && <span className="chip tiny accent">{t('ctr.badge.app')}</span>}
      {c.health && <span className={`chip tiny ${c.health === 'healthy' ? 'good' : c.health === 'unhealthy' ? 'bad' : 'warn'}`}>{c.health}</span>}
    </>
  );
}

export function PortLinks({ c, max = 4 }: { c: Container; max?: number }) {
  const t = useT();
  const seen = new Set<string>();
  const ports = c.ports.filter((p) => {
    const k = `${p.public ?? ''}:${p.private}/${p.protocol}`;
    if (!p.public || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  if (!ports.length) return <span className="muted small">{t('ctr.noPorts')}</span>;
  return (
    <span className="ct-ports">
      {ports.slice(0, max).map((p) =>
        p.protocol === 'tcp' ? (
          <a key={`${p.public}/${p.protocol}`} className="ct-port" href={`http://${window.location.hostname}:${p.public}/`} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()} title={t('ctr.openPort', { port: p.public })}>
            {p.public}→{p.private}
          </a>
        ) : (
          <span key={`${p.public}/${p.protocol}`} className="ct-port udp">
            {p.public}→{p.private}/{p.protocol}
          </span>
        ),
      )}
      {ports.length > max && <span className="muted small">+{ports.length - max}</span>}
    </span>
  );
}

/** A local address as a short link (https://web.nocap.local → web.nocap.local). */
export function LocalLink({ url }: { url: string }) {
  return (
    <a className="ct-local" href={url} target="_blank" rel="noopener noreferrer" onClick={(e) => e.stopPropagation()} title={url}>
      <Icon name="globe" size={11} /> {url.replace(/^https?:\/\//, '')}
    </a>
  );
}
