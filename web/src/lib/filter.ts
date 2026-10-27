// Filter bar syntax, same keys as the CLI: host: status: method: type: plus free text.
import type { FlowRow } from './api';
import { shortType } from './format';

export interface Chip {
  key: 'host' | 'status' | 'method' | 'type';
  value: string;
}

export const chipKeys = ['host', 'status', 'method', 'type'] as const;

/** Splits "host:api.* foo status:5xx" into chips and the remaining text. */
export function parseQuery(q: string): { chips: Chip[]; text: string } {
  const chips: Chip[] = [];
  const rest: string[] = [];
  for (const tok of q.trim().split(/\s+/).filter(Boolean)) {
    const m = /^(host|status|method|type):(.+)$/i.exec(tok);
    if (m) chips.push({ key: m[1].toLowerCase() as Chip['key'], value: m[2] });
    else rest.push(tok);
  }
  return { chips, text: rest.join(' ') };
}

function wildcard(p: string): RegExp {
  const esc = p.toLowerCase().replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*');
  return new RegExp(`^${esc}$`);
}

/** "2xx", "404", "400-499", "4xx,5xx" (the CLI --status grammar). */
function statusMatcher(v: string): (s: number) => boolean {
  const parts = v.split(',').map((p) => p.trim().toLowerCase());
  return (s) =>
    parts.some((p) => {
      if (/^[1-5]xx$/.test(p)) return Math.floor(s / 100) === Number(p[0]);
      const r = /^(\d{3})-(\d{3})$/.exec(p);
      if (r) return s >= Number(r[1]) && s <= Number(r[2]);
      return s === Number(p);
    });
}

export function isError(r: FlowRow): boolean {
  return r.state === 'failed' || !!r.error_kind || (r.status ?? 0) >= 400;
}

export function compileFilter(chips: Chip[], text: string, errorsOnly: boolean): (r: FlowRow) => boolean {
  const tests: ((r: FlowRow) => boolean)[] = [];
  for (const c of chips) {
    switch (c.key) {
      case 'host': {
        const re = c.value.includes('*') ? wildcard(c.value) : null;
        const v = c.value.toLowerCase();
        tests.push((r) => (re ? re.test(r.host.toLowerCase()) : r.host.toLowerCase().includes(v)));
        break;
      }
      case 'status': {
        const m = statusMatcher(c.value);
        tests.push((r) => m(r.status ?? 0));
        break;
      }
      case 'method': {
        const set = new Set(c.value.toUpperCase().split(','));
        tests.push((r) => set.has(r.method));
        break;
      }
      case 'type': {
        const v = c.value.toLowerCase();
        tests.push((r) => shortType(r.content_type) === v || (r.content_type ?? '').toLowerCase().includes(v));
        break;
      }
    }
  }
  if (text) {
    const v = text.toLowerCase();
    tests.push((r) => r.url.toLowerCase().includes(v) || (r.error_kind ?? '').includes(v));
  }
  if (errorsOnly) tests.push(isError);
  return (r) => tests.every((f) => f(r));
}

/** Type strip segments: protocol (All/HTTP/HTTPS/WebSocket) and content kinds. */
export const protoTypes = ['all', 'http', 'https', 'ws'] as const;
export const contentTypes = ['json', 'form', 'xml', 'js', 'css', 'graphql', 'document', 'media', 'other'] as const;
export type TypeFilter = (typeof protoTypes)[number] | (typeof contentTypes)[number];

function isWS(r: FlowRow): boolean {
  const s = r.scheme.toLowerCase();
  return s === 'ws' || s === 'wss' || r.status === 101;
}

function contentKind(r: FlowRow): Exclude<TypeFilter, 'all' | 'http' | 'https' | 'ws'> {
  if (r.gql_op) return 'graphql';
  const mt = (r.content_type ?? '').split(';')[0].trim().toLowerCase();
  if (mt.includes('json')) return 'json';
  if (mt === 'application/x-www-form-urlencoded' || mt === 'multipart/form-data') return 'form';
  if (mt.includes('xml') && !mt.includes('xhtml')) return 'xml';
  if (mt.includes('javascript') || mt.includes('ecmascript')) return 'js';
  if (mt === 'text/css') return 'css';
  if (mt === 'text/html' || mt === 'application/xhtml+xml' || mt === 'application/pdf') return 'document';
  if (/^(image|video|audio|font)\//.test(mt) || mt.includes('font')) return 'media';
  return 'other';
}

export function typeMatcher(f: TypeFilter): (r: FlowRow) => boolean {
  switch (f) {
    case 'all':
      return () => true;
    case 'http':
      return (r) => r.scheme.toLowerCase() === 'http';
    case 'https':
      return (r) => r.scheme.toLowerCase() === 'https';
    case 'ws':
      return isWS;
    default:
      return (r) => contentKind(r) === f;
  }
}
