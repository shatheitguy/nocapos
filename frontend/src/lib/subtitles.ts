// Subtitles for the video player: SubRip (.srt) turned into WebVTT, and the
// subtitle files that belong to a video. Pure, so it is tested on its own.

/** SubRip text as WebVTT (WebVTT is passed through, minus a byte-order mark). */
export function srtToVtt(text: string): string {
  const s = text.replace(/^﻿/, '').replace(/\r\n?/g, '\n');
  if (/^WEBVTT/.test(s)) return s;
  const body = s
    .trim()
    .split(/\n{2,}/)
    .map((block) => {
      const lines = block.split('\n');
      // Drop the cue number; keep the timing line with "," → "." in the times.
      if (/^\d+$/.test(lines[0]?.trim() ?? '')) lines.shift();
      if (!lines.length || !lines[0].includes('-->')) return '';
      lines[0] = lines[0].replace(/(\d{1,2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2');
      return lines.join('\n');
    })
    .filter(Boolean)
    .join('\n\n');
  return `WEBVTT\n\n${body}\n`;
}

export interface SubtitleFile {
  name: string;
  /** A label for the menu: the language part of the name ("en", "English"), else the file name. */
  label: string;
  /** Two-letter language code when the name has one ("movie.en.srt"). */
  lang?: string;
}

const stem = (n: string) => n.replace(/\.[^.]+$/, '');

/**
 * The subtitle files in a folder that belong to a video: the same name with
 * .srt or .vtt, optionally with a language in between ("movie.en.srt").
 * Exact matches come first.
 */
export function subtitlesFor(video: string, names: string[]): SubtitleFile[] {
  const base = stem(video).toLowerCase();
  const out: (SubtitleFile & { exact: boolean })[] = [];
  for (const name of names) {
    const m = /^(.*)\.(srt|vtt)$/i.exec(name);
    if (!m) continue;
    const s = m[1].toLowerCase();
    if (s === base) out.push({ name, label: name, exact: true });
    else if (s.startsWith(`${base}.`)) {
      const extra = m[1].slice(base.length + 1);
      out.push({ name, label: extra, lang: /^[a-z]{2,3}$/i.test(extra) ? extra.toLowerCase() : undefined, exact: false });
    }
  }
  out.sort((a, b) => Number(b.exact) - Number(a.exact) || a.name.localeCompare(b.name));
  return out.map(({ exact: _exact, ...f }) => f);
}
