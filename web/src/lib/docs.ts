// Bundled docs: web/src/docs/<lang>/NN-slug.md. Translate by copying the folder.
import { parse, type Doc } from './md';

const files = import.meta.glob<string>('../docs/*/*.md', { eager: true, query: '?raw', import: 'default' });

export function loadDocs(lang: string, vars: Record<string, string>): Doc[] {
  const pick = (l: string) =>
    Object.entries(files)
      .filter(([p]) => p.includes(`/docs/${l}/`))
      .sort(([a], [b]) => a.localeCompare(b));
  let entries = pick(lang.split('-')[0]);
  if (!entries.length) entries = pick('en');
  return entries.map(([p, src]) => parse(p.replace(/^.*\/\d+-(.+)\.md$/, '$1'), src, vars));
}
