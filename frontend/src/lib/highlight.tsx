// A small, dependency-free syntax highlighter for file previews. It tokenizes
// with per-language regexes and renders React spans (never innerHTML).
import type { ReactNode } from 'react';

type Tok = 'com' | 'str' | 'num' | 'kw' | 'lit' | 'fn' | 'type' | 'tag' | 'attr' | 'key' | 'var' | 'op' | 'meta';

interface Lang {
  name: string;
  rules: [Tok, RegExp][];
}

const words = (s: string) => new RegExp(`\\b(?:${s.split(' ').join('|')})\\b`, 'y');

const C_STR: [Tok, RegExp][] = [
  ['str', /"(?:\\.|[^"\\\n])*"?/y],
  ['str', /'(?:\\.|[^'\\\n])*'?/y],
];
const NUM: [Tok, RegExp] = ['num', /\b(?:0[xX][\da-fA-F_]+|0[bB][01_]+|\d[\d_]*(?:\.\d[\d_]*)?(?:[eE][+-]?\d+)?n?)\b/y];
const FN: [Tok, RegExp] = ['fn', /\b[A-Za-z_$][\w$]*(?=\s*\()/y];
const TYPE: [Tok, RegExp] = ['type', /\b[A-Z][A-Za-z0-9_]*\b/y];
const OP: [Tok, RegExp] = ['op', /[-+*/%=<>!&|^~?:]+/y];

const JS: Lang = {
  name: 'JavaScript',
  rules: [
    ['com', /\/\/[^\n]*/y],
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ...C_STR,
    ['str', /`(?:\\.|[^`\\])*`?/y],
    ['kw', words('import export from as default const let var function return if else for while do switch case break continue new delete typeof instanceof in of class extends super this async await yield try catch finally throw interface type enum implements public private protected readonly static declare namespace keyof satisfies')],
    ['lit', words('true false null undefined NaN Infinity void never any unknown string number boolean')],
    NUM,
    FN,
    TYPE,
    OP,
  ],
};

const GO: Lang = {
  name: 'Go',
  rules: [
    ['com', /\/\/[^\n]*/y],
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ...C_STR,
    ['str', /`[^`]*`?/y],
    ['kw', words('package import func return if else for range switch case default break continue go defer select chan map struct interface type const var fallthrough goto')],
    ['lit', words('true false nil iota')],
    ['type', words('string int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 uintptr byte rune float32 float64 bool error any')],
    NUM,
    FN,
    TYPE,
    OP,
  ],
};

const PY: Lang = {
  name: 'Python',
  rules: [
    ['com', /#[^\n]*/y],
    ['str', /(?:[rbfuRBFU]{0,2})("""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$))/y],
    ...C_STR,
    ['meta', /@[\w.]+/y],
    ['kw', words('def class return if elif else for while in not and or is import from as with try except finally raise pass break continue lambda yield global nonlocal async await del assert match case')],
    ['lit', words('True False None self cls')],
    NUM,
    FN,
    TYPE,
    OP,
  ],
};

const SH: Lang = {
  name: 'Shell',
  rules: [
    ['com', /#[^\n]*/y],
    ['str', /"(?:\\.|[^"\\])*"?/y],
    ['str', /'[^']*'?/y],
    ['var', /\$(?:\{[^}\n]*\}?|[\w@#?$!*-]+|\()/y],
    ['kw', words('if then else elif fi for in do done while until case esac function return local export readonly declare set unset shift exit source alias sudo')],
    ['fn', /^[ \t]*[\w./-]+/my],
    ['attr', /(?<=\s)--?[\w-]+/y],
    NUM,
    ['op', /[|&;<>]+/y],
  ],
};

const PS: Lang = {
  name: 'PowerShell',
  rules: [
    ['com', /<#[\s\S]*?(?:#>|$)/y],
    ['com', /#[^\n]*/y],
    ...C_STR,
    ['var', /\$[\w:]+/y],
    ['kw', /\b(?:function|param|if|elseif|else|foreach|for|while|do|switch|return|try|catch|finally|throw|in|begin|process|end|exit)\b/iy],
    ['fn', /\b[A-Z][a-z]+-[A-Z]\w+\b/y],
    ['attr', /(?<=\s)-[A-Za-z]\w*/y],
    NUM,
    OP,
  ],
};

const JSON_: Lang = {
  name: 'JSON',
  rules: [
    ['key', /"(?:\\.|[^"\\\n])*"(?=\s*:)/y],
    ['str', /"(?:\\.|[^"\\\n])*"?/y],
    ['lit', words('true false null')],
    NUM,
  ],
};

const YAML: Lang = {
  name: 'YAML',
  rules: [
    ['com', /#[^\n]*/y],
    ['key', /^[ \t-]*[\w.\-/"' ]+?(?=\s*:(?:\s|$))/my],
    ...C_STR,
    ['lit', /\b(?:true|false|yes|no|on|off|null)\b/iy],
    ['meta', /^---$|[&*][\w-]+|![\w!]+/my],
    NUM,
  ],
};

const INI: Lang = {
  name: 'Config',
  rules: [
    ['com', /^[ \t]*[#;][^\n]*/my],
    ['tag', /^[ \t]*\[[^\]\n]*\]/my],
    ['key', /^[ \t]*(?:export\s+)?[\w.\-]+(?=\s*[=:])/my],
    ...C_STR,
    ['lit', /\b(?:true|false|yes|no|on|off)\b/iy],
    NUM,
  ],
};

const CSS: Lang = {
  name: 'CSS',
  rules: [
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ...C_STR,
    ['meta', /@[\w-]+/y],
    ['attr', /[\w-]+(?=\s*:[^:{};]*[;}])/y],
    ['var', /--[\w-]+/y],
    ['num', /#[\da-fA-F]{3,8}\b|-?\d*\.?\d+(?:px|em|rem|%|vh|vw|s|ms|deg|fr)?\b/y],
    ['tag', /[.#]?[A-Za-z][\w-]*(?=[^{};]*\{)/y],
  ],
};

const HTML: Lang = {
  name: 'HTML',
  rules: [
    ['com', /<!--[\s\S]*?(?:-->|$)/y],
    ['tag', /<\/?[A-Za-z][\w:-]*|\/?>/y],
    ['attr', /\b[\w:-]+(?==)/y],
    ...C_STR,
    ['meta', /&\w+;/y],
  ],
};

const SQL: Lang = {
  name: 'SQL',
  rules: [
    ['com', /--[^\n]*/y],
    ['com', /\/\*[\s\S]*?(?:\*\/|$)/y],
    ...C_STR,
    ['kw', /\b(?:select|from|where|and|or|not|insert|into|values|update|set|delete|create|table|index|drop|alter|add|primary|key|foreign|references|join|left|right|inner|outer|on|group|by|order|having|limit|offset|as|distinct|union|all|null|default|integer|text|real|blob|varchar|if|exists|begin|commit|rollback)\b/iy],
    NUM,
    OP,
  ],
};

const MD: Lang = {
  name: 'Markdown',
  rules: [
    ['tag', /^#{1,6} [^\n]*/my],
    ['str', /```[\s\S]*?(?:```|$)|`[^`\n]*`/y],
    ['kw', /\*\*[^*\n]+\*\*|__[^_\n]+__/y],
    ['attr', /\[[^\]\n]*\]\([^)\n]*\)/y],
    ['meta', /^[ \t]*(?:[-*+]|\d+\.) /my],
    ['com', /^>[^\n]*/my],
  ],
};

const DOCKER: Lang = {
  name: 'Dockerfile',
  rules: [
    ['com', /#[^\n]*/y],
    ['kw', /^[ \t]*(?:FROM|RUN|CMD|LABEL|EXPOSE|ENV|ADD|COPY|ENTRYPOINT|VOLUME|USER|WORKDIR|ARG|ONBUILD|STOPSIGNAL|HEALTHCHECK|SHELL|AS)\b/imy],
    ...C_STR,
    ['var', /\$\{?[\w]+\}?/y],
    NUM,
  ],
};

const BY_EXT: Record<string, Lang> = {
  js: JS, mjs: JS, cjs: JS, jsx: JS, ts: JS, tsx: JS, mts: JS,
  go: GO, py: PY,
  sh: SH, bash: SH, zsh: SH, env: INI,
  ps1: PS, psm1: PS, bat: PS,
  json: JSON_, yaml: YAML, yml: YAML, toml: INI, ini: INI, conf: INI, cfg: INI, service: INI,
  css: CSS, scss: CSS, html: HTML, htm: HTML, xml: HTML, svg: HTML, vue: HTML,
  sql: SQL, md: MD, markdown: MD,
  rs: JS, java: JS, c: JS, h: JS, cpp: JS, cs: JS, kt: JS, swift: JS, php: JS,
};

/** The language for a file name, or null for plain text. */
export function langFor(name: string): Lang | null {
  const lower = name.toLowerCase();
  if (lower === 'dockerfile' || lower.endsWith('.dockerfile')) return DOCKER;
  if (lower === 'makefile' || lower.startsWith('.bashrc') || lower === '.profile') return SH;
  if (lower.startsWith('.env')) return INI;
  const ext = lower.includes('.') ? lower.slice(lower.lastIndexOf('.') + 1) : '';
  return BY_EXT[ext] ?? null;
}

/** Splits text into highlighted runs: [token class | null, text][]. */
export function tokenize(text: string, lang: Lang): [Tok | null, string][] {
  const out: [Tok | null, string][] = [];
  let plain = '';
  let i = 0;
  outer: while (i < text.length) {
    for (const [tok, re] of lang.rules) {
      re.lastIndex = i;
      const m = re.exec(text);
      if (m && m[0].length > 0) {
        if (plain) out.push([null, plain]), (plain = '');
        out.push([tok, m[0]]);
        i += m[0].length;
        continue outer;
      }
    }
    // Skip a whole identifier at once so keywords only match at word starts.
    const w = /[A-Za-z0-9_$]+|\s+|./y;
    w.lastIndex = i;
    const m = w.exec(text)!;
    plain += m[0];
    i += m[0].length;
  }
  if (plain) out.push([null, plain]);
  return out;
}

/** Highlighted code with line numbers. */
export function CodeView({ text, name }: { text: string; name: string }): ReactNode {
  const lang = langFor(name);
  const runs = lang ? tokenize(text, lang) : [[null, text] as [null, string]];
  const lines = text.split('\n').length;
  return (
    <div className="code-view">
      <pre className="code-gutter" aria-hidden="true">
        {Array.from({ length: lines }, (_, i) => i + 1).join('\n')}
      </pre>
      <pre className="code-body">
        <code>
          {runs.map(([t, s], i) => (t ? <span key={i} className={`hl-${t}`}>{s}</span> : s))}
        </code>
      </pre>
    </div>
  );
}

export const langName = (name: string) => langFor(name)?.name ?? 'Plain text';
