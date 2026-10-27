<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t } from '../../lib/i18n';
  import { cn } from '$lib/utils.js';

  // The pane header used by the detail panel and the breakpoint editor: title, then text tabs.
  interface Props {
    title: string;
    list: readonly string[];
    active: string;
    pick: (id: string) => void;
    disabled?: (id: string) => boolean;
    end?: Snippet;
  }
  let { title, list, active, pick, disabled = () => false, end }: Props = $props();
</script>

<div class="flex h-9 shrink-0 items-center gap-4 border-b border-border px-4">
  <span class="font-semibold text-foreground">{title}</span>
  <div class="flex min-w-0 items-center gap-3.5 overflow-x-auto" role="tablist">
    {#each list as id (id)}
      <button
        type="button"
        role="tab"
        aria-selected={active === id}
        disabled={disabled(id)}
        class={cn('shrink-0 text-[12.5px] hover:text-foreground disabled:opacity-40', active === id ? 'font-medium text-accent-foreground' : 'text-muted-foreground')}
        onclick={() => pick(id)}>{t(`detail.tabs.${id}`)}</button
      >
    {/each}
  </div>
  {#if end}<span class="grow"></span>{@render end()}{/if}
</div>
