import { Fragment, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { api, confirmNetwork } from '../api/client';
import { hostAccessApi, type HostAccess } from '../api/hostaccess';
import { hostApi, type IPv4Config, type IPv6Config, type MachineSetup, type NetInterface } from '../api/hostctl';
import type { User } from '../api/types';
import { Choice, ThemeGallery } from '../apps/Personalize';
import { openApp } from '../apps/meta';
import { AccentPicker } from '../components/AccentPicker';
import { Icon, type IconName } from '../components/Icon';
import { LogoMark } from '../components/Logo';
import { translate, useT } from '../i18n';
import { WALLPAPERS, shownWallpaper, usePrefs, type Lingo, type ThemeMode } from '../state/prefs';
import { useSystem } from '../state/system';
import { hostnameProblem, networkProblem, nextOrigin as originAfter, splitDNS } from '../lib/setupCheck';
import '../styles/welcome.css';

// First-start setup wizard, shown once to an administrator after the account
// is made: voice, look, then the machine itself (name, network, time zone,
// the screen, updates), a summary that applies it all, and a celebration.
// The look applies the moment it's picked; the machine settings wait for the
// summary. Every step can be skipped; skipping keeps what the server has.
// The machine part is done once per server (another admin later only gets
// voice and look). Where you are is kept in your (server-synced) prefs.

type StepId = 'intro' | 'voice' | 'look' | 'name' | 'network' | 'time' | 'extras' | 'summary' | 'done';
const ALL_STEPS: StepId[] = ['intro', 'voice', 'look', 'name', 'network', 'time', 'extras', 'summary', 'done'];
const MACHINE_STEPS: StepId[] = ['name', 'network', 'time', 'extras'];
const STEP_ICON: Record<StepId, IconName> = {
  intro: 'sparkles', voice: 'sparkles', look: 'palette', name: 'edit', network: 'network',
  time: 'clock', extras: 'desktop', summary: 'list', done: 'check',
};

// ---- the machine draft ----

type V6Mode = 'keep' | IPv6Config['method'];
interface NetDraft {
  conn: string;
  iface: string;
  v4: IPv4Config;
  v6mode: V6Mode;
  v6: IPv6Config;
}
interface Draft {
  hostname: string; // '' = keep
  net: NetDraft | null; // null = keep
  zone: string; // '' = keep
  desktop: boolean | null; // null = keep
  updates: boolean; // look for a newer NoCapOS
}
const EMPTY_DRAFT: Draft = { hostname: '', net: null, zone: '', desktop: null, updates: true };
const DRAFT_KEY = 'nocap.setup.draft';

function loadDraft(): Draft {
  try {
    const raw = JSON.parse(sessionStorage.getItem(DRAFT_KEY) ?? 'null') as Partial<Draft> | null;
    return raw && typeof raw === 'object' ? { ...EMPTY_DRAFT, ...raw } : EMPTY_DRAFT;
  } catch {
    return EMPTY_DRAFT;
  }
}
function saveDraft(d: Draft) {
  try {
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify(d));
  } catch {
    /* storage unavailable: the draft just lives in memory */
  }
}

// ---- the wizard ----

export function Welcome({ user }: { user: User }) {
  const t = useT();
  const saved = usePrefs((s) => s.welcomeStep);
  const set = usePrefs((s) => s.set);
  const [machine, setMachine] = useState<MachineSetup | null>(null);
  const [host, setHost] = useState<HostAccess | null>(null);
  const [draft, setDraftState] = useState<Draft>(loadDraft);
  const [dir, setDir] = useState<'fwd' | 'back'>('fwd');
  const setDraft = (p: Partial<Draft>) =>
    setDraftState((d) => {
      const next = { ...d, ...p };
      saveDraft(next);
      return next;
    });

  useEffect(() => {
    void hostApi.machineSetup().then((r) => setMachine(r.ok ? r.data : { done: false, hostname: '', timezone: { zone: '', supported: false } }));
    void hostAccessApi.get().then((r) => r.ok && setHost(r.data));
  }, []);

  // Another administrator already set up the machine: just voice and look.
  const steps = useMemo(() => (machine?.done ? ALL_STEPS.filter((s) => !MACHINE_STEPS.includes(s)) : ALL_STEPS), [machine?.done]);
  const last = steps.length - 1;
  const index = Math.min(Math.max(0, Math.round(saved) || 0), last);
  const id = steps[index];

  const go = (n: number) => {
    setDir(n < index ? 'back' : 'fwd');
    set({ welcomeStep: Math.min(Math.max(0, n), last) });
  };
  const finish = async () => {
    if (!machine?.done) await hostApi.machineSetupDone();
    try {
      sessionStorage.removeItem(DRAFT_KEY);
    } catch {
      /* storage unavailable */
    }
    set({ welcomeDone: true, welcomeStep: 0 });
  };

  if (!machine) {
    return (
      <div className="welcome wiz" role="dialog" aria-modal="true" aria-label={t('wiz.title')}>
        <div className="wiz-loading">
          <LogoMark size={64} className="wiz-pulse" />
        </div>
      </div>
    );
  }

  // Progress counts the real steps (not the intro or the finish).
  const real: StepId[] = steps.filter((s) => s !== 'intro' && s !== 'done');
  const pos = real.indexOf(id);
  const pct = id === 'done' ? 100 : id === 'intro' ? 0 : ((pos + 1) / real.length) * 100;
  const skippable = id !== 'intro' && id !== 'summary' && id !== 'done';
  const invalid =
    (id === 'name' && !!draft.hostname && !!hostnameProblem(draft.hostname)) || (id === 'network' && !!draft.net && !!networkProblem(draft.net));

  const skipStep = () => {
    if (id === 'name') setDraft({ hostname: '' });
    if (id === 'network') setDraft({ net: null });
    if (id === 'time') setDraft({ zone: '' });
    if (id === 'extras') setDraft({ desktop: null, updates: false });
    go(index + 1);
  };

  return (
    <div className="welcome wiz" role="dialog" aria-modal="true" aria-label={t('wiz.title')}>
      <div className={`welcome-card wiz-card wiz-at-${id}`}>
        {id !== 'intro' && id !== 'done' && (
          <header className="wiz-top">
            <div className="wiz-progress" role="progressbar" aria-valuemin={0} aria-valuemax={real.length} aria-valuenow={pos + 1}
              aria-label={t('wiz.progress', { n: pos + 1, total: real.length })}>
              <span className="wiz-progress-fill" style={{ width: `${pct}%` }} />
            </div>
            <ol className="wiz-dots" aria-hidden>
              {real.map((s, i) => (
                <li key={s} className={i === pos ? 'on' : i < pos ? 'done' : ''} title={t(`wiz.step.${s}`)}>
                  <Icon name={i < pos ? 'check' : STEP_ICON[s]} size={12} />
                  <span>{t(`wiz.step.${s}`)}</span>
                </li>
              ))}
            </ol>
            <span className="wiz-count">{t('wiz.progress', { n: pos + 1, total: real.length })}</span>
          </header>
        )}

        <div className={`welcome-body wiz-body wiz-${dir}`} key={id}>
          {id === 'intro' && <Intro user={user} onStart={() => go(1)} onSkip={() => void finish()} />}
          {id === 'voice' && <VoiceStep />}
          {id === 'look' && <LookStep />}
          {id === 'name' && <NameStep draft={draft} setDraft={setDraft} current={machine.hostname} host={host} />}
          {id === 'network' && <NetworkStep draft={draft} setDraft={setDraft} />}
          {id === 'time' && <TimeStep draft={draft} setDraft={setDraft} machine={machine} />}
          {id === 'extras' && <ExtrasStep draft={draft} setDraft={setDraft} host={host} />}
          {id === 'summary' && (
            <SummaryStep draft={draft} machine={machine} machineSteps={!machine.done} onDone={() => go(index + 1)} onEdit={(s) => go(steps.indexOf(s))}
              onBack={() => go(index - 1)} />
          )}
          {id === 'done' && <Finish draft={draft} oldHost={machine.hostname} onClose={() => void finish()} />}
        </div>

        {id !== 'intro' && id !== 'done' && id !== 'summary' && (
          <footer className="welcome-foot wiz-foot">
            <button type="button" className="ghost" onClick={() => go(index - 1)}>
              <Icon name="chevronLeft" size={14} /> {t('common.back')}
            </button>
            <span className="spacer" />
            {skippable && (
              <button type="button" className="ghost" onClick={skipStep}>
                {t('wiz.skip')}
              </button>
            )}
            <button type="button" className="welcome-next" disabled={invalid} onClick={() => go(index + 1)}>
              {t('wiz.next')} <Icon name="chevronRight" size={14} />
            </button>
          </footer>
        )}
      </div>
    </div>
  );
}

function Step({ icon, title, text, children }: { icon: IconName; title: string; text: string; children?: ReactNode }) {
  return (
    <div className="welcome-step wiz-step">
      <span className="welcome-step-icon wiz-step-icon">
        <Icon name={icon} size={24} />
      </span>
      <h2>{title}</h2>
      <p className="welcome-text">{text}</p>
      {children}
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="welcome-field">
      <span className="welcome-label">{label}</span>
      {children}
      {hint && <span className="wiz-hint">{hint}</span>}
    </div>
  );
}

function Unsupported({ text }: { text: string }) {
  return (
    <div className="wiz-note">
      <Icon name="info" size={16} />
      <span>{text}</span>
    </div>
  );
}

// ---- intro: the NoCap logo arrives ----

function Intro({ user, onStart, onSkip }: { user: User; onStart: () => void; onSkip: () => void }) {
  const t = useT();
  return (
    <div className="wiz-intro">
      <div className="wiz-logo-stage" aria-hidden>
        <span className="wiz-ring r1" />
        <span className="wiz-ring r2" />
        <span className="wiz-ring r3" />
        <LogoMark size={104} className="wiz-logo" />
      </div>
      <h1 className="wiz-intro-title">
        {t('wiz.introTitle')
          .split(' ')
          .map((w, i) => (
            <Fragment key={i}>
              {i > 0 && ' '}
              <span className="wiz-word" style={{ '--i': i } as CSSProperties}>
                {w}
              </span>
            </Fragment>
          ))}
      </h1>
      <p className="wiz-intro-sub">{t('wiz.introSub', { name: user.username })}</p>
      <div className="wiz-intro-actions">
        <button type="button" className="welcome-next wiz-go" autoFocus onClick={onStart}>
          {t('wiz.introGo')} <Icon name="chevronRight" size={15} />
        </button>
        <button type="button" className="ghost small" onClick={onSkip}>
          {t('wiz.introSkip')}
        </button>
      </div>
    </div>
  );
}

// ---- voice ----

function VoiceStep() {
  const t = useT();
  const lingo = usePrefs((s) => s.lingo);
  const set = usePrefs((s) => s.set);
  const card = (l: Lingo, title: string) => (
    <button type="button" role="radio" aria-checked={lingo === l} className={`wiz-choice ${lingo === l ? 'on' : ''}`} onClick={() => set({ lingo: l })}>
      <span className="wiz-choice-head">
        <b>{title}</b>
        {lingo === l && <Icon name="check" size={15} />}
      </span>
      <span className="wiz-bubble">{translate(l, 'wiz.voiceSample')}</span>
      <span className="wiz-choice-sub">{translate(l, 'wiz.voiceSample2')}</span>
    </button>
  );
  return (
    <Step icon="sparkles" title={t('wiz.voiceTitle')} text={t('wiz.voiceText')}>
      <div className="wiz-choices" role="radiogroup" aria-label={t('wiz.voiceTitle')}>
        {card('genz', t('wiz.voiceGenz'))}
        {card('standard', t('wiz.voiceStandard'))}
      </div>
    </Step>
  );
}

// ---- look ----

function LookStep() {
  const t = useT();
  const theme = usePrefs((s) => s.theme);
  const uiTheme = usePrefs((s) => s.uiTheme);
  const wallpaper = usePrefs((s) => s.wallpaper);
  const set = usePrefs((s) => s.set);
  const shown = shownWallpaper(wallpaper);
  // Glass and Cyber-Deck are dark by design; light and auto use NoCap classic.
  const pickMode = (m: ThemeMode) => set(m === 'dark' ? { theme: m } : { theme: m, uiTheme: 'classic' });
  const mode: ThemeMode = uiTheme === 'classic' ? theme : 'dark';
  return (
    <Step icon="palette" title={t('wiz.lookTitle')} text={t('wiz.lookText')}>
      <Field label={t('wiz.mode')} hint={t('wiz.modeHint')}>
        <div className="wiz-modes" role="radiogroup" aria-label={t('wiz.mode')}>
          {(['dark', 'light', 'auto'] as ThemeMode[]).map((m) => (
            <button key={m} type="button" role="radio" aria-checked={mode === m} className={`wiz-mode wiz-mode-${m} ${mode === m ? 'on' : ''}`} onClick={() => pickMode(m)}>
              <span className="wiz-mode-art" aria-hidden />
              <Icon name={m === 'dark' ? 'moon' : m === 'light' ? 'sun' : 'auto'} size={14} />
              {t(`pz.${m}`)}
            </button>
          ))}
        </div>
      </Field>
      <Field label={t('pz.theme')}>
        <ThemeGallery />
      </Field>
      <Field label={t('pz.accent')}>
        <AccentPicker />
      </Field>
      <Field label={t('wiz.wallpaper')}>
        <div className="wiz-walls" role="radiogroup" aria-label={t('wiz.wallpaper')}>
          {WALLPAPERS.map((w) => (
            <button key={w.id} type="button" role="radio" aria-checked={shown === w.id} className={`wp-thumb ${shown === w.id ? 'on' : ''}`} onClick={() => set({ wallpaper: w.id })}>
              <span className={`wp-preview wp-${w.id}`} />
              <span className="small">{w.name}</span>
            </button>
          ))}
        </div>
      </Field>
    </Step>
  );
}

// ---- host name ----

function NameStep({ draft, setDraft, current, host }: { draft: Draft; setDraft: (p: Partial<Draft>) => void; current: string; host: HostAccess | null }) {
  const t = useT();
  const live = useSystem((s) => s.info?.host.hostname);
  const now = current || live || '';
  const value = draft.hostname || now;
  const problem = draft.hostname ? hostnameProblem(draft.hostname) : '';
  return (
    <Step icon="edit" title={t('wiz.nameTitle')} text={t('wiz.nameText')}>
      {host && !host.native && <Unsupported text={t('wiz.nativeOnly')} />}
      <Field label={t('wiz.hostname')} hint={problem ? undefined : t('wiz.hostHint', { name: (value || 'nocap').toLowerCase() })}>
        <input
          className={`wiz-input ${problem ? 'bad' : ''}`}
          value={value}
          onChange={(e) => setDraft({ hostname: e.target.value === now ? '' : e.target.value })}
          spellCheck={false}
          autoCapitalize="none"
          maxLength={253}
          disabled={!!host && !host.native}
          aria-invalid={!!problem}
          aria-label={t('wiz.hostname')}
        />
      </Field>
      {problem && <p className="error small" role="alert">{t(problem)}</p>}
    </Step>
  );
}

// ---- network ----

function NetworkStep({ draft, setDraft }: { draft: Draft; setDraft: (p: Partial<Draft>) => void }) {
  const t = useT();
  const [ifs, setIfs] = useState<NetInterface[] | null>(null);
  const [supported, setSupported] = useState(true);
  useEffect(() => {
    void hostApi.network().then((r) => {
      if (!r.ok) {
        setIfs([]);
        setSupported(false);
        return;
      }
      setSupported(r.data.manager === 'networkmanager');
      setIfs(r.data.interfaces.filter((i) => i.connection && i.ipv4?.editable));
    });
  }, []);

  if (ifs === null) {
    return (
      <Step icon="network" title={t('wiz.netTitle')} text={t('wiz.netText')}>
        <div className="wiz-note muted">
          <span className="spinner sm" /> {t('wiz.netLooking')}
        </div>
      </Step>
    );
  }
  if (!supported || ifs.length === 0) {
    return (
      <Step icon="network" title={t('wiz.netTitle')} text={t('wiz.netText')}>
        <Unsupported text={supported ? t('wiz.netNone') : t('wiz.netUnsupported')} />
      </Step>
    );
  }

  const fromIface = (i: NetInterface): NetDraft => ({
    conn: i.connection ?? '',
    iface: i.name,
    v4: {
      method: i.ipv4?.method === 'manual' ? 'manual' : 'auto',
      address: i.ipv4?.addresses[0] ?? '',
      gateway: i.ipv4?.gateway ?? '',
      // With DHCP the DNS servers shown came from the router: leave them automatic.
      dns: i.ipv4?.method === 'manual' ? (i.ipv4?.dns ?? []) : [],
    },
    v6mode: 'keep',
    v6: { method: 'auto', address: '', gateway: '', dns: [] },
  });
  const n = draft.net ?? fromIface(ifs[0]);
  const upd = (p: Partial<NetDraft>) => setDraft({ net: { ...n, ...p } });
  const problem = draft.net ? networkProblem(draft.net) : '';

  return (
    <Step icon="network" title={t('wiz.netTitle')} text={t('wiz.netText')}>
      {ifs.length > 1 && (
        <Field label={t('wiz.netAdapter')}>
          <select className="wiz-input" value={n.iface} onChange={(e) => setDraft({ net: fromIface(ifs.find((i) => i.name === e.target.value) ?? ifs[0]) })}>
            {ifs.map((i) => (
              <option key={i.name} value={i.name}>
                {i.name} · {i.connection}
              </option>
            ))}
          </select>
        </Field>
      )}
      <div className="wiz-panel">
        <div className="wiz-panel-head">
          <b>IPv4</b>
          <Choice value={n.v4.method} onChange={(method) => upd({ v4: { ...n.v4, method } })}
            options={[{ id: 'auto', label: t('wiz.dhcp') }, { id: 'manual', label: t('wiz.static') }]} />
        </div>
        {n.v4.method === 'manual' && (
          <div className="wiz-grid">
            <Labeled label={t('wiz.address')} value={n.v4.address ?? ''} ph="192.168.1.50/24" onChange={(address) => upd({ v4: { ...n.v4, address } })} />
            <Labeled label={t('wiz.gateway')} value={n.v4.gateway ?? ''} ph="192.168.1.1" onChange={(gateway) => upd({ v4: { ...n.v4, gateway } })} />
          </div>
        )}
        <Labeled label={t('wiz.dns')} value={n.v4.dns.join(', ')} ph={n.v4.method === 'auto' ? t('wiz.dnsAuto') : '1.1.1.1, 9.9.9.9'}
          onChange={(v) => upd({ v4: { ...n.v4, dns: splitDNS(v) } })} />
      </div>
      <div className="wiz-panel">
        <div className="wiz-panel-head">
          <b>IPv6</b>
          <Choice value={n.v6mode} onChange={(v6mode) => upd({ v6mode, v6: { ...n.v6, method: v6mode === 'keep' ? 'auto' : v6mode } })}
            options={[{ id: 'keep', label: t('wiz.v6keep') }, { id: 'auto', label: t('wiz.v6auto') }, { id: 'manual', label: t('wiz.static') }, { id: 'disabled', label: t('wiz.v6off') }]} />
        </div>
        {n.v6mode === 'manual' && (
          <div className="wiz-grid">
            <Labeled label={t('wiz.address')} value={n.v6.address ?? ''} ph="fd00::50/64" onChange={(address) => upd({ v6: { ...n.v6, address } })} />
            <Labeled label={t('wiz.gateway')} value={n.v6.gateway ?? ''} ph="fe80::1" onChange={(gateway) => upd({ v6: { ...n.v6, gateway } })} />
          </div>
        )}
        {(n.v6mode === 'manual' || n.v6mode === 'auto') && (
          <Labeled label={t('wiz.dns')} value={n.v6.dns.join(', ')} ph={n.v6mode === 'auto' ? t('wiz.dnsAuto') : 'fd00::1'}
            onChange={(v) => upd({ v6: { ...n.v6, dns: splitDNS(v) } })} />
        )}
      </div>
      {problem ? (
        <p className="error small" role="alert">{t(problem)}</p>
      ) : (
        <div className="wiz-note">
          <Icon name="shield" size={16} />
          <span>{t('wiz.netSafe')}</span>
        </div>
      )}
    </Step>
  );
}

function Labeled({ label, value, ph, onChange }: { label: string; value: string; ph: string; onChange: (v: string) => void }) {
  return (
    <label className="wiz-labeled">
      <span>{label}</span>
      <input className="wiz-input" value={value} placeholder={ph} spellCheck={false} autoCapitalize="none" onChange={(e) => onChange(e.target.value)} />
    </label>
  );
}

// ---- time zone ----

function allZones(): string[] {
  const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
  try {
    return f ? f('timeZone') : [];
  } catch {
    return [];
  }
}
const browserZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch {
    return '';
  }
};

function TimeStep({ draft, setDraft, machine }: { draft: Draft; setDraft: (p: Partial<Draft>) => void; machine: MachineSetup }) {
  const t = useT();
  const zones = useMemo(allZones, []);
  const [q, setQ] = useState('');
  const server = machine.timezone.zone;
  const suggested = browserZone();
  const value = draft.zone || server || suggested;
  // A fresh server is usually on UTC: suggest this browser's zone.
  useEffect(() => {
    if (!draft.zone && machine.timezone.supported && suggested && (!server || server === 'UTC' || server === 'Etc/UTC') && suggested !== server) {
      setDraft({ zone: suggested });
    }
  }, []); // once, on arrival
  const list = useMemo(() => {
    const base = zones.length ? zones : [value].filter(Boolean);
    const needle = q.trim().toLowerCase().replace(/\s+/g, '_');
    const out = needle ? base.filter((z) => z.toLowerCase().includes(needle)) : base;
    return value && !out.includes(value) ? [value, ...out] : out;
  }, [zones, q, value]);
  const clock = (z: string) => {
    try {
      return new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', timeZone: z });
    } catch {
      return '';
    }
  };
  return (
    <Step icon="clock" title={t('wiz.timeTitle')} text={t('wiz.timeText')}>
      {!machine.timezone.supported && <Unsupported text={machine.timezone.reason || t('wiz.nativeOnly')} />}
      <div className="wiz-clock" aria-live="polite">
        <span className="wiz-clock-time">{value ? clock(value) : '--:--'}</span>
        <span className="wiz-clock-zone">{(value || '—').replace(/_/g, ' ')}</span>
      </div>
      {machine.timezone.supported && (
        <>
          <input className="wiz-input" value={q} placeholder={t('wiz.timeSearch')} aria-label={t('wiz.timeSearch')} onChange={(e) => setQ(e.target.value)} />
          <select className="wiz-input wiz-zones" size={6} value={value} aria-label={t('wiz.timeTitle')}
            onChange={(e) => setDraft({ zone: e.target.value === server ? '' : e.target.value })}>
            {list.slice(0, 400).map((z) => (
              <option key={z} value={z}>
                {z.replace(/_/g, ' ')}
              </option>
            ))}
          </select>
          {suggested && suggested !== value && (
            <button type="button" className="link small" onClick={() => setDraft({ zone: suggested === server ? '' : suggested })}>
              {t('wiz.timeUseBrowser', { zone: suggested.replace(/_/g, ' ') })}
            </button>
          )}
        </>
      )}
    </Step>
  );
}

// ---- extras: the server's own screen, updates ----

function ExtrasStep({ draft, setDraft, host }: { draft: Draft; setDraft: (p: Partial<Draft>) => void; host: HostAccess | null }) {
  const t = useT();
  const desk = host?.desktop;
  const deskOn = draft.desktop ?? !!desk?.enabled;
  return (
    <Step icon="desktop" title={t('wiz.extrasTitle')} text={t('wiz.extrasText')}>
      <label className={`wiz-toggle ${desk?.available ? '' : 'dimmed'}`}>
        <span className="wiz-toggle-icon">
          <Icon name="desktop" size={18} />
        </span>
        <span className="wiz-toggle-text">
          <b>{t('wiz.desktop')}</b>
          <span>{desk?.available ? t('wiz.desktopHint') : desk?.reason || t('wiz.nativeOnly')}</span>
        </span>
        <input type="checkbox" role="switch" checked={deskOn} disabled={!desk?.available}
          onChange={(e) => setDraft({ desktop: e.target.checked === !!desk?.enabled ? null : e.target.checked })} />
      </label>
      <label className="wiz-toggle">
        <span className="wiz-toggle-icon">
          <Icon name="download" size={18} />
        </span>
        <span className="wiz-toggle-text">
          <b>{t('wiz.updates')}</b>
          <span>{t('wiz.updatesHint')}</span>
        </span>
        <input type="checkbox" role="switch" checked={draft.updates} onChange={(e) => setDraft({ updates: e.target.checked })} />
      </label>
    </Step>
  );
}

// ---- summary: review, then apply ----

type TaskState = 'wait' | 'run' | 'ok' | 'fail' | 'skip';
interface Task {
  id: string;
  label: string;
  detail: string;
  state: TaskState;
  note?: string;
}

/** Where the browser will find the server after a name or address change. */
const nextOrigin = (draft: Draft, oldHost: string) => originAfter(draft, oldHost, window.location);

function SummaryStep({ draft, machine, machineSteps, onDone, onEdit, onBack }: {
  draft: Draft; machine: MachineSetup; machineSteps: boolean; onDone: () => void; onEdit: (s: StepId) => void; onBack: () => void;
}) {
  const t = useT();
  const prefs = usePrefs();
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [net, setNet] = useState<{ token: string; until: number; link: string | null } | null>(null);
  const [now, setNow] = useState(Date.now());
  const [stuck, setStuck] = useState(false);
  const running = useRef(false);

  const keep = t('wiz.keep');
  const rows: { step: StepId; label: string; value: string }[] = [
    { step: 'voice', label: t('wiz.step.voice'), value: prefs.lingo === 'genz' ? t('wiz.voiceGenz') : t('wiz.voiceStandard') },
    {
      step: 'look',
      label: t('wiz.step.look'),
      value: `${t(`pz.${prefs.uiTheme === 'classic' ? prefs.theme : 'dark'}`)} · ${WALLPAPERS.find((w) => w.id === shownWallpaper(prefs.wallpaper))?.name ?? t('pz.yourPhoto')}`,
    },
  ];
  if (machineSteps) {
    rows.push(
      { step: 'name', label: t('wiz.hostname'), value: draft.hostname.trim().toLowerCase() || `${machine.hostname || '—'} (${keep})` },
      {
        step: 'network',
        label: t('wiz.step.network'),
        value: draft.net
          ? [
              draft.net.v4.method === 'manual' ? `IPv4 ${draft.net.v4.address}` : `IPv4 ${t('wiz.dhcp')}`,
              draft.net.v6mode === 'keep' ? '' : draft.net.v6mode === 'manual' ? `IPv6 ${draft.net.v6.address}` : `IPv6 ${t(draft.net.v6mode === 'auto' ? 'wiz.v6auto' : 'wiz.v6off')}`,
            ].filter(Boolean).join(' · ')
          : keep,
      },
      { step: 'time', label: t('wiz.step.time'), value: (draft.zone || `${machine.timezone.zone || '—'} (${keep})`).replace(/_/g, ' ') },
      {
        step: 'extras',
        label: t('wiz.step.extras'),
        value: [draft.desktop === null ? '' : draft.desktop ? t('wiz.desktopOn') : t('wiz.desktopOff'), draft.updates ? t('wiz.updatesCheck') : ''].filter(Boolean).join(' · ') || keep,
      },
    );
  }

  useEffect(() => {
    if (!net) return;
    const id = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(id);
  }, [net]);

  const patch = (id: string, p: Partial<Task>) => setTasks((ts) => ts?.map((x) => (x.id === id ? { ...x, ...p } : x)) ?? null);

  const apply = async () => {
    if (running.current) return;
    running.current = true;
    const list: Task[] = [];
    if (machineSteps && draft.hostname.trim()) list.push({ id: 'name', label: t('wiz.hostname'), detail: draft.hostname.trim().toLowerCase(), state: 'wait' });
    if (machineSteps && draft.zone) list.push({ id: 'time', label: t('wiz.step.time'), detail: draft.zone.replace(/_/g, ' '), state: 'wait' });
    if (machineSteps && draft.desktop !== null) list.push({ id: 'desktop', label: t('wiz.desktop'), detail: draft.desktop ? t('wiz.desktopOn') : t('wiz.desktopOff'), state: 'wait' });
    if (machineSteps && draft.updates) list.push({ id: 'updates', label: t('wiz.updates'), detail: '', state: 'wait' });
    if (machineSteps && draft.net) list.push({ id: 'net', label: t('wiz.step.network'), detail: '', state: 'wait' });
    list.push({ id: 'look', label: t('wiz.saveLook'), detail: '', state: 'wait' });
    setTasks(list);
    let failed = false;
    const pause = () => new Promise((r) => window.setTimeout(r, 350)); // let each tick land

    for (const task of list) {
      patch(task.id, { state: 'run' });
      await pause();
      let state: TaskState = 'ok';
      let note: string | undefined;
      if (task.id === 'name') {
        const r = await hostApi.setHostname(draft.hostname.trim());
        if (!r.ok) [state, note] = ['fail', r.error];
      } else if (task.id === 'time') {
        const r = await hostApi.setTimeZone(draft.zone);
        if (!r.ok) [state, note] = ['fail', r.error];
      } else if (task.id === 'desktop') {
        const r = await hostAccessApi.setDesktopMode(draft.desktop ? 'full' : 'off');
        if (!r.ok) [state, note] = ['fail', r.error];
      } else if (task.id === 'updates') {
        const r = await api<{ available: boolean; latest?: string; current: string; error?: string }>('/api/v1/system/update?refresh=1');
        if (!r.ok || r.data.error) [state, note] = ['fail', r.error ?? r.data.error];
        else note = r.data.available ? t('wiz.updateAvail', { v: r.data.latest ?? '' }) : t('wiz.updateNone');
      } else if (task.id === 'net' && draft.net) {
        const n = draft.net;
        const v4: IPv4Config = n.v4.method === 'manual' ? { ...n.v4, gateway: n.v4.gateway ?? '' } : { method: 'auto', dns: n.v4.dns };
        const v6: IPv6Config | undefined = n.v6mode === 'keep' ? undefined : n.v6mode === 'manual' ? { ...n.v6, method: 'manual' } : { method: n.v6mode, dns: n.v6mode === 'auto' ? n.v6.dns : [] };
        const r = await hostApi.setIP(n.conn, v4, v6);
        if (!r.ok) [state, note] = ['fail', r.error];
        else {
          const result = await waitForNetwork(r.data.token, r.data.revert_in, nextOrigin(draft, machine.hostname), setNet);
          setNet(null);
          if (result !== 'kept') [state, note] = ['fail', t('wiz.netReverted')];
        }
      }
      // 'look': voice and look were saved the moment they were picked (they
      // follow you to every browser); this row just says so.
      patch(task.id, { state, note });
      if (state === 'fail') failed = true;
    }
    running.current = false;
    // All good: on to the celebration. Something didn't work: let them read why first.
    if (failed) setStuck(true);
    else window.setTimeout(onDone, 900);
  };

  const left = net ? Math.max(0, Math.ceil((net.until - now) / 1000)) : 0;

  return (
    <Step icon="list" title={t('wiz.sumTitle')} text={tasks ? t('wiz.applying') : t('wiz.sumText')}>
      {!tasks ? (
        <>
          <div className="welcome-rows wiz-summary">
            {rows.map((r, i) => (
              <div key={r.step} className="welcome-row wiz-sum-row" style={{ '--i': i } as CSSProperties}>
                <Icon name={STEP_ICON[r.step]} size={16} />
                <span className="welcome-label">{r.label}</span>
                <b className="welcome-grow">{r.value}</b>
                <button type="button" className="ghost small" onClick={() => onEdit(r.step)}>
                  {t('wiz.change')}
                </button>
              </div>
            ))}
          </div>
          <div className="wiz-apply">
            <button type="button" className="ghost" onClick={onBack}>
              <Icon name="chevronLeft" size={14} /> {t('common.back')}
            </button>
            <button type="button" className="welcome-next wiz-go" autoFocus onClick={() => void apply()}>
              <Icon name="check" size={15} /> {t('wiz.apply')}
            </button>
          </div>
        </>
      ) : (
        <ul className="wiz-tasks" aria-live="polite">
          {tasks.map((task) => (
            <li key={task.id} className={`wiz-task ${task.state}`}>
              <span className="wiz-task-mark" aria-hidden>
                {task.state === 'run' ? <span className="spinner sm" /> : task.state === 'ok' ? <Icon name="check" size={14} /> : task.state === 'fail' ? <Icon name="alert" size={14} /> : null}
              </span>
              <span className="wiz-task-text">
                <b>{task.label}</b>
                {task.detail && <span> · {task.detail}</span>}
                {task.note && <span className="wiz-task-note">{task.note}</span>}
                {task.id === 'net' && task.state === 'run' && net && (
                  <span className="wiz-net-wait">
                    <span className="wiz-countdown" style={{ '--p': left / 60 } as CSSProperties}>
                      {left}s
                    </span>
                    <span>
                      {net.link ? t('wiz.netOpenNew') : t('wiz.netChecking')}
                      {net.link && (
                        <a className="wiz-net-link" href={net.link} target="_blank" rel="noreferrer">
                          {new URL(net.link).host}
                        </a>
                      )}
                    </span>
                  </span>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
      {stuck && (
        <div className="wiz-apply">
          <button type="button" className="welcome-next wiz-go" autoFocus onClick={onDone}>
            {t('wiz.next')} <Icon name="chevronRight" size={15} />
          </button>
        </div>
      )}
    </Step>
  );
}

/**
 * Waits for proof the new address works: this page reaching the server (same
 * origin: the name didn't change), or the new address answering — then the
 * browser goes there with the confirm token in the fragment (App picks it
 * up). Without proof within the window, the server puts the old settings back.
 */
async function waitForNetwork(
  token: string,
  windowS: number,
  origin: string | null,
  show: (s: { token: string; until: number; link: string | null }) => void,
): Promise<'kept' | 'gone'> {
  const until = Date.now() + windowS * 1000;
  const link = origin ? `${origin}/#net-confirm=${token}` : null;
  show({ token, until, link });
  const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms));
  await sleep(1500);
  while (Date.now() < until) {
    const r = await confirmNetwork(token);
    if (r === 'kept') return 'kept';
    if (r === 'gone') return 'gone';
    if (origin) {
      try {
        // Opaque, but it only resolves when the new address answers.
        await fetch(`${origin}/healthz`, { mode: 'no-cors', cache: 'no-store', signal: AbortSignal.timeout(2500) });
        window.location.href = link!;
        await sleep(windowS * 1000);
      } catch {
        /* not there yet (or its certificate isn't trusted yet: the link works) */
      }
    }
    await sleep(2000);
  }
  return 'gone';
}

// ---- finish: a small celebration ----

const CONFETTI = Array.from({ length: 28 }, (_, i) => i);

function Finish({ draft, oldHost, onClose }: { draft: Draft; oldHost: string; onClose: () => void }) {
  const t = useT();
  const moved = nextOrigin(draft, oldHost);
  return (
    <div className="wiz-finish">
      <div className="wiz-confetti" aria-hidden>
        {CONFETTI.map((i) => (
          <span key={i} style={{ '--i': i, '--x': `${((i * 37) % 100) - 50}`, '--r': `${(i * 53) % 360}deg`, '--d': `${(i % 7) * 0.06}s` } as CSSProperties} />
        ))}
      </div>
      <span className="welcome-check wiz-check">
        <Icon name="check" size={42} />
      </span>
      <h1>{t('wiz.doneTitle')}</h1>
      <p>{t('wiz.doneText')}</p>
      {moved && (
        <p className="wiz-hint">
          {t('wiz.doneMoved')}{' '}
          <a href={moved}>{new URL(moved).host}</a>
        </p>
      )}
      <div className="wiz-intro-actions">
        <button type="button" className="welcome-next wiz-go" autoFocus onClick={onClose}>
          {t('wiz.doneGo')}
        </button>
        <button type="button" className="ghost small" onClick={() => { onClose(); openApp('settings'); }}>
          {t('wiz.doneSettings')}
        </button>
      </div>
    </div>
  );
}
