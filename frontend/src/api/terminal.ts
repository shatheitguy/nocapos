import { api } from './client';

export interface TerminalInfo {
  host_available: boolean;
  host_user: string;
  host_root: boolean; // host shells run as root
}

export const terminalApi = {
  info: () => api<TerminalInfo>('/api/v1/terminal/info'),
  ticket: (target: string, cols: number, rows: number) =>
    api<{ ticket: string }>('/api/v1/terminal/ticket', { method: 'POST', body: { target, cols, rows } }),
};

export interface TerminalSocket {
  send: (data: string) => void;
  resize: (cols: number, rows: number) => void;
  close: () => void;
}

/**
 * Opens a terminal WebSocket: output arrives as binary frames (onData),
 * input is sent as binary, resize as a JSON text frame.
 */
export function openTerminal(
  ticket: string,
  handlers: { onData: (text: string) => void; onClose: (reason: string) => void; onOpen: () => void },
): TerminalSocket {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws/terminal?ticket=${encodeURIComponent(ticket)}`);
  ws.binaryType = 'arraybuffer';
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();

  ws.onopen = () => handlers.onOpen();
  ws.onmessage = (ev) => {
    if (typeof ev.data === 'string') handlers.onData(ev.data);
    else handlers.onData(decoder.decode(new Uint8Array(ev.data)));
  };
  ws.onclose = (ev) => handlers.onClose(ev.reason || '');
  ws.onerror = () => {
    /* onclose follows */
  };

  return {
    send: (data) => ws.readyState === WebSocket.OPEN && ws.send(encoder.encode(data)),
    resize: (cols, rows) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ resize: [cols, rows] })),
    close: () => ws.close(),
  };
}
