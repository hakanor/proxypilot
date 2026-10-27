<script lang="ts">
  import { t } from '../lib/i18n';
  import type { SSLPolicy, Status } from '../lib/api';
  import { href } from '../lib/route.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Checkbox } from '$lib/components/ui/checkbox/index.js';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
  import Pause from '@lucide/svelte/icons/pause';
  import Play from '@lucide/svelte/icons/play';
  import Send from '@lucide/svelte/icons/send';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Bell from '@lucide/svelte/icons/bell';
  import BellRing from '@lucide/svelte/icons/bell-ring';
  import BellOff from '@lucide/svelte/icons/bell-off';
  import { notify, enableNotifications, sendNotification } from '../lib/notify.svelte';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import Settings2 from '@lucide/svelte/icons/settings-2';
  import { api, type ThrottleInfo } from '../lib/api';
  import { cn } from '$lib/utils.js';

  interface Props {
    status: Status | null;
    ssl: SSLPolicy | null;
    theme: 'dark' | 'light';
    onrecord: () => void;
    onclear: () => void;
    /** The server is still deleting: the trash icon becomes a spinner. */
    clearing?: boolean;
    sysProxy: boolean | null;
    ontogglesysproxy: () => void;
    ontogglenocaching: () => void;
    onexport: (kind: 'har' | 'session', all: boolean, secrets: boolean) => void;
    onimport: (file: File) => void;
    visibleCount: number;
    onssl: (mode: SSLPolicy['mode']) => void;
    ontheme: () => void;
  }
  let { status, ssl, theme, onrecord, onclear, clearing = false, sysProxy, ontogglesysproxy, ontogglenocaching, onexport, onimport, visibleCount, onssl, ontheme }: Props =
    $props();

  let secrets = $state(false);
  const recording = $derived(status?.recording ?? true);
  const running = $derived(status?.running ?? false);
  const sslModes: SSLPolicy['mode'][] = ['all', 'allowlist', 'off'];
  let importInput = $state<HTMLInputElement>();

  // Network throttle: presets from the server; hosts limit it to some hosts.
  let throttle = $state<ThrottleInfo | null>(null);
  let throttleHosts = $state('');
  function loadThrottle() {
    api.throttle().then((t) => {
      throttle = t;
      throttleHosts = (t.hosts ?? []).join(', ');
    }, () => {});
  }
  async function setThrottle(name: string) {
    const hosts = throttleHosts.split(',').map((h) => h.trim()).filter(Boolean);
    throttle = await api.setThrottle(name === 'off' ? { enabled: false } : { enabled: true, name, hosts }).catch(() => throttle);
    // the proxy.state event refreshes the status (badge)
  }
  function profileText(name: string): string {
    const p = throttle?.profiles.find((x) => x.name === name);
    if (!p) return '';
    const bw = (k: number) => (k >= 1000 ? `${k / 1000} Mbit/s` : `${k} kbit/s`);
    return t('toolbar.throttleSpec', { down: bw(p.down_kbps), up: bw(p.up_kbps), ms: p.latency_ms });
  }

  // "Allow devices on this network" (also on Setup → Physical device).
  const port = $derived(status?.proxy_addr.split(':').pop() ?? '9090');
  const lanOn = $derived(!!status && !/^(127\.|\[?::1|localhost)/.test(status.proxy_addr));
  let lanFixed = $state(false);
  let lanBusy = $state(false);
  let lanError = $state('');
  function loadLAN() {
    api.deviceSetup().then((d) => (lanFixed = !!d.lan_fixed), () => {});
  }
  async function toggleLAN() {
    lanBusy = true;
    lanError = '';
    try {
      await api.setLAN(!lanOn); // the proxy.state event refreshes the status
    } catch (e) {
      lanError = e instanceof Error ? e.message : String(e);
    } finally {
      lanBusy = false;
    }
  }
</script>

{#snippet row(label: string, hint: string, on: boolean, disabled: boolean, toggle: () => void)}
  <DropdownMenu.Item
    {disabled}
    closeOnSelect={false}
    onSelect={toggle}
    class="flex items-start gap-3 rounded-lg px-2.5 py-2"
  >
    <span class="flex min-w-0 flex-1 flex-col gap-0.5">
      <span class="font-medium text-foreground">{label}</span>
      <span class="text-xs leading-snug whitespace-normal text-muted-foreground">{hint}</span>
    </span>
    <Switch class="pointer-events-none mt-0.5" checked={on} tabindex={-1} aria-hidden="true" />
  </DropdownMenu.Item>
{/snippet}

<header class="flex h-[52px] min-w-0 shrink-0 items-center gap-1 border-b border-border px-gutter text-[13px]">
  <Button
    variant="ghost"
    size="icon"
    class="text-muted-foreground"
    aria-label={recording ? t('toolbar.pauseRecording') : t('toolbar.resumeRecording')}
    title={recording ? t('toolbar.pauseRecording') : t('toolbar.resumeRecording')}
    onclick={onrecord}
  >
    {#if recording}<Pause class="size-4" />{:else}<Play class="size-4 text-ok" />{/if}
  </Button>
  <Button variant="ghost" size="icon" class="text-muted-foreground" href={href('compose')} aria-label={t('toolbar.compose')} title={t('toolbar.compose')}>
    <Send class="size-4" />
  </Button>
  <Button variant="ghost" size="icon" class="text-muted-foreground" aria-label={t('toolbar.clear')} title={t('toolbar.clearHint')} onclick={onclear}>
    {#if clearing}<LoaderCircle class="size-4 animate-spin" />{:else}<Trash2 class="size-4" />{/if}
  </Button>

  <!-- The address pill opens the proxy's own switches, so the toolbar stays short. -->
  <span class="flex min-w-0 flex-1 justify-center">
    <DropdownMenu.Root onOpenChange={(o) => o && (loadLAN(), loadThrottle())}>
      <DropdownMenu.Trigger>
        {#snippet child({ props })}
          <button
            {...props}
            type="button"
            class="inline-flex h-7 min-w-0 items-center gap-2 truncate rounded-full bg-field px-3 text-xs text-muted-foreground hover:text-foreground"
            title={status ? t('toolbar.proxyUp', { addr: status.proxy_addr }) : t('toolbar.notRunning')}
          >
            <span class={cn('size-2 shrink-0 rounded-full', running ? 'bg-primary' : 'bg-faint')}></span>
            {#if status}{t('toolbar.listening', { addr: status.proxy_addr })}{:else}{t('toolbar.notRunning')}{/if}
            {#if lanOn}<span class="rounded bg-warn-bg px-1.5 text-[10.5px] font-medium text-warn">{t('toolbar.badgeLan')}</span>{/if}
            {#if status?.throttle}<span class="rounded bg-c4-bg px-1.5 text-[10.5px] font-medium text-c4">{t(`toolbar.throttleProfiles.${status.throttle}`)}</span>{/if}
            {#if status?.no_caching}<span class="rounded bg-accent px-1.5 text-[10.5px] font-medium text-accent-foreground">{t('toolbar.badgeNoCache')}</span>{/if}
            <span class="ml-0.5 flex items-center gap-0.5 border-l border-border pl-2 text-muted-foreground"><Settings2 class="size-3.5" /><ChevronDown class="size-3" /></span>
          </button>
        {/snippet}
      </DropdownMenu.Trigger>
      <DropdownMenu.Content align="center" class="w-[340px] p-1.5">
        {#if status}
          {@render row(t('toolbar.noCaching'), t('toolbar.noCachingHint'), !!status.no_caching, false, ontogglenocaching)}
          {@render row(
            t('setup.device.lan'),
            lanFixed ? t('setup.device.lanFixed', { addr: status.proxy_addr }) : lanOn ? t('setup.device.lanOn', { port: port }) : t('setup.device.lanOff'),
            lanOn,
            lanFixed || lanBusy,
            toggleLAN,
          )}
          {#if lanError}<p class="m-0 px-2.5 pb-1 text-xs text-err">{lanError}</p>{/if}
          <DropdownMenu.Separator />
          <div class="px-2.5 pt-1.5 pb-1">
            <div class="font-medium text-foreground">{t('toolbar.throttle')}</div>
            <div class="text-xs leading-snug text-muted-foreground">{t('toolbar.throttleHint')}</div>
          </div>
          <div class="flex flex-wrap gap-1 px-2.5 pb-2">
            {#each ['off', ...(throttle?.profiles.map((p) => p.name) ?? [])] as name (name)}
              {@const on = name === 'off' ? !status.throttle : status.throttle === name}
              <button
                type="button"
                class={cn('h-7 rounded-md border px-2.5 text-xs', on ? 'border-primary bg-accent font-medium text-accent-foreground' : 'border-border text-muted-foreground hover:text-foreground')}
                title={name === 'off' ? '' : profileText(name)}
                onclick={() => setThrottle(name)}>{t(`toolbar.throttleProfiles.${name}`)}</button
              >
            {/each}
          </div>
          <div class="px-2.5 pb-2">
            <input
              class="h-8 w-full rounded-md border border-input bg-field px-2.5 font-mono text-xs text-foreground outline-none placeholder:text-faint focus:border-primary"
              placeholder={t('toolbar.throttleHosts')}
              bind:value={throttleHosts}
              onkeydown={(e) => {
                e.stopPropagation(); // keep typing inside the menu
                if (e.key === 'Enter' && status?.throttle) setThrottle(status.throttle);
              }}
              onblur={() => status?.throttle && setThrottle(status.throttle)}
            />
            {#if status.throttle && throttle}<p class="m-0 mt-1 text-[11px] text-faint">{profileText(status.throttle)}</p>{/if}
          </div>
        {/if}
      </DropdownMenu.Content>
    </DropdownMenu.Root>
  </span>

  {#if sysProxy !== null}
    <label class="mr-1 inline-flex cursor-pointer items-center gap-2 whitespace-nowrap text-muted-foreground">
      {t('toolbar.systemProxy')}
      <Switch checked={sysProxy} onCheckedChange={() => ontogglesysproxy()} aria-label={t('toolbar.systemProxy')} />
    </label>
  {/if}
  {#if notify.permission === 'granted'}
    <Button variant="ghost" size="icon" class="text-muted-foreground" aria-label={t('toolbar.notifyOn')} title={t('toolbar.notifyOn')} onclick={() => sendNotification(t('toolbar.notifyTestTitle'), t('toolbar.notifyTestBody'), 'pp-test', true)}><Bell class="size-4" /></Button>
  {:else if notify.permission === 'denied'}
    <Button variant="ghost" size="icon" class="relative text-err" aria-label={t('toolbar.notifyBlocked')} title={t('toolbar.notifyBlocked')} onclick={() => alert(t('toolbar.notifyBlocked'))}>
      <BellOff class="size-4" /><span class="absolute top-1.5 right-1.5 size-2 rounded-full bg-err"></span>
    </Button>
  {:else}
    <Button variant="outline" size="sm" class="animate-pulse border-warn text-warn hover:text-warn" title={t('toolbar.notifyAsk')} onclick={enableNotifications}>
      <BellRing class="size-4" />{t('toolbar.notifyEnable')}
    </Button>
  {/if}
  <DropdownMenu.Root>
    <DropdownMenu.Trigger>
      {#snippet child({ props })}
        <Button {...props} variant="ghost" size="icon" class="text-muted-foreground" aria-label={t('toolbar.more')} title={t('toolbar.more')}>
          <Ellipsis class="size-4" />
        </Button>
      {/snippet}
    </DropdownMenu.Trigger>
    <DropdownMenu.Content align="end" class="min-w-[250px]">
      <DropdownMenu.Sub>
        <DropdownMenu.SubTrigger>{t('toolbar.export')}</DropdownMenu.SubTrigger>
        <DropdownMenu.SubContent class="min-w-[250px]">
          <DropdownMenu.Item onSelect={() => onexport('har', false, secrets)}>{t('toolbar.exportHar', { n: visibleCount })}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => onexport('har', true, secrets)}>{t('toolbar.exportHarAll')}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => onexport('session', false, secrets)}>{t('toolbar.exportSession', { n: visibleCount })}</DropdownMenu.Item>
          <DropdownMenu.Item onSelect={() => onexport('session', true, secrets)}>{t('toolbar.exportSessionAll')}</DropdownMenu.Item>
          <DropdownMenu.Separator />
          <label class="flex items-center gap-1.5 px-2 pb-0.5 pt-1.5 text-muted-foreground">
            <Checkbox bind:checked={secrets} /> {t('toolbar.includeSecrets')}
          </label>
          {#if !secrets}<span class="block px-2 pb-1 pt-0.5 text-[11px] text-faint">{t('toolbar.exportRedacted')}</span>{/if}
        </DropdownMenu.SubContent>
      </DropdownMenu.Sub>
      <DropdownMenu.Item onSelect={() => importInput?.click()}>{t('toolbar.import')}…</DropdownMenu.Item>
      {#if ssl}
        <DropdownMenu.Separator />
        <DropdownMenu.Label class="text-xs text-faint">{t('toolbar.ssl')}</DropdownMenu.Label>
        <DropdownMenu.RadioGroup value={ssl.mode} onValueChange={(v) => onssl(v as SSLPolicy['mode'])}>
          {#each sslModes as m (m)}
            <DropdownMenu.RadioItem value={m}>{t(`toolbar.sslModes.${m}`)}</DropdownMenu.RadioItem>
          {/each}
        </DropdownMenu.RadioGroup>
      {/if}
      <DropdownMenu.Separator />
      <DropdownMenu.Item onSelect={ontheme}>{theme === 'dark' ? t('toolbar.themeLight') : t('toolbar.themeDark')}</DropdownMenu.Item>
      <DropdownMenu.Item onSelect={() => (location.hash = href('docs'))}>{t('toolbar.docs')}</DropdownMenu.Item>
    </DropdownMenu.Content>
  </DropdownMenu.Root>
  <input
    bind:this={importInput}
    type="file"
    accept=".har,.proxypilot,application/json,application/zip"
    hidden
    onchange={(e) => {
      const f = (e.currentTarget as HTMLInputElement).files?.[0];
      if (f) onimport(f);
      (e.currentTarget as HTMLInputElement).value = '';
    }}
  />
</header>
