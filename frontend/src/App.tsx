import { useEffect, useRef, useState } from 'react';
import { loadAvatar } from './lib/avatar';
import { Logo } from './components/Logo';
import { getUser, logout, onSessionChange, refresh, setupRequired } from './api/client';
import type { User } from './api/types';
import { AuthScreen, type AuthMode } from './shell/AuthScreen';
import { Desktop } from './shell/Desktop';
import { installUiFeedback, playCue } from './lib/uiSound';
import { applyPrefs, usePrefs } from './state/prefs';

type Phase = { kind: 'boot' } | { kind: 'auth'; mode: AuthMode; lockedUser?: string } | { kind: 'desktop'; user: User };

export function App() {
  const [phase, setPhase] = useState<Phase>({ kind: 'boot' });
  // Re-apply the look whenever a pref changes, and on resize (phones force a
  // bottom dock, so the reserved edges depend on the viewport).
  useEffect(() => {
    applyPrefs(usePrefs.getState());
    const off = usePrefs.subscribe((s) => applyPrefs(s));
    const onResize = () => applyPrefs(usePrefs.getState());
    window.addEventListener('resize', onResize);
    return () => {
      off();
      window.removeEventListener('resize', onResize);
    };
  }, []);

  useEffect(() => installUiFeedback(), []);

  // Lock / unlock sounds, only on a real transition (not on page load).
  const prevKind = useRef(phase.kind);
  useEffect(() => {
    const prev = prevKind.current;
    prevKind.current = phase.kind;
    if (prev === 'auth' && phase.kind === 'desktop') playCue('unlock');
    else if (prev === 'desktop' && phase.kind === 'auth') playCue('lock');
  }, [phase.kind]);

  useEffect(() => {
    // Session changes drive the phase: sign-in → desktop, sign-out/expiry → auth.
    const off = onSessionChange((user) => {
      setPhase((p) => {
        if (user) return { kind: 'desktop', user };
        if (p.kind === 'auth') return p;
        return { kind: 'auth', mode: 'login' };
      });
    });
    void (async () => {
      if (await setupRequired()) {
        setPhase({ kind: 'auth', mode: 'setup' });
        return;
      }
      if (!(await refresh())) setPhase({ kind: 'auth', mode: 'login' });
      else {
        const u = getUser();
        if (u) setPhase({ kind: 'desktop', user: u });
      }
    })();
    return off;
  }, []);

  // Profile photo for the dock / Control Center, and remember this user for
  // the login screen.
  const deskUser = phase.kind === 'desktop' ? phase.user.username : null;
  useEffect(() => {
    if (deskUser) void loadAvatar(deskUser);
  }, [deskUser]);

  // Lock = end the server session but keep the username and window layout,
  // so unlocking requires the password and restores the desktop as it was.
  const lock = async () => {
    const name = phase.kind === 'desktop' ? phase.user.username : undefined;
    setPhase({ kind: 'auth', mode: 'locked', lockedUser: name });
    await logout();
  };

  if (phase.kind === 'boot') return <div className="boot"><Logo size={110} /></div>;
  if (phase.kind === 'auth') return <AuthScreen key={phase.mode} mode={phase.mode} lockedUser={phase.lockedUser} />;
  return <Desktop user={phase.user} onLock={() => void lock()} />;
}
