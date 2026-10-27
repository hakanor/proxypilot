<script lang="ts">
  import { t } from '../lib/i18n';
  import type { StreamState } from '../lib/stream';
  import { Button } from '$lib/components/ui/button/index.js';

  interface Props {
    conn: StreamState;
    notice: string | null;
    onretry: () => void;
    ondismiss: () => void;
  }
  let { conn, notice, onretry, ondismiss }: Props = $props();

  let now = $state(Date.now());
  $effect(() => {
    if (conn.kind !== 'waiting') return;
    const id = setInterval(() => (now = Date.now()), 250);
    return () => clearInterval(id);
  });
  const seconds = $derived(conn.kind === 'waiting' ? Math.max(0, Math.ceil((conn.retryAt - now) / 1000)) : 0);
</script>

{#if conn.kind === 'waiting'}
  <div class="flex shrink-0 items-center gap-3 bg-err-bg px-gutter py-1.5 text-[13px] font-medium text-err" role="alert">
    <span>{t('banner.disconnected', { seconds })}</span>
    <Button variant="outline" size="sm" class="h-[22px]" onclick={onretry}>{t('banner.retryNow')}</Button>
  </div>
{:else if conn.kind === 'unauthorized'}
  <div class="flex shrink-0 items-center gap-3 bg-err-bg px-gutter py-1.5 text-[13px] font-medium text-err" role="alert">{t('banner.unauthorized')}</div>
{:else if notice}
  <div class="flex shrink-0 items-center gap-3 bg-warn-bg px-gutter py-1.5 text-[13px] font-medium text-warn" role="status">
    <span>{notice}</span>
    <Button variant="outline" size="sm" class="h-[22px]" onclick={ondismiss}>×</Button>
  </div>
{/if}

