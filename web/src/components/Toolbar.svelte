<script lang="ts">
  import { t } from '../lib/i18n';
  import type { SSLPolicy, Status } from '../lib/api';
  import { href, type Page } from '../lib/route.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Checkbox } from '$lib/components/ui/checkbox/index.js';
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu/index.js';
  import Pause from '@lucide/svelte/icons/pause';
  import Play from '@lucide/svelte/icons/play';
  import Trash2 from '@lucide/svelte/icons/trash-2';
  import LoaderCircle from '@lucide/svelte/icons/loader-circle';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Bell from '@lucide/svelte/icons/bell';
  import BellRing from '@lucide/svelte/icons/bell-ring';
  import BellOff from '@lucide/svelte/icons/bell-off';
  import { notify, enableNotifications, sendNotification } from '../lib/notify.svelte';
  import Settings2 from '@lucide/svelte/icons/settings-2';
  import ShieldCheck from '@lucide/svelte/icons/shield-check';
  import { api, type ThrottleInfo } from '../lib/api';
  import { cn } from '$lib/utils.js';
  import NetworkSettings from './NetworkSettings.svelte';

  interface Props {
    status: Status | null;
    ssl: SSLPolicy | null;
    theme: 'dark' | 'light';
    /** Current page and paused breakpoints, for the page tabs. */
    page: Page;
    paused?: number;
    ontogglenocaching: () => void;
    onexport: (kind: 'har' | 'session', all: boolean, secrets: boolean) => void;
    onimport: (file: File) => void;
    visibleCount: number;
    onssllist: () => void;
    ontheme: () => void;
  }
  let { status, ssl, theme, page, paused = 0, ontogglenocaching, onexport, onimport, visibleCount, onssllist, ontheme }: Props =
    $props();

  let secrets = $state(false);
  const lanOn = $derived(!!status && !/^(127\.|\[?::1|localhost)/.test(status.proxy_addr));
  const networkOn = $derived(!!status && (!!status.no_caching || !!status.throttle || lanOn));
  const pages: Page[] = ['traffic', 'rules', 'breakpoints', 'setup', 'settings'];
  let importInput = $state<HTMLInputElement>();

</script>


<header class="flex h-[52px] min-w-0 shrink-0 items-center gap-1 border-b border-border px-gutter text-[13px]">
  <a href={href('traffic')} class="mr-4 flex items-center gap-2 font-semibold text-foreground no-underline"><img src="./icon.svg" alt="" class="size-5" />{t('app.name')}</a>
  <nav class="flex min-w-0 items-center gap-0.5 overflow-x-auto" aria-label={t('sidebar.views')}>
    {#each pages as p (p)}
      <a
        href={href(p)}
        aria-current={page === p ? 'page' : undefined}
        class={cn('flex items-center gap-1.5 whitespace-nowrap rounded-md px-3 py-1.5 no-underline', page === p ? 'bg-sel text-foreground' : 'text-muted-foreground hover:text-foreground')}
        >{t(`nav.${p}`)}{#if p === 'breakpoints' && paused}<span class="rounded-full bg-warn px-1.5 text-[11px] font-semibold text-background">{paused}</span>{/if}</a
      >
    {/each}
  </nav>
  <span class="flex-1"></span>

  {#if ssl}
    <Button variant="outline" size="sm" class="mr-1 gap-1.5 text-muted-foreground" title={t('ssl.title')} onclick={onssllist}
      ><ShieldCheck class="size-3.5" />{t('toolbar.scope')}: <span class="font-medium text-foreground">{t('toolbar.decryptHosts', { n: ssl.allowlist.length })}</span></Button
    >
  {/if}
  <!-- Quick network switches; a dot shows when any is on. -->
  <DropdownMenu.Root>
    <DropdownMenu.Trigger>
      {#snippet child({ props })}
        <Button {...props} variant="ghost" size="icon" class="relative text-muted-foreground" aria-label={t('settings.network')} title={t('settings.network')}>
          <Settings2 class="size-4" />
          {#if networkOn}<span class="absolute top-1.5 right-1.5 size-2 rounded-full bg-warn"></span>{/if}
        </Button>
      {/snippet}
    </DropdownMenu.Trigger>
    <DropdownMenu.Content align="end" class="w-[380px] px-4 pt-3 pb-1">
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div onkeydown={(e) => e.stopPropagation()}><NetworkSettings {status} {ontogglenocaching} /></div>
    </DropdownMenu.Content>
  </DropdownMenu.Root>
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
      <DropdownMenu.Item onSelect={() => (location.hash = href('compose'))}>{t('nav.compose')}</DropdownMenu.Item>
      <DropdownMenu.Separator />
      <DropdownMenu.Item onSelect={() => importInput?.click()}>{t('toolbar.import')}…</DropdownMenu.Item>
      <DropdownMenu.Separator />
      {#if notify.permission === 'granted'}
        <DropdownMenu.Item onSelect={() => sendNotification(t('toolbar.notifyTestTitle'), t('toolbar.notifyTestBody'), 'pp-test', true)}><Bell class="size-4" />{t('toolbar.notifyOn')}</DropdownMenu.Item>
      {:else if notify.permission === 'denied'}
        <DropdownMenu.Item onSelect={() => alert(t('toolbar.notifyBlocked'))} class="text-err"><BellOff class="size-4" />{t('toolbar.notifyBlockedShort')}</DropdownMenu.Item>
      {:else}
        <DropdownMenu.Item onSelect={enableNotifications}><BellRing class="size-4" />{t('toolbar.notifyEnable')}</DropdownMenu.Item>
      {/if}
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
