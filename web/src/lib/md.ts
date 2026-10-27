// Minimal Markdown for the bundled docs: headings, paragraphs, fenced code,
// tables, lists, inline code, bold and links. Output is escaped HTML per block.
export type Block =
  | { type: 'h1' | 'h2' | 'p'; html: string; id?: string }
  | { type: 'code'; lang: string; code: string }
  | { type: 'ul'; items: string[] }
  | { type: 'table'; head: string[]; rows: string[][] };

export interface Doc {
  slug: string;
  title: string;
  section: string;
  blocks: Block[];
  text: string; // plain text for search
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

export function inline(s: string): string {
  const parts = s.split(/(`[^`]+`)/g);
  return parts
    .map((p) => {
      if (p.startsWith('`') && p.endsWith('`') && p.length > 1) return `<code>${esc(p.slice(1, -1).replace(/\\\|/g, '|'))}</code>`;
      return esc(p)
        .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
        .replace(/\[([^\]]+)\]\((#[^)\s]*|https?:\/\/[^)\s]+)\)/g, '<a href="$2">$1</a>');
    })
    .join('');
}

export const slugify = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

function cells(line: string): string[] {
  // Split on | that is not escaped (\|) and not inside backticks.
  const out: string[] = [];
  let cur = '';
  let tick = false;
  const body = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    if (c === '`') tick = !tick;
    if (c === '|' && !tick && body[i - 1] !== '\\') {
      out.push(cur.trim());
      cur = '';
    } else cur += c;
  }
  out.push(cur.trim());
  return out;
}

export function parse(slug: string, src: string, vars: Record<string, string>): Doc {
  src = src.replace(/\{(\w+)\}/g, (m, k) => vars[k] ?? m);
  let title = slug;
  let section = '';
  const fm = /^---\n([\s\S]*?)\n---\n/.exec(src);
  if (fm) {
    for (const line of fm[1].split('\n')) {
      const [k, ...v] = line.split(':');
      if (k.trim() === 'title') title = v.join(':').trim();
      if (k.trim() === 'section') section = v.join(':').trim();
    }
    src = src.slice(fm[0].length);
  }
  const lines = src.split('\n');
  const blocks: Block[] = [];
  let para: string[] = [];
  const flush = () => {
    if (para.length) blocks.push({ type: 'p', html: inline(para.join(' ')) });
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.startsWith('```')) {
      flush();
      const lang = line.slice(3).trim();
      const code: string[] = [];
      while (++i < lines.length && !lines[i].startsWith('```')) code.push(lines[i]);
      blocks.push({ type: 'code', lang, code: code.join('\n') });
    } else if (/^#{1,2} /.test(line)) {
      flush();
      const level = line.startsWith('## ') ? 'h2' : 'h1';
      const text = line.replace(/^#+ /, '');
      blocks.push({ type: level, html: inline(text), id: slugify(text) });
    } else if (line.startsWith('|')) {
      flush();
      const head = cells(line);
      const rows: string[][] = [];
      i++; // separator
      while (i + 1 < lines.length && lines[i + 1].startsWith('|')) rows.push(cells(lines[++i]).map(inline));
      blocks.push({ type: 'table', head: head.map(inline), rows });
    } else if (/^- /.test(line)) {
      flush();
      const items = [inline(line.slice(2))];
      while (i + 1 < lines.length && /^- /.test(lines[i + 1])) items.push(inline(lines[++i].slice(2)));
      blocks.push({ type: 'ul', items });
    } else if (line.trim() === '') {
      flush();
    } else {
      para.push(line.trim());
    }
  }
  flush();
  return { slug, title, section, blocks, text: src.toLowerCase() };
}
