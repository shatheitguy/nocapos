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

interface ConfirmStore {
  current: (ConfirmRequest & { resolve: (ok: boolean) => void }) | null;
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
