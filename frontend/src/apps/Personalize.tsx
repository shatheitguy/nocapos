import { useRef, useState, type ReactNode } from 'react';
import { AccentPicker } from '../components/AccentPicker';
import { Icon } from '../components/Icon';
import {
  DEFAULT_PREFS,
  PERSONALIZATION_KEYS,
  UI_THEMES,
  WALLPAPERS,
  loadCustomWallpaper,
  saveCustomWallpaper,
  usePrefs,
  type PrefValues,
} from '../state/prefs';
import { useDesktopIcons } from '../state/desktopIcons';

// Settings → Personalization pages, plus the layout pieces every settings page
// shares: a Section (title above a rounded card) holding evenly spaced Rows,
// each with its label on the left and its control on the right.

export function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="settings-section">
      <h3 className="settings-section-title">{title}</h3>
      {hint && <p className="settings-section-hint">{hint}</p>}
      <div className="settings-card">{children}</div>
    </section>
  );
}

/** One setting: label (+ hint) on the left, control on the right. `stacked` puts the control underneath. */
export function Row({ label, hint, stacked, children }: { label?: string; hint?: string; stacked?: boolean; children: ReactNode }) {
  return (
    <div className={`settings-row ${stacked ? 'stacked' : ''} ${label ? '' : 'full'}`}>
      {label && (
        <span className="settings-row-text">
          <span className="settings-row-label">{label}</span>
          {hint && <span className="settings-row-hint">{hint}</span>}
        </span>
      )}
      <div className="settings-row-control">{children}</div>
    </div>
  );
}

export function Toggle({ label, hint, checked, onChange, disabled }: { label: string; hint?: string; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <label className={`settings-row toggle row ${disabled ? 'dimmed' : ''}`}>
      <span className="settings-row-text">
        <span className="settings-row-label">{label}</span>
        {hint && <span className="settings-row-hint">{hint}</span>}
      </span>
      <input type="checkbox" role="switch" aria-checked={checked} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

export function Choice<T extends string>({ value, options, onChange }: { value: T; options: { id: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="segmented">
      {options.map((o) => (
        <button key={o.id} type="button" className={value === o.id ? 'on' : ''} onClick={() => onChange(o.id)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Slider({ label, hint, value, min, max, step = 1, format, onChange }: {
  label: string; hint?: string; value: number; min: number; max: number; step?: number; format: (v: number) => string; onChange: (v: number) => void;
}) {
  return (
    <label className="settings-row stacked">
      <span className="settings-row-text slider-head">
        <span className="settings-row-label">{label}</span>
        <span className="settings-value">{format(value)}</span>
      </span>
      {hint && <span className="settings-row-hint">{hint}</span>}
      <input
        type="range"
        className="nc-range"
        min={min}
        max={max}
        step={step}
        value={value}
        aria-label={label}
        style={{ ['--fill' as string]: `${((value - min) / (max - min)) * 100}%` }}
        onChange={(e) => onChange(Number(e.target.value))}
      />
    </label>
  );
}

// ---- Appearance ----

export function Appearance() {
  const prefs = usePrefs();
  const set = prefs.set;
  const classic = prefs.uiTheme === 'classic';
  const current = UI_THEMES.find((t) => t.id === prefs.uiTheme) ?? UI_THEMES[0];
  return (
    <div className="stack settings-page">
      <Section title="Theme">
        <Row>
          <div className="theme-gallery">
            {UI_THEMES.map((t) => (
              <button key={t.id} type="button" className={`theme-card ${prefs.uiTheme === t.id ? 'on' : ''}`} data-preview={t.id}
                onClick={() => set({ uiTheme: t.id })} aria-pressed={prefs.uiTheme === t.id}>
                <span className="theme-preview">
                  <span className="theme-preview-window" style={{ borderColor: t.swatch[0], boxShadow: `0 0 12px ${t.swatch[0]}66` }}>
                    <span style={{ background: t.swatch[0] }} />
                    <span style={{ background: t.swatch[1] }} />
                  </span>
                </span>
                <span className="theme-name">{t.name}</span>
              </button>
            ))}
          </div>
        </Row>
        <Row label={current.name} hint={current.blurb}>
          <span className="theme-dots">
            {current.swatch.map((c, i) => <span key={i} style={{ background: c }} />)}
          </span>
        </Row>
        {classic && (
          <Row label="Light or dark" hint="Auto follows your device's light or dark mode">
            <Choice value={prefs.theme} onChange={(theme) => set({ theme })} options={[
              { id: 'auto', label: 'Auto' }, { id: 'light', label: 'Light' }, { id: 'dark', label: 'Dark' },
            ]} />
          </Row>
        )}
        <Row label="Accent color" hint={classic ? 'Highlights, selections, sliders and switches' : 'Recolors the neon glow, highlights and switches'}>
          <AccentPicker />
        </Row>
      </Section>

      <Section title="Display">
        <Slider label="Brightness" hint="Dims NoCapOS on this screen — also in the Control Center" value={prefs.brightness}
          min={30} max={100} step={1} format={(v) => `${v}%`} onChange={(brightness) => set({ brightness })} />
      </Section>

      <Section title="Effects">
        {!classic && (
          <Toggle label="Data grid backdrop" hint="A faint holographic grid behind the desktop" checked={prefs.fxGrid} onChange={(fxGrid) => set({ fxGrid })} />
        )}
        <Toggle label="Reduce transparency" hint="Solid panels instead of frosted glass — sharper and faster" checked={prefs.reduceTransparency}
          onChange={(reduceTransparency) => set({ reduceTransparency })} />
        <Toggle label="Reduce motion" hint="Turn off animations, scan sweeps and click pulses" checked={prefs.reduceMotion} onChange={(reduceMotion) => set({ reduceMotion })} />
        {classic && (
          <Toggle label="Square corners" hint="Sharper windows and panels" checked={prefs.squareCorners} onChange={(squareCorners) => set({ squareCorners })} />
        )}
      </Section>

      <ResetSection />
    </div>
  );
}

// ---- Wallpaper ----

export function Wallpaper() {
  const prefs = usePrefs();
  const set = prefs.set;
  const fileRef = useRef<HTMLInputElement>(null);
  const [custom, setCustom] = useState<string | null>(loadCustomWallpaper);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const onPick = async (file: File | undefined) => {
    if (!file) return;
    setError(null);
    setBusy(true);
    try {
      setCustom(await saveCustomWallpaper(file));
      set({ wallpaper: 'custom' });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not use that image');
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  };

  return (
    <div className="stack settings-page">
      <Section title="Wallpaper" hint={prefs.uiTheme !== 'classic' && prefs.wallpaper !== 'custom'
        ? `${UI_THEMES.find((t) => t.id === prefs.uiTheme)?.name} shows its own backdrop. Your own photo still works here; switch to NoCap Classic for these wallpapers.`
        : undefined}>
        <Row>
          <div className="wallpapers">
            {WALLPAPERS.map((w) => (
              <button key={w.id} type="button" className={`wp-thumb ${prefs.wallpaper === w.id ? 'on' : ''}`} onClick={() => set({ wallpaper: w.id })}>
                <span className={`wp-preview wp-${w.id}`} />
                <span className="small">{w.name}</span>
              </button>
            ))}
            {custom && (
              <button type="button" className={`wp-thumb ${prefs.wallpaper === 'custom' ? 'on' : ''}`} onClick={() => set({ wallpaper: 'custom' })}>
                <span className="wp-preview" style={{ backgroundImage: `url("${custom}")`, backgroundSize: 'cover', backgroundPosition: 'center' }} />
                <span className="small">Your photo</span>
              </button>
            )}
            <button type="button" className="wp-thumb" onClick={() => fileRef.current?.click()} disabled={busy}>
              <span className="wp-preview wp-upload">{busy ? <span className="spinner sm" /> : <Icon name="upload" size={22} />}</span>
              <span className="small">{custom ? 'Change photo' : 'Your photo…'}</span>
            </button>
            <input ref={fileRef} type="file" accept="image/*" hidden onChange={(e) => void onPick(e.target.files?.[0])} />
          </div>
          {error && <p className="error small">{error}</p>}
        </Row>
      </Section>

      <Section title="Brightness">
        <Slider label="Dim wallpaper" hint="Makes icons and widgets easier to read on bright photos" value={prefs.wallpaperDim}
          min={0} max={60} step={5} format={(v) => (v ? `${v}%` : 'Off')} onChange={(wallpaperDim) => set({ wallpaperDim })} />
      </Section>
    </div>
  );
}

// ---- Dock ----

export function DockSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title="Layout">
        <Row label="Position on screen">
          <Choice value={prefs.dockPosition} onChange={(dockPosition) => set({ dockPosition })} options={[
            { id: 'left', label: 'Left' }, { id: 'bottom', label: 'Bottom' }, { id: 'right', label: 'Right' },
          ]} />
        </Row>
        <Slider label="Icon size" value={prefs.dockSize} min={32} max={64} step={2} format={(v) => `${v}px`} onChange={(dockSize) => set({ dockSize })} />
        <Row label="Magnify on hover">
          <Choice value={prefs.dockMagnify} onChange={(dockMagnify) => set({ dockMagnify })} options={[
            { id: 'off', label: 'Off' }, { id: 'small', label: 'Small' }, { id: 'large', label: 'Large' },
          ]} />
        </Row>
      </Section>

      <Section title="Behavior">
        <Toggle label="Automatically hide the Dock" hint="Move the pointer to the screen edge to show it — windows get the whole screen"
          checked={prefs.dockAutoHide} onChange={(dockAutoHide) => set({ dockAutoHide })} />
        <Toggle label="Show recent apps" hint="Up to three recently used apps after a divider" checked={prefs.dockRecents} onChange={(dockRecents) => set({ dockRecents })} />
        <Toggle label="Show dots for open apps" hint="A dot under each app that has a window open" checked={prefs.dockIndicators} onChange={(dockIndicators) => set({ dockIndicators })} />
      </Section>

      <p className="settings-tip">Tip: drag icons to reorder the Dock, or right-click the Dock to reset it.</p>
    </div>
  );
}

// ---- Desktop & Widgets ----

export function DesktopSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  const cleanUp = useDesktopIcons((s) => s.cleanUp);
  const iconCount = useDesktopIcons((s) => s.icons.length);
  return (
    <div className="stack settings-page">
      <Section title="Widgets">
        <Toggle label="Show widgets on the desktop" hint="Right-click the desktop and choose Edit widgets to add, move or resize them"
          checked={prefs.widgets} onChange={(widgets) => set({ widgets })} />
      </Section>

      <Section title="Desktop icons">
        <Row label="Icon size">
          <Choice value={prefs.iconSize} onChange={(iconSize) => set({ iconSize })} options={[
            { id: 'small', label: 'Small' }, { id: 'medium', label: 'Medium' }, { id: 'large', label: 'Large' },
          ]} />
        </Row>
        <Toggle label="Show icon names" checked={prefs.iconLabels} onChange={(iconLabels) => set({ iconLabels })} />
        <Toggle label="Snap icons to grid" hint="Dropped icons line up neatly" checked={prefs.iconSnap} onChange={(iconSnap) => set({ iconSnap })} />
        <Row label="Clean Up" hint="Line up every desktop icon on the grid">
          <button type="button" className="ghost small" disabled={!iconCount} onClick={cleanUp}>
            Clean Up
          </button>
        </Row>
      </Section>
    </div>
  );
}

// ---- Windows ----

export function WindowSettings() {
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title="Title bar">
        <Row label="Window buttons" hint="Close, minimize and maximize">
          <Choice value={prefs.titleButtons} onChange={(titleButtons) => set({ titleButtons })} options={[
            { id: 'right', label: 'Right' }, { id: 'left', label: 'Left (macOS)' },
          ]} />
        </Row>
        <Row label="Double-click a title bar to">
          <Choice value={prefs.titleDoubleClick} onChange={(titleDoubleClick) => set({ titleDoubleClick })} options={[
            { id: 'maximize', label: 'Maximize' }, { id: 'minimize', label: 'Minimize' }, { id: 'none', label: 'Nothing' },
          ]} />
        </Row>
      </Section>
      <p className="settings-tip">Tip: right-click a title bar or an app icon for New window; drag a window to a screen edge to snap it.</p>
    </div>
  );
}

function ResetSection() {
  const set = usePrefs((s) => s.set);
  const reset = () => {
    const defaults = Object.fromEntries(PERSONALIZATION_KEYS.map((k) => [k, DEFAULT_PREFS[k]])) as Partial<PrefValues>;
    set(defaults);
  };
  return (
    <Section title="Reset">
      <Row label="Reset personalization" hint="Theme, colors, wallpaper, Dock, desktop and window settings go back to defaults">
        <button type="button" className="ghost small" onClick={reset}>
          Reset
        </button>
      </Row>
    </Section>
  );
}
