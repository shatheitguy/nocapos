import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { agentsApi, type Approval, type Decision } from '../api/agents';
import { subscribe } from '../api/socket';
import { Icon } from '../components/Icon';
import { playCue } from '../lib/uiSound';
import { toast } from '../state/toasts';

// The approval card: an AI agent wants to change something and waits (up to
// 2 minutes) for your answer. No answer = no.

export function AgentApprovals() {
  const [pending, setPending] = useState<Approval[]>([]);
  const [now, setNow] = useState(Date.now());
  const [busy, setBusy] = useState(false);

  useEffect(
    () =>
      subscribe('agents.approvals', (data) => {
        const list = Array.isArray(data) ? (data as Approval[]) : [];
        setPending((old) => {
          if (list.some((a) => !old.find((o) => o.id === a.id))) playCue('info');
          return list;
        });
      }),
    [],
  );

  const a = pending[0];
  useEffect(() => {
    if (!a) return;
    const t = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(t);
  }, [a]);

  if (!a) return null;

  const decide = async (d: Decision) => {
    setBusy(true);
    const r = await agentsApi.decide(a.id, d);
    setBusy(false);
    if (!r.ok && r.status !== 404) return toast('error', 'Couldn’t send your answer', r.error);
    setPending((p) => p.filter((x) => x.id !== a.id));
    if (r.ok && d !== 'deny') toast('success', d === 'always' ? `${a.agent} can ${a.tool_title.toLowerCase()} without asking now` : 'Approved');
  };

  const total = Math.max(1, new Date(a.expires_at).getTime() - new Date(a.created_at).getTime());
  const leftMs = Math.max(0, new Date(a.expires_at).getTime() - now);
  const left = Math.ceil(leftMs / 1000);
  const args = Object.keys(a.arguments ?? {}).length ? JSON.stringify(a.arguments, null, 2) : '';

  return createPortal(
    <div className="confirm-backdrop agent-approve-backdrop">
      <div className="confirm-dialog glass agent-approve" role="alertdialog" aria-modal="true" aria-labelledby="agent-approve-title">
        <div className="confirm-icon danger">
          <Icon name="sparkles" size={22} />
        </div>
        <h3 id="agent-approve-title">
          {a.agent} wants to {a.tool_title.toLowerCase()}
        </h3>
        <p>{a.summary}</p>
        {args && (
          <details className="agent-args">
            <summary>
              Details <code>{a.tool}</code>
            </summary>
            <pre>{args}</pre>
          </details>
        )}
        <div className="confirm-countdown">
          <span style={{ width: `${(leftMs / total) * 100}%` }} />
          <b>
            {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}
          </b>
        </div>
        <p className="muted small">No answer before the timer runs out counts as a no.{pending.length > 1 ? ` ${pending.length - 1} more waiting.` : ''}</p>
        <div className="confirm-actions agent-approve-actions">
          <button type="button" className="ghost" disabled={busy} onClick={() => void decide('deny')}>
            Deny
          </button>
          <button type="button" className="ghost" disabled={busy} onClick={() => void decide('always')} title="Don’t ask again for this tool and agent">
            Always allow
          </button>
          <button type="button" disabled={busy} onClick={() => void decide('once')}>
            Approve once
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
