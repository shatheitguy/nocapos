import { useState } from 'react';
import type { WinState } from '../state/windows';
import { RemoteHome, type SessionStart } from './remote/RemoteHome';
import { RemoteSession } from './remote/RemoteSession';
import { blankForm, type ConnForm } from './remote/rdModel';

// Remote Desktop: a start card (computer, user name, Connect, and the option
// tabs), saved computers below it, and full-window sessions over RDP or VNC
// with a slim connection bar.

export function RemoteDesktop({ win }: { win: WinState }) {
  const [draft, setDraft] = useState<ConnForm>(() => blankForm());
  const [session, setSession] = useState<{ start: SessionStart; n: number } | null>(null);

  const exit = () => {
    setSession(null);
    // A saved computer goes back to the list; a one-off stays on the card.
    setDraft((d) => (d.id ? blankForm(d.protocol) : d));
  };

  if (session) return <RemoteSession key={session.n} start={session.start} win={win} onExit={exit} />;
  return (
    <div className="rd">
      <RemoteHome draft={draft} onDraft={setDraft} onConnect={(start) => setSession({ start, n: Date.now() })} />
    </div>
  );
}
