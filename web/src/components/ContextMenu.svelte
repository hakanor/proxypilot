<script lang="ts">
  import { onMount } from 'svelte';
  import type { Item } from '../lib/menu';
  import { cn } from '$lib/utils.js';

  interface Props {
    x: number;
    y: number;
    items: Item[];
    onclose: () => void;
  }
  let { x, y, items, onclose }: Props = $props();

  let el = $state<HTMLDivElement>();
  let sub = $state<number | null>(null);
  let pos = $state({ x: 0, y: 0 });

  // Keep the menu inside the window.
  $effect(() => {
    if (!el) return;
    const r = el.getBoundingClientRect();
    pos = { x: Math.min(x, innerWidth - r.width - 8), y: Math.min(y, innerHeight - r.height - 8) };
  });

  onMount(() => {
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== 'Escape') return;
      if (e instanceof MouseEvent && el?.contains(e.target as Node)) return;
      onclose();
    };
    const t = setTimeout(() => {
      addEventListener('mousedown', close);
      addEventListener('keydown', close);
      addEventListener('blur', onclose);
      addEventListener('resize', onclose);
    });
    return () => {
      clearTimeout(t);
      removeEventListener('mousedown', close);
      removeEventListener('keydown', close);
      removeEventListener('blur', onclose);
      removeEventListener('resize', onclose);
    };
  });

  function run(it: Item) {
    if (it.disabled || it.children) return;
    it.action?.();
    onclose();
  }
</script>

{#snippet list(entries: Item[], nested: boolean)}
  {#each entries as it, i (i)}
    {#if it.sep}<div class="mx-0.5 my-1 border-t border-border"></div>{/if}
    <div class="relative" role="none" onmouseenter={() => !nested && (sub = it.children ? i : null)}>
      <button type="button" role="menuitem" class={cn(
          'flex h-8 w-full items-center justify-between gap-4 whitespace-nowrap rounded-lg border-0 bg-transparent px-2.5 text-left text-[13px] text-popover-foreground enabled:hover:bg-accent enabled:hover:text-accent-foreground disabled:cursor-default disabled:opacity-40',
          !nested && sub === i && 'bg-accent text-accent-foreground',
        )} disabled={it.disabled} onclick={() => run(it)}>
        <span>{it.label}</span>
        <span class="text-[11px] text-faint">{it.children ? '▸' : (it.hint ?? '')}</span>
      </button>
      {#if !nested && sub === i && it.children}
        <div class="absolute left-[calc(100%-4px)] top-[-5px] z-[100] min-w-[250px] rounded-xl border border-border bg-popover p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.45)]" role="menu">{@render list(it.children, true)}</div>
      {/if}
    </div>
  {/each}
{/snippet}

<div class="fixed z-[100] min-w-[250px] rounded-xl border border-border bg-popover p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.45)]" role="menu" bind:this={el} style="left:{pos.x || x}px;top:{pos.y || y}px">
  {@render list(items, false)}
</div>

