// Build-time check for NoCapOS's two voices (src/i18n/genz, src/i18n/standard):
//  - both folders hold the same files, and each file the same keys
//  - no empty strings, and the same {placeholders} in both voices
//  - every literal t('key') / tr('key') used in src exists
// Exits non-zero (failing `npm run build`) on any problem.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'src');
const i18n = join(root, 'i18n');
const VOICES = ['genz', 'standard'];
const problems = [];

const load = (voice, file) => {
  try {
    return JSON.parse(readFileSync(join(i18n, voice, file), 'utf8'));
  } catch (e) {
    problems.push(`${voice}/${file}: ${e.message}`);
    return {};
  }
};
const holes = (s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(',');

const files = new Set(VOICES.flatMap((v) => readdirSync(join(i18n, v)).filter((f) => f.endsWith('.json'))));
const all = {};
for (const f of files) {
  const [a, b] = VOICES.map((v) => load(v, f));
  for (const [k, v] of Object.entries(a)) {
    if (!(k in b)) problems.push(`${f}: "${k}" is in genz but missing from standard`);
  }
  for (const [k, v] of Object.entries(b)) {
    if (!(k in a)) problems.push(`${f}: "${k}" is in standard but missing from genz`);
    else if (holes(a[k]) !== holes(v)) problems.push(`${f}: "${k}" has different {placeholders} in genz and standard`);
    if (k in all) problems.push(`${f}: "${k}" is also defined in ${all[k]}`);
    all[k] = f;
  }
  for (const [voice, d] of [['genz', a], ['standard', b]]) {
    for (const [k, v] of Object.entries(d)) if (typeof v !== 'string' || !v.trim()) problems.push(`${voice}/${f}: "${k}" is empty`);
  }
}

// Every literal key used in the code must exist.
const walk = (dir) =>
  readdirSync(dir).flatMap((n) => {
    const p = join(dir, n);
    return statSync(p).isDirectory() ? walk(p) : /\.(tsx?|ts)$/.test(n) ? [p] : [];
  });
let used = 0;
for (const file of walk(root).filter((f) => !f.startsWith(i18n))) {
  const src = readFileSync(file, 'utf8');
  for (const m of src.matchAll(/\b(?:t|tr)\(\s*'([a-z][\w.-]*)'/g)) {
    used++;
    if (!(m[1] in all)) problems.push(`${file.slice(root.length + 1)}: unknown key "${m[1]}"`);
  }
}

if (problems.length) {
  console.error(`i18n check failed (${problems.length}):\n  ` + problems.join('\n  '));
  process.exit(1);
}
console.log(`i18n ok: ${Object.keys(all).length} keys in both voices, ${used} uses checked`);
