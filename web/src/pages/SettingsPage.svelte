<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../lib/i18n';
  import { api, type Rule } from '../lib/api';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Textarea } from '$lib/components/ui/textarea/index.js';

  let disk = $state(0);
  let hosts = $state('');
  let apps = $state('');
  let blocks = $state<Rule[]>([]);
  let newBlock = $state('');
  let message = $state('');

  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean);
  const fail = (e: unknown) => (message = e instanceof Error ? e.message : String(e));

  // Block rules are ordinary Map Local rules, the same ones the right-click Block item adds.
  async function loadBlocks() {
    const { rules } = await api.rules();
    blocks = rules.filter((r) => r.type === 'map_local' && r.name.startsWith('Block '));
  }
  onMount(() => {
    api.settings().then((s) => {
      disk = s.max_disk_mb;
      hosts = (s.capture.hosts ?? []).join('\n');
      apps = (s.capture.apps ?? []).join('\n');
    }, fail);
    loadBlocks().catch(fail);
  });

  async function save() {
    try {
      await api.setSettings({ max_disk_mb: Math.max(0, Math.floor(Number(disk) || 0)), capture: { hosts: lines(hosts), apps: lines(apps) } });
      message = t('settings.saved');
    } catch (e) {
      fail(e);
    }
  }
  async function addBlock(e: Event) {
    e.preventDefault();
    const host = newBlock.trim();
    if (!host) return;
    try {
      await api.addRule({ name: `Block ${host}`, type: 'map_local', enabled: true, owner: 'user', match: { url: `*://${host}/*` }, spec: { status: 403, body: 'Blocked by ProxyPilot' }, priority: 10 });
      newBlock = '';
      await loadBlocks();
    } catch (err) {
      fail(err);
    }
  }
  async function removeBlock(id: string) {
    try {
      await api.deleteRule(id);
      await loadBlocks();
    } catch (e) {
      fail(e);
    }
  }
</script>

<main class="min-w-0 flex-1 overflow-y-auto p-6 text-[13px]">
  <div class="mx-auto flex max-w-2xl flex-col gap-6">
    <h1 class="m-0 text-lg font-semibold">{t('settings.title')}</h1>

    <section class="flex flex-col gap-2">
      <h2 class="m-0 text-sm font-semibold">{t('settings.disk')}</h2>
      <p class="m-0 text-muted-foreground">{t('settings.diskHint')}</p>
      <label class="flex items-center gap-2"><Input type="number" min="0" class="w-32" aria-label={t('settings.disk')} bind:value={disk} />{t('settings.mb')}</label>
    </section>

    <section class="flex flex-col gap-2">
      <h2 class="m-0 text-sm font-semibold">{t('settings.record')}</h2>
      <p class="m-0 text-muted-foreground">{t('settings.recordHint')}</p>
      <label class="flex flex-col gap-1">{t('settings.hosts')}<Textarea rows={3} spellcheck="false" bind:value={hosts} /></label>
      <label class="flex flex-col gap-1">{t('settings.apps')}<Textarea rows={3} spellcheck="false" bind:value={apps} /></label>
    </section>

    <div class="flex items-center gap-3">
      <Button onclick={save}>{t('settings.save')}</Button>
      {#if message}<span class="text-muted-foreground" role="status">{message}</span>{/if}
    </div>

    <section class="flex flex-col gap-2 border-t border-border pt-5">
      <h2 class="m-0 text-sm font-semibold">{t('settings.block')}</h2>
      <p class="m-0 text-muted-foreground">{t('settings.blockHint')}</p>
      <form class="flex gap-2" onsubmit={addBlock}>
        <Input class="flex-1" spellcheck="false" aria-label={t('settings.block')} placeholder={t('settings.blockPlaceholder')} bind:value={newBlock} />
        <Button type="submit" variant="secondary">{t('settings.add')}</Button>
      </form>
      {#each blocks as r (r.id)}
        <div class="flex items-center gap-2 rounded-md border border-border px-2.5 py-1.5">
          <span class="min-w-0 flex-1 truncate font-mono text-xs" title={r.match.url}>{r.match.url}</span>
          <Button variant="ghost" size="xs" onclick={() => removeBlock(r.id)}>{t('settings.remove')}</Button>
        </div>
      {:else}
        <p class="m-0 text-faint">{t('settings.noBlocks')}</p>
      {/each}
    </section>
  </div>
</main>
