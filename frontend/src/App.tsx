import { useEffect, useRef, useState } from 'react';
import { loadAvatar } from './lib/avatar';
import { Logo } from './components/Logo';
import { confirmNetwork, getUser, linkSignIn, logout, onSessionChange, refresh, setupRequired } from './api/client';
import { useT } from './i18n';
import type { User } from './api/types';
import { AuthScreen, type AuthMode } from './shell/AuthScreen';
import { Desktop } from './shell/Desktop';
import { UpdateOverlay } from './shell/UpdateOverlay';
import { resumeRememberedUpdate, resumeRunningUpdate } from './state/update';
import { installUiFeedback, playCue } from './lib/uiSound';
import { applyPrefs, resetPrefs, restorePrefs, usePrefs } from './state/prefs';
import { useRecents } from './state/recents';

type Phase = { kind: 'boot' } | { kind: 'auth'; mode: AuthMode; lockedUser?: string; lockedUserId?: string } | { kind: 'desktop'; user: User };

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

  // An update was in flight when this tab reloaded: pick it back up.
  useEffect(() => resumeRememberedUpdate(), []);

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
      // A NoCap Link ticket from the phone app signs in without the password.
      if (await linkSignIn()) {
        const u = getUser();
        if (u) {
          setPhase({ kind: 'desktop', user: u });
          return;
        }
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

  // Each person's own look and recent apps. Locking keeps them on screen;
  // signing out goes back to this device's look.
  const deskId = phase.kind === 'desktop' ? phase.user.id : null;
  const deskAdmin = phase.kind === 'desktop' && phase.user.role === 'admin';
  const signedOut = phase.kind === 'auth' && phase.mode !== 'locked';
  useEffect(() => {
    if (deskId) {
      restorePrefs(deskId, deskAdmin);
      useRecents.getState().restore();
    } else if (signedOut) {
      resetPrefs();
      useRecents.getState().restore();
    }
  }, [deskId, deskAdmin, signedOut]);

  // Admins: if NoCapOS is updating right now, show it.
  useEffect(() => {
    if (deskAdmin) void resumeRunningUpdate();
  }, [deskAdmin]);

  // Lock = end the server session but keep the username and window layout,
  // so unlocking requires the password and restores the desktop as it was.
  const lock = async () => {
    const u = phase.kind === 'desktop' ? phase.user : undefined;
    setPhase({ kind: 'auth', mode: 'locked', lockedUser: u?.username, lockedUserId: u?.id });
    await logout();
  };

  let screen;
  if (phase.kind === 'boot') screen = <div className="boot"><Logo size={110} /></div>;
  else if (phase.kind === 'auth') screen = <AuthScreen key={phase.mode} mode={phase.mode} lockedUser={phase.lockedUser} lockedUserId={phase.lockedUserId} />;
  else screen = <Desktop user={phase.user} onLock={() => void lock()} />;
  return (
    <>
      {screen}
      <UpdateOverlay />
      <NetConfirm />
    </>
  );
}

/**
 * The setup wizard changed the server's address and sent this browser to the
 * new one with #net-confirm=<token>: reaching it proves the new settings work,
 * so say so to the server (otherwise it puts the old ones back).
 */
function NetConfirm() {
  const t = useT();
  const [state, setState] = useState<'checking' | 'kept' | 'gone' | null>(() => (/^#net-confirm=[0-9a-f]{32}$/.test(window.location.hash) ? 'checking' : null));
  useEffect(() => {
    if (state !== 'checking') return;
    const token = window.location.hash.slice('#net-confirm='.length);
    window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search);
    let alive = true;
    void (async () => {
      for (let i = 0; i < 30 && alive; i++) {
        const r = await confirmNetwork(token);
        if (r === 'kept' || r === 'gone') return alive && setState(r);
        await new Promise((res) => window.setTimeout(res, 1500));
      }
      if (alive) setState('gone');
    })();
    return () => {
      alive = false;
    };
  }, [state]);
  useEffect(() => {
    if (state !== 'kept' && state !== 'gone') return;
    const id = window.setTimeout(() => setState(null), 9000);
    return () => window.clearTimeout(id);
  }, [state]);
  if (!state) return null;
  return (
    <div className={`net-confirm ${state}`} role="status">
      {state === 'checking' ? <span className="spinner sm" /> : null}
      {t(state === 'checking' ? 'wiz.netConfirming' : state === 'kept' ? 'wiz.netKept' : 'wiz.netReverted')}
    </div>
  );
}
