// Code snippets for a composed request (cURL is built server-side for exact escaping too,
// but the client copy keeps the Composer instant).
import type { ComposeReq } from './api';

const skip = (n: string) => ['host', 'content-length', 'connection'].includes(n.toLowerCase());
const sq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`;
const js = (s: string) => JSON.stringify(s);

export type Lang = 'curl' | 'fetch' | 'swift' | 'kotlin' | 'go';
export const langs: Lang[] = ['curl', 'fetch', 'swift', 'kotlin', 'go'];

export function generate(r: ComposeReq, lang: Lang): string {
  const hs = r.headers.filter((h) => h.name && !skip(h.name));
  const m = (r.method || 'GET').toUpperCase();
  const body = r.body ?? '';
  switch (lang) {
    case 'curl': {
      const lines = [`curl${m !== 'GET' && !(m === 'POST' && body) ? ` -X ${m}` : ''} ${sq(r.url)}`];
      let compressed = false;
      for (const h of hs) {
        if (h.name.toLowerCase() === 'accept-encoding') compressed = true;
        else lines.push(`-H ${sq(`${h.name}: ${h.value}`)}`);
      }
      if (compressed) lines.push('--compressed');
      if (body) lines.push(`--data-raw ${sq(body)}`);
      return lines.join(' \\\n  ');
    }
    case 'fetch': {
      const opts: string[] = [`  method: ${js(m)},`];
      if (hs.length) opts.push(`  headers: {\n${hs.map((h) => `    ${js(h.name)}: ${js(h.value)},`).join('\n')}\n  },`);
      if (body) opts.push(`  body: ${js(body)},`);
      return `const res = await fetch(${js(r.url)}, {\n${opts.join('\n')}\n});\nconst data = await res.text();`;
    }
    case 'swift':
      return [
        `var request = URLRequest(url: URL(string: ${js(r.url)})!)`,
        `request.httpMethod = ${js(m)}`,
        ...hs.map((h) => `request.setValue(${js(h.value)}, forHTTPHeaderField: ${js(h.name)})`),
        ...(body ? [`request.httpBody = ${js(body)}.data(using: .utf8)`] : []),
        `let (data, response) = try await URLSession.shared.data(for: request)`,
      ].join('\n');
    case 'kotlin':
      return [
        `val request = Request.Builder()`,
        `    .url(${js(r.url)})`,
        ...hs.map((h) => `    .header(${js(h.name)}, ${js(h.value)})`),
        body
          ? `    .method(${js(m)}, ${js(body)}.toRequestBody(${js(hs.find((h) => h.name.toLowerCase() === 'content-type')?.value ?? 'text/plain')}.toMediaType()))`
          : `    .method(${js(m)}, null)`,
        `    .build()`,
        `val response = OkHttpClient().newCall(request).execute()`,
      ].join('\n');
    case 'go':
      return [
        body ? `body := strings.NewReader(${js(body)})` : `var body io.Reader`,
        `req, err := http.NewRequest(${js(m)}, ${js(r.url)}, body)`,
        `if err != nil {\n\treturn err\n}`,
        ...hs.map((h) => `req.Header.Set(${js(h.name)}, ${js(h.value)})`),
        `resp, err := http.DefaultClient.Do(req)`,
      ].join('\n');
  }
}
