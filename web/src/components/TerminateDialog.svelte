<script lang="ts">
  import { onMount } from 'svelte';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import Power from '@lucide/svelte/icons/power';
  import { t } from '../lib/i18n';
  import { api } from '../lib/api';

  let { onclose }: { onclose: () => void } = $props();

  // One confirmation; the button unlocks after a 3-2-1 countdown so it cannot be hit by accident.
  let left = $state(3);
  let busy = $state(false);
  let done = $state(false);
  let error = $state<string | null>(null);

  onMount(() => {
    const id = setInterval(() => --left <= 0 && clearInterval(id), 1000);
    return () => clearInterval(id);
  });

  async function run() {
    busy = true;
    try {
      await api.terminate(true);
      done = true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
    busy = false;
  }
</script>

<Dialog.Root open onOpenChange={(o) => !o && !done && onclose()}>
  <Dialog.Content showCloseButton={!done} class="flex w-[min(460px,calc(100%-32px))] flex-col gap-4 sm:max-w-none">
    <Dialog.Title class="flex items-center gap-2"><Power class="size-4 text-err" />{t('terminate.title')}</Dialog.Title>
    {#if done}
      <p class="m-0">{t('terminate.done')}</p>
      <p class="m-0 font-mono text-xs text-faint">proxypilot start --detach</p>
    {:else}
      <p class="m-0 text-muted-foreground">{t('terminate.body')}</p>
      <ul class="m-0 flex list-disc flex-col gap-1 pl-5">
        {#each ['release', 'agentRules', 'bpRules', 'systemProxy', 'stop'] as k (k)}<li>{t(`terminate.items.${k}`)}</li>{/each}
      </ul>
      <p class="m-0 text-faint">{t('terminate.keeps')}</p>
      <p class="m-0 font-medium">{t('terminate.confirm')}</p>
      {#if error}<div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>{/if}
      <div class="flex justify-end gap-2">
        <Button variant="outline" onclick={onclose}>{t('terminate.cancel')}</Button>
        <Button variant="destructive" class="min-w-32 tabular-nums" disabled={left > 0 || busy} onclick={run}>{left > 0 ? t('terminate.wait', { n: left }) : t('terminate.button')}</Button>
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
