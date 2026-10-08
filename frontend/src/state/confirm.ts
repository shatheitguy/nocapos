import { create } from 'zustand';

export interface ConfirmRequest {
  title: string;
  message: string;
  confirmLabel?: string;
  danger?: boolean;
  cancelLabel?: string;
  /** Auto-cancel after this many seconds (shown as a countdown). */
  timeoutSec?: number;
}

export interface Choice {
  id: string;
  label: string;
  danger?: boolean;
}

interface ConfirmStore {
  current: (ConfirmRequest & { resolve: (ok: boolean) => void; choices?: Choice[]; pick?: (id: string) => void }) | null;
}

export const useConfirm = create<ConfirmStore>(() => ({ current: null }));

/** Ask before doing something risky. Resolves true only if the user confirms. */
export function confirmDialog(req: ConfirmRequest): Promise<boolean> {
  return new Promise((resolve) => {
    useConfirm.getState().current?.resolve(false);
    useConfirm.setState({
      current: {
        ...req,
        resolve: (ok) => {
          useConfirm.setState({ current: null });
          resolve(ok);
        },
      },
    });
  });
}

/** Ask the user to pick one of several answers. Resolves the choice id, or null if dismissed. */
export function choiceDialog(req: ConfirmRequest & { choices: Choice[] }): Promise<string | null> {
  return new Promise((resolve) => {
    useConfirm.getState().current?.resolve(false);
    const done = (v: string | null) => {
      useConfirm.setState({ current: null });
      resolve(v);
    };
    useConfirm.setState({ current: { ...req, resolve: () => done(null), pick: (id) => done(id) } });
  });
}
