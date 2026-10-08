// Running a saved script and following its output (Scripts app + launcher widget).
import { useEffect, useRef, useState } from 'react';
import { scriptsApi, type Script, type ScriptRun } from '../api/scripts';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

/** Tracks one run at a time: start it, poll its output until it ends, stop it. */
export function useScriptRun(onFinished?: () => void) {
  const [run, setRun] = useState<ScriptRun | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const finished = useRef(onFinished);
  finished.current = onFinished;

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const follow = (id: string) => {
    const poll = async () => {
      const r = await scriptsApi.getRun(id);
      if (!r.ok) {
        setRun((cur) => (cur && cur.id === id ? { ...cur, running: false, error: r.error ?? 'Lost track of the run' } : cur));
        return;
      }
      setRun(r.data);
      if (r.data.running) timer.current = window.setTimeout(() => void poll(), 700);
      else finished.current?.();
    };
    void poll();
  };

  const start = async (s: Script) => {
    if (run?.running) {
      toast('error', 'A script is still running', `Stop “${run.name}” first.`);
      return;
    }
    if (s.confirm) {
      const ok = await confirmDialog({
        title: `Run “${s.name}”?`,
        message: s.description || 'This script runs on the host.',
        confirmLabel: 'Run',
      });
      if (!ok) return;
    }
    window.clearTimeout(timer.current);
    const r = await scriptsApi.run(s.id);
    if (!r.ok) {
      toast('error', `Could not run ${s.name}`, r.error);
      return;
    }
    setRun({ id: r.data.run_id, script_id: s.id, name: s.name, started_at: new Date().toISOString(), running: true, output: '', truncated: false });
    follow(r.data.run_id);
  };

  const stop = async () => {
    if (run?.running) await scriptsApi.cancel(run.id);
  };

  return { run, start, stop, clear: () => setRun(null) };
}

export function runStatus(run: ScriptRun): { label: string; tone: 'good' | 'bad' | 'warn' } {
  if (run.running) return { label: 'Running…', tone: 'warn' };
  if (run.error) return { label: run.error, tone: 'bad' };
  return run.exit_code === 0 ? { label: 'Done · exit 0', tone: 'good' } : { label: `Failed · exit ${run.exit_code}`, tone: 'bad' };
}
