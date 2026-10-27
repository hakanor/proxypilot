// Pure helpers for the detail panel: cURL export, query/cookie parsing, body kinds.
import type { Flow, FlowDetail, Header } from './api';

export function header(hs: Header[] | undefined, name: string): string {
  const n = name.toLowerCase();
  return hs?.find((h) => h.name.toLowerCase() === n)?.value ?? '';
}

export function parseQuery(q: string): [string, string][] {
  if (!q) return [];
  return q.split('&').filter(Boolean).map((p) => {
    const [k, v = ''] = p.split(/=(.*)/s);
    const dec = (s: string) => {
      try {
        return decodeURIComponent(s.replace(/\+/g, ' '));
      } catch {
        return s;
      }
    };
    return [dec(k), dec(v)];
  });
}

/** Request "Cookie" headers and response "Set-Cookie" headers as name/value pairs. */
export function parseCookies(hs: Header[] | undefined, response: boolean): [string, string][] {
  const out: [string, string][] = [];
  for (const h of hs ?? []) {
    const n = h.name.toLowerCase();
    if (!response && n === 'cookie') {
      for (const part of h.value.split(';')) {
        const [k, v = ''] = part.trim().split(/=(.*)/s);
        if (k) out.push([k, v]);
      }
    } else if (response && n === 'set-cookie') {
      const [first, ...attrs] = h.value.split(';');
      const [k, v = ''] = first.trim().split(/=(.*)/s);
      out.push([k, attrs.length ? `${v}  ·  ${attrs.map((a) => a.trim()).join('; ')}` : v]);
    }
  }
  return out;
}

const shq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`;
const skipCurl = new Set(['content-length', 'host', 'accept-encoding', 'proxy-connection', 'connection']);

/** A cURL command that repeats the request. Pass an unredacted flow. */
export function toCurl(f: FlowDetail, body: string | null): string {
  const lines = [`curl${f.method !== 'GET' || body ? ` -X ${f.method}` : ''} ${shq(f.url)}`];
  for (const h of f.req_headers ?? []) {
    if (!skipCurl.has(h.name.toLowerCase())) lines.push(`-H ${shq(`${h.name}: ${h.value}`)}`);
  }
  if (header(f.req_headers, 'accept-encoding')) lines.push('--compressed');
  if (body) lines.push(`--data-raw ${shq(body)}`);
  return lines.join(' \\\n  ');
}

export type BodyKind = 'json' | 'html' | 'xml' | 'js' | 'css' | 'image' | 'form' | 'multipart' | 'text' | 'binary';

export function bodyKind(contentType: string | undefined, sample: string | null, base64: boolean): BodyKind {
  const ct = (contentType ?? '').split(';')[0].trim().toLowerCase();
  if (ct.startsWith('image/')) return 'image';
  if (ct.includes('json')) return 'json';
  if (ct === 'application/x-www-form-urlencoded') return 'form';
  if (ct.startsWith('multipart/')) return 'multipart';
  if (ct.includes('html')) return 'html';
  if (ct.includes('xml')) return 'xml';
  if (ct.includes('javascript') || ct.includes('ecmascript')) return 'js';
  if (ct === 'text/css') return 'css';
  if (base64) return 'binary';
  const s = sample?.trimStart() ?? '';
  if (s.startsWith('{') || s.startsWith('[')) return 'json';
  if (s.startsWith('<')) return 'html';
  return 'text';
}

/** Splits a multipart body into parts (name, filename, content type, text or size). */
export function parseMultipart(body: string, contentType: string): { name: string; info: string }[] {
  const m = /boundary="?([^";]+)"?/i.exec(contentType);
  if (!m) return [];
  const parts = body.split(`--${m[1]}`).slice(1, -1);
  return parts.map((p) => {
    const [head, ...rest] = p.replace(/^\r?\n/, '').split(/\r?\n\r?\n/);
    const value = rest.join('\n\n').replace(/\r?\n$/, '');
    const name = /name="([^"]*)"/i.exec(head)?.[1] ?? '';
    const file = /filename="([^"]*)"/i.exec(head)?.[1];
    const ct = /content-type:\s*([^\r\n]+)/i.exec(head)?.[1];
    return { name, info: file ? `${file} · ${ct ?? ''} · ${value.length} B` : value };
  });
}

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export async function copyText(s: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(s);
    return true;
  } catch {
    // Fallback for non-secure contexts.
    const ta = document.createElement('textarea');
    ta.value = s;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    ta.remove();
    return ok;
  }
}

/** JSON path segment: .key, ["odd key"], [3]. */
export function pathSeg(key: string | number): string {
  if (typeof key === 'number') return `[${key}]`;
  return /^[A-Za-z_$][\w$]*$/.test(key) ? `.${key}` : `[${JSON.stringify(key)}]`;
}

const REASON: Record<number, string> = {
  100: 'Continue', 101: 'Switching Protocols', 200: 'OK', 201: 'Created', 202: 'Accepted', 204: 'No Content',
  206: 'Partial Content', 301: 'Moved Permanently', 302: 'Found', 303: 'See Other', 304: 'Not Modified',
  307: 'Temporary Redirect', 308: 'Permanent Redirect', 400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden',
  404: 'Not Found', 405: 'Method Not Allowed', 408: 'Request Timeout', 409: 'Conflict', 410: 'Gone',
  412: 'Precondition Failed', 413: 'Payload Too Large', 415: 'Unsupported Media Type', 422: 'Unprocessable Entity',
  429: 'Too Many Requests', 500: 'Internal Server Error', 501: 'Not Implemented', 502: 'Bad Gateway',
  503: 'Service Unavailable', 504: 'Gateway Timeout',
};

/** The start line and headers of a message, as they appear on the wire (the Raw view). */
export function rawHead(flow: Flow, side: 'req' | 'resp'): string {
  const proto = flow.protocol || 'HTTP/1.1';
  const start =
    side === 'req'
      ? `${flow.method} ${flow.path || '/'}${flow.query ? '?' + flow.query : ''} ${proto}`
      : `${proto} ${flow.status}${REASON[flow.status] ? ' ' + REASON[flow.status] : ''}`;
  const hs = (side === 'req' ? flow.req_headers : flow.resp_headers) ?? [];
  const host = side === 'req' && !hs.some((h) => h.name.toLowerCase() === 'host') ? [`Host: ${flow.host}`] : [];
  return [start, ...host, ...hs.map((h) => `${h.name}: ${h.value}`)].join('\n');
}
