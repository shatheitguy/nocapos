// Phone mirroring, the wire side: one WebSocket to /ws/mirror, decoding the
// phone's H.264 with WebCodecs (or its JPEG pictures, where the browser has
// no video decoder) onto a canvas, and sending taps and keys back.
//
// Binary messages: [type][8-byte pts, µs, big-endian][payload]
//   1 codec config (SPS/PPS, Annex-B), 2 key frame, 3 frame, 5 JPEG
// Text messages: JSON with a "t" field (hello, phone, state, meta, pong).

export const PKT_CONFIG = 1;
export const PKT_KEY = 2;
export const PKT_DELTA = 3;
export const PKT_JPEG = 5;

/** Whether this browser can play the phone's H.264 (else it asks for pictures). */
export async function h264Supported(): Promise<boolean> {
  if (typeof VideoDecoder === 'undefined' || typeof EncodedVideoChunk === 'undefined') return false;
  try {
    const r = await VideoDecoder.isConfigSupported({ codec: 'avc1.42E01F', optimizeForLatency: true });
    return !!r.supported;
  } catch {
    return false;
  }
}

const hex2 = (n: number) => n.toString(16).toUpperCase().padStart(2, '0');

/** "avc1.PPCCLL" from the first SPS (NAL type 7) in an Annex-B config packet. */
export function codecFromConfig(b: Uint8Array): string | null {
  for (let i = 0; i + 3 < b.length; i++) {
    // Start code 00 00 01 (a 4-byte 00 00 00 01 ends the same way).
    if (b[i] === 0 && b[i + 1] === 0 && b[i + 2] === 1) {
      const n = i + 3;
      if (n + 3 < b.length && (b[n] & 0x1f) === 7) return `avc1.${hex2(b[n + 1])}${hex2(b[n + 2])}${hex2(b[n + 3])}`;
      i = n - 1;
    }
  }
  return null;
}

/** Maps a point in the shown (possibly rotated) view back onto the phone's picture. */
export function unrotate(u: number, v: number, rot: number): [number, number] {
  const c = (n: number) => Math.min(1, Math.max(0, n));
  switch (((rot % 4) + 4) % 4) {
    case 1:
      return [c(v), c(1 - u)];
    case 2:
      return [c(1 - u), c(1 - v)];
    case 3:
      return [c(1 - v), c(u)];
    default:
      return [c(u), c(v)];
  }
}

export type PhoneMessage = { t: string } & Record<string, unknown>;

export interface StreamHandlers {
  onOpen: () => void;
  onMessage: (m: PhoneMessage) => void;
  /** The picture changed size (or the first one arrived). */
  onPicture: (w: number, h: number) => void;
  onClose: (code: number, reason: string) => void;
}

/** One connection: decodes onto canvas, sends input. */
export class PhoneStream {
  private ws: WebSocket;
  private decoder: VideoDecoder | null = null;
  private codec = '';
  private config: Uint8Array | null = null;
  private needKey = true;
  private closed = false;
  private jpegBusy = false;
  private jpegNext: Uint8Array | null = null;
  private w = 0;
  private h = 0;
  private lastKeyAsk = 0;

  constructor(
    url: string,
    private canvas: HTMLCanvasElement,
    private handlers: StreamHandlers,
  ) {
    this.ws = new WebSocket(url);
    this.ws.binaryType = 'arraybuffer';
    this.ws.onopen = () => handlers.onOpen();
    this.ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try {
          const m = JSON.parse(ev.data) as PhoneMessage;
          if (m && typeof m.t === 'string') handlers.onMessage(m);
        } catch {
          /* not for us */
        }
      } else this.packet(ev.data as ArrayBuffer);
    };
    this.ws.onerror = () => {
      /* onclose follows */
    };
    this.ws.onclose = (ev) => {
      this.teardown();
      if (!this.closed) handlers.onClose(ev.code, ev.reason || '');
    };
  }

  send(m: Record<string, unknown>) {
    if (this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  close() {
    this.closed = true;
    this.teardown();
    try {
      this.ws.close(1000, 'bye');
    } catch {
      /* already closed */
    }
  }

  private teardown() {
    if (this.decoder && this.decoder.state !== 'closed') {
      try {
        this.decoder.close();
      } catch {
        /* closed already */
      }
    }
    this.decoder = null;
    this.codec = '';
  }

  /** Asks the phone for a key frame (at most every 500 ms from here). */
  private askKey() {
    const now = performance.now();
    if (now - this.lastKeyAsk < 500) return;
    this.lastKeyAsk = now;
    this.needKey = true;
    this.send({ t: 'keyframe' });
  }

  private packet(buf: ArrayBuffer) {
    if (buf.byteLength < 9) return;
    const all = new Uint8Array(buf);
    const type = all[0];
    const pts = Number(new DataView(buf).getBigUint64(1));
    const payload = all.subarray(9);
    switch (type) {
      case PKT_JPEG:
        return this.jpeg(payload);
      case PKT_CONFIG:
        return this.configure(payload);
      case PKT_KEY:
      case PKT_DELTA:
        return this.video(type === PKT_KEY, pts, payload);
    }
  }

  private configure(payload: Uint8Array) {
    this.config = payload.slice();
    this.needKey = true;
    const codec = codecFromConfig(this.config);
    if (!codec || typeof VideoDecoder === 'undefined') return;
    if (codec === this.codec && this.decoder && this.decoder.state === 'configured') return;
    this.teardown();
    try {
      const d = new VideoDecoder({
        output: (frame) => this.draw(frame),
        error: () => {
          // The decoder is closed now: start over at the next config + key frame.
          if (this.decoder === d) {
            this.decoder = null;
            this.codec = '';
          }
          this.askKey();
        },
      });
      d.configure({ codec, optimizeForLatency: true, hardwareAcceleration: 'no-preference' });
      this.decoder = d;
      this.codec = codec;
    } catch {
      this.decoder = null;
      this.codec = '';
    }
  }

  private video(key: boolean, pts: number, payload: Uint8Array) {
    const d = this.decoder;
    if (!d || d.state !== 'configured') {
      if (key) this.askKey();
      return;
    }
    if (!key && this.needKey) return;
    // Falling behind (a slow machine): skip ahead to the next key frame.
    if (d.decodeQueueSize > 20) {
      this.askKey();
      return;
    }
    let data = payload;
    if (key && this.config) {
      data = new Uint8Array(this.config.length + payload.length);
      data.set(this.config, 0);
      data.set(payload, this.config.length);
    }
    try {
      d.decode(new EncodedVideoChunk({ type: key ? 'key' : 'delta', timestamp: pts, data }));
      if (key) this.needKey = false;
    } catch {
      this.askKey();
    }
  }

  private size(w: number, h: number) {
    if (w === this.w && h === this.h) return;
    this.w = w;
    this.h = h;
    this.canvas.width = w;
    this.canvas.height = h;
    this.handlers.onPicture(w, h);
  }

  private draw(frame: VideoFrame) {
    try {
      if (this.closed) return;
      this.size(frame.displayWidth, frame.displayHeight);
      this.canvas.getContext('2d')?.drawImage(frame, 0, 0, this.w, this.h);
    } finally {
      frame.close();
    }
  }

  /** Pictures are drawn one at a time; one waiting behind it is enough (the newest wins). */
  private jpeg(payload: Uint8Array) {
    if (this.jpegBusy) {
      this.jpegNext = payload.slice();
      return;
    }
    this.jpegBusy = true;
    const blob = new Blob([payload.slice()], { type: 'image/jpeg' });
    void createImageBitmap(blob)
      .then((bmp) => {
        if (!this.closed) {
          this.size(bmp.width, bmp.height);
          this.canvas.getContext('2d')?.drawImage(bmp, 0, 0);
        }
        bmp.close();
      })
      .catch(() => {
        /* a broken picture: the next one comes soon */
      })
      .finally(() => {
        this.jpegBusy = false;
        const next = this.jpegNext;
        this.jpegNext = null;
        if (next && !this.closed) this.jpeg(next);
      });
  }
}
