import { UI_THEMES, usePrefs } from '../state/prefs';

/**
 * Accent color: a single color picker. Empty pref = the theme's own accent;
 * picking a color overrides it (in the FUI themes that recolors the neon too).
 */
export function AccentPicker({ compact = false }: { compact?: boolean }) {
  const accent = usePrefs((s) => s.accent);
  const uiTheme = usePrefs((s) => s.uiTheme);
  const set = usePrefs((s) => s.set);
  const themeAccent = UI_THEMES.find((t) => t.id === uiTheme)?.accent ?? UI_THEMES[0].accent;
  const current = accent || themeAccent;
  return (
    <div className={`accent-picker ${compact ? 'compact' : ''}`}>
      <label className="accent-well" style={{ background: current }} title="Pick an accent color">
        <input type="color" value={current} aria-label="Accent color" onChange={(e) => set({ accent: e.target.value })} />
      </label>
      {!compact && <span className="accent-hex">{current.toUpperCase()}</span>}
      {accent && (
        <button type="button" className="ghost small" onClick={() => set({ accent: '' })}>
          Use theme color
        </button>
      )}
    </div>
  );
}
