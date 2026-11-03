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

  // Column widths in px. Columns open at their minimum width and the URL column takes the
  // rest. Dragging a divider resizes only the column to its left; the columns to its right
  // keep their widths and move with it (like Proxyman). Double-click a divider to reset.
  const MIN_W: Partial<Record<SortKey, number>> = { n: 40, url: 120, client: 110, method: 60, status: 52, at: 72, duration: 68, size: 64 };
  let widths = $state<Partial<Record<SortKey, number>>>(loadWidths());
  function loadWidths(): Partial<Record<SortKey, number>> {
    try {
      return JSON.parse(localStorage.getItem('pp.cols.v4') ?? '{}');
    } catch {
      return {};
    }
  }
  function saveWidths() {
    try {
      localStorage.setItem('pp.cols.v4', JSON.stringify(widths));
    } catch {
      /* private mode */
    }
  }
  const custom = $derived(Object.keys(widths).length > 0);
  const colsCSS = $derived(
    cols
      .map((c) => (c.key === 'url' ? (widths.url ? `${widths.url}px` : 'minmax(60px, 1fr)') : `${widths[c.key] ?? MIN_W[c.key]}px`))
      .join(' ') + ' minmax(18px, 1fr)',
  );
  // Columns may outgrow the table; it then scrolls sideways (8 column gaps of 14px, 16px padding each side).
  const minTotal = $derived(cols.reduce((n, c) => n + (widths[c.key] ?? MIN_W[c.key] ?? 0), 0) + 18 + 8 * 14 + 32);
  function startResize(e: PointerEvent, i: number) {
    e.preventDefault();
    e.stopPropagation();
    const row = (e.currentTarget as HTMLElement).closest('.row') as HTMLElement;
    const cells = [...row.children] as HTMLElement[];
    // Freeze every column at its rendered width so only the dragged one changes.
    const frozen: Partial<Record<SortKey, number>> = {};
    cols.forEach((c, ci) => (frozen[c.key] = Math.round(cells[ci].getBoundingClientRect().width)));
    const key = cols[i].key;
    const w0 = frozen[key]!;
    const x0 = e.clientX;
    const move = (ev: PointerEvent) => {
      const dx = Math.round(ev.clientX - x0);
      widths = { ...frozen, [key]: Math.max(MIN_W[key] ?? 24, w0 + dx) };
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
  function resetCol(i: number) {
    const { [cols[i].key]: _, ...rest } = widths;
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
    hideTip();
  }

  // The cell splits the URL in two halves that shrink equally: start…end stays visible.
  // Full URL on hover, only when the cell cuts it off. One shared element, so rows stay light.
  let tip = $state<{ url: string; left: number; top?: number; bottom?: number; el: HTMLElement } | null>(null);
  let tipTimer: ReturnType<typeof setTimeout> | undefined;
  function showTip(e: PointerEvent, url: string) {
    const cell = e.currentTarget as HTMLElement;
    clearTimeout(tipTimer);
    if (!url || ![...cell.children].some((c) => c.scrollWidth > c.clientWidth)) return;
    tipTimer = setTimeout(() => {
      const b = cell.getBoundingClientRect();
      const left = Math.max(8, Math.min(b.left, innerWidth - 728));
      tip = b.bottom > innerHeight / 2 ? { url, left, bottom: innerHeight - b.top + 4, el: cell } : { url, left, top: b.bottom + 4, el: cell };
    }, 250);
  }
  function hideTip() {
    clearTimeout(tipTimer);
    tip = null;
  }
  // The virtualizer can drop the hovered row without a pointerleave.
  $effect(() => {
    void items;
    if (tip && !tip.el.isConnected) hideTip();
  });
  // A container query ancestor would capture position: fixed, so the tooltip lives on body.
  function portal(node: HTMLElement) {
    document.body.append(node);
    return { destroy: () => node.remove() };
  }

  /** Keeps the keyboard selection visible. */
  export function reveal(id: string) {
    const i = rows.findIndex((r) => r.id === id);
    if (i >= 0) v.scrollToIndex(i, { align: 'auto' });
  }
</script>

<div class="ttable min-h-0 flex-1 overflow-x-auto text-[13px]" role="grid" aria-rowcount={rows.length} style={custom ? `--cols: ${colsCSS}` : undefined} onscroll={hideTip}>
  <div class="flex h-full flex-col" style={custom ? `min-width: ${minTotal}px` : undefined}>
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
          ondblclick={() => resetCol(ci)}
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
          {@const text = r.url || r.error_kind || ''}
          {@const mid = Math.ceil(text.length / 2)}
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
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="flex min-w-0 overflow-hidden font-mono text-[12.5px]" onpointerenter={(e) => showTip(e, r.url)} onpointerleave={hideTip} onpointerdown={hideTip}
              >{#if r.gql_op}<span class="mr-1 shrink-0 rounded-[5px] bg-neutral-bg px-1.5 font-sans text-[10px] text-muted-foreground">{r.gql_type} {r.gql_op}</span>{/if}<span class="min-w-0 truncate">{text.slice(0, mid)}</span><span class="min-w-0 overflow-hidden whitespace-nowrap" dir="rtl"><span dir="ltr">{text.slice(mid)}</span></span></span
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
              >{pinned.has(r.id) ? '★' : ''}{r.matched_rules?.length ? 'R' : ''}</span
            >
          </div>
        {/if}
      {/each}
    </div>
  </div>
  </div>
</div>

{#if tip}
  {@const m = tip.url.match(/^([a-z]+:\/\/[^/?#]+)(.*)$/is)}
  <div
    use:portal
    role="tooltip"
    class="pointer-events-none fixed z-50 line-clamp-[12] w-max max-w-[min(720px,calc(100vw-16px))] rounded-lg border border-border bg-popover px-3 py-2 font-mono text-xs leading-[1.6] break-all text-muted-foreground shadow-xl ring-1 ring-black/10"
    style:left="{tip.left}px"
    style:top={tip.top === undefined ? undefined : `${tip.top}px`}
    style:bottom={tip.bottom === undefined ? undefined : `${tip.bottom}px`}
  >
    {#if m}<span class="font-semibold text-foreground">{m[1]}</span>{m[2]}{:else}{tip.url}{/if}
  </div>
{/if}

<style>
  /* Grid columns, virtual positioning and container queries stay in CSS. */
  .ttable {
    container-type: inline-size;
  }
  .row {
    display: grid;
    grid-template-columns: var(--cols, 40px minmax(120px, 1fr) 110px 60px 52px 72px 68px 64px 18px);
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
      grid-template-columns: var(--cols, 40px minmax(120px, 1fr) 110px 60px 52px 72px 68px 0 18px);
    }
    .ttable:not([style*="--cols"]) .row > :nth-child(8) {
      visibility: hidden;
    }
  }
  @container (max-width: 600px) {
    .row {
      grid-template-columns: var(--cols, 40px minmax(100px, 1fr) 0 60px 52px 72px 68px 0 14px);
    }
    .ttable:not([style*="--cols"]) .row > :nth-child(3),
    .ttable:not([style*="--cols"]) .row > :nth-child(8) {
      visibility: hidden;
    }
  }
</style>
