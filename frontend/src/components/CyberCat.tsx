import { useEffect, useRef, useState, type RefObject } from 'react';
import { createPortal } from 'react-dom';

export type CatMood = 'idle' | 'watch' | 'shy' | 'peek' | 'error' | 'happy';

const WALK = 150; // px/s
const RUN = 340;
const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v));

/** A leg hanging from its hip at 0,0 with the paw pointing forward. */
const LEG = 'M-7 -4 L-6 32 Q-6 40 1 40 L9 40 Q14 40 12 35 L7 -4Z';
const EYES = [54, 86];

/**
 * An anime cat that lives along the bottom of the login and lock screens.
 * It walks (or runs) after the pointer, sits and watches it, wanders off when
 * you stop moving, trots over to the password field when you focus it, hides
 * its eyes while you type a password, peeks when it's shown, hisses at a wrong
 * password and purrs when you sign in — or when you click it.
 *
 * `anchor` is the password field; `look` (-1…1) is where the caret is;
 * `pulse` changes on every key so the ears twitch.
 */
export function CyberCat({
  mood,
  look = 0,
  pulse = 0,
  anchor,
}: {
  mood: CatMood;
  look?: number;
  pulse?: number;
  anchor?: RefObject<HTMLElement | null>;
}) {
  const [petted, setPetted] = useState(false);
  const shown: CatMood = petted && (mood === 'idle' || mood === 'watch') ? 'happy' : mood;
  const [walking, setWalking] = useState(false);

  const root = useRef<HTMLDivElement>(null);
  const walkSvg = useRef<SVGSVGElement>(null);
  const sitSvg = useRef<SVGSVGElement>(null);
  const legs = useRef<(SVGPathElement | null)[]>([]);
  const walkBody = useRef<SVGGElement>(null);
  const walkTail = useRef<(SVGPathElement | null)[]>([]);
  const sitTail = useRef<(SVGPathElement | null)[]>([]);
  const sitHead = useRef<SVGGElement>(null);
  const pupils = useRef<SVGGElement>(null);
  const moodRef = useRef(shown);
  const lookRef = useRef(look);
  moodRef.current = shown;
  lookRef.current = look;

  const petTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(petTimer.current), []);
  const pet = () => {
    setPetted(true);
    window.clearTimeout(petTimer.current);
    petTimer.current = window.setTimeout(() => setPetted(false), 1800);
  };

  useEffect(() => {
    const reduce =
      window.matchMedia('(prefers-reduced-motion: reduce)').matches || document.documentElement.hasAttribute('data-reduce-motion');
    let x = window.innerWidth / 2 + Math.min(260, window.innerWidth / 3);
    let target = x;
    let facing = -1;
    let phase = 0;
    let isWalking = false;
    let running = false;
    let last = performance.now();
    let px = x;
    let py = window.innerHeight / 2;
    let moved = -1e9; // when the pointer last moved
    let wanderAt = performance.now() + 8000;
    const gaze = { x: 0, y: 0 };
    let raf = 0;

    const onMove = (e: PointerEvent) => {
      px = e.clientX;
      py = e.clientY;
      moved = performance.now();
    };
    window.addEventListener('pointermove', onMove);

    const tick = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      const m = moodRef.current;
      const W = window.innerWidth;
      const edge = 60;

      // Where to go.
      const a = anchor?.current?.getBoundingClientRect();
      if ((m === 'watch' || m === 'shy' || m === 'peek') && a) {
        target = a.left + a.width / 2 + a.width * 0.38; // sit just below the right end of the field
      } else if (m === 'idle') {
        if (now - moved < 6000) {
          if (Math.abs(px - x) > 110) target = px + (px < x ? 46 : -46);
        } else if (now > wanderAt) {
          target = edge + Math.random() * (W - 2 * edge);
          wanderAt = now + 9000 + Math.random() * 9000;
        }
      } else {
        target = x; // error / happy: react right where it is
      }
      target = clamp(target, edge, W - edge);

      // Move.
      const d = target - x;
      const go = Math.abs(d) > (isWalking ? 4 : 30);
      if (go && reduce) {
        x = target;
      } else if (go) {
        running = Math.abs(d) > 380 || (running && Math.abs(d) > 120);
        const speed = running ? RUN : WALK;
        const step = Math.sign(d) * Math.min(Math.abs(d), speed * dt);
        x += step;
        facing = Math.sign(d) || facing;
        phase += Math.abs(step) / (running ? 20 : 14);
      }
      const nowWalking = go && !reduce;
      if (nowWalking !== isWalking) {
        isWalking = nowWalking;
        setWalking(nowWalking);
      }
      if (root.current) root.current.style.transform = `translateX(${x.toFixed(1)}px)`;

      if (isWalking) {
        const amp = running ? 38 : 26;
        const s = Math.sin(phase);
        const angles = [s, -s, -s, s]; // far back, far front, near back, near front
        legs.current.forEach((l, i) => l?.setAttribute('transform', `rotate(${(angles[i] * amp).toFixed(1)})`));
        walkBody.current?.setAttribute('transform', `translate(0 ${(-Math.abs(Math.cos(phase)) * (running ? 3.5 : 2)).toFixed(2)})`);
        if (walkSvg.current) walkSvg.current.style.transform = facing < 0 ? 'scaleX(-1)' : '';
        const sw = Math.sin(phase * 0.5) * 6;
        const tail = `M50 60 C34 54 ${(24 + sw).toFixed(1)} 40 ${(28 + sw * 1.5).toFixed(1)} ${running ? 30 : 20}`;
        walkTail.current.forEach((t) => t?.setAttribute('d', tail));
      } else {
        // Sitting: look at the pointer, or up at the field while you type a name.
        let tx = px;
        let ty = py;
        if (m === 'watch' && a) {
          tx = a.left + a.width / 2 + lookRef.current * a.width * 0.45;
          ty = a.top + a.height / 2;
        }
        const r = sitSvg.current?.getBoundingClientRect();
        let gx = 0;
        let gy = 0;
        if (r && (m === 'idle' || m === 'watch' || m === 'peek')) {
          gx = clamp((tx - (r.left + r.width / 2)) / 260, -1, 1);
          gy = clamp((ty - (r.top + r.height * 0.35)) / 260, -1, 1);
        }
        gaze.x += (gx - gaze.x) * Math.min(1, dt * 10);
        gaze.y += (gy - gaze.y) * Math.min(1, dt * 10);
        pupils.current?.setAttribute('transform', `translate(${(gaze.x * 3.4).toFixed(2)} ${(gaze.y * 2.6).toFixed(2)})`);
        sitHead.current?.setAttribute('transform', `rotate(${(gaze.x * 7).toFixed(2)} 70 70) translate(0 ${(gaze.y * 1.5).toFixed(2)})`);
        const up = m === 'happy' || m === 'error';
        const sw = Math.sin(now / (m === 'happy' ? 240 : m === 'error' ? 120 : 700)) * (up ? 7 : 5);
        const f = (v: number) => v.toFixed(1);
        const tail = up
          ? `M96 136 C116 140 126 124 ${f(124 + sw)} 104 S ${f(120 + sw * 1.4)} 74 ${f(112 + sw * 1.6)} 70`
          : `M94 140 C118 147 138 138 ${f(134 + sw)} 118 S ${f(126 + sw * 1.5)} 94 ${f(117 + sw * 1.6)} 98`;
        sitTail.current.forEach((t) => t?.setAttribute('d', tail));
      }
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener('pointermove', onMove);
    };
  }, [anchor]);

  const furLine = 'cat-fur cat-line';
  return createPortal(
    <div ref={root} className={`cat-pet mood-${shown}`} aria-hidden="true">
      {/* ---- walking, side view (faces right; flipped to walk left) ---- */}
      <svg ref={walkSvg} className={`cat-pose walk ${walking ? '' : 'off'}`} viewBox="0 0 180 120">
        <g ref={walkBody}>
          {[68, 124].map((hx, i) => (
            <g key={hx} transform={`translate(${hx} 76)`}>
              <path ref={(el) => void (legs.current[i] = el)} d={LEG} className="cat-fur-dk cat-line" />
            </g>
          ))}
          <path ref={(el) => void (walkTail.current[0] = el)} className="cat-tail-line" d="M50 60 C34 54 24 40 28 20" />
          <path ref={(el) => void (walkTail.current[1] = el)} className="cat-tail-fur" d="M50 60 C34 54 24 40 28 20" />
          <path d="M46 64 C44 46 66 38 98 39 C124 40 140 47 142 62 C144 78 128 88 98 88 C70 88 48 82 46 64Z" className={furLine} />
          <path d="M66 84 C84 90 114 90 132 80 C128 86 114 89 98 89 C84 89 72 87 66 84Z" className="cat-cream" />
          <path d="M80 41 q3 7 0 12 M94 40 q3 7 0 12 M108 41 q3 7 0 11" className="cat-stripe" />
          {[60, 118].map((hx, i) => (
            <g key={hx} transform={`translate(${hx} 78)`}>
              <path ref={(el) => void (legs.current[i + 2] = el)} d={LEG} className={furLine} />
            </g>
          ))}
          <g transform="translate(146 44)">
            <path d="M-19 -8 L-15 -38 L1 -19Z" className={furLine} />
            <path d="M-15 -14 L-13 -30 L-4 -19Z" className="cat-ear-in" />
            <path d="M-3 -19 L11 -39 L17 -10Z" className={furLine} />
            <ellipse rx="24" ry="21" className={furLine} />
            <path d="M-8 -20 q1 5 0 8 M-1 -21 q1 5 0 8" className="cat-stripe thin" />
            <ellipse cx="14" cy="8" rx="11" ry="8" className="cat-cream" />
            <ellipse cx="1" cy="8" rx="5" ry="3" className="cat-blush" />
            <g className="cat-blink">
              <ellipse cx="6" cy="-4" rx="6.5" ry="8.5" fill="url(#cat-iris)" className="cat-line" />
              <ellipse cx="8.5" cy="-4" rx="3" ry="6.5" className="cat-pupil" />
              <circle cx="4.5" cy="-8.5" r="2.4" className="cat-shine" />
            </g>
            <path d="M24 1 h5.5 l-2.7 3.6z" className="cat-nose" />
            <path d="M26.5 4.6 q-1 4 -6 3.4" className="cat-mouth" />
            <path d="M19 6 l19 -4 M19 9 l20 1" className="cat-whisk" />
          </g>
        </g>
      </svg>

      {/* ---- sitting, facing you ---- */}
      <svg ref={sitSvg} className={`cat-pose sit ${walking ? 'off' : ''}`} viewBox="0 0 140 150" onClick={pet}>
        <defs>
          <radialGradient id="cat-iris" cx="0.45" cy="0.35" r="0.75">
            <stop offset="0" stopColor="#fbf08a" />
            <stop offset="0.5" stopColor="#8ccf4d" />
            <stop offset="1" stopColor="#2e6b2c" />
          </radialGradient>
        </defs>
        <path ref={(el) => void (sitTail.current[0] = el)} className="cat-tail-line" d="M94 140 C118 147 138 138 134 118 S 126 94 117 98" />
        <path ref={(el) => void (sitTail.current[1] = el)} className="cat-tail-fur" d="M94 140 C118 147 138 138 134 118 S 126 94 117 98" />
        <g className="cat-sit-body">
          <ellipse cx="46" cy="134" rx="16" ry="13" className={furLine} />
          <ellipse cx="94" cy="134" rx="16" ry="13" className={furLine} />
          <path d="M70 62 C48 62 38 94 40 122 C42 142 54 148 70 148 C86 148 98 142 100 122 C102 94 92 62 70 62Z" className={furLine} />
          <path d="M70 80 C58 82 54 102 58 122 C62 136 78 136 82 122 C86 102 82 82 70 80Z" className="cat-cream" />
          <g className="cat-leg-down l">
            <path d="M56 108 L54 141 Q54 147 61 147 L66 147 Q71 147 70 141 L68 108Z" className={furLine} />
            <path d="M60 147 v-4 M65 147 v-4" className="cat-toes" />
          </g>
          <g className="cat-leg-down r">
            <path d="M84 108 L86 141 Q86 147 79 147 L74 147 Q69 147 70 141 L72 108Z" className={furLine} />
            <path d="M75 147 v-4 M80 147 v-4" className="cat-toes" />
          </g>
        </g>

        <g ref={sitHead}>
          <g className={`cat-ears ${pulse ? (pulse % 2 ? 'tw-a' : 'tw-b') : ''}`}>
            <g className="cat-ear l">
              <path d="M40 46 L36 8 L64 28Z" className={furLine} />
              <path d="M43 38 L41 17 L57 29Z" className="cat-ear-in" />
            </g>
            <g className="cat-ear r">
              <path d="M100 46 L104 8 L76 28Z" className={furLine} />
              <path d="M97 38 L99 17 L83 29Z" className="cat-ear-in" />
            </g>
          </g>
          <path d="M34 56 C32 34 48 24 70 24 C92 24 108 34 106 56 C105 72 92 82 70 82 C48 82 35 72 34 56Z" className={furLine} />
          <path d="M35 60 l-6 4 l7 1 M105 60 l6 4 l-7 1" className="cat-line-only" />
          <path d="M64 28 q1 6 0 9 M70 26 v10 M76 28 q-1 6 0 9" className="cat-stripe thin" />
          <ellipse cx="70" cy="70" rx="13" ry="9" className="cat-cream" />
          <ellipse cx="47" cy="66" rx="6" ry="3.5" className="cat-blush" />
          <ellipse cx="93" cy="66" rx="6" ry="3.5" className="cat-blush" />

          {/* open eyes; the pupils follow the pointer */}
          <g className="cat-eyes open">
            {EYES.map((cx) => (
              <ellipse key={cx} className={`cat-iris ${cx < 70 ? 'l' : 'r'} cat-line`} cx={cx} cy="52" rx="9.5" ry="11.5" fill="url(#cat-iris)" />
            ))}
            <g ref={pupils}>
              {EYES.map((cx) => (
                <ellipse key={cx} className={`cat-pupil ${cx < 70 ? 'l' : 'r'}`} cx={cx} cy="53" rx="4.4" ry="8.2" />
              ))}
            </g>
            {EYES.map((cx) => (
              <g key={cx} className={cx < 70 ? 'l' : 'r'}>
                <circle cx={cx - 3} cy="47" r="3.3" className="cat-shine" />
                <circle cx={cx + 3.6} cy="57" r="1.6" className="cat-shine" />
                <path d={`M${cx - 10.5} 47 Q${cx} 36 ${cx + 10.5} 46`} className="cat-lash" />
              </g>
            ))}
          </g>
          {/* ^ ^ */}
          <path className="cat-eyes happy" d="M45 54 Q54 43 63 54 M77 54 Q86 43 95 54" />
          {/* cross */}
          <g className="cat-eyes angry">
            {EYES.map((cx) => (
              <g key={cx}>
                <path d={`M${cx - 9} 52 Q${cx} 47 ${cx + 9} 54 Q${cx} 60 ${cx - 9} 52Z`} fill="url(#cat-iris)" className="cat-line" />
                <rect x={cx - 1.3} y="49.5" width="2.6" height="8" rx="1.3" className="cat-pupil" />
              </g>
            ))}
            <path d="M43 43 L63 49 M97 43 L77 49" className="cat-lash" />
          </g>

          <path d="M65.5 62 h9 l-4.5 4.8z" className="cat-nose" />
          <path className="cat-mouth calm" d="M70 66.8 v1.6 M63 68 Q66.5 72 70 68.4 Q73.5 72 77 68" />
          <g className="cat-mouth-hiss">
            <path d="M62 68 Q70 84 78 68 Q70 72 62 68Z" className="cat-hiss" />
            <path d="M64.5 69 l1.6 4.4 l1.6 -3.8 M75.5 69 l-1.6 4.4 l-1.6 -3.8" className="cat-fang" />
          </g>
          <path d="M44 64 L21 59 M44 68 L20 69 M96 64 L119 59 M96 68 L120 69" className="cat-whisk" />
        </g>

        {/* paws over the eyes */}
        {[
          { cls: 'l', d: 'M58 116 C48 96 44 74 54 62', cx: 54 },
          { cls: 'r', d: 'M82 116 C92 96 96 74 86 62', cx: 86 },
        ].map(({ cls, d, cx }) => (
          <g key={cls} className={`cat-arm ${cls}`}>
            <path d={d} className="cat-tail-line arm" />
            <path d={d} className="cat-tail-fur arm" />
            <ellipse cx={cx} cy="55" rx="11.5" ry="10.5" className={furLine} />
            <g className="cat-bean">
              <ellipse cx={cx} cy="58" rx="4.2" ry="3.2" />
              <circle cx={cx - 6} cy="51.5" r="2" />
              <circle cx={cx - 2} cy="48.5" r="2" />
              <circle cx={cx + 2} cy="48.5" r="2" />
              <circle cx={cx + 6} cy="51.5" r="2" />
            </g>
          </g>
        ))}

        {/* anger mark and hearts */}
        <path className="cat-vein" d="M104 14 q5 5 0 10 M114 14 q-5 5 0 10 M104 14 q5 -5 10 0 M104 24 q5 5 10 0" />
        <g className="cat-hearts">
          <path className="h1" d="M108 30 c-3 -5 -10 -2 -7 3 l7 6 l7 -6 c3 -5 -4 -8 -7 -3z" />
          <path className="h2" d="M30 24 c-2 -4 -8 -2 -6 2.5 l6 5 l6 -5 c2 -4.5 -4 -6.5 -6 -2.5z" />
        </g>
      </svg>
    </div>,
    document.body,
  );
}
