import { useEffect, useRef, useState } from 'react';

export type CatMood = 'idle' | 'watch' | 'shy' | 'peek' | 'error' | 'happy';

/**
 * A neon "cyber cat" that sits on the password field of the login screen:
 * it follows the pointer, watches you type your name, covers its eyes while
 * you type a password, peeks when the password is shown, and reacts to a
 * wrong password or a successful sign-in.
 *
 * `look` (-1…1) steers the eyes while typing; `pulse` changes on every key so
 * the ears twitch.
 */
export function CyberCat({ mood, look = 0, pulse = 0 }: { mood: CatMood; look?: number; pulse?: number }) {
  const ref = useRef<SVGSVGElement>(null);
  const [gaze, setGaze] = useState({ x: 0, y: 0 });

  // Idle: the eyes follow the pointer.
  useEffect(() => {
    if (mood !== 'idle') return;
    const onMove = (e: PointerEvent) => {
      const r = ref.current?.getBoundingClientRect();
      if (!r) return;
      const dx = (e.clientX - (r.left + r.width / 2)) / (window.innerWidth / 2);
      const dy = (e.clientY - (r.top + r.height * 0.55)) / (window.innerHeight / 2);
      setGaze({ x: Math.max(-1, Math.min(1, dx * 1.6)), y: Math.max(-1, Math.min(1, dy * 1.6)) });
    };
    window.addEventListener('pointermove', onMove);
    return () => window.removeEventListener('pointermove', onMove);
  }, [mood]);

  const g = mood === 'watch' ? { x: look, y: 0.9 } : mood === 'idle' ? gaze : { x: 0, y: 0 };
  const px = g.x * 4.5;
  const py = g.y * 3;

  return (
    <svg ref={ref} className={`cyber-cat mood-${mood}`} viewBox="0 0 160 112" aria-hidden="true">
      <defs>
        <linearGradient id="cat-fur" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#262a33" />
          <stop offset="1" stopColor="#121419" />
        </linearGradient>
        <radialGradient id="cat-eye" cx="0.5" cy="0.45" r="0.6">
          <stop offset="0" stopColor="var(--cat-eye-hi, #ffd2d2)" />
          <stop offset="0.45" stopColor="var(--cat-eye, #ff2a2a)" />
          <stop offset="1" stopColor="#5a0006" />
        </radialGradient>
      </defs>

      {/* tail, curling out from behind on the right */}
      <path className="cat-tail" d="M128 96 C150 92 156 70 146 58 C140 50 134 56 140 62" fill="none" />

      {/* ears (re-mounted on every key so the twitch restarts) */}
      <g key={pulse} className={`cat-ears ${pulse ? 'twitch' : ''}`}>
        <path className="cat-ear l" d="M38 48 L44 10 L68 32 Z" />
        <path className="cat-ear r" d="M122 48 L116 10 L92 32 Z" />
        <path className="cat-ear-in" d="M45 40 L48 20 L60 32 M115 40 L112 20 L100 32" />
        <circle className="cat-led" cx="116" cy="16" r="1.8" />
      </g>

      {/* head */}
      <g className="cat-head">
        <ellipse className="cat-face" cx="80" cy="64" rx="46" ry="38" />
        {/* circuit markings */}
        <path className="cat-circuit" d="M80 30 V40 L74 46 M80 40 L86 46 M48 70 H40 L36 74 M112 70 H120 L124 74" />
        <circle className="cat-node" cx="74" cy="46" r="1.6" />
        <circle className="cat-node" cx="86" cy="46" r="1.6" />

        {/* eyes */}
        <g className="cat-eyes">
          <g className="cat-eye-open">
            <path className="cat-eye-shape" d="M50 62 Q62 50 74 62 Q62 72 50 62 Z" />
            <path className="cat-eye-shape" d="M86 62 Q98 50 110 62 Q98 72 86 62 Z" />
            <g className="cat-pupils" style={{ transform: `translate(${px}px, ${py}px)` }}>
              <rect className="cat-pupil" x="60.6" y="56" width="2.8" height="12" rx="1.4" />
              <rect className="cat-pupil" x="96.6" y="56" width="2.8" height="12" rx="1.4" />
              <circle className="cat-glint" cx="64.5" cy="58.5" r="1.1" />
              <circle className="cat-glint" cx="100.5" cy="58.5" r="1.1" />
            </g>
          </g>
          <path className="cat-eye-happy" d="M52 64 Q62 54 72 64 M88 64 Q98 54 108 64" />
          <path className="cat-eye-x" d="M56 57 L68 67 M68 57 L56 67 M92 57 L104 67 M104 57 L92 67" />
        </g>

        {/* nose, mouth, whiskers */}
        <path className="cat-nose" d="M76.5 75 H83.5 L80 79 Z" />
        <path className="cat-mouth" d="M80 79 Q76 84 72 81 M80 79 Q84 84 88 81" />
        <path className="cat-whisk" d="M44 76 L22 72 M44 81 L22 84 M116 76 L138 72 M116 81 L138 84" />
      </g>

      {/* paws: resting on the field, or covering the eyes */}
      <g className="cat-paw l">
        <ellipse cx="54" cy="104" rx="15" ry="9" />
        <path d="M48 99 V104 M54 98 V104 M60 99 V104" />
      </g>
      <g className="cat-paw r">
        <ellipse cx="106" cy="104" rx="15" ry="9" />
        <path d="M100 99 V104 M106 98 V104 M112 99 V104" />
      </g>
    </svg>
  );
}
