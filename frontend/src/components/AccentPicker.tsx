import { UI_THEMES, WALLPAPERS, shownWallpaper, usePrefs, wallpaperSrc } from '../state/prefs';
import { ensurePalette, useWallPalette, wallChoices } from '../state/wallPalette';
import { useT } from '../i18n';

/**
 * Accent color: three colors taken from the current wallpaper, then a color
 * picker for anything else. The first one is automatic (empty pref): it
 * follows the wallpaper when it changes (in Glass; the other themes use their
 * own color). Built-in wallpapers lead with their own accent.
 */
export function AccentPicker({ compact = false }: { compact?: boolean }) {
  const t = useT();
  const accent = usePrefs((s) => s.accent);
  const uiTheme = usePrefs((s) => s.uiTheme);
  const wallpaper = usePrefs((s) => s.wallpaper);
  const set = usePrefs((s) => s.set);
  const wallId = shownWallpaper(wallpaper);
  const colors = useWallPalette((s) => s.byId[wallId]);
  if (!colors) ensurePalette(wallId, wallpaperSrc(wallId));
  const glass = uiTheme === 'glass';
  const builtIn = WALLPAPERS.find((w) => w.id === wallId)?.accent;
  const choices = wallChoices(builtIn, colors);
  const themeColor = UI_THEMES.find((x) => x.id === uiTheme)?.accent ?? UI_THEMES[0].accent;
  // The automatic choice: the wallpaper's main color in Glass, else the theme's.
  const auto = (glass ? choices[0] : undefined) ?? themeColor;
  // The others: the remaining wallpaper colors (all three outside Glass would
  // be four with the theme color, so keep it to three circles).
  const rest = (glass ? choices.slice(1) : choices).filter((c) => c.toLowerCase() !== auto.toLowerCase()).slice(0, 2);
  const current = accent || auto;
  const picked = (c: string) => accent.toLowerCase() === c.toLowerCase();
  const custom = !!accent && !rest.some(picked);

  if (compact) {
    return (
      <div className="accent-picker compact">
        <label className="accent-well" style={{ background: current }} title={t('pz.accentPick')}>
          <input type="color" value={current} aria-label={t('pz.accent')} onChange={(e) => set({ accent: e.target.value })} />
        </label>
      </div>
    );
  }
  return (
    <div className="accent-picker accent-choices" role="radiogroup" aria-label={t('pz.accent')}>
      <button type="button" role="radio" aria-checked={!accent} className={`accent-swatch auto ${!accent ? 'on' : ''}`}
        style={{ background: auto }} title={glass ? t('pz.accentMatch') : t('pz.accentTheme')} aria-label={glass ? t('pz.accentMatch') : t('pz.accentTheme')}
        onClick={() => set({ accent: '' })} />
      {rest.map((c) => (
        <button key={c} type="button" role="radio" aria-checked={picked(c)} aria-label={t('pz.accentFromWall')} title={t('pz.accentFromWall')}
          className={`accent-swatch ${picked(c) ? 'on' : ''}`} style={{ background: c }} onClick={() => set({ accent: c })} />
      ))}
      <label className={`accent-well ${custom ? 'on' : ''}`} style={{ background: custom ? current : undefined }} title={t('pz.accentPick')}>
        <input type="color" value={current} aria-label={t('pz.accentPick')} onChange={(e) => set({ accent: e.target.value })} />
      </label>
    </div>
  );
}
