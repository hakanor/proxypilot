<script lang="ts">
  import { t } from '../../lib/i18n';
  import { formatDuration } from '../../lib/format';
  import type { Timing } from '../../lib/api';

  interface Props {
    timing: Timing | undefined;
  }
  let { timing }: Props = $props();

  // Phases are measured cumulatively from request start; draw them as a waterfall.
  const phases = $derived.by(() => {
    const tm = timing;
    if (!tm || !tm.total_ms) return [];
    const total = tm.total_ms;
    const dns = tm.dns_ms ?? 0;
    const connect = tm.connect_ms ?? 0;
    const tls = tm.tls_ms ?? 0;
    const ttfb = tm.ttfb_ms ?? 0;
    const pre = dns + connect + tls;
    const list: [string, number, string][] = [
      ['dns', dns, 'var(--faint)'],
      ['connect', connect, 'var(--neutral)'],
      ['tls', tls, 'var(--primary)'],
      ['wait', Math.max(0, ttfb - pre), 'var(--warn)'],
      ['download', Math.max(0, total - Math.max(ttfb, pre)), 'var(--ok)'],
    ];
    let start = 0;
    return list.map(([id, ms, color]) => {
      const p = { id, ms, color, left: (start / total) * 100, width: Math.max((ms / total) * 100, ms > 0 ? 0.8 : 0) };
      start += ms;
      return p;
    });
  });
</script>

{#if phases.length === 0}
  <p class="mx-gutter my-3 text-faint">{t('timing.none')}</p>
{:else}
  <div class="flex flex-col gap-2 px-gutter py-3">
    {#each phases as p (p.id)}
      <div class="grid grid-cols-[120px_1fr_64px] items-center gap-2.5">
        <span>{t(`timing.${p.id}`)}</span>
        <span class="relative h-2.5 rounded-[5px] bg-neutral-bg"><span class="absolute inset-y-0 rounded-[5px]" style="left:{p.left}%;width:{p.width}%;background:{p.color}"></span></span>
        <span class="text-right font-mono text-xs">{p.ms > 0 ? formatDuration(p.ms) : '—'}</span>
      </div>
    {/each}
    <div class="grid grid-cols-[120px_1fr_64px] items-center gap-2.5 border-t border-border pt-2 font-semibold">
      <span>{t('timing.total')}</span><span></span><span class="text-right font-mono text-xs">{formatDuration(timing?.total_ms)}</span>
    </div>
    <p class="m-0 text-faint">{t('timing.reused')}: {timing?.reused ? t('timing.yes') : t('timing.no')}</p>
  </div>
{/if}
