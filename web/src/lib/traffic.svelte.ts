// Client-side traffic model: ordered rows, live merge of stream batches,
// throughput (1 s window) and session byte totals.
import { api, toRow, type FlowRow } from './api';
import type { WireEvent } from './stream';

export interface Row extends FlowRow {
  n: number; // display index (#)
}

const RATE_WINDOW_MS = 1000;

export class Traffic {
  /** Oldest first. Replaced (not mutated) on change so derived filters rerun once per batch. */
  rows = $state.raw<Row[]>([]);
  rateDown = $state(0);
  rateUp = $state(0);
  totalDown = $state(0);
  totalUp = $state(0);

  /** Mirrors the daemon's retention cap so rows never outlive their stored flow. */
  maxRows = 50_000;

  private index = new Map<string, number>(); // id → position in rows
  private seq = 0;
  private samples: { t: number; up: number; down: number }[] = [];

  constructor() {
    setInterval(() => this.updateRate(), 250);
  }

  /** Initial load / resync: the latest 500 from REST, merged with what we have. */
  async load(replace: boolean) {
    const [{ flows }, sum] = await Promise.all([api.flows(500), replace ? api.summaryAll().catch(() => null) : Promise.resolve(null)]);
    const incoming = flows.map(toRow).reverse(); // API is newest first
    if (replace) {
      this.index.clear();
      this.seq = 0;
      this.rows = [];
      this.totalDown = sum?.bytes_down ?? 0;
      this.totalUp = sum?.bytes_up ?? 0;
    }
    this.merge(incoming);
  }

  apply(events: WireEvent[]) {
    const updates: FlowRow[] = [];
    const now = Date.now();
    for (const e of events) {
      if (e.type === 'breakpoint.paused' || e.type === 'breakpoint.resumed') {
        this.markPaused(e.data?.flow_id, e.type === 'breakpoint.paused');
        continue;
      }
      if (!e.type.startsWith('flow.') || !e.data) continue;
      const r = e.data as FlowRow;
      updates.push(r);
      if (e.type === 'flow.completed' || e.type === 'flow.failed') {
        this.totalDown += r.bytes_down;
        this.totalUp += r.bytes_up;
        this.samples.push({ t: now, up: r.bytes_up, down: r.bytes_down });
      }
    }
    if (updates.length) this.merge(updates);
  }

  clear() {
    this.index.clear();
    this.rows = [];
    this.seq = 0;
    this.totalDown = this.totalUp = 0;
    this.samples = [];
  }

  /** Breakpoint events tint the row until the flow completes. */
  private markPaused(flowId: string | undefined, paused: boolean) {
    const i = flowId ? this.index.get(flowId) : undefined;
    if (i === undefined) return;
    const r = this.rows[i];
    if (paused === (r.state === 'paused') || (!paused && r.state !== 'paused')) return;
    const next = this.rows.slice();
    next[i] = { ...r, state: paused ? 'paused' : 'pending' };
    this.rows = next;
  }

  get(id: string): Row | undefined {
    const i = this.index.get(id);
    return i === undefined ? undefined : this.rows[i];
  }

  private merge(list: FlowRow[]) {
    let next = this.rows.slice();
    let appended = false;
    for (const r of list) {
      const i = this.index.get(r.id);
      if (i !== undefined) {
        next[i] = { ...r, n: next[i].n };
      } else {
        this.index.set(r.id, next.length);
        next.push({ ...r, n: ++this.seq });
        appended = true;
      }
    }
    if (appended && next.some((r, i) => i > 0 && r.created_at < next[i - 1].created_at)) {
      // Resync can interleave older flows; keep chronological order.
      next.sort((a, b) => (a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0));
      next = next.map((r, i) => ({ ...r, n: i + 1 }));
      this.seq = next.length;
      this.reindex(next);
    }
    if (next.length > this.maxRows) {
      next = next.slice(next.length - this.maxRows);
      this.reindex(next);
    }
    this.rows = next;
  }

  private reindex(rows: Row[]) {
    this.index.clear();
    rows.forEach((r, i) => this.index.set(r.id, i));
  }

  private updateRate() {
    const cutoff = Date.now() - RATE_WINDOW_MS;
    while (this.samples.length && this.samples[0].t < cutoff) this.samples.shift();
    let up = 0;
    let down = 0;
    for (const s of this.samples) {
      up += s.up;
      down += s.down;
    }
    const k = 1000 / RATE_WINDOW_MS;
    if (this.rateDown !== down * k) this.rateDown = down * k;
    if (this.rateUp !== up * k) this.rateUp = up * k;
  }
}
