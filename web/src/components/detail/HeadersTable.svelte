<script lang="ts">
  import { t } from '../../lib/i18n';
  import { cn } from '$lib/utils.js';
  import { Button } from '$lib/components/ui/button/index.js';

  interface Props {
    title?: string;
    rows: [string, string][];
    empty: string;
    oncopy: (text: string) => void;
  }
  let { title, rows, empty, oncopy }: Props = $props();
  const masked = (v: string) => v.includes('[redacted');
</script>

<section class="mb-3">
  {#if title}
    <div class="flex items-center justify-between pr-gutter">
      <span class="section-label">{title}</span>
      {#if rows.length}
        <Button variant="ghost" size="icon-xs" class="text-muted-foreground" onclick={() => oncopy(rows.map(([k, v]) => `${k}: ${v}`).join('\n'))}>⧉</Button>
      {/if}
    </div>
  {/if}
  {#if rows.length === 0}
    <p class="mx-gutter my-1 text-faint">{empty}</p>
  {:else}
    {#each rows as [k, v], i (i)}
      <div class={cn('grid grid-cols-[180px_1fr] gap-3 px-gutter py-1.5 font-mono text-[12.5px]', i > 0 && 'border-t border-border')}>
        <span class="truncate text-head" title={k}>{k}</span>
        <span
          class={cn('cursor-copy break-all text-foreground', masked(v) && 'text-faint')}
          role="button"
          tabindex="-1"
          title={t('viewer.copyValue')}
          onclick={() => oncopy(v)}
          onkeydown={() => {}}>{v}</span
        >
      </div>
    {/each}
  {/if}
</section>
