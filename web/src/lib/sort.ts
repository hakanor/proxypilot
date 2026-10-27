import type { Row } from './traffic.svelte';
import { shortType } from './format';
import { appOf } from './view';

export type SortKey = 'n' | 'method' | 'host' | 'path' | 'status' | 'type' | 'size' | 'duration' | 'at' | 'url' | 'client';

const value = (r: Row, k: SortKey): string | number => {
  switch (k) {
    case 'n':
      return r.n;
    case 'method':
      return r.method;
    case 'host':
      return r.host;
    case 'path':
      return r.gql_op ?? r.path;
    case 'status':
      return r.status ?? (r.error_kind ? 1000 : 0);
    case 'type':
      return shortType(r.content_type);
    case 'size':
      return r.resp_size || r.bytes_down;
    case 'duration':
      return r.total_ms;
    case 'at':
      return r.created_at;
    case 'url':
      return r.url;
    case 'client':
      return appOf(r);
  }
};

/** Sorted copy; ties keep arrival order. */
export function sortRows(rows: Row[], key: SortKey, dir: 1 | -1): Row[] {
  return rows.slice().sort((a, b) => {
    const x = value(a, key), y = value(b, key);
    const c = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y));
    return c * dir || a.n - b.n;
  });
}

/** Header clicks cycle: ascending → descending → off (arrival order). */
export function nextSort(cur: { key: SortKey; dir: 1 | -1 } | null, key: SortKey): { key: SortKey; dir: 1 | -1 } | null {
  if (!cur || cur.key !== key) return { key, dir: key === 'size' || key === 'duration' ? -1 : 1 };
  const first = key === 'size' || key === 'duration' ? -1 : 1;
  return cur.dir === first ? { key, dir: (-first) as 1 | -1 } : null;
}
