import { useEffect, useState, type ReactNode } from 'react';
import type { ApiResult } from '../api/client';
import type { DnsPoint, FwState } from '../api/shield';
import { Icon, type IconName } from '../components/Icon';
import { tr } from '../i18n';
import { toast } from '../state/toasts';

// Pieces the Shield app's tabs share.

/** A titled glass card. */
export function Card({ title, icon, hint, actions, children, className = '' }: { title?: string; icon?: IconName; hint?: string; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`sh-card ${className}`}>
      {(title || actions) && (
        <header className="sh-card-head">
          {icon && <span className="sh-card-ico"><Icon name={icon} size={16} /></span>}
          <span className="sh-card-titles">
            {title && <h3>{title}</h3>}
            {hint && <p className="muted">{hint}</p>}
          </span>
          {actions && <span className="sh-card-actions">{actions}</span>}
        </header>
      )}
      {children}
    </section>
  );
}

/** A big number with a label. */
export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: 'good' | 'bad' | 'accent' }) {
  return (
    <div className={`sh-stat ${tone ?? ''}`}>
      <span className="sh-stat-value">{value}</span>
      <span className="sh-stat-label">{label}</span>
    </div>
  );
}

/** Bars of total and blocked queries. */
export function QueryBars({ points, label }: { points: DnsPoint[]; label: string }) {
  const max = Math.max(1, ...points.map((p) => p.total));
  return (
    <div className="sh-bars" role="img" aria-label={label}>
      {points.map((p) => (
        <span key={p.time} className="sh-bar" title={`${new Date(p.time).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} · ${p.total} / ${p.blocked}`}>
          <span className="sh-bar-total" style={{ height: `${(p.total / max) * 100}%` }}>
            <span className="sh-bar-blocked" style={{ height: p.total ? `${(p.blocked / p.total) * 100}%` : 0 }} />
          </span>
        </span>
      ))}
    </div>
  );
}

/** Seconds until iso (0 when past), ticking. */
export function useSecondsLeft(iso?: string): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!iso) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [iso]);
  if (!iso) return 0;
  return Math.max(0, Math.ceil((new Date(iso).getTime() - now) / 1000));
}

/** Runs a firewall call: the new state on success, a toast on failure. */
export async function fwCall(p: Promise<ApiResult<FwState>>, set: (s: FwState) => void, done?: string): Promise<boolean> {
  const r = await p;
  if (!r.ok) {
    toast('error', tr('sh.fw.failed'), r.error);
    return false;
  }
  set(r.data);
  if (done) toast('success', done);
  return true;
}

export const fmtNum = (n: number) => n.toLocaleString();

export function pct(part: number, whole: number): string {
  if (!whole) return '0%';
  const v = (part / whole) * 100;
  return `${v < 10 ? v.toFixed(1) : Math.round(v)}%`;
}

export function fmtClock(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

/** A little select bound to string values. */
export function Select<T extends string | number>({ value, options, onChange, label, disabled }: { value: T; options: { id: T; label: string }[]; onChange: (v: T) => void; label: string; disabled?: boolean }) {
  return (
    <select
      className="sh-select"
      aria-label={label}
      value={String(value)}
      disabled={disabled}
      onChange={(e) => {
        const o = options.find((x) => String(x.id) === e.target.value);
        if (o) onChange(o.id);
      }}
    >
      {options.map((o) => (
        <option key={String(o.id)} value={String(o.id)}>{o.label}</option>
      ))}
    </select>
  );
}
