import { type ReactNode } from 'react';

// Lightweight, safe renderer: fenced code blocks, inline code, bold, and
// paragraphs. No HTML is ever interpreted — everything is React text nodes.
export function renderMarkdown(text: string): ReactNode {
  const blocks: ReactNode[] = [];
  const parts = text.split(/```/);
  parts.forEach((part, i) => {
    if (i % 2 === 1) {
      // code fence: first line may be a language label
      const nl = part.indexOf('\n');
      const body = nl >= 0 ? part.slice(nl + 1) : part;
      blocks.push(
        <pre key={`c${i}`} className="md-code">
          <code>{body.replace(/\n$/, '')}</code>
        </pre>,
      );
    } else if (part) {
      part.split(/\n{2,}/).forEach((para, j) => {
        if (para.trim()) blocks.push(<p key={`p${i}-${j}`}>{inline(para)}</p>);
      });
    }
  });
  return blocks;
}

// Inline: `code` and **bold**, newlines preserved as <br>.
function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  const tokens = text.split(/(`[^`]+`|\*\*[^*]+\*\*)/g);
  tokens.forEach((t, i) => {
    if (t.startsWith('`') && t.endsWith('`')) {
      out.push(<code key={i} className="md-inline">{t.slice(1, -1)}</code>);
    } else if (t.startsWith('**') && t.endsWith('**')) {
      out.push(<strong key={i}>{t.slice(2, -2)}</strong>);
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
