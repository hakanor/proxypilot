<script lang="ts">
  import { onMount } from 'svelte';
  import CodeBlock from '../components/CodeBlock.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import * as Select from '$lib/components/ui/select/index.js';
  import { t } from '../lib/i18n';
  import { api, type AndroidDevice, type AndroidResult } from '../lib/api';

  interface Props {
    onnotice: (msg: string) => void;
  }
  let { onnotice }: Props = $props();

  let adb = $state('');
  let adbError = $state<string | null>(null);
  let devices = $state<AndroidDevice[]>([]);
  let notes = $state<{ network_security_config: string; flutter: string } | null>(null);
  let mode = $state('');
  let caMode = $state('user');
  let busy = $state<string | null>(null);
  let result = $state<AndroidResult | null>(null);
  let error = $state<string | null>(null);

  async function load() {
    try {
      const r = await api.android();
      [adb, adbError, devices, notes] = [r.adb, r.error ?? null, r.devices, r.notes];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(load);

  async function connect(d: AndroidDevice) {
    busy = d.serial;
    error = null;
    try {
      result = await api.androidSetup(d.serial, mode, caMode);
      onnotice(t('setup.android.connected', { serial: d.serial, proxy: result.proxy }));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = null;
      load();
    }
  }
  async function disconnect(d: AndroidDevice) {
    busy = d.serial;
    error = null;
    try {
      result = await api.androidRemove(d.serial);
      onnotice(t('setup.android.removed', { serial: d.serial }));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = null;
      load();
    }
  }
  const cliCmd = $derived(
    `proxypilot setup android${mode ? ` --mode ${mode}` : ''}${caMode !== 'user' ? ` --ca ${caMode}` : ''}\nproxypilot setup android --remove`,
  );
  const modes = ['', 'emulator', 'usb', 'wifi'] as const;
  const caModes = ['user', 'system', 'none'] as const;
  const modeLabel = (m: string) => t(`setup.android.modes.${m || 'auto'}`);
  const cardCls = 'gap-0 overflow-hidden rounded-xl border border-border bg-card py-0 ring-0';
</script>

<h1 class="mb-1 text-lg font-semibold">{t('setup.android.title')}</h1>
<p class="mb-3.5 max-w-[720px] leading-normal text-muted-foreground">{t('setup.android.body')}</p>

{#if adbError}
  <div class="max-w-[900px] rounded-lg bg-warn-bg px-3 py-2.5 text-warn">{t('setup.android.noAdb')}</div>
{:else}
  <div class="layout">
    <Card.Root class={cardCls}>
      <div class="flex items-center gap-2.5 border-b border-border px-3.5 py-2.5">
        <strong>{t('setup.android.device')}</strong>
        <span class="font-mono text-xs text-faint">{adb}</span>
        <span class="flex-1"></span>
        <Button variant="outline" onclick={load}>{t('setup.android.refresh')}</Button>
      </div>
      <div class="flex gap-3 border-b border-border px-3.5 py-2.5">
        <div class="flex flex-col gap-1 text-muted-foreground">
          <span>{t('setup.android.mode')}</span>
          <Select.Root type="single" bind:value={mode}>
            <Select.Trigger class="min-w-36" aria-label={t('setup.android.mode')}>{modeLabel(mode)}</Select.Trigger>
            <Select.Content>
              {#each modes as m (m)}<Select.Item value={m} label={modeLabel(m)}>{modeLabel(m)}</Select.Item>{/each}
            </Select.Content>
          </Select.Root>
        </div>
        <div class="flex flex-col gap-1 text-muted-foreground">
          <span>{t('setup.android.caMode')}</span>
          <Select.Root type="single" bind:value={caMode}>
            <Select.Trigger class="min-w-36" aria-label={t('setup.android.caMode')}>{t(`setup.android.caModes.${caMode}`)}</Select.Trigger>
            <Select.Content>
              {#each caModes as m (m)}<Select.Item value={m} label={t(`setup.android.caModes.${m}`)}>{t(`setup.android.caModes.${m}`)}</Select.Item>{/each}
            </Select.Content>
          </Select.Root>
        </div>
      </div>
      {#each devices as d (d.serial)}
        <div class="flex flex-col gap-1 border-b border-border px-3.5 py-2.5">
          <div class="flex flex-wrap items-center gap-1.5">
            <strong>{d.model || d.serial}</strong>
            <Badge class="bg-neutral-bg text-neutral-fg">{d.emulator ? t('setup.android.emulator') : t('setup.android.physical')}</Badge>
            {#if d.api_level}<span class="text-faint">{t('setup.android.api')} {d.api_level}</span>{/if}
            <span class="flex-1"></span>
            {#if d.state !== 'device'}
              <Badge class="bg-warn-bg text-warn">{d.state}</Badge>
            {:else if d.ours}
              <Button variant="outline" size="sm" disabled={busy !== null} onclick={() => disconnect(d)}>{t('setup.android.remove')}</Button>
            {:else}
              <Button size="sm" disabled={busy !== null} onclick={() => connect(d)}>{t('setup.android.connect')}</Button>
            {/if}
          </div>
          <div class="flex flex-wrap items-center gap-1.5 text-faint">
            <span class="font-mono text-xs">{d.serial}</span>
            <span>·</span>
            <span>{t('setup.android.proxy')}: {d.proxy || t('setup.android.none')}{d.ours ? ` (${t('setup.android.ours')})` : ''}</span>
            {#if d.user_ca_pushed}<span>· {t('setup.android.userCa')}</span>{/if}
            {#if d.system_ca}<span class="text-ok">· {t('setup.android.systemCa')}</span>{/if}
          </div>
          {#if d.state === 'unauthorized'}<div class="text-warn">{t('setup.android.unauthorized')}</div>{/if}
        </div>
      {:else}
        <p class="px-3.5 py-2.5 text-faint">{t('setup.android.noDevices')}</p>
      {/each}
      {#if error}<div class="mx-3.5 my-2.5 rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>{/if}
      {#if result}
        <div class="flex flex-col gap-2 px-3.5 py-2.5">
          {#each result.done as x (x)}<span class="text-ok">✓ {x}</span>{/each}
          {#if result.manual?.length}
            <span class="section-label">{t('setup.android.manual')}</span>
            {#each result.manual as x (x)}<span>→ {x}</span>{/each}
          {/if}
        </div>
      {/if}
      <div class="px-3.5 py-2.5"><span class="text-faint">{t('setup.android.cli')}</span><CodeBlock lang="bash" code={cliCmd} /></div>
    </Card.Root>

    {#if notes}
      <Card.Root class={cardCls}>
        <div class="flex flex-col gap-2 px-3.5 py-2.5">
          <span class="text-muted-foreground">{t('setup.android.devNote')}</span>
          <CodeBlock lang="xml" code={notes.network_security_config} />
          <span class="text-muted-foreground">{t('setup.android.flutter')}</span>
          <CodeBlock lang="dart" code={notes.flutter} />
        </div>
      </Card.Root>
    {/if}
  </div>
{/if}

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 420px;
    gap: 18px;
    align-items: start;
  }
  @media (max-width: 1200px) {
    .layout {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
