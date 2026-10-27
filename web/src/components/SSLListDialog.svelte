<script lang="ts">
  import { onMount } from 'svelte';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import X from '@lucide/svelte/icons/x';
  import { t } from '../lib/i18n';
  import { api, type SSLPolicy } from '../lib/api';

  interface Props {
    ssl: SSLPolicy;
    onchange: (p: SSLPolicy) => void;
    onclose: () => void;
  }
  let { ssl, onchange, onclose }: Props = $props();

  type List = 'allowlist' | 'denylist';
  let draft = $state<Record<List, string>>({ allowlist: '', denylist: '' });
  let error = $state<string | null>(null);

  async function run(fn: () => Promise<SSLPolicy>) {
    error = null;
    try {
      onchange(await fn());
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  function add(list: List) {
    const pattern = draft[list].trim().toLowerCase();
    if (!pattern || ssl[list].includes(pattern)) return;
    draft[list] = '';
    run(() => api.setSSLPolicy({ ...ssl, [list]: [...ssl[list], pattern] }));
  }
  const remove = (list: List, pattern: string) => run(() => api.setSSLPolicy({ ...ssl, [list]: ssl[list].filter((p) => p !== pattern) }));
  // Keep tunneling after a restart: move the learned host to the denylist.
  const keep = (host: string) =>
    run(async () => {
      await api.setSSLPolicy({ ...ssl, denylist: [...ssl.denylist, host] });
      return api.forgetTunnel(host);
    });

  // Learned tunnels change while the page is open; refresh on open.
  onMount(() => void run(api.sslPolicy));

  const learned = $derived(Object.entries(ssl.learned_tunnels ?? {}));
  const rowCls = 'flex items-center gap-2 rounded-md px-2 py-1 font-mono text-xs hover:bg-neutral-bg';
</script>

<Dialog.Root open onOpenChange={(o) => !o && onclose()}>
  <Dialog.Content class="flex max-h-[calc(100%-64px)] w-[min(560px,calc(100%-32px))] flex-col gap-4 overflow-auto sm:max-w-none">
    <Dialog.Title>{t('ssl.title')}</Dialog.Title>
    <p class="-mt-2 leading-normal text-muted-foreground">{t('ssl.body', { mode: t(`toolbar.sslModes.${ssl.mode}`) })}</p>

    {#each ['allowlist', 'denylist'] as const as list (list)}
      <section class="flex flex-col gap-1.5">
        <strong>{t(`ssl.${list}.title`)}</strong>
        <span class="text-faint">{t(`ssl.${list}.hint`)}</span>
        {#each ssl[list] as p (p)}
          <div class={rowCls}>
            <span class="flex-1 truncate">{p}</span>
            <Button variant="ghost" size="icon" class="size-6" aria-label={t('ssl.remove', { pattern: p })} onclick={() => remove(list, p)}><X class="size-3.5" /></Button>
          </div>
        {:else}
          <span class="px-2 text-faint">{t('ssl.empty')}</span>
        {/each}
        <form class="flex gap-2" onsubmit={(e) => (e.preventDefault(), add(list))}>
          <Input class="font-mono text-xs" bind:value={draft[list]} placeholder="api.example.com, *.example.com" spellcheck="false" aria-label={t(`ssl.${list}.title`)} />
          <Button type="submit" variant="outline" disabled={!draft[list].trim()}>{t('ssl.add')}</Button>
        </form>
      </section>
    {/each}

    <section class="flex flex-col gap-1.5">
      <strong>{t('ssl.learned.title')}</strong>
      <span class="text-faint">{t('ssl.learned.hint')}</span>
      {#each learned as [host, reason] (host)}
        <div class={rowCls}>
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="truncate">{host}</span>
            <span class="truncate font-sans text-[11px] text-faint">{reason}</span>
          </span>
          <Button variant="outline" size="sm" onclick={() => keep(host)}>{t('ssl.learned.keep')}</Button>
          <Button variant="ghost" size="sm" onclick={() => run(() => api.forgetTunnel(host))}>{t('ssl.learned.retry')}</Button>
        </div>
      {:else}
        <span class="px-2 text-faint">{t('ssl.empty')}</span>
      {/each}
    </section>

    {#if error}
      <div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
