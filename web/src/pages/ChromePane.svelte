<script lang="ts">
  import { onMount } from 'svelte';
  import CodeBlock from '../components/CodeBlock.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import { Separator } from '$lib/components/ui/separator/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import { t } from '../lib/i18n';
  import { api, type ChromeStatus } from '../lib/api';
  import { formatCount } from '../lib/format';

  interface Props {
    /** Bumped on chrome.state events. */
    version: number;
  }
  let { version }: Props = $props();

  let st = $state<ChromeStatus | null>(null);
  let startURL = $state('');
  let port = $state('9222');
  let busy = $state(false);
  let error = $state<string | null>(null);

  async function load() {
    try {
      st = await api.chrome();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(() => {
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  });
  $effect(() => {
    void version;
    load();
  });

  async function run(fn: () => Promise<ChromeStatus>) {
    busy = true;
    error = null;
    try {
      st = await fn();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }
  async function toggle(id: string, watch: boolean) {
    await api.chromeWatch(id, watch).catch((e) => (error = String(e)));
    load();
  }
  const cardCls = 'gap-0 overflow-hidden rounded-xl border border-border bg-card py-0 ring-0';
</script>

<h1 class="mb-1 text-lg font-semibold">{t('setup.chrome.title')}</h1>
<p class="mb-3.5 max-w-[720px] leading-normal text-muted-foreground">{t('setup.chrome.body')}</p>

<div class="layout">
  <Card.Root class={cardCls}>
    {#if st?.connected}
      <div class="flex items-center gap-2 border-b border-border px-3.5 py-2.5">
        <Badge class="bg-ok-bg text-ok">●</Badge>
        <strong>{t('setup.chrome.connected', { browser: st.browser ?? 'Chrome' })}</strong>
        <span class="text-faint">{st.launched ? t('setup.chrome.launched') : t('setup.chrome.attached', { endpoint: st.endpoint ?? '' })}</span>
        <span class="flex-1"></span>
        <Button variant="outline" disabled={busy} onclick={() => run(api.chromeDetach)}>{t('setup.chrome.detach')}</Button>
      </div>
      <div class="section-label !px-3.5 !pt-2.5 !pb-1">{t('setup.chrome.tabs')}</div>
      {#each st.tabs as tab (tab.id)}
        <div class="grid grid-cols-[30px_minmax(0,1fr)_auto] items-center gap-2.5 border-t border-border px-3.5 py-2">
          <Switch
            checked={tab.watching}
            aria-label={tab.watching ? t('setup.chrome.watching') : t('setup.chrome.paused')}
            onCheckedChange={() => toggle(tab.id, !tab.watching)}
          />
          <div class="flex min-w-0 flex-col gap-0.5">
            <strong class="truncate">{tab.title || tab.url}</strong>
            <span class="truncate font-mono text-xs text-faint">{tab.url}</span>
          </div>
          <span class="text-faint">{t('setup.chrome.requests', { n: formatCount(tab.requests) })}</span>
        </div>
      {:else}
        <p class="px-3.5 py-3 text-faint">{t('setup.chrome.noTabs')}</p>
      {/each}
    {:else}
      <div class="flex flex-col gap-2.5 px-3.5 py-3">
        <p class="text-muted-foreground">{t('setup.chrome.notConnected')}</p>
        <label class="flex flex-col gap-1.5 text-muted-foreground"
          >{t('setup.chrome.url')}<Input class="font-mono text-xs" bind:value={startURL} placeholder="https://example.com" spellcheck="false" /></label
        >
        <div><Button disabled={busy} onclick={() => run(() => api.chromeLaunch(startURL))}>{t('setup.chrome.launch')}</Button></div>
        <span class="text-faint">{t('setup.chrome.launchHint')}</span>
        <span class="text-faint">{t('setup.chrome.macHint')}</span>
        <Separator class="my-1" />
        <span class="text-muted-foreground">{t('setup.chrome.attachHint')}</span>
        <div class="flex items-center gap-2.5">
          <label class="flex items-center gap-1.5 text-muted-foreground">{t('setup.chrome.port')}<Input class="w-20 font-mono text-xs" bind:value={port} /></label>
          <Button variant="outline" disabled={busy} onclick={() => run(() => api.chromeAttach(port))}>{t('setup.chrome.attach')}</Button>
        </div>
      </div>
    {/if}
    {#if error}<div class="mx-3.5 my-2.5 rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>{/if}
  </Card.Root>
  <Card.Root class="{cardCls} flex flex-col gap-2.5 px-3.5 py-3">
    <span class="text-faint">{t('setup.chrome.cli')}</span>
    <CodeBlock lang="bash" code={'proxypilot chrome launch --url https://example.com\nproxypilot chrome status\nproxypilot chrome detach'} />
  </Card.Root>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 380px;
    gap: 18px;
    align-items: start;
  }
  @media (max-width: 1100px) {
    .layout {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
