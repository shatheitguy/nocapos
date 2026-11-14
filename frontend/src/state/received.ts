import { create } from 'zustand';
import { receivedApi, type ReceivedEvent, type ReceivedInfo } from '../api/received';
import { openApp } from '../apps/meta';

// The Received folder: its place, the unread count shown in Files, and the
// banner for files that just arrived from a device.

interface ReceivedStore {
  info: ReceivedInfo | null;
  /** Files that just arrived (shown as a banner). */
  banner: ReceivedEvent | null;
  load: () => Promise<void>;
  /** Opened: nothing is new any more. */
  markSeen: () => void;
}

export const useReceived = create<ReceivedStore>((set, get) => ({
  info: null,
  banner: null,
  load: async () => {
    const r = await receivedApi.info();
    if (r.ok && r.data && typeof r.data.folder === 'string') set({ info: { ...r.data, devices: r.data.devices ?? {} } });
  },
  markSeen: () => {
    const info = get().info;
    if (info && info.unread) set({ info: { ...info, unread: 0 } });
    void receivedApi.seen();
  },
}));

/** Files arrived from a device: count them and show the banner. */
export function receivedArrived(e: ReceivedEvent) {
  const s = useReceived.getState();
  const info = s.info ? { ...s.info, unread: s.info.unread + e.paths.length } : null;
  useReceived.setState({ banner: e, info });
  void s.load();
}

export const closeReceivedBanner = () => useReceived.setState({ banner: null });

/** Opens a folder of the Received area in Files. */
export function showInFiles(root: string, path: string) {
  openApp('files', { props: { root, path }, newWindow: true });
}
