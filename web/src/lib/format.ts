// Number formatting helpers (DESIGN.md §2, §8): 1024 base, one decimal from 1 KB.
const nf1 = new Intl.NumberFormat(undefined, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const nf0 = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });
const units = ['B', 'KB', 'MB', 'GB', 'TB'];

export function formatBytes(n: number | undefined | null): string {
  if (n == null || n < 0) return '—';
  if (n < 1024) return `${nf0.format(n)} B`;
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${nf1.format(v)} ${units[i]}`;
}

export function formatRate(bytesPerSec: number): string {
  return `${formatBytes(Math.round(bytesPerSec))}/s`;
}

export function formatDuration(ms: number | undefined | null): string {
  if (ms == null || ms <= 0) return '—';
  if (ms < 1000) return `${nf0.format(ms)} ms`;
  if (ms < 60_000) return `${nf1.format(ms / 1000)} s`;
  return `${nf1.format(ms / 60_000)} min`;
}

const tf = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });

export function formatClock(iso: string): string {
  const d = new Date(iso);
  return isNaN(d.getTime()) ? '' : tf.format(d);
}

export function formatCount(n: number): string {
  return nf0.format(n);
}

/** "application/json; charset=utf-8" → "json". */
export function shortType(ct: string | undefined): string {
  if (!ct) return '';
  const mt = ct.split(';')[0].trim().toLowerCase();
  const sub = mt.split('/')[1] ?? mt;
  if (sub.includes('json')) return 'json';
  if (sub.includes('xml')) return 'xml';
  if (sub === 'javascript' || sub === 'ecmascript') return 'js';
  if (sub === 'x-www-form-urlencoded') return 'form';
  if (mt.startsWith('font/')) return 'font';
  if (sub === 'plain') return 'text';
  return sub.replace(/^x-/, '').slice(0, 8);
}
