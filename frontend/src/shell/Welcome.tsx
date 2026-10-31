import { useEffect, useState, type ReactNode } from 'react';
import { fileApi, type FileRoot } from '../api/files';
import { storageApi, type Pool } from '../api/storage';
import type { User } from '../api/types';
import { openApp } from '../apps/meta';
import { Choice, ThemeGallery, TransparencySlider, WallpaperStrip } from '../apps/Personalize';
import { Icon, type IconName } from '../components/Icon';
import { LogoMark } from '../components/Logo';
import { fmtBytes } from '../lib/format';
import { ICON_STYLES, usePrefs } from '../state/prefs';
import { useSystem } from '../state/system';
import '../styles/welcome.css';

// First-start welcome: shown once to an administrator after setup. Where they
// are is kept in their (server-synced) prefs, so it picks up on the same step
// after a reload or on another browser; finishing or skipping ends it for good.

const STEPS: { id: string; label: string }[] = [
  { id: 'welcome', label: 'Welcome' },
  { id: 'you', label: 'You' },
  { id: 'look', label: 'Look' },
  { id: 'storage', label: 'Storage' },
  { id: 'apps', label: 'Apps' },
  { id: 'security', label: 'Security' },
  { id: 'done', label: 'Done' },
];
const LAST = STEPS.length - 1;

export function Welcome({ user }: { user: User }) {
  const saved = usePrefs((s) => s.welcomeStep);
  const set = usePrefs((s) => s.set);
  const step = Math.min(Math.max(0, Math.round(saved) || 0), LAST);
  // While a link from the welcome is open (Settings, App Store…), it steps aside.
  const [away, setAway] = useState(false);

  const go = (n: number) => set({ welcomeStep: Math.min(Math.max(0, n), LAST) });
  const finish = () => set({ welcomeDone: true, welcomeStep: 0 });
  const visit = (open: () => void) => {
    open();
    setAway(true);
  };

  if (away) {
    return (
      <div className="welcome-resume" role="status">
        <LogoMark size={22} />
        <span>
          Setup · step {step + 1} of {STEPS.length}
        </span>
        <button type="button" className="small" onClick={() => setAway(false)}>
          Continue setup
        </button>
        <button type="button" className="ghost icon-btn" title="Skip setup" aria-label="Skip setup" onClick={finish}>
          <Icon name="close" size={14} />
        </button>
      </div>
    );
  }

  const id = STEPS[step].id;
  return (
    <div className="welcome" role="dialog" aria-modal="true" aria-label="Welcome to NoCapOS">
      <div className="welcome-card">
        <header className="welcome-top">
          <ol className="welcome-steps" aria-label="Setup steps">
            {STEPS.map((s, i) => (
              <li key={s.id} className={i === step ? 'on' : i < step ? 'done' : ''} aria-current={i === step ? 'step' : undefined}>
                <span className="welcome-step-dot" />
                <span className="welcome-step-label">{s.label}</span>
              </li>
            ))}
          </ol>
          {step < LAST && (
            <button type="button" className="ghost small welcome-skip" onClick={finish}>
              Skip setup
            </button>
          )}
        </header>

        <div className="welcome-body" key={id}>
          {id === 'welcome' && (
            <div className="welcome-hero">
              <LogoMark size={88} className="welcome-logo" />
              <h1>Welcome to NoCapOS</h1>
              <p>Your server is up and running. A few quick steps make it yours, and you can skip any of them; everything here is also in Settings later.</p>
            </div>
          )}

          {id === 'you' && <YouStep user={user} visit={visit} />}

          {id === 'look' && (
            <Step icon="palette" title="Make it look like yours" text="Changes apply right away. Fine-tune more in Settings → Appearance.">
              <div className="welcome-field">
                <span className="welcome-label">Theme</span>
                <ThemeGallery />
              </div>
              <div className="welcome-field">
                <span className="welcome-label">Wallpaper</span>
                <WallpaperStrip strip />
              </div>
              <div className="welcome-field">
                <span className="welcome-label">App icons</span>
                <IconStyle />
              </div>
              <div className="welcome-field settings-card">
                <TransparencySlider />
              </div>
            </Step>
          )}

          {id === 'storage' && <StorageStep visit={visit} />}

          {id === 'apps' && (
            <Step icon="store" title="Add apps" text="Install apps from the App Store with one click. They appear in Launchpad; right-click one there (or drag it out) to put it on your desktop.">
              <div className="welcome-actions">
                <button type="button" onClick={() => visit(() => openApp('appcenter'))}>
                  <Icon name="store" size={15} /> Open the App Store
                </button>
              </div>
            </Step>
          )}

          {id === 'security' && (
            <Step icon="shield" title="Keep it yours" text="NoCapOS only lets in what you allow. Add a second step to signing in, so a password alone is never enough.">
              <Firewall />
              <div className="welcome-actions">
                <button type="button" onClick={() => visit(() => openApp('settings', { props: { section: 'security' } }))}>
                  <Icon name="shield" size={15} /> Set up two-factor sign-in
                </button>
              </div>
            </Step>
          )}

          {id === 'done' && (
            <div className="welcome-hero">
              <span className="welcome-check">
                <Icon name="check" size={40} />
              </span>
              <h1>You’re all set</h1>
              <p>Open Launchpad from the Dock to find every app, and press Ctrl+Space to search anything.</p>
            </div>
          )}
        </div>

        <footer className="welcome-foot">
          {step > 0 && step < LAST && (
            <button type="button" className="ghost" onClick={() => go(step - 1)}>
              Back
            </button>
          )}
          <span className="spacer" />
          {step > 0 && step < LAST && (
            <button type="button" className="ghost" onClick={() => go(step + 1)}>
              Skip
            </button>
          )}
          {step < LAST ? (
            <button type="button" className="welcome-next" autoFocus onClick={() => go(step + 1)}>
              {step === 0 ? 'Get started' : 'Continue'}
            </button>
          ) : (
            <button type="button" className="welcome-next" autoFocus onClick={finish}>
              Start using NoCapOS
            </button>
          )}
        </footer>
      </div>
    </div>
  );
}

function Step({ icon, title, text, children }: { icon: IconName; title: string; text: string; children: ReactNode }) {
  return (
    <div className="welcome-step">
      <span className="welcome-step-icon">
        <Icon name={icon} size={24} />
      </span>
      <h2>{title}</h2>
      <p className="welcome-text">{text}</p>
      {children}
    </div>
  );
}

function IconStyle() {
  const iconStyle = usePrefs((s) => s.iconStyle);
  const set = usePrefs((s) => s.set);
  return <Choice value={iconStyle} options={ICON_STYLES} onChange={(v) => set({ iconStyle: v })} />;
}

function YouStep({ user, visit }: { user: User; visit: (open: () => void) => void }) {
  const host = useSystem((s) => s.info?.host.hostname);
  return (
    <Step icon="user" title="You and this device" text="You're the administrator: you can add people, install apps and change how the server works.">
      <div className="welcome-rows">
        <div className="welcome-row">
          <span className="welcome-label">Signed in as</span>
          <b>{user.username}</b>
          <span className="chip">Administrator</span>
        </div>
        <div className="welcome-row">
          <span className="welcome-label">Device name</span>
          <b className="welcome-host">{host || '…'}</b>
          <button type="button" className="ghost small" onClick={() => visit(() => openApp('settings', { props: { section: 'about' } }))}>
            Rename in Settings
          </button>
        </div>
      </div>
      <p className="welcome-note muted small">The device name is how this server shows up on your network. Change it any time in Settings → General → About.</p>
    </Step>
  );
}

function StorageStep({ visit }: { visit: (open: () => void) => void }) {
  const [roots, setRoots] = useState<FileRoot[] | null>(null);
  const [pools, setPools] = useState<Pool[] | null>(null);
  useEffect(() => {
    void fileApi.roots().then((r) => setRoots(r.ok ? r.data : []));
    void storageApi.pools().then((r) => setPools(r.ok ? r.data.pools : []));
  }, []);
  return (
    <Step icon="hdd" title="Your storage" text="Files you keep on NoCapOS live on this server's disks. Pools combine several disks so one can fail without losing anything.">
      <div className="welcome-rows">
        {roots === null ? (
          <div className="welcome-row muted">Looking at your drives…</div>
        ) : roots.length === 0 ? (
          <div className="welcome-row muted">No file locations yet.</div>
        ) : (
          roots.slice(0, 4).map((r) => (
            <div key={r.id} className="welcome-row">
              <Icon name="folder" size={16} />
              <b className="welcome-grow">{r.name}</b>
              {r.total > 0 && <span className="muted small">{fmtBytes(r.free, 0)} free of {fmtBytes(r.total, 0)}</span>}
            </div>
          ))
        )}
        {pools && pools.length > 0 && pools.map((p) => (
          <div key={p.name} className="welcome-row">
            <Icon name="hdd" size={16} />
            <b className="welcome-grow">{p.name}</b>
            <span className={`chip ${p.health === 'ONLINE' ? 'good' : 'warn'}`}>{p.health === 'ONLINE' ? 'Healthy' : p.health.toLowerCase()}</span>
            <span className="muted small">{fmtBytes(p.free, 0)} free</span>
          </div>
        ))}
        {pools && pools.length === 0 && <div className="welcome-row muted small">No storage pools yet — Storage can combine disks into one.</div>}
      </div>
      <div className="welcome-actions">
        <button type="button" className="ghost" onClick={() => visit(() => openApp('files'))}>
          <Icon name="folder" size={15} /> Open Files
        </button>
        <button type="button" onClick={() => visit(() => openApp('storage'))}>
          <Icon name="hdd" size={15} /> Open Storage
        </button>
      </div>
    </Step>
  );
}

/** A small firewall at work: green traffic passes, red is stopped at the wall. */
function Firewall() {
  const lanes = [
    { ok: true, d: 0 },
    { ok: false, d: 0.7 },
    { ok: true, d: 1.4 },
    { ok: false, d: 2.1 },
  ];
  return (
    <div className="fw" aria-hidden>
      <span className="fw-side fw-out">Internet</span>
      <span className="fw-side fw-in">NoCapOS</span>
      {lanes.map((l, i) => (
        <span key={i} className={`fw-packet ${l.ok ? 'ok' : 'bad'}`} style={{ top: `${18 + i * 20}%`, ['--d' as string]: `${l.d}s` }} />
      ))}
      <span className="fw-wall">
        <Icon name="shield" size={18} />
      </span>
    </div>
  );
}
