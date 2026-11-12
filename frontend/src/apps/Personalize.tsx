import { useRef, useState, type ReactNode } from 'react';
import { AccentPicker } from '../components/AccentPicker';
import { Icon } from '../components/Icon';
import {
  DEFAULT_PREFS,
  ICON_STYLES,
  PERSONALIZATION_KEYS,
  UI_THEMES,
  WALLPAPERS,
  customWallpapers,
  deleteCustomWallpaper,
  saveCustomWallpaper,
  shownWallpaper,
  usePrefs,
  type PrefValues,
} from '../state/prefs';
import { useDesktopIcons } from '../state/desktopIcons';
import { confirmDialog } from '../state/confirm';
import { tr, useT } from '../i18n';

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

/** The theme cards: Glass, NoCap classic and Cyber-Deck, applied on click. */
export function ThemeGallery() {
  const uiTheme = usePrefs((s) => s.uiTheme);
  const set = usePrefs((s) => s.set);
  return (
    <div className="theme-gallery">
      {UI_THEMES.map((t) => (
        <button key={t.id} type="button" className={`theme-card ${uiTheme === t.id ? 'on' : ''}`} data-preview={t.id}
          onClick={() => set({ uiTheme: t.id })} aria-pressed={uiTheme === t.id}>
          <span className="theme-preview">
            <span className="theme-preview-window" style={t.id === 'cyberdeck' ? { borderColor: t.swatch[0], boxShadow: `0 0 12px ${t.swatch[0]}66` } : undefined}>
              <span style={{ background: t.id === 'glass' ? 'var(--accent)' : t.swatch[0] }} />
              <span style={{ background: t.id === 'glass' ? 'rgba(255,255,255,.75)' : t.swatch[1] }} />
            </span>
          </span>
          <span className="theme-name">
            {t.name === 'NoCap' ? 'NoCap classic' : t.name}
            {uiTheme === t.id && <Icon name="check" size={14} />}
          </span>
        </button>
      ))}
    </div>
  );
}

/** The Transparency slider (0% solid … 100% the most see-through). */
export function TransparencySlider() {
  const t = useT();
  const transparency = usePrefs((s) => s.transparency);
  const set = usePrefs((s) => s.set);
  return (
    <Slider label={t('pz.transparency')} hint={t('pz.transparencyHint')} value={transparency}
      min={0} max={100} step={5} format={(v) => (v === 0 ? t('pz.solid') : `${v}%`)} onChange={(v) => set({ transparency: v, reduceTransparency: v === 0 })} />
  );
}

export function Appearance() {
  const t = useT();
  const prefs = usePrefs();
  const set = prefs.set;
  const classic = prefs.uiTheme === 'classic';
  const current = UI_THEMES.find((t) => t.id === prefs.uiTheme) ?? UI_THEMES[0];
  return (
    <div className="stack settings-page">
      <Section title={t('pz.theme')} hint={current.blurb}>
        <Row>
          <ThemeGallery />
        </Row>
        {classic && (
          <Row label={t('pz.lightDark')} hint={t('pz.lightDarkHint')}>
            <Choice value={prefs.theme} onChange={(theme) => set({ theme })} options={[
              { id: 'auto', label: t('pz.auto') }, { id: 'light', label: t('pz.light') }, { id: 'dark', label: t('pz.dark') },
            ]} />
          </Row>
        )}
        <Row label={t('pz.accent')} stacked
          hint={prefs.uiTheme === 'glass' ? t('pz.accentGlass') : classic ? t('pz.accentClassic') : t('pz.accentFui')}>
          <AccentPicker />
        </Row>
      </Section>

      <WallpaperSections />

      <Section title={t('pz.display')} hint={t('pz.displayHint')}>
        <Slider label={t('pz.brightness')} hint={t('pz.brightnessHint')} value={prefs.brightness}
          min={30} max={100} step={1} format={(v) => `${v}%`} onChange={(brightness) => set({ brightness })} />
      </Section>

      <Section title={t('pz.icons')} hint={t('pz.iconsHint')}>
        <Row label={t('pz.style')}>
          <Choice value={prefs.iconStyle} onChange={(iconStyle) => set({ iconStyle })} options={ICON_STYLES} />
        </Row>
      </Section>

      <Section title={t('pz.effects')} hint={t('pz.effectsHint')}>
        {prefs.uiTheme === 'cyberdeck' && (
          <Toggle label={t('pz.grid')} hint={t('pz.gridHint')} checked={prefs.fxGrid} onChange={(fxGrid) => set({ fxGrid })} />
        )}
        <TransparencySlider />
        <Toggle label={t('pz.motion')} hint={t('pz.motionHint')} checked={prefs.reduceMotion} onChange={(reduceMotion) => set({ reduceMotion })} />
        {classic && (
          <Toggle label={t('pz.square')} hint={t('pz.squareHint')} checked={prefs.squareCorners} onChange={(squareCorners) => set({ squareCorners })} />
        )}
      </Section>

      <ResetSection />
    </div>
  );
}

// ---- Wallpaper ----

/**
 * The built-in wallpapers, the pictures you added (each with a trash button)
 * and an upload tile. Clicking one applies it right away. `strip` = one
 * scrolling row (the welcome); otherwise a grid.
 */
export function WallpaperStrip({ strip = false }: { strip?: boolean }) {
  const t = useT();
  const wallpaper = usePrefs((s) => s.wallpaper);
  const set = usePrefs((s) => s.set);
  const fileRef = useRef<HTMLInputElement>(null);
  const [mine, setMine] = useState(customWallpapers);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const shown = shownWallpaper(wallpaper);

  const onPick = async (file: File | undefined) => {
    if (!file) return;
    setError(null);
    setBusy(true);
    try {
      const id = await saveCustomWallpaper(file);
      setMine(customWallpapers());
      set({ wallpaper: id });
    } catch (e) {
      setError(e instanceof Error && e.message === 'too-large' ? t('pz.tooLarge') : t('pz.badImage'));
    } finally {
      setBusy(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  };

  const remove = async (id: string) => {
    const ok = await confirmDialog({ title: tr('pz.deletePhotoTitle'), message: tr('pz.deletePhotoMsg'), confirmLabel: tr('common.delete'), danger: true });
    if (!ok) return;
    deleteCustomWallpaper(id);
    setMine(customWallpapers());
  };

  return (
    <>
      <div className={strip ? 'wp-strip' : 'wallpapers'}>
        {WALLPAPERS.map((w) => (
          <button key={w.id} type="button" className={`wp-thumb ${shown === w.id ? 'on' : ''}`} onClick={() => set({ wallpaper: w.id })} title={w.name}>
            <span className={`wp-preview wp-${w.id}`} />
            {!strip && <span className="small">{w.name}</span>}
          </button>
        ))}
        {mine.map((w) => (
          <div key={w.id} className={`wp-thumb wp-mine ${shown === w.id ? 'on' : ''}`}>
            <button type="button" className="wp-pick" onClick={() => set({ wallpaper: w.id })} title={t('pz.yourPhoto')}>
              <span className="wp-preview" style={{ backgroundImage: `url("${w.url}")`, backgroundSize: 'cover', backgroundPosition: 'center' }} />
              {!strip && <span className="small">{t('pz.yourPhoto')}</span>}
            </button>
            <button type="button" className="wp-delete" aria-label={t('pz.deletePhoto')} title={t('pz.deletePhoto')} onClick={() => void remove(w.id)}>
              <Icon name="trash" size={13} />
            </button>
          </div>
        ))}
        <button type="button" className="wp-thumb" onClick={() => fileRef.current?.click()} disabled={busy} title={t('pz.addPhotoTitle')}>
          <span className="wp-preview wp-upload">{busy ? <span className="spinner sm" /> : <Icon name="upload" size={20} />}</span>
          {!strip && <span className="small">{t('pz.addPhoto')}</span>}
        </button>
        <input ref={fileRef} type="file" accept="image/*" hidden onChange={(e) => void onPick(e.target.files?.[0])} />
      </div>
      {error && <p className="error small">{error}</p>}
    </>
  );
}

/** Wallpaper picker and dim slider: part of the Appearance page. */
function WallpaperSections() {
  const t = useT();
  const prefs = usePrefs();
  const set = prefs.set;
  const custom = shownWallpaper(prefs.wallpaper).startsWith('custom');
  return (
    <>
      <Section title={t('pz.wallpapers')} hint={prefs.uiTheme === 'cyberdeck' && !custom ? t('pz.cyberWall') : t('pz.wallpapersHint')}>
        <Row>
          <WallpaperStrip />
        </Row>
        <Slider label={t('pz.dim')} hint={t('pz.dimHint')} value={prefs.wallpaperDim}
          min={0} max={60} step={5} format={(v) => (v ? `${v}%` : t('pz.off'))} onChange={(wallpaperDim) => set({ wallpaperDim })} />
      </Section>
    </>
  );
}

// ---- Dock ----

export function DockSettings() {
  const t = useT();
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title={t('pz.layout')} hint={t('pz.layoutHint')}>
        <Row label={t('pz.position')}>
          <Choice value={prefs.dockPosition} onChange={(dockPosition) => set({ dockPosition })} options={[
            { id: 'left', label: t('pz.left') }, { id: 'bottom', label: t('pz.bottom') }, { id: 'right', label: t('pz.right') },
          ]} />
        </Row>
        <Slider label={t('pz.iconSize')} value={prefs.dockSize} min={32} max={64} step={2} format={(v) => `${v}px`} onChange={(dockSize) => set({ dockSize })} />
        <Row label={t('pz.magnify')}>
          <Choice value={prefs.dockMagnify} onChange={(dockMagnify) => set({ dockMagnify })} options={[
            { id: 'off', label: t('pz.off') }, { id: 'small', label: t('pz.small') }, { id: 'large', label: t('pz.large') },
          ]} />
        </Row>
      </Section>

      <Section title={t('pz.behavior')} hint={t('pz.behaviorHint')}>
        <Toggle label={t('pz.autoHide')} hint={t('pz.autoHideHint')}
          checked={prefs.dockAutoHide} onChange={(dockAutoHide) => set({ dockAutoHide })} />
        <Toggle label={t('pz.recents')} hint={t('pz.recentsHint')} checked={prefs.dockRecents} onChange={(dockRecents) => set({ dockRecents })} />
        <Toggle label={t('pz.dots')} hint={t('pz.dotsHint')} checked={prefs.dockIndicators} onChange={(dockIndicators) => set({ dockIndicators })} />
      </Section>

      <p className="settings-tip">{t('pz.dockTip')}</p>
    </div>
  );
}

// ---- Desktop & Widgets ----

export function DesktopSettings() {
  const t = useT();
  const prefs = usePrefs();
  const set = prefs.set;
  const cleanUp = useDesktopIcons((s) => s.cleanUp);
  const iconCount = useDesktopIcons((s) => s.icons.length);
  return (
    <div className="stack settings-page">
      <Section title={t('pz.widgets')} hint={t('pz.widgetsHint')}>
        <Toggle label={t('pz.showWidgets')} hint={t('pz.showWidgetsHint')}
          checked={prefs.widgets} onChange={(widgets) => set({ widgets })} />
      </Section>

      <Section title={t('pz.desktopIcons')} hint={t('pz.desktopIconsHint')}>
        <Row label={t('pz.iconSize')}>
          <Choice value={prefs.iconSize} onChange={(iconSize) => set({ iconSize })} options={[
            { id: 'small', label: t('pz.small') }, { id: 'medium', label: t('pz.medium') }, { id: 'large', label: t('pz.large') },
          ]} />
        </Row>
        <Toggle label={t('pz.iconNames')} checked={prefs.iconLabels} onChange={(iconLabels) => set({ iconLabels })} />
        <Toggle label={t('pz.snap')} hint={t('pz.snapHint')} checked={prefs.iconSnap} onChange={(iconSnap) => set({ iconSnap })} />
        <Row label={t('pz.cleanUp')} hint={t('pz.cleanUpHint')}>
          <button type="button" className="ghost small" disabled={!iconCount} onClick={cleanUp}>
            {t('pz.cleanUp')}
          </button>
        </Row>
      </Section>
    </div>
  );
}

// ---- Windows ----

export function WindowSettings() {
  const t = useT();
  const prefs = usePrefs();
  const set = prefs.set;
  return (
    <div className="stack settings-page">
      <Section title={t('pz.titleBar')} hint={t('pz.titleBarHint')}>
        <Row label={t('pz.winButtons')} hint={t('pz.winButtonsHint')}>
          <Choice value={prefs.titleButtons} onChange={(titleButtons) => set({ titleButtons })} options={[
            { id: 'right', label: t('pz.right') }, { id: 'left', label: t('pz.left') },
          ]} />
        </Row>
        <Row label={t('pz.dblClick')}>
          <Choice value={prefs.titleDoubleClick} onChange={(titleDoubleClick) => set({ titleDoubleClick })} options={[
            { id: 'maximize', label: t('common.maximize') }, { id: 'minimize', label: t('common.minimize') }, { id: 'none', label: t('pz.nothing') },
          ]} />
        </Row>
      </Section>
      <p className="settings-tip">{t('pz.winTip')}</p>
    </div>
  );
}

function ResetSection() {
  const t = useT();
  const set = usePrefs((s) => s.set);
  const reset = () => {
    const defaults = Object.fromEntries(PERSONALIZATION_KEYS.map((k) => [k, DEFAULT_PREFS[k]])) as Partial<PrefValues>;
    set(defaults);
  };
  return (
    <Section title={t('pz.resetSection')} hint={t('pz.resetSectionHint')}>
      <Row label={t('pz.resetRow')} hint={t('pz.resetHint')}>
        <button type="button" className="ghost small" onClick={reset}>
          {t('pz.reset')}
        </button>
      </Row>
    </Section>
  );
}
