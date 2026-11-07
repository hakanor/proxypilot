<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../lib/i18n';
  import { api, type Status, type ThrottleInfo } from '../lib/api';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { cn } from '$lib/utils.js';

  interface Props {
    status: Status | null;
    ontogglenocaching: () => void;
  }
  let { status, ontogglenocaching }: Props = $props();

  // Network throttle: presets from the server; hosts limit it to some hosts.
  let throttle = $state<ThrottleInfo | null>(null);
  let throttleHosts = $state('');
  async function setThrottle(name: string) {
    const hosts = throttleHosts.split(',').map((h) => h.trim()).filter(Boolean);
    throttle = await api.setThrottle(name === 'off' ? { enabled: false } : { enabled: true, name, hosts }).catch(() => throttle);
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

  onMount(() => {
    api.throttle().then((x) => {
      throttle = x;
      throttleHosts = (x.hosts ?? []).join(', ');
    }, () => {});
    api.deviceSetup().then((d) => (lanFixed = !!d.lan_fixed), () => {});
  });
</script>

{#snippet row(label: string, hint: string, on: boolean, disabled: boolean, toggle: () => void)}
  <label class="flex items-center gap-4 border-t border-border py-3.5">
    <span class="flex min-w-0 flex-1 flex-col gap-0.5">
      <span class="text-foreground">{label}</span>
      <span class="text-xs text-muted-foreground">{hint}</span>
    </span>
    <Switch checked={on} {disabled} onCheckedChange={toggle} aria-label={label} />
  </label>
{/snippet}

<section class="flex flex-col">
  <h2 class="m-0 mb-2 text-xs font-medium text-muted-foreground">{t('settings.network')}</h2>
  {#if status}
    {@render row(t('toolbar.noCaching'), t('toolbar.noCachingHint'), !!status.no_caching, false, ontogglenocaching)}
    {@render row(
      t('setup.device.lan'),
      lanFixed ? t('setup.device.lanFixed', { addr: status.proxy_addr }) : lanOn ? t('setup.device.lanOn', { port }) : t('setup.device.lanOff'),
      lanOn,
      lanFixed || lanBusy,
      toggleLAN,
    )}
    {#if lanError}<p class="m-0 pb-2 text-xs text-err">{lanError}</p>{/if}
    <div class="flex flex-col gap-2 border-t border-border py-3.5">
      <span class="flex flex-col gap-0.5"><span class="text-foreground">{t('toolbar.throttle')}</span><span class="text-xs text-muted-foreground">{t('toolbar.throttleHint')}</span></span>
      <div class="flex flex-wrap gap-1">
        {#each ['off', ...(throttle?.profiles.map((p) => p.name) ?? [])] as name (name)}
          {@const on = name === 'off' ? !status.throttle : status.throttle === name}
          <button
            type="button"
            aria-pressed={on}
            class={cn('h-7 rounded-md border px-2.5 text-xs', on ? 'border-primary bg-accent font-medium text-accent-foreground' : 'border-border text-muted-foreground hover:text-foreground')}
            title={name === 'off' ? '' : profileText(name)}
            onclick={() => setThrottle(name)}>{t(`toolbar.throttleProfiles.${name}`)}</button
          >
        {/each}
      </div>
      <input
        class="h-8 w-full rounded-md border border-input bg-field px-2.5 font-mono text-xs text-foreground outline-none placeholder:text-faint focus:border-primary"
        aria-label={t('toolbar.throttleHosts')}
        placeholder={t('toolbar.throttleHosts')}
        bind:value={throttleHosts}
        onkeydown={(e) => e.key === 'Enter' && status?.throttle && setThrottle(status.throttle)}
        onblur={() => status?.throttle && setThrottle(status.throttle)}
      />
      {#if status.throttle && throttle}<p class="m-0 text-[11px] text-faint">{profileText(status.throttle)}</p>{/if}
    </div>
  {/if}
</section>
