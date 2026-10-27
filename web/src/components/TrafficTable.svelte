<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Virtualizer,
    elementScroll,
    observeElementOffset,
    observeElementRect,
    type VirtualItem,
  } from '@tanstack/virtual-core';
  import type { Row } from '../lib/traffic.svelte';
  import type { SortKey } from '../lib/sort';
  import { t } from '../lib/i18n';
  import { formatBytes, formatClock, formatDuration } from '../lib/format';
  import { statusPill } from '../lib/status';
  import { cn } from '$lib/utils.js';
  import { appOf, deviceOf } from '../lib/view';
  import AppWindow from '@lucide/svelte/icons/app-window';

  const statusTone: Record<string, string> = {
    ok: 'text-ok',
    c4: 'text-c4',
    err: 'text-err',
    warn: 'text-warn',
    neutral: 'text-neutral-fg',
  };
  // App icons that failed to load; never refetched.
  let iconFail = $state(new Set<string>());
  const hasIcon = (r: Row) => {
    const d = deviceOf(r);
    return (d === 'This Mac' || d.includes('Simulator')) && !iconFail.has(appOf(r));
  };

  interface Props {
    rows: Row[];
    selected: string | null;
    pinned: Set<string>;
    autoScroll: boolean;
    onselect: (id: string) => void;
    onopen: (id: string) => void;
    oncontext: (id: string, x: number, y: number) => void;
    sort: { key: SortKey; dir: 1 | -1 } | null;
    onsort: (key: SortKey) => void;
    empty?: import('svelte').Snippet;
  }
  let { rows, selected, pinned, autoScroll, onselect, onopen, oncontext, sort, onsort, empty }: Props = $props();

  const cols: { key: SortKey; label: string; num?: boolean }[] = [
    { key: 'n', label: 'table.index' },
    { key: 'url', label: 'table.url' },
    { key: 'client', label: 'table.client' },
    { key: 'method', label: 'table.method' },
    { key: 'status', label: 'table.status' },
    { key: 'at', label: 'table.clock', num: true },
    { key: 'duration', label: 'table.dur', num: true },
    { key: 'size', label: 'table.size', num: true },
  ];

  // Column widths in px (the URL column always takes the rest). Drag a header edge to
  // resize, double-click it to reset; kept per browser in localStorage.
  const DEFAULT_W: Partial<Record<SortKey, number>> = { n: 52, client: 170, method: 72, status: 64, at: 92, duration: 80, size: 80 };
  const MIN_W = 24;
  let widths = $state<Partial<Record<SortKey, number>>>(loadWidths());
  function loadWidths(): Partial<Record<SortKey, number>> {
    try {
      return JSON.parse(localStorage.getItem('pp.cols.v3') ?? '{}');
    } catch {
      return {};
    }
  }
  function saveWidths() {
    try {
      localStorage.setItem('pp.cols.v3', JSON.stringify(widths));
    } catch {
      /* private mode */
    }
  }
  const custom = $derived(Object.keys(widths).length > 0);
  const colsCSS = $derived(
    cols.map((c) => (c.key === 'url' ? 'minmax(60px, 1fr)' : `${widths[c.key] ?? DEFAULT_W[c.key]}px`)).join(' ') + ' 18px',
  );
  // Each divider sits between column i and i+1 and follows the pointer: the two neighbours trade
  // width (the flexible URL column just absorbs its side). Any column can shrink down to MIN_W.
  const w = (k: SortKey) => widths[k] ?? DEFAULT_W[k] ?? 80;
  function startResize(e: PointerEvent, i: number) {
    e.preventDefault();
    e.stopPropagation();
    const [a, b] = [cols[i].key, cols[i + 1].key];
    const x0 = e.clientX;
    const [wa, wb] = [w(a), w(b)];
    const move = (ev: PointerEvent) => {
      let dx = Math.round(ev.clientX - x0);
      if (a !== 'url') dx = Math.max(dx, MIN_W - wa);
      if (b !== 'url') dx = Math.min(dx, wb - MIN_W);
      const next = { ...widths };
      if (a !== 'url') next[a] = wa + dx;
      if (b !== 'url') next[b] = wb - dx;
      widths = next;
    };
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      document.body.style.cursor = '';
      saveWidths();
    };
    document.body.style.cursor = 'col-resize';
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }
  function resetPair(i: number) {
    const { [cols[i].key]: _a, [cols[i + 1].key]: _b, ...rest } = widths;
    widths = rest;
    saveWidths();
  }

  const ROW = 30;
  let el: HTMLDivElement;
  let items = $state<VirtualItem[]>([]);
  let total = $state(0);
  let atBottom = true;
  let atTop = true;
  // Where new traffic appears: the bottom in arrival order, the top when sorted
  // newest-first, nowhere fixed for other sorts (so there is nothing to follow).
  const follow = $derived<'bottom' | 'top' | null>(
    !sort || ((sort.key === 'n' || sort.key === 'at') && sort.dir === 1)
      ? 'bottom'
      : (sort.key === 'n' || sort.key === 'at') && sort.dir === -1
        ? 'top'
        : null,
  );
  let lastFollow: typeof follow = 'bottom';

  const v = new Virtualizer<HTMLDivElement, HTMLDivElement>({
    count: 0,
    getScrollElement: () => el,
    estimateSize: () => ROW,
    overscan: 20,
    observeElementRect,
    observeElementOffset,
    scrollToFn: elementScroll,
    onChange: (inst) => {
      items = inst.getVirtualItems();
      total = inst.getTotalSize();
    },
  });

  onMount(() => {
    const cleanup = v._didMount();
    v._willUpdate();
    return cleanup;
  });

  $effect(() => {
    const n = rows.length;
    v.setOptions({ ...v.options, count: n });
    v._willUpdate();
    items = v.getVirtualItems();
    total = v.getTotalSize();
    // Follow new traffic while the user sits at that end. Changing the sort jumps to the new end.
    const f = follow;
    const switched = f !== lastFollow;
    lastFollow = f;
    if (!autoScroll || n === 0 || !f) return;
    if (f === 'bottom' && (atBottom || switched)) queueMicrotask(() => v.scrollToIndex(n - 1, { align: 'end' }));
    if (f === 'top' && (atTop || switched)) queueMicrotask(() => v.scrollToOffset(0));
  });

  function onscroll() {
    atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - ROW * 2;
    atTop = el.scrollTop <= ROW * 2;
  }

  /** Keeps the keyboard selection visible. */
  export function reveal(id: string) {
    const i = rows.findIndex((r) => r.id === id);
    if (i >= 0) v.scrollToIndex(i, { align: 'auto' });
  }
</script>

<div class="ttable flex min-h-0 flex-1 flex-col text-[13px]" role="grid" aria-rowcount={rows.length} style={custom ? `--cols: ${colsCSS}` : undefined}>
  <div class="row head h-[30px] shrink-0 border-b border-border text-xs font-medium text-head" role="row">
    {#each cols as c, ci (c.key)}
      <div class="relative flex h-full min-w-0">
      <button
        type="button"
        role="columnheader"
        class={cn(
          'flex h-full w-full min-w-0 items-center gap-1 overflow-hidden border-0 bg-transparent p-0 text-left whitespace-nowrap text-inherit hover:text-foreground',
          c.num && 'justify-end',
          sort?.key === c.key && 'font-semibold text-accent-foreground',
        )}
        aria-sort={sort?.key === c.key ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'}
        onclick={() => onsort(c.key)}>{t(c.label)}{#if sort?.key === c.key}<span class="text-[8px]">{sort.dir === 1 ? '▲' : '▼'}</span>{/if}</button
      >
      {#if ci < cols.length - 1}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <span
          class="absolute top-1 -right-[13px] bottom-1 z-[1] w-3 cursor-col-resize after:absolute after:inset-y-[6px] after:left-1/2 after:w-px after:-translate-x-1/2 after:rounded-full after:bg-faint/35 after:transition-all hover:after:inset-y-0 hover:after:w-[3px] hover:after:bg-primary"
          title={t('table.resize')}
          onpointerdown={(e) => startResize(e, ci)}
          ondblclick={() => resetPair(ci)}
        ></span>
      {/if}
      </div>
    {/each}
    <span></span>
  </div>
  <div class="body relative flex-1 overflow-x-hidden overflow-y-auto" bind:this={el} {onscroll}>
    {#if rows.length === 0}
      {@render empty?.()}
    {/if}
    <div style="height: {total}px; position: relative;">
      {#each items as it (it.key)}
        {@const r = rows[it.index]}
        {#if r}
          {@const p = statusPill(r)}
          {@const sel = r.id === selected}
          {@const app = appOf(r)}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <div
            class={cn(
              'row absolute inset-x-0 top-0 h-[30px] cursor-default hover:bg-hover',
              it.index % 2 === 1 && 'bg-zebra',
              r.state === 'paused' && 'bg-warn-bg',
              sel && 'bg-sel shadow-[inset_3px_0_0_0_var(--primary)] hover:bg-sel',
            )}
            role="row"
            tabindex="-1"
            aria-selected={sel}
            style="transform: translateY({it.start}px)"
            onclick={() => onselect(r.id)}
            ondblclick={() => onopen(r.id)}
            oncontextmenu={(e) => {
              e.preventDefault();
              onselect(r.id);
              oncontext(r.id, e.clientX, e.clientY);
            }}
          >
            <span class="text-faint">{r.n}</span>
            <span class="truncate font-mono text-[12.5px]" title={r.url}
              >{#if r.gql_op}<span class="mr-1 rounded-[5px] bg-neutral-bg px-1.5 font-sans text-[10px] text-muted-foreground">{r.gql_type} {r.gql_op}</span>{/if}{r.url || r.error_kind}</span
            >
            <span class="flex min-w-0 items-center gap-1.5 text-muted-foreground" title={app}>
              {#if hasIcon(r)}
                <img
                  src="/api/v1/app-icons/{encodeURIComponent(app)}"
                  alt=""
                  class="size-4 shrink-0 rounded-[4px]"
                  onerror={() => (iconFail = new Set(iconFail).add(app))}
                />
              {:else}
                <AppWindow class="size-4 shrink-0 text-faint" />
              {/if}
              <span class="truncate">{app}</span>
            </span>
            <span class="font-mono text-xs font-medium">{r.method}</span>
            <span class={cn('font-mono text-xs', p.label === t('status.pending') && p.cls === 'neutral' ? 'text-faint' : statusTone[p.cls])}>{p.label}</span>
            <span class="text-right font-mono text-xs text-faint">{formatClock(r.created_at)}</span>
            <span class="text-right font-mono text-xs text-muted-foreground">{formatDuration(r.total_ms)}</span>
            <span class="text-right font-mono text-xs text-muted-foreground">{formatBytes(r.resp_size || r.bytes_down)}</span>
            <span class="text-[10px] font-semibold text-primary" title={r.matched_rules?.length ? t('table.ruleMark', { ids: r.matched_rules.join(', ') }) : undefined}
              >{r.matched_rules?.length ? 'R' : pinned.has(r.id) ? '★' : ''}</span
            >
          </div>
        {/if}
      {/each}
    </div>
  </div>
</div>

<style>
  /* Grid columns, virtual positioning and container queries stay in CSS. */
  .ttable {
    container-type: inline-size;
  }
  .row {
    display: grid;
    grid-template-columns: var(--cols, 52px minmax(160px, 1fr) 170px 72px 64px 92px 80px 80px 18px);
    column-gap: 14px; /* resize grips sit centred in this gap */
    align-items: center;
    padding: 0 16px;
  }
  .row.head {
    position: static;
  }
  .body {
    contain: strict;
  }
  /* Narrow (detail panel open): drop Size, then Client. */
  @container (max-width: 760px) {
    .row {
      grid-template-columns: var(--cols, 48px minmax(120px, 1fr) 140px 66px 60px 84px 74px 0 18px);
    }
    .ttable:not([style*="--cols"]) .row > :nth-child(8) {
      visibility: hidden;
    }
  }
  @container (max-width: 600px) {
    .row {
      grid-template-columns: var(--cols, 42px minmax(100px, 1fr) 0 60px 56px 76px 66px 0 14px);
    }
    .ttable:not([style*="--cols"]) .row > :nth-child(3),
    .ttable:not([style*="--cols"]) .row > :nth-child(8) {
      visibility: hidden;
    }
  }
</style>
