// Every UI string comes from locales/<lang>.json. Adding a language = drop a new
// JSON file with the same keys next to en.json; it is picked up automatically.
type Dict = { [k: string]: string | string[] | Dict };

const files = import.meta.glob<Dict>('../locales/*.json', { eager: true, import: 'default' });
const dicts: Record<string, Dict> = {};
for (const [path, d] of Object.entries(files)) {
  dicts[path.replace(/^.*\/(\w+)\.json$/, '$1')] = d;
}

const fallback = dicts.en;
let current: Dict = fallback;
let currentLang = 'en';

/** The active language code ("en"). */
export const lang = () => currentLang;

export const languages = Object.keys(dicts);

export function setLanguage(lang: string) {
  const code = dicts[lang] ? lang : dicts[lang.split('-')[0]] ? lang.split('-')[0] : 'en';
  current = dicts[code];
  currentLang = code;
}

setLanguage(globalThis.navigator?.language ?? 'en');

function node(d: Dict, key: string): Dict[string] | undefined {
  let v: Dict[string] | undefined = d;
  for (const part of key.split('.')) {
    if (typeof v !== 'object' || Array.isArray(v)) return undefined;
    v = v[part];
  }
  return v;
}

function lookup(d: Dict, key: string): string | undefined {
  const v = node(d, key);
  return typeof v === 'string' ? v : undefined;
}

/** Whether a key exists (in the current language or English). */
export function has(key: string): boolean {
  return node(current, key) !== undefined || node(fallback, key) !== undefined;
}

/** A list of strings (e.g. error causes). */
export function tlist(key: string): string[] {
  const v = node(current, key) ?? node(fallback, key);
  return Array.isArray(v) ? v : [];
}

/** t('banner.disconnected', { seconds: 3 }) — missing keys fall back to English, then to the key. */
export function t(key: string, params?: Record<string, string | number>): string {
  const s = lookup(current, key) ?? lookup(fallback, key) ?? key;
  return params ? s.replace(/\{(\w+)\}/g, (m, k) => (k in params ? String(params[k]) : m)) : s;
}
