import { useMemo, useState } from 'react';
import { Icon, type IconName } from '../components/Icon';

// Curated catalog preview. One-click installs arrive with the App Store
// milestone (compose manifests + Traefik routing); until then this is browse-only.
interface CatalogApp {
  name: string;
  category: Category;
  description: string;
  icon: IconName;
  tile: [string, string];
}

type Category = 'AI' | 'Media' | 'Cloud' | 'Home' | 'Network' | 'Developer';
const CATEGORIES: ('All' | Category)[] = ['All', 'AI', 'Media', 'Cloud', 'Home', 'Network', 'Developer'];

const CATALOG: CatalogApp[] = [
  { name: 'Ollama', category: 'AI', description: 'Run open LLMs locally on your GPU or CPU.', icon: 'cpu', tile: ['#434343', '#111'] },
  { name: 'Open WebUI', category: 'AI', description: 'Chat interface for local and remote models.', icon: 'terminal', tile: ['#5b5bd6', '#2f2f8f'] },
  { name: 'ComfyUI', category: 'AI', description: 'Node-based image generation pipelines.', icon: 'image', tile: ['#f76b1c', '#b8420b'] },
  { name: 'Jellyfin', category: 'Media', description: 'Stream your movies, shows and music.', icon: 'play', tile: ['#aa5cc3', '#00a4dc'] },
  { name: 'Immich', category: 'Media', description: 'Photo and video backup with face search.', icon: 'image', tile: ['#fa2921', '#ed79b5'] },
  { name: 'Navidrome', category: 'Media', description: 'Personal music streaming server.', icon: 'play', tile: ['#0b86d1', '#0a4f7a'] },
  { name: 'Nextcloud', category: 'Cloud', description: 'Files, calendar and contacts in your cloud.', icon: 'files', tile: ['#0082c9', '#00569a'] },
  { name: 'Syncthing', category: 'Cloud', description: 'Continuous peer-to-peer file sync.', icon: 'restart', tile: ['#0891d1', '#065b86'] },
  { name: 'Vaultwarden', category: 'Cloud', description: 'Self-hosted password manager server.', icon: 'lock', tile: ['#175ddc', '#0c3a8f'] },
  { name: 'Home Assistant', category: 'Home', description: 'Home automation hub for every device.', icon: 'settings', tile: ['#18bcf2', '#0b7fb0'] },
  { name: 'Frigate', category: 'Home', description: 'NVR with real-time AI object detection.', icon: 'monitor', tile: ['#3f51b5', '#233086'] },
  { name: 'Pi-hole', category: 'Network', description: 'Network-wide ad and tracker blocking.', icon: 'network', tile: ['#96060c', '#5c0307'] },
  { name: 'WireGuard', category: 'Network', description: 'Fast, modern VPN to reach home securely.', icon: 'lock', tile: ['#88171a', '#4d0c0e'] },
  { name: 'Uptime Kuma', category: 'Network', description: 'Monitor services and get alerts.', icon: 'monitor', tile: ['#5cdd8b', '#2a9d58'] },
  { name: 'Gitea', category: 'Developer', description: 'Lightweight self-hosted Git service.', icon: 'terminal', tile: ['#609926', '#3b5f17'] },
  { name: 'code-server', category: 'Developer', description: 'VS Code in the browser.', icon: 'terminal', tile: ['#007acc', '#004f85'] },
];

export function AppCenter() {
  const [cat, setCat] = useState<'All' | Category>('All');
  const [q, setQ] = useState('');
  const apps = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return CATALOG.filter(
      (a) => (cat === 'All' || a.category === cat) && (!needle || `${a.name} ${a.description}`.toLowerCase().includes(needle)),
    );
  }, [cat, q]);

  return (
    <div className="appcenter">
      <div className="appcenter-hero">
        <div>
          <h2 className="no-margin">App Center</h2>
          <p>One-click self-hosted apps, sandboxed in containers and routed automatically.</p>
          <span className="chip">One-click install arrives with the App Store milestone</span>
        </div>
        <label className="search">
          <Icon name="search" size={16} />
          <input placeholder="Search apps" value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
      </div>
      <div className="chip-row tabs">
        {CATEGORIES.map((c) => (
          <button key={c} type="button" className={`chip-btn ${cat === c ? 'on' : ''}`} onClick={() => setCat(c)}>
            {c}
          </button>
        ))}
      </div>
      <div className="catalog">
        {apps.map((a) => (
          <div key={a.name} className="catalog-card">
            <span className="app-icon" style={{ background: `linear-gradient(145deg, ${a.tile[0]}, ${a.tile[1]})` }}>
              <Icon name={a.icon} size={22} />
            </span>
            <div className="catalog-text">
              <b>{a.name}</b>
              <span className="muted small">{a.category}</span>
              <p className="small">{a.description}</p>
            </div>
            <button type="button" className="ghost small" disabled title="Coming with the App Store milestone">
              Install
            </button>
          </div>
        ))}
        {!apps.length && <p className="muted">No apps match your search.</p>}
      </div>
    </div>
  );
}
