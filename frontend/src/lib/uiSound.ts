// Sound effects and click micro-interactions. Tones are synthesized with Web
// Audio (no sound files). Settings → Sound decides *when* they play: clicks,
// windows opening/closing, notifications, and lock/unlock — plus volume.
// The click glow pulse is visual and follows the Cyber-Deck theme instead.
import { usePrefs, type PrefValues } from '../state/prefs';
import { useToasts } from '../state/toasts';
import { useWM } from '../state/windows';

export type Cue = 'click' | 'open' | 'close' | 'success' | 'info' | 'error' | 'lock' | 'unlock';

/** Which Settings → Sound switch controls each cue. */
const CATEGORY: Record<Cue, keyof PrefValues> = {
  click: 'soundClicks',
  open: 'soundWindows',
  close: 'soundWindows',
  success: 'soundAlerts',
  info: 'soundAlerts',
  error: 'soundAlerts',
  lock: 'soundLock',
  unlock: 'soundLock',
};

// Each theme has its own voice.
const VOICES = {
  glass: { type: 'sine' as OscillatorType, freq: 880, gain: 0.045 },
  classic: { type: 'sine' as OscillatorType, freq: 1040, gain: 0.05 },
  cyberdeck: { type: 'triangle' as OscillatorType, freq: 1320, gain: 0.06 },
};

// A cue is a few notes: [start freq × voice, end freq × voice, start (s), length (s)].
const NOTES: Record<Cue, [number, number, number, number][]> = {
  click: [[1, 0.85, 0, 0.045]],
  open: [[0.6, 1.2, 0, 0.12]],
  close: [[1.1, 0.5, 0, 0.12]],
  info: [[0.75, 0.75, 0, 0.09]],
  success: [[0.75, 0.75, 0, 0.08], [1.0, 1.0, 0.09, 0.12]],
  error: [[0.5, 0.5, 0, 0.11], [0.38, 0.38, 0.13, 0.16]],
  lock: [[1.0, 1.0, 0, 0.07], [0.75, 0.75, 0.08, 0.07], [0.5, 0.5, 0.16, 0.12]],
  unlock: [[0.5, 0.5, 0, 0.07], [0.75, 0.75, 0.08, 0.07], [1.0, 1.0, 0.16, 0.14]],
};

let ctx: AudioContext | null = null;
let last = 0;

function audio(): AudioContext | null {
  try {
    ctx ??= new AudioContext();
    if (ctx.state === 'suspended') void ctx.resume();
    return ctx;
  } catch {
    return null;
  }
}

/** Play a cue if sound effects and its category are on. `force` ignores the switches (for previews). */
export function playCue(cue: Cue, force = false) {
  const p = usePrefs.getState();
  if (!force && (!p.uiSounds || !p[CATEGORY[cue]])) return;
  // Focus silences routine notification sounds (errors still chime).
  if (!force && p.focusMode && (cue === 'success' || cue === 'info')) return;
  const now = performance.now();
  if (cue === 'click' && now - last < 35) return; // don't machine-gun on rapid clicks
  last = now;
  const ac = audio();
  if (!ac || p.soundVolume <= 0) return;
  const v = VOICES[p.uiTheme] ?? VOICES.classic;
  const peak = v.gain * (p.soundVolume / 50); // 50 = the voice's natural level
  const t0 = ac.currentTime + 0.005;
  for (const [from, to, at, dur] of NOTES[cue]) {
    const osc = ac.createOscillator();
    const amp = ac.createGain();
    const t = t0 + at;
    osc.type = v.type;
    osc.frequency.setValueAtTime(v.freq * from, t);
    osc.frequency.exponentialRampToValueAtTime(v.freq * to, t + dur);
    amp.gain.setValueAtTime(0.0001, t);
    amp.gain.exponentialRampToValueAtTime(Math.max(0.0002, peak), t + 0.006);
    amp.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    osc.connect(amp).connect(ac.destination);
    osc.start(t);
    osc.stop(t + dur + 0.02);
  }
}

const CLICKABLE = 'button, [role="button"], [role="switch"], a[href], .dock-item, .desktop-icon, .launcher-app, select, input[type="checkbox"]';

/** Wire up click blips, glow pulses, window and notification cues. Returns a cleanup. */
export function installUiFeedback(): () => void {
  const onDown = (e: PointerEvent) => {
    if (e.button !== 0) return;
    const el = (e.target as Element | null)?.closest<HTMLElement>(CLICKABLE);
    if (!el || (el as HTMLButtonElement).disabled) return;
    playCue('click');
    const p = usePrefs.getState();
    if (p.uiTheme !== 'classic' && !p.reduceMotion) {
      el.classList.remove('fx-pulse');
      void el.offsetWidth; // restart the animation
      el.classList.add('fx-pulse');
      window.setTimeout(() => el.classList.remove('fx-pulse'), 450);
    }
  };
  document.addEventListener('pointerdown', onDown, true);

  let count = useWM.getState().windows.length;
  const offWM = useWM.subscribe((s) => {
    if (s.windows.length > count) playCue('open');
    else if (s.windows.length < count) playCue('close');
    count = s.windows.length;
  });

  let seen = new Set(useToasts.getState().toasts.map((t) => t.id));
  const offToasts = useToasts.subscribe((s) => {
    for (const t of s.toasts) if (!seen.has(t.id)) playCue(t.kind);
    seen = new Set(s.toasts.map((t) => t.id));
  });

  return () => {
    document.removeEventListener('pointerdown', onDown, true);
    offWM();
    offToasts();
  };
}
