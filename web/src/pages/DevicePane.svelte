<script lang="ts">
  import { onMount } from 'svelte';
  import CodeBlock from '../components/CodeBlock.svelte';
  import PhoneGuide from '../components/PhoneGuide.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { t } from '../lib/i18n';
  import { api, type ClientDevice, type DeviceSetup } from '../lib/api';
  import { formatCount } from '../lib/format';

  interface Props {
    /** Bumped on device.connected / flow events so the list stays live. */
    version: number;
  }
  let { version }: Props = $props();

  let setup = $state<DeviceSetup | null>(null);
  let devices = $state<ClientDevice[]>([]);
  let platform = $state<'ios' | 'android'>('ios');
  let editing = $state<string | null>(null);
  let newName = $state('');

  // "Allow devices on this network": opens the proxy port on all interfaces, live.
  let lanBusy = $state(false);
  let lanError = $state('');
  async function setLAN(on: boolean) {
    lanBusy = true;
    lanError = '';
    try {
      setup = await api.setLAN(on);
    } catch (e) {
      lanError = e instanceof Error ? e.message : String(e);
    } finally {
      lanBusy = false;
    }
  }

  async function load() {
    try {
      devices = (await api.clients()).clients;
    } catch {
      /* banner */
    }
  }
  onMount(() => {
    api.deviceSetup().then((s) => (setup = s), () => {});
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  });
  $effect(() => {
    void version;
    load();
  });

  const ip = $derived(setup?.lan_ips[0] ?? '');
  const tlsClass = (s: ClientDevice['tls']) =>
    s === 'ok' ? 'text-ok bg-ok-bg' : s === 'rejected' ? 'text-err bg-err-bg' : s === 'tunneled' ? 'text-neutral-fg bg-neutral-bg' : 'text-warn bg-warn-bg';
  const cardCls = 'gap-0 overflow-hidden rounded-xl border border-border bg-card py-0 ring-0';
  const warnCls = 'mb-3 flex max-w-[900px] flex-col gap-2 rounded-lg bg-warn-bg px-3 py-2.5 text-warn';

  async function rename(d: ClientDevice) {
    await api.renameClient(d.ip, newName);
    editing = null;
    load();
  }
</script>

<h1 class="mb-1 text-lg font-semibold">{t('setup.device.title')}</h1>
<p class="mb-3.5 max-w-[720px] leading-normal text-muted-foreground">{t('setup.device.body')}</p>

{#if setup}
  <label class="mb-3 flex max-w-[720px] cursor-pointer items-start gap-3 rounded-[10px] border border-border bg-panel px-3.5 py-3">
    <Switch class="mt-0.5" checked={setup.reachable} disabled={lanBusy || setup.lan_fixed} onCheckedChange={setLAN} aria-label={t('setup.device.lan')} />
    <span class="flex min-w-0 flex-col gap-0.5">
      <span class="font-medium text-foreground">{t('setup.device.lan')}</span>
      <span class="text-xs leading-normal text-muted-foreground"
        >{setup.lan_fixed ? t('setup.device.lanFixed', { addr: setup.proxy_addr }) : setup.reachable ? t('setup.device.lanOn', { port: setup.port }) : t('setup.device.lanOff')}</span
      >
      {#if lanError}<span class="text-xs text-err">{lanError}</span>{/if}
    </span>
  </label>
  {#if !setup.reachable}
    <div class={warnCls}>
      <span>{t('setup.device.loopback', { addr: setup.proxy_addr })}</span>
      <CodeBlock lang="bash" code={`proxypilot stop && proxypilot start --detach --proxy-listen 0.0.0.0:${setup.port}`} />
    </div>
  {/if}
  {#if setup.firewall === 'on'}<div class={warnCls}>{t('setup.device.firewall')}</div>{/if}
  {#if !setup.lan_ips.length}<div class={warnCls}>{t('setup.device.noLan')}</div>{/if}

  <div class="layout">
    <Card.Root class={cardCls}>
      <div class="flex flex-wrap gap-5 border-b border-border p-3.5">
        {#if setup.page_url}
          <img class="rounded-lg bg-white p-1.5 [image-rendering:pixelated]" src={api.qrURL(setup.page_url)} alt={setup.page_url} width="200" height="200" />
          <div class="flex flex-col gap-1.5">
            <span class="section-label !p-0">{t('setup.device.scan')}</span>
            <span class="text-muted-foreground">{t('setup.device.or')} <span class="font-mono text-xs">{setup.page_url}</span></span>
            <span class="section-label mt-2.5 !p-0">{t('setup.device.proxyFor')}</span>
            <div class="grid grid-cols-[60px_1fr] items-baseline"><span class="text-muted-foreground">{t('setup.device.server')}</span><span class="font-mono text-base text-foreground">{ip}</span></div>
            <div class="grid grid-cols-[60px_1fr] items-baseline"><span class="text-muted-foreground">{t('setup.device.port')}</span><span class="font-mono text-base text-foreground">{setup.port}</span></div>
            {#if setup.lan_ips.length > 1}<span class="text-faint">{t('setup.device.otherIps', { ips: setup.lan_ips.slice(1).join(', ') })}</span>{/if}
          </div>
        {/if}
      </div>
      <div class="px-3.5 py-3">
        <Tabs.Root bind:value={platform} class="mb-3">
          <Tabs.List>
            {#each ['ios', 'android'] as const as p (p)}
              <Tabs.Trigger value={p}>{t(`setup.device.${p}`)}</Tabs.Trigger>
            {/each}
          </Tabs.List>
        </Tabs.Root>
        <PhoneGuide {platform} {ip} port={setup?.port ?? '9090'} />
      </div>
    </Card.Root>

    <Card.Root class={cardCls}>
      <div class="border-b border-border px-3.5 py-2.5"><strong>{t('setup.device.devices')}</strong></div>
      {#each devices as d (d.ip)}
        <div class="flex flex-col gap-1 border-b border-border px-3.5 py-2.5">
          <div class="flex items-center gap-1.5">
            {#if editing === d.ip}
              <Input class="h-7 w-auto" bind:value={newName} placeholder={d.guess || d.ip} onkeydown={(e) => e.key === 'Enter' && rename(d)} />
              <Button variant="outline" size="sm" onclick={() => rename(d)}>{t('setup.device.save')}</Button>
            {:else}
              <strong>{d.name}</strong>
              {#if d.local}<span class="text-faint">({t('setup.device.local')})</span>{/if}
              <button
                type="button"
                class="border-0 bg-transparent text-[11px] text-muted-foreground hover:text-accent-foreground"
                onclick={() => {
                  editing = d.ip;
                  newName = d.custom ? d.name : '';
                }}>{t('setup.device.rename')}</button
              >
            {/if}
            <span class="flex-1"></span>
            <Badge class={tlsClass(d.tls)}>{t(`setup.device.tls.${d.tls || 'unknown'}`)}</Badge>
          </div>
          <div class="flex items-center gap-1.5 text-faint">
            <span class="font-mono text-xs">{d.ip}</span><span>·</span><span>{t('setup.device.requests', { n: formatCount(d.requests) })}</span>
          </div>
          {#if d.tls === 'rejected'}<div class="leading-[1.4] text-err">{t('setup.device.rejectedHint', { host: d.rejected_host ?? '' })}</div>{/if}
        </div>
      {:else}
        <p class="px-3.5 py-3 text-faint">{t('setup.device.noDevices')}</p>
      {/each}
    </Card.Root>
  </div>
{/if}

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
