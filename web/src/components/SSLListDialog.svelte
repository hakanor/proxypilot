<script lang="ts">
  import { onMount } from 'svelte';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Checkbox } from '$lib/components/ui/checkbox/index.js';
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
  let drafts = $state<Record<List, string>>({ allowlist: '', denylist: '' });
  let error = $state<string | null>(null);

  async function run(fn: () => Promise<SSLPolicy>) {
    error = null;
    try {
      onchange(await fn());
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  const save = (p: Partial<SSLPolicy>) => run(() => api.setSSLPolicy({ ...ssl, ...p }));
  // Only allowlisted hosts are decrypted; the denylist always wins (SSL pinning, exceptions to a *. pattern).
  function add(list: List) {
    const items = drafts[list].split(/[\s,]+/).map((p) => p.trim().toLowerCase()).filter((p) => p && !ssl[list].includes(p));
    if (!items.length) return;
    drafts[list] = '';
    save({ [list]: [...ssl[list], ...items] });
  }
  const remove = (list: List, pattern: string) => save({ [list]: ssl[list].filter((p) => p !== pattern) });
  // Keep tunneling after a restart: move the learned host to the denylist.
  const keep = (host: string) =>
    run(async () => {
      await api.setSSLPolicy({ ...ssl, denylist: [...ssl.denylist, host] });
      return api.forgetTunnel(host);
    });

  // Learned tunnels and favorite hosts change while the page is open; refresh on open.
  onMount(() => void run(api.sslPolicy));

  const learned = $derived(Object.entries(ssl.learned_tunnels ?? {}));
  const favs = $derived(ssl.favorite_hosts ?? []);
  const rowCls = 'flex items-center gap-2 rounded-md px-2 py-1 font-mono text-xs hover:bg-neutral-bg';
</script>

{#snippet listSection(list: List)}
  <section class="flex flex-col gap-1.5">
    <strong>{t(`ssl.${list}.title`)}</strong>
    <span class="text-faint">{t(`ssl.${list}.hint`)}</span>
    {#each ssl[list] as p (p)}
      <div class={rowCls}>
        <span class="flex-1 truncate">{p}</span>
        <Button variant="ghost" size="icon" class="size-6" aria-label={t('ssl.remove', { pattern: p })} onclick={() => remove(list, p)}><X class="size-3.5" /></Button>
      </div>
    {:else}
      <span class="px-2 text-faint">{t(`ssl.${list}.empty`)}</span>
    {/each}
    <form class="flex gap-2" onsubmit={(e) => (e.preventDefault(), add(list))}>
      <Input class="font-mono text-xs" bind:value={drafts[list]} placeholder="api.example.com, *.example.com" spellcheck="false" aria-label={t(`ssl.${list}.title`)} />
      <Button type="submit" variant="outline" disabled={!drafts[list].trim()}>{t('ssl.add')}</Button>
    </form>
  </section>
{/snippet}

<Dialog.Root open onOpenChange={(o) => !o && onclose()}>
  <Dialog.Content class="flex max-h-[calc(100%-64px)] w-[min(580px,calc(100%-32px))] flex-col gap-4 overflow-auto sm:max-w-none">
    <Dialog.Title>{t('ssl.title')}</Dialog.Title>
    <p class="-mt-2 leading-normal text-muted-foreground">{t('ssl.body')}</p>

    {@render listSection('allowlist')}

    <section class="flex flex-col gap-1.5 rounded-lg border border-border p-3">
      <label class="flex cursor-pointer items-center gap-2 font-medium">
        <Checkbox checked={!!ssl.include_favorites} onCheckedChange={(v) => save({ include_favorites: !!v })} />
        {t('ssl.favorites.title')}
      </label>
      <span class="text-faint">{t('ssl.favorites.hint')}</span>
      {#if ssl.include_favorites}
        <div class="flex flex-wrap gap-1.5">
          {#each favs as h (h)}<span class="rounded-full bg-neutral-bg px-2 py-0.5 font-mono text-[11px] text-neutral-fg">{h}</span>{:else}<span class="text-faint">{t('ssl.favorites.none')}</span>{/each}
        </div>
      {/if}
    </section>

    {@render listSection('denylist')}

    {#if learned.length}
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
        {/each}
      </section>
    {/if}

    {#if error}
      <div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>
    {/if}
  </Dialog.Content>
</Dialog.Root>
