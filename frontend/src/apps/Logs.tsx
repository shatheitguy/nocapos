import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { LogLine } from '../api/types';
import { Icon } from '../components/Icon';
import { useTopic } from '../lib/hooks';
import type { WinState } from '../state/windows';

const MAX_LINES = 5000;

export function Logs({ win }: { win: WinState }) {
  const id = win.props?.id ?? '';
  const [lines, setLines] = useState<LogLine[]>([]);
  const [ended, setEnded] = useState<string | null>(null);
  const [follow, setFollow] = useState(true);
  const [timestamps, setTimestamps] = useState(false);
  const [filter, setFilter] = useState('');
  const box = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setLines([]);
    setEnded(null);
  }, [id]);

  useTopic<LogLine[]>(
    id ? `container.logs/${id}` : null,
    (batch) => setLines((cur) => [...cur, ...batch].slice(-MAX_LINES)),
    (err) => setEnded(err ?? 'Log stream ended (container stopped).'),
  );

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q ? lines.filter((l) => l.text.toLowerCase().includes(q)) : lines;
  }, [lines, filter]);

  useLayoutEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [shown, follow]);

  return (
    <div className="logs">
      <div className="toolbar">
        <label className="search compact">
          <Icon name="search" size={15} />
          <input placeholder="Filter lines" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
        <label className="toggle">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
        </label>
        <label className="toggle">
          <input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} /> Timestamps
        </label>
        <span className="spacer" />
        <span className="muted small">{shown.length} lines</span>
        <button type="button" className="ghost icon-btn" title="Clear" onClick={() => setLines([])}>
          <Icon name="trash" size={15} />
        </button>
      </div>
      <div
        ref={box}
        className="log-view mono"
        onWheel={(e) => e.deltaY < 0 && follow && setFollow(false)}
      >
        {shown.map((l, i) => (
          <div key={i} className={`log-line ${l.stream}`}>
            {timestamps && l.time && <span className="log-ts">{new Date(l.time).toLocaleTimeString()} </span>}
            {l.text}
          </div>
        ))}
        {ended && <div className="log-end">— {ended} —</div>}
        {!shown.length && !ended && <div className="muted">Waiting for output…</div>}
      </div>
    </div>
  );
}
