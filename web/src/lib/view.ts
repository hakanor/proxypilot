import type { FlowRow } from './api';

/** Sidebar selection. device/app/host narrow each other (tree path). */
export type View =
  | { kind: 'all' }
  | { kind: 'pinned' }
  | { kind: 'source'; value: string }
  | { kind: 'tree'; device: string; app?: string; host?: string }
  | { kind: 'domain'; value: string; host?: string }
  | { kind: 'tab'; value: string };

// Flows recorded before device/app tagging only have client_label (an app name):
// treat them as this Mac's traffic with that app.
export const deviceOf = (r: FlowRow) => r.device || (r.source === 'cdp' ? 'Chrome' : 'This Mac');
export const appOf = (r: FlowRow) => r.app || r.tab || r.client_label || 'Unknown';

/** "api.invio.com" → "invio.com" (last two labels; three for co.uk-style). */
export function rootDomain(host: string): string {
  if (/^[\d.]+$/.test(host) || host.includes(':')) return host;
  const p = host.split('.');
  if (p.length <= 2) return host;
  const two = p.slice(-2).join('.');
  return /^(co|com|net|org|gov|ac|edu)\.[a-z]{2}$/.test(two) ? p.slice(-3).join('.') : two;
}

export function sameView(a: View, b: View): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function viewMatcher(v: View, pinned: Set<string>): (r: FlowRow) => boolean {
  switch (v.kind) {
    case 'all':
      return () => true;
    case 'pinned':
      return (r) => pinned.has(r.id);
    case 'source':
      return (r) => r.source === v.value;
    case 'tree':
      return (r) => deviceOf(r) === v.device && (v.app === undefined || appOf(r) === v.app) && (v.host === undefined || r.host === v.host);
    case 'domain':
      return (r) => rootDomain(r.host) === v.value && (v.host === undefined || r.host === v.host);
    case 'tab':
      return (r) => r.tab === v.value;
  }
}
