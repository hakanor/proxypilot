<script lang="ts">
  import { onMount } from 'svelte';
  import CodeBlock from '../components/CodeBlock.svelte';
  import DevicePane from './DevicePane.svelte';
  import AndroidPane from './AndroidPane.svelte';
  import ChromePane from './ChromePane.svelte';
  import McpPane from './McpPane.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import { cn } from '$lib/utils.js';
  import { t } from '../lib/i18n';
  import { api, APIError, type CAInfo, type Simulator, type SystemProxy } from '../lib/api';

  interface Props {
    onnotice: (msg: string) => void;
    version?: number;
  }
  let { onnotice, version = 0 }: Props = $props();

  type Target = 'ios' | 'mac' | 'android' | 'device' | 'chrome' | 'cli' | 'mcp';
  // Grouped in the side list: AI agents first, then phones and emulators, then this Mac.
  const groups: { label: string; ids: Target[] }[] = [
    { label: 'agents', ids: ['mcp'] },
    { label: 'mobile', ids: ['device', 'ios', 'android'] },
    { label: 'desktop', ids: ['mac', 'chrome', 'cli'] },
  ];
  let target = $state<Target>('device');

  let sims = $state<Simulator[] | null>(null);
  let simError = $state<string | null>(null);
  let installCmd = $state('');
  let proxy = $state<SystemProxy | null>(null);
  let proxyError = $state<string | null>(null);
  let adminCmds = $state<string[] | null>(null);
  let ca = $state<CAInfo | null>(null);
  let busy = $state<string | null>(null);
  let failures = $state<Record<string, string>>({});

  const booted = $derived(sims?.filter((s) => s.booted) ?? []);

  async function loadSims() {
    try {
      const r = await api.simulators();
      [sims, installCmd, simError] = [r.simulators, r.install_command, null];
    } catch (e) {
      simError = e instanceof Error ? e.message : String(e);
    }
  }
  async function loadProxy() {
    try {
      [proxy, proxyError] = [await api.systemProxy(), null];
    } catch (e) {
      proxyError = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(() => {
    loadSims();
    loadProxy();
    api.ca().then((c) => (ca = c), () => {});
  });

  async function install(udid?: string) {
    busy = udid ?? 'all';
    try {
      const { results } = await api.installCA(udid);
      const next = { ...failures };
      for (const r of results) {
        if (r.ok) {
          delete next[r.udid];
          onnotice(t('setup.installed', { name: r.name }));
        } else next[r.udid] = r.error ?? '';
      }
      failures = next;
      await loadSims();
    } catch (e) {
      simError = e instanceof Error ? e.message : String(e);
    } finally {
      busy = null;
    }
  }

  async function setProxy(on: boolean) {
    busy = 'proxy';
    adminCmds = null;
    try {
      proxy = await api.setSystemProxy(on);
    } catch (e) {
      if (e instanceof APIError && e.code === 'needs_admin') {
        const d = e.body?.details;
        adminCmds = (on ? d?.on_commands : d?.off_commands)?.map((c: string) => 'sudo ' + c) ?? [];
      } else proxyError = e instanceof Error ? e.message : String(e);
    } finally {
      busy = null;
    }
  }

  const tone = {
    ok: 'text-ok bg-ok-bg',
    warn: 'text-warn bg-warn-bg',
    neutral: 'text-neutral-fg bg-neutral-bg',
  } as const;
  const cardCls = 'gap-0 overflow-hidden rounded-xl border border-border bg-card py-0 ring-0';
  const headCls = 'flex items-center gap-2.5 border-b border-border px-3.5 py-2.5';

  const proxyText = (p: { enabled: boolean; server: string; port: number }) => (p.enabled ? `${p.server}:${p.port}` : t('setup.noProxy'));
</script>

<div class="flex min-h-0 flex-1">
  <nav class="w-[200px] shrink-0 border-r border-border bg-panel p-3">
    {#each groups as g, gi (g.label)}
    <div class={cn('section-label', gi > 0 && 'mt-4')}>{t(`setup.targetGroups.${g.label}`)}</div>
    {#each g.ids as id (id)}
      <button
        type="button"
        class={cn(
          'flex w-full justify-between rounded-[5px] px-2.5 py-1.5 text-left hover:bg-neutral-bg',
          target === id && 'bg-accent font-medium text-accent-foreground hover:bg-accent',
        )}
        onclick={() => (target = id)}
      >
        <span>{t(`setup.targetList.${id}`)}</span>
        {#if id === 'ios' && booted.length}<span>{booted.length}</span>{/if}
      </button>
    {/each}
    {/each}
  </nav>

  <main class="min-w-0 flex-1 overflow-auto px-gutter py-5">
    {#if target === 'device'}
      <DevicePane {version} />
    {:else if target === 'android'}
      <AndroidPane {onnotice} />
    {:else if target === 'chrome'}
      <ChromePane {version} />
    {:else if target === 'mcp'}
      <McpPane />
    {:else if target !== 'ios' && target !== 'mac'}
      <h1 class="mb-1 text-lg font-semibold">{t(`setup.targetList.${target}`)}</h1>
      <p class="text-muted-foreground">{t('setup.later')}</p>
    {:else}
      {#if target === 'ios'}
        <h1 class="mb-1 text-lg font-semibold">{t('setup.iosTitle')}</h1>
        <p class="mb-[18px] max-w-[720px] leading-normal text-muted-foreground">{t('setup.iosBody')}</p>
      {:else}
        <h1 class="mb-1 text-lg font-semibold">{t('setup.targetList.mac')}</h1>
      {/if}
      <div class="layout" class:single={target === 'mac'}>
        {#if target === 'ios'}
          <Card.Root class={cardCls}>
            <div class={headCls}>
              <strong>{t('setup.simulators')}</strong>
              {#if sims}<span class="text-faint">{t('setup.simCount', { booted: booted.length, total: sims.length })}</span>{/if}
              <span class="flex-1"></span>
              <Button variant="outline" onclick={loadSims}>{t('setup.refresh')}</Button>
              <Button disabled={!booted.length || busy !== null} onclick={() => install()}>{t('setup.installAll')}</Button>
            </div>
            {#if simError}
              <div class="mx-3.5 my-2.5 rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{simError}</div>
            {:else if sims && sims.length === 0}
              <p class="px-3.5 py-2.5 text-faint">{t('setup.noSims')}</p>
            {:else if sims}
              <div class="row border-b border-border font-medium text-muted-foreground">
                <span>{t('setup.device')}</span><span>{t('setup.runtime')}</span><span>{t('setup.state')}</span><span>{t('setup.ca')}</span><span></span>
              </div>
              <div class="max-h-[420px] overflow-auto">
                {#each sims as s, i (s.udid)}
                  <div class={cn("row", i % 2 === 1 && "bg-[var(--zebra)]")}>
                    <span class="truncate"><span>{s.name}</span> <span class="font-mono text-[10px] text-faint">{s.udid.slice(0, 8)}</span></span>
                    <span class="text-muted-foreground">{s.runtime}</span>
                    <span><Badge class={s.booted ? tone.ok : tone.neutral}>{s.state}</Badge></span>
                    <span>
                      {#if s.ca === 'unknown'}<span class="text-faint">{t('setup.caStates.unknown')}</span>
                      {:else}<Badge class={s.ca === 'installed' ? tone.ok : tone.warn}>{t(`setup.caStates.${s.ca}`)}</Badge>{/if}
                    </span>
                    <span class="text-right">
                      <Button
                        size="sm"
                        variant={s.booted && s.ca !== 'installed' ? 'default' : 'outline'}
                        disabled={!s.booted || busy !== null}
                        title={s.booted ? '' : t('setup.notBooted')}
                        onclick={() => install(s.udid)}>{s.ca === 'installed' ? t('setup.reinstall') : t('setup.install')}</Button
                      >
                    </span>
                    {#if failures[s.udid]}<div class="col-span-full font-mono text-xs whitespace-pre-wrap text-err">{failures[s.udid]}</div>{/if}
                  </div>
                {/each}
              </div>
              <p class="px-3.5 py-2.5 text-faint">{t('setup.simNote')}</p>
            {/if}
            {#if installCmd}
              <div class="px-3.5 py-2.5"><CodeBlock lang="bash" code={`proxypilot setup ios-sim --all-booted\n# or: ${installCmd}`} /></div>
            {/if}
          </Card.Root>
        {/if}

        <div class="flex flex-col gap-[18px]">
          <Card.Root class={cardCls}>
            <div class={headCls}>
              <strong>{t('setup.sysProxy')}</strong>
              <span class="flex-1"></span>
              {#if proxy}<Badge class={proxy.status.enabled ? tone.ok : tone.warn}>{proxy.status.enabled ? t('setup.on') : t('setup.off')}</Badge>{/if}
            </div>
            <div class="flex flex-col gap-2.5 px-3.5 py-2.5">
              {#if proxyError}
                <div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{proxyError}</div>
              {:else if proxy}
                <p class="leading-normal text-muted-foreground">{t('setup.sysProxyBody', { target: proxy.status.target })}</p>
                <div class="font-mono text-xs leading-[1.7] text-muted-foreground">
                  {#each proxy.status.services as s (s.name)}
                    <div>{s.name} · {proxyText(s.web)} / {proxyText(s.secure)}</div>
                  {/each}
                </div>
                {#if proxy.status.managed && proxy.status.backup_at}
                  <span class="text-faint">{t('setup.managed', { time: new Date(proxy.status.backup_at).toLocaleString() })}</span>
                {/if}
                <div class="flex gap-2">
                  {#if proxy.status.enabled || proxy.status.managed}
                    <Button variant="outline" disabled={busy !== null || !proxy.status.managed} onclick={() => setProxy(false)}>{t('setup.turnOff')}</Button>
                  {/if}
                  {#if !proxy.status.enabled}
                    <Button disabled={busy !== null} onclick={() => setProxy(true)}>{t('setup.turnOn')}</Button>
                  {/if}
                </div>
                {#if adminCmds}
                  <p class="leading-normal text-warn">{t('setup.needsAdmin')}</p>
                  <CodeBlock lang="bash" code={adminCmds.join('\n')} />
                {/if}
                <span class="text-faint">{t('setup.cmdTitle')}</span>
                <CodeBlock lang="bash" code={proxy.status.enabled ? 'proxypilot system-proxy off' : 'proxypilot system-proxy on'} />
                <span class="text-faint">{t('setup.restoreNote')}</span>
              {/if}
            </div>
          </Card.Root>

          {#if ca}
            <Card.Root class={cardCls}>
              <div class={headCls}><strong>{t('setup.caTitle')}</strong></div>
              <div class="flex flex-col gap-2.5 px-3.5 py-2.5">
                <span class="font-mono text-xs break-all text-muted-foreground">{t('setup.fingerprint', { fp: ca.fingerprint_sha256 })}</span>
                <span class="flex items-center gap-2">
                  <Badge class={ca.trust === 'trusted' ? tone.ok : ca.trust === 'not_trusted' ? tone.warn : tone.neutral}>{t(`setup.trustStates.${ca.trust}`)}</Badge>
                  <span class="text-muted-foreground">{t('setup.onThisMac')}</span>
                </span>
                {#if ca.trust !== 'trusted'}
                  <span class="text-faint">{t('setup.trustCmd')}</span>
                  <CodeBlock lang="bash" code={ca.trust_command} />
                {/if}
              </div>
            </Card.Root>
          {/if}
        </div>
      </div>
    {/if}
  </main>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 380px;
    gap: 18px;
    align-items: start;
  }
  .layout.single {
    grid-template-columns: minmax(0, 520px);
    margin-top: 12px;
  }
  @media (max-width: 1100px) {
    .layout {
      grid-template-columns: minmax(0, 1fr);
    }
  }
  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 100px 90px 120px 110px;
    align-items: center;
    padding: 7px 14px;
    gap: 6px;
  }
</style>
