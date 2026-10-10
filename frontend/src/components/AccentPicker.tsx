import { UI_THEMES, WALLPAPERS, usePrefs } from '../state/prefs';

const PRESETS = ['#2dd4bf', '#60a5fa', '#818cf8', '#a78bfa', '#f472b6', '#ef4444', '#fb923c', '#f59e0b', '#84cc16', '#34d399'];

/**
 * Accent color. Empty pref = automatic: in Glass it matches the wallpaper,
 * in the other themes it's the theme's own color. A preset or the color well
 * overrides it (in the FUI themes that recolors the neon too).
 */
export function AccentPicker({ compact = false }: { compact?: boolean }) {
  const accent = usePrefs((s) => s.accent);
  const uiTheme = usePrefs((s) => s.uiTheme);
  const wallpaper = usePrefs((s) => s.wallpaper);
  const set = usePrefs((s) => s.set);
  const glass = uiTheme === 'glass';
  const wall = WALLPAPERS.find((w) => w.id === wallpaper);
  const auto = (glass && wall ? wall.accent : undefined) ?? UI_THEMES.find((t) => t.id === uiTheme)?.accent ?? UI_THEMES[0].accent;
  const current = accent || auto;
  const autoLabel = glass ? 'Match wallpaper' : 'Theme color';
  const custom = !!accent && !PRESETS.includes(accent.toLowerCase());

  if (compact) {
    return (
      <div className="accent-picker compact">
        <label className="accent-well" style={{ background: current }} title="Pick an accent color">
          <input type="color" value={current} aria-label="Accent color" onChange={(e) => set({ accent: e.target.value })} />
        </label>
      </div>
    );
  }
  return (
    <div className="accent-picker accent-choices" role="radiogroup" aria-label="Accent color">
      <button type="button" role="radio" aria-checked={!accent} className={`accent-auto ${!accent ? 'on' : ''}`}
        title={autoLabel} onClick={() => set({ accent: '' })}>
        <span className="accent-dot" style={{ background: auto }} />
        {autoLabel}
      </button>
      {PRESETS.map((c) => (
        <button key={c} type="button" role="radio" aria-checked={accent.toLowerCase() === c} aria-label={c}
          className={`accent-swatch ${accent.toLowerCase() === c ? 'on' : ''}`} style={{ background: c }} onClick={() => set({ accent: c })} />
      ))}
      <label className={`accent-well ${custom ? 'on' : ''}`} style={{ background: custom ? current : undefined }} title="Pick any color">
        <input type="color" value={current} aria-label="Custom accent color" onChange={(e) => set({ accent: e.target.value })} />
      </label>
    </div>
  );
}
