// Tiny dependency-free highlighter for HTML/XML, JS, CSS and JSON text.
// Produces escaped HTML with <span class="h-*"> tokens. Big inputs are returned plain.
export type Lang = 'html' | 'xml' | 'js' | 'css' | 'json';

const LIMIT = 512 * 1024;
const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

const rules: Record<Lang, [RegExp, string][]> = {
  html: [
    [/<!--[\s\S]*?-->/y, 'com'],
    [/<\/?[A-Za-z][\w:-]*/y, 'tag'],
    [/\/?>/y, 'tag'],
    [/\s[\w:-]+(?==)/y, 'attr'],
    [/"[^"]*"|'[^']*'/y, 'str'],
  ],
  xml: [],
  js: [
    [/\/\/[^\n]*|\/\*[\s\S]*?\*\//y, 'com'],
    [/`(?:\\.|[^`\\])*`|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'/y, 'str'],
    [/\b(?:const|let|var|function|return|if|else|for|while|new|class|import|export|from|async|await|try|catch|throw|typeof|this|null|undefined|true|false)\b/y, 'kw'],
    [/\b\d[\d_.eExXa-fA-F]*\b/y, 'num'],
  ],
  css: [
    [/\/\*[\s\S]*?\*\//y, 'com'],
    [/"[^"]*"|'[^']*'/y, 'str'],
    [/[\w-]+(?=\s*:)/y, 'attr'],
    [/#[0-9a-fA-F]{3,8}\b|\b\d[\d.]*(?:px|em|rem|%|s|ms|vh|vw)?\b/y, 'num'],
    [/[.#]?[A-Za-z][\w-]*(?=[^{}]*\{)/y, 'tag'],
  ],
  json: [
    [/"(?:\\.|[^"\\])*"(?=\s*:)/y, 'key'],
    [/"(?:\\.|[^"\\])*"/y, 'str'],
    [/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/y, 'num'],
    [/\b(?:true|false)\b/y, 'bool'],
    [/\bnull\b/y, 'null'],
  ],
};
rules.xml = rules.html;

export function highlight(text: string, lang: Lang): string {
  if (text.length > LIMIT) return esc(text);
  const rs = rules[lang];
  let out = '';
  let plain = '';
  let i = 0;
  outer: while (i < text.length) {
    for (const [re, cls] of rs) {
      re.lastIndex = i;
      const m = re.exec(text);
      if (m && m[0].length) {
        out += esc(plain) + `<span class="h-${cls}">${esc(m[0])}</span>`;
        plain = '';
        i += m[0].length;
        continue outer;
      }
    }
    plain += text[i++];
  }
  return out + esc(plain);
}

/** Pretty-prints JSON text if it parses (cheap path for the Pretty view). */
export function prettyJSON(value: unknown): string {
  return JSON.stringify(value, null, 2);
}
