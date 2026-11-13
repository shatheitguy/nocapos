import { useState, type ReactNode } from 'react';
import { Icon } from '../../components/Icon';
import { useT } from '../../i18n';

// Lightweight, safe renderer for chat replies: fenced code blocks (with a copy
// button), headings, bullet and numbered lists, quotes, simple tables, inline
// code, bold and links. No HTML is ever interpreted — everything is React
// nodes, and only http(s) links become anchors.
export function renderMarkdown(text: string): ReactNode {
  const blocks: ReactNode[] = [];
  const parts = text.split(/```/);
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      // code fence: the first line may be a language label
      const nl = part.indexOf('\n');
      const lang = nl >= 0 ? part.slice(0, nl).trim() : '';
      const body = nl >= 0 ? part.slice(nl + 1) : part;
      blocks.push(<CodeBlock key={`c${i}`} lang={/^[\w+#.-]{1,24}$/.test(lang) ? lang : ''} code={body.replace(/\n$/, '')} />);
    } else if (part) {
      blocks.push(...prose(part, `p${i}`));
    }
  });
  return blocks;
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Plain-http servers have no async clipboard; fall back to a hidden field.
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      return ok;
    } catch {
      return false;
    }
  }
}

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  return (
    <div className="md-code">
      <div className="md-code-bar">
        <span>{lang || t('ai.code')}</span>
        <button
          type="button"
          className="md-code-copy"
          onClick={() =>
            void copyText(code).then((ok) => {
              if (!ok) return;
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1500);
            })
          }
        >
          <Icon name={copied ? 'check' : 'copy'} size={13} /> {copied ? t('ai.copied') : t('ai.copy')}
        </button>
      </div>
      <pre>
        <code>{code}</code>
      </pre>
    </div>
  );
}

const isTableRow = (l: string) => /^\s*\|.*\|\s*$/.test(l);
const isTableRule = (l: string) => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(l);
const cells = (l: string) =>
  l
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((c) => c.trim());

// Block-level prose: walks the lines once, grouping lists, quotes and tables.
function prose(text: string, key: string): ReactNode[] {
  const out: ReactNode[] = [];
  const lines = text.split('\n');
  let para: string[] = [];
  let n = 0;
  const k = () => `${key}-${n++}`;
  const flush = () => {
    const body = para.join('\n').trim();
    if (body) out.push(<p key={k()}>{inline(body)}</p>);
    para = [];
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line.trim()) {
      flush();
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      flush();
      const level = Math.min(h[1].length + 2, 6);
      const Tag = `h${level}` as 'h3' | 'h4' | 'h5' | 'h6';
      out.push(<Tag key={k()}>{inline(h[2])}</Tag>);
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      flush();
      out.push(<hr key={k()} />);
      continue;
    }
    if (isTableRow(line) && i + 1 < lines.length && isTableRule(lines[i + 1])) {
      flush();
      const head = cells(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && isTableRow(lines[i])) rows.push(cells(lines[i++]));
      i--;
      out.push(
        <div key={k()} className="md-table">
          <table>
            <thead>
              <tr>
                {head.map((c, j) => (
                  <th key={j}>{inline(c)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, ri) => (
                <tr key={ri}>
                  {head.map((_, j) => (
                    <td key={j}>{inline(r[j] ?? '')}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }
    const bullet = /^\s*[-*+]\s+(.*)$/;
    const numbered = /^\s*(\d+)[.)]\s+(.*)$/;
    if (bullet.test(line) || numbered.test(line)) {
      flush();
      const ordered = numbered.test(line);
      const re = ordered ? numbered : bullet;
      const items: string[] = [];
      const start = ordered ? Number(numbered.exec(line)![1]) : 1;
      while (i < lines.length) {
        const m = re.exec(lines[i]);
        if (m) items.push(ordered ? m[2] : m[1]);
        else if (lines[i].trim() && /^\s{2,}/.test(lines[i]) && items.length) items[items.length - 1] += '\n' + lines[i].trim();
        else break;
        i++;
      }
      i--;
      const lis = items.map((it, j) => <li key={j}>{inline(it)}</li>);
      out.push(
        ordered ? (
          <ol key={k()} start={start}>
            {lis}
          </ol>
        ) : (
          <ul key={k()}>{lis}</ul>
        ),
      );
      continue;
    }
    if (/^\s*>/.test(line)) {
      flush();
      const quote: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) quote.push(lines[i++].replace(/^\s*>\s?/, ''));
      i--;
      out.push(<blockquote key={k()}>{inline(quote.join('\n'))}</blockquote>);
      continue;
    }
    para.push(line);
  }
  flush();
  return out;
}

// Inline: `code`, **bold**, [links](https://…) and bare https:// URLs;
// newlines are kept as <br>.
function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  const tokens = text.split(/(`[^`]+`|\*\*[^*]+\*\*|\[[^\]\n]+\]\(https?:\/\/[^\s)]+\)|https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g);
  tokens.forEach((t, i) => {
    if (!t) return;
    if (t.length > 1 && t.startsWith('`') && t.endsWith('`')) {
      out.push(
        <code key={i} className="md-inline">
          {t.slice(1, -1)}
        </code>,
      );
    } else if (t.length > 4 && t.startsWith('**') && t.endsWith('**')) {
      out.push(<strong key={i}>{t.slice(2, -2)}</strong>);
    } else if (t.startsWith('[')) {
      const m = /^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/.exec(t);
      out.push(
        m ? (
          <a key={i} href={m[2]} target="_blank" rel="noopener noreferrer">
            {m[1]}
          </a>
        ) : (
          t
        ),
      );
    } else if (/^https?:\/\//.test(t)) {
      out.push(
        <a key={i} href={t} target="_blank" rel="noopener noreferrer">
          {t}
        </a>,
      );
    } else {
      const lines = t.split('\n');
      lines.forEach((line, k) => {
        out.push(line);
        if (k < lines.length - 1) out.push(<br key={`${i}-br-${k}`} />);
      });
    }
  });
  return out;
}
