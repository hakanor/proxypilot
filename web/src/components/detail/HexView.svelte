<script lang="ts">
  import { t } from '../../lib/i18n';
  import { cn } from '$lib/utils.js';
  import { Button } from '$lib/components/ui/button/index.js';

  const rowCls = 'grid grid-cols-[74px_1fr_130px] gap-2 rounded px-1.5 py-0.5 whitespace-pre';

  interface Props {
    bytes: Uint8Array;
  }
  let { bytes }: Props = $props();

  const PAGE = 4096; // 256 rows
  let limit = $state(PAGE);
  const rows = $derived.by(() => {
    const out: { off: string; hex: string; ascii: string }[] = [];
    const n = Math.min(bytes.length, limit);
    for (let o = 0; o < n; o += 16) {
      const chunk = bytes.subarray(o, Math.min(o + 16, n));
      const h = Array.from(chunk, (b) => b.toString(16).padStart(2, '0'));
      out.push({
        off: o.toString(16).padStart(8, '0'),
        hex: h.slice(0, 8).join(' ') + '  ' + h.slice(8).join(' '),
        ascii: Array.from(chunk, (b) => (b >= 32 && b < 127 ? String.fromCharCode(b) : '.')).join(''),
      });
    }
    return out;
  });
</script>

<div class="py-2 font-mono text-xs">
  <div class={cn(rowCls, 'text-muted-foreground')}><span>{t('viewer.offset')}</span><span></span><span></span></div>
  {#each rows as r, i (r.off)}
    <div class={cn(rowCls, i % 2 === 1 && 'bg-[var(--zebra)]')}>
      <span class="text-faint">{r.off}</span><span>{r.hex}</span><span class="text-muted-foreground">{r.ascii}</span>
    </div>
  {/each}
  {#if bytes.length > limit}
    <Button variant="link" size="sm" class="text-accent-foreground" onclick={() => (limit += PAGE * 4)}>{t('viewer.showMore', { n: Math.min(PAGE * 4, bytes.length - limit) })}</Button>
  {/if}
</div>
