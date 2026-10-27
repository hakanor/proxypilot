import type { BPHeader } from './api';

/** The editable parts of a paused message (request: method+url, response: status). */
export interface Message {
  method: string;
  url: string;
  status: number;
  headers: BPHeader[];
  body: string;
}

/** Renders a message as raw HTTP text ("GET url HTTP/1.1" or "HTTP/1.1 200", headers, blank line, body). */
export function toRaw(m: Message, phase: 'request' | 'response'): string {
  const start = phase === 'request' ? `${m.method} ${m.url} HTTP/1.1` : `HTTP/1.1 ${m.status}`;
  const head = m.headers.filter((h) => h.name).map((h) => `${h.name}: ${h.value}`);
  return [start, ...head].join('\n') + '\n\n' + m.body;
}

/** Parses raw HTTP text back into a message; null with a reason when the start line is unreadable. */
export function fromRaw(text: string, phase: 'request' | 'response'): { msg?: Message; error?: string } {
  const norm = text.replace(/\r\n/g, '\n');
  const split = norm.indexOf('\n\n');
  const head = split < 0 ? norm : norm.slice(0, split);
  const body = split < 0 ? '' : norm.slice(split + 2);
  const [start = '', ...lines] = head.split('\n');
  const msg: Message = { method: '', url: '', status: 0, headers: [], body };
  if (phase === 'request') {
    const m = start.trim().match(/^(\S+)\s+(\S+)(?:\s+HTTP\/\S+)?$/);
    if (!m) return { error: 'start' };
    [msg.method, msg.url] = [m[1], m[2]];
  } else {
    const m = start.trim().match(/^HTTP\/\S+\s+(\d{3})/);
    if (!m) return { error: 'start' };
    msg.status = Number(m[1]);
  }
  for (const line of lines) {
    if (!line.trim()) continue;
    const i = line.indexOf(':');
    if (i <= 0) return { error: 'header' };
    msg.headers.push({ name: line.slice(0, i).trim(), value: line.slice(i + 1).trim() });
  }
  return { msg };
}

/** Pretty-prints JSON text; null when it is not JSON. */
export function prettyJSON(text: string): string | null {
  if (!text.trim()) return null;
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return null;
  }
}

/** Reports the JSON parse error message, or '' when valid. */
export function jsonError(text: string): string {
  try {
    JSON.parse(text);
    return '';
  } catch (e) {
    return e instanceof Error ? e.message : String(e);
  }
}
