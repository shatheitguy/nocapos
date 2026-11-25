// Which files a file request takes: the `accept` list an <input type=file>
// gave (MIME types like "image/*" or "text/plain", and name patterns like
// "*.pub" or ".pub"), applied to file names. NoCap Desktop's file picker
// shows the files that fit and dims the rest.

const MIME: Record<string, string> = {};
const add = (mime: string, exts: string) => exts.split(' ').forEach((e) => (MIME[e] = mime));
add('image/png', 'png');
add('image/jpeg', 'jpg jpeg jpe jfif');
add('image/gif', 'gif');
add('image/webp', 'webp');
add('image/avif', 'avif');
add('image/bmp', 'bmp');
add('image/x-icon', 'ico');
add('image/svg+xml', 'svg');
add('image/heic', 'heic');
add('image/heif', 'heif');
add('image/tiff', 'tif tiff');
add('video/mp4', 'mp4 m4v');
add('video/webm', 'webm');
add('video/ogg', 'ogv');
add('video/quicktime', 'mov');
add('video/x-matroska', 'mkv');
add('video/x-msvideo', 'avi');
add('audio/mpeg', 'mp3');
add('audio/mp4', 'm4a');
add('audio/aac', 'aac');
add('audio/wav', 'wav');
add('audio/flac', 'flac');
add('audio/ogg', 'ogg oga opus');
add('application/pdf', 'pdf');
add('application/zip', 'zip');
add('application/json', 'json');
add('application/xml', 'xml');
add('application/gzip', 'gz tgz');
add('application/x-tar', 'tar');
add('text/plain', 'txt log pub conf cfg ini env');
add('text/markdown', 'md');
add('text/csv', 'csv');
add('text/html', 'html htm');
add('text/css', 'css');
add('text/javascript', 'js mjs');

/** The MIME type a file name suggests ("" when unknown). */
export function mimeOf(name: string): string {
  const lower = name.toLowerCase();
  const dot = lower.lastIndexOf('.');
  if (dot <= 0) return '';
  return MIME[lower.slice(dot + 1)] ?? '';
}

/** A shell-style name pattern ("*.pub", "photo-??.png") as a test. */
function globTest(pattern: string): (name: string) => boolean {
  const re = new RegExp(
    '^' +
      pattern
        .split('')
        .map((c) => (c === '*' ? '.*' : c === '?' ? '.' : c.replace(/[.+^${}()|[\]\\/-]/g, '\\$&')))
        .join('') +
      '$',
    'i',
  );
  return (name) => re.test(name);
}

const ANY = new Set(['*', '*/*', 'application/octet-stream']);

/** Whether an accept list takes every file (empty, or "any file"). */
export function acceptsEverything(accept: readonly string[]): boolean {
  const list = accept.map((a) => a.trim().toLowerCase()).filter(Boolean);
  return !list.length || list.some((a) => ANY.has(a));
}

/** A test for file names from an accept list; an empty list takes everything. */
export function acceptTest(accept: readonly string[]): (name: string) => boolean {
  if (acceptsEverything(accept)) return () => true;
  const tests: ((name: string) => boolean)[] = [];
  for (const raw of accept) {
    const a = raw.trim().toLowerCase();
    if (!a) continue;
    if (a.startsWith('.')) tests.push(globTest('*' + a));
    else if (/^[a-z0-9.+-]+\/\*$/.test(a)) {
      const major = a.slice(0, a.indexOf('/') + 1);
      tests.push((n) => mimeOf(n).startsWith(major));
    } else if (a.includes('*') || a.includes('?') || !a.includes('/')) tests.push(globTest(a));
    else tests.push((n) => mimeOf(n) === a);
  }
  if (!tests.length) return () => true;
  return (name) => tests.some((t) => t(name));
}

/** What the accept list asks for, for the picker's label: a kind, or the file name endings. */
export function acceptKind(accept: readonly string[]): 'any' | 'images' | 'media' | 'videos' | 'audio' | 'other' {
  const list = accept.map((a) => a.trim().toLowerCase()).filter(Boolean);
  if (!list.length || list.some((a) => a === '*' || a === '*/*')) return 'any';
  const majors = new Set(
    list.map((a) => {
      if (a.startsWith('image/')) return 'image';
      if (a.startsWith('video/')) return 'video';
      if (a.startsWith('audio/')) return 'audio';
      const m = mimeOf('x' + a.replace(/^\*/, ''));
      if (m.startsWith('image/')) return 'image';
      if (m.startsWith('video/')) return 'video';
      if (m.startsWith('audio/')) return 'audio';
      return 'other';
    }),
  );
  if (majors.size === 1 && majors.has('image')) return 'images';
  if (majors.size === 1 && majors.has('video')) return 'videos';
  if (majors.size === 1 && majors.has('audio')) return 'audio';
  if (!majors.has('other')) return 'media';
  return 'other';
}

/** The file name endings an accept list names (".pub", ".db"), for the picker's label. */
export function acceptEndings(accept: readonly string[]): string[] {
  const out = new Set<string>();
  for (const raw of accept) {
    const a = raw.trim().toLowerCase();
    const m = /^\*?(\.[a-z0-9]+)$/.exec(a);
    if (m) out.add(m[1]);
  }
  return [...out];
}
