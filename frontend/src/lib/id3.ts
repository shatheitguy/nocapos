// Just enough ID3v2 (the tags at the start of most MP3s) for the music
// player: title, artist, album and the cover picture. Pure, tested on its own.

export interface Tags {
  title?: string;
  artist?: string;
  album?: string;
  /** The embedded cover. */
  picture?: { mime: string; data: Uint8Array };
}

const syncsafe = (b: Uint8Array, o: number) => ((b[o] & 0x7f) << 21) | ((b[o + 1] & 0x7f) << 14) | ((b[o + 2] & 0x7f) << 7) | (b[o + 3] & 0x7f);

/** How many bytes the tag at the start of the file takes (0 when there is none), to know how much to read. */
export function id3Size(head: Uint8Array): number {
  if (head.length < 10 || head[0] !== 0x49 || head[1] !== 0x44 || head[2] !== 0x33) return 0;
  return 10 + syncsafe(head, 6);
}

function decode(enc: number, b: Uint8Array): string {
  let label = 'latin1';
  if (enc === 1) label = b[0] === 0xfe && b[1] === 0xff ? 'utf-16be' : 'utf-16le';
  else if (enc === 2) label = 'utf-16be';
  else if (enc === 3) label = 'utf-8';
  try {
    return new TextDecoder(label).decode(b).replace(/\0+$/, '').split('\0')[0].trim();
  } catch {
    return '';
  }
}

/** Where a string in the given encoding ends (its terminator), from `from`. */
function strEnd(b: Uint8Array, from: number, enc: number): number {
  if (enc === 1 || enc === 2) {
    for (let i = from; i + 1 < b.length; i += 2) if (b[i] === 0 && b[i + 1] === 0) return i;
    return b.length;
  }
  const i = b.indexOf(0, from);
  return i < 0 ? b.length : i;
}

export function readId3(b: Uint8Array): Tags {
  const tags: Tags = {};
  const total = id3Size(b);
  if (!total) return tags;
  const ver = b[3];
  const end = Math.min(total, b.length);
  let o = 10;
  if (b[5] & 0x40 && ver >= 3) o += ver === 4 ? syncsafe(b, 10) : ((b[10] << 24) | (b[11] << 16) | (b[12] << 8) | b[13]) + 4;
  const idLen = ver === 2 ? 3 : 4;
  const headLen = ver === 2 ? 6 : 10;
  while (o + headLen <= end) {
    const id = String.fromCharCode(...b.subarray(o, o + idLen));
    if (!/^[A-Z0-9]+$/.test(id)) break; // padding
    const size =
      ver === 2 ? (b[o + 3] << 16) | (b[o + 4] << 8) | b[o + 5] : ver === 4 ? syncsafe(b, o + 4) : ((b[o + 4] << 24) | (b[o + 5] << 16) | (b[o + 6] << 8) | b[o + 7]) >>> 0;
    const start = o + headLen;
    if (size <= 0 || start + size > end) break;
    const f = b.subarray(start, start + size);
    o = start + size;
    const enc = f[0];
    if (id === 'TIT2' || id === 'TT2') tags.title = decode(enc, f.subarray(1));
    else if (id === 'TPE1' || id === 'TP1') tags.artist = decode(enc, f.subarray(1));
    else if (id === 'TALB' || id === 'TAL') tags.album = decode(enc, f.subarray(1));
    else if ((id === 'APIC' || id === 'PIC') && !tags.picture) {
      let p = 1;
      let mime: string;
      if (id === 'PIC') {
        const fmt = String.fromCharCode(...f.subarray(1, 4)).toLowerCase();
        mime = fmt === 'png' ? 'image/png' : 'image/jpeg';
        p = 4;
      } else {
        const me = strEnd(f, 1, 0);
        mime = new TextDecoder('latin1').decode(f.subarray(1, me)) || 'image/jpeg';
        if (!mime.includes('/')) mime = `image/${mime.toLowerCase() === 'png' ? 'png' : 'jpeg'}`;
        p = me + 1;
      }
      p += 1; // picture type
      const de = strEnd(f, p, enc);
      p = de + (enc === 1 || enc === 2 ? 2 : 1);
      if (p < f.length) tags.picture = { mime, data: f.slice(p) };
    }
  }
  return tags;
}
