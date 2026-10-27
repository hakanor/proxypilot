<script lang="ts">
  import { t } from '../lib/i18n';
  import { formatBytes, formatCount, formatRate } from '../lib/format';
  import type { Rule, SSLPolicy } from '../lib/api';
  import { href } from '../lib/route.svelte';

  interface Props {
    totalDown: number;
    totalUp: number;
    rateDown: number;
    rateUp: number;
    shown: number;
    total: number;
    selected?: boolean;
    ruleCounts: Partial<Record<Rule['type'], number>>;
    sslMode?: SSLPolicy['mode'];
  }
  let { totalDown, totalUp, rateDown, rateUp, shown, total, selected = false, ruleCounts, sslMode }: Props = $props();
  const ruleTypes: Rule['type'][] = ['map_local', 'breakpoint', 'rewrite', 'map_remote'];
</script>

<footer class="flex h-[30px] shrink-0 items-center gap-4 overflow-hidden whitespace-nowrap border-t border-border bg-panel px-gutter text-xs text-muted-foreground">
  <span>{t('traffic.rows', { shown: formatCount(shown), total: formatCount(total) })}</span>
  {#if selected}<span>{t('traffic.selected', { n: 1 })}</span>{/if}
  <span class="font-mono tabular-nums text-ok" title={t('traffic.rateHint')}>{t('traffic.down', { rate: formatRate(rateDown) })}</span>
  <span class="font-mono tabular-nums" title={t('traffic.rateHint')}>{t('traffic.up', { rate: formatRate(rateUp) })}</span>
  <span>{t('traffic.session')} <span class="font-mono text-foreground">↓ {formatBytes(totalDown)} ↑ {formatBytes(totalUp)}</span></span>
  <span class="flex-1"></span>
  {#each ruleTypes as k (k)}
    {#if ruleCounts[k]}
      <a href={href('rules')} class="rounded-full bg-neutral-bg px-2 py-px text-[11px] text-neutral-fg no-underline hover:text-foreground"
        >{t(`traffic.ruleChips.${k}`, { n: ruleCounts[k] ?? 0 })}</a
      >
    {/if}
  {/each}
  {#if sslMode}
    <span class="rounded-full bg-neutral-bg px-2 py-px text-[11px] text-neutral-fg">{t('traffic.sslChip', { mode: t(`toolbar.sslModes.${sslMode}`) })}</span>
  {/if}
</footer>
