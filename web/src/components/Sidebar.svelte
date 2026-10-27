<script lang="ts">
  import { t } from '../lib/i18n';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import PanelLeftClose from '@lucide/svelte/icons/panel-left-close';
  import PanelLeftOpen from '@lucide/svelte/icons/panel-left-open';
  import Laptop from '@lucide/svelte/icons/laptop';
  import Smartphone from '@lucide/svelte/icons/smartphone';
  import TabletSmartphone from '@lucide/svelte/icons/tablet-smartphone';
  import Globe from '@lucide/svelte/icons/globe';
  import AppWindow from '@lucide/svelte/icons/app-window';
  import Link2 from '@lucide/svelte/icons/link-2';
  import WandSparkles from '@lucide/svelte/icons/wand-sparkles';
  import Activity from '@lucide/svelte/icons/activity';
  import Star from '@lucide/svelte/icons/star';
  import Monitor from '@lucide/svelte/icons/monitor';
  import ArrowRightLeft from '@lucide/svelte/icons/arrow-right-left';
  import SquarePen from '@lucide/svelte/icons/square-pen';
  import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
  import CirclePause from '@lucide/svelte/icons/circle-pause';
  import Wrench from '@lucide/svelte/icons/wrench';
  import BookOpen from '@lucide/svelte/icons/book-open';
  import { href, type Page } from '../lib/route.svelte';
  import { formatCount } from '../lib/format';
  import { isError } from '../lib/filter';
  import { appOf, deviceOf, rootDomain, sameView, type View } from '../lib/view';
  import type { Row } from '../lib/traffic.svelte';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { cn } from '$lib/utils.js';

  interface Props {
    rows: Row[];
    pinnedCount: number;
    view: View;
    onview: (v: View) => void;
    /** Current page: the sidebar is the app's navigation on every page. */
    page?: Page;
    /** Paused breakpoints, shown next to Breakpoints. */
    paused?: number;
  }
  let { rows, pinnedCount, view, onview, page = 'traffic', paused = 0 }: Props = $props();

  const nav: { page: Page; icon: typeof ArrowRightLeft }[] = [
    { page: 'traffic', icon: ArrowRightLeft },
    { page: 'compose', icon: SquarePen },
    { page: 'rules', icon: SlidersHorizontal },
    { page: 'breakpoints', icon: CirclePause },
    { page: 'setup', icon: Wrench },
    { page: 'docs', icon: BookOpen },
  ];
  // Picking a device, app or domain from another page jumps back to Traffic.
  function go(v: View) {
    if (page !== 'traffic') location.hash = href('traffic');
    onview(v);
  }
  const active = (v: View) => page === 'traffic' && sameView(view, v);

  // Always open in its own column; it can be collapsed to a rail (remembered per browser).
  const load = () => {
    try {
      return localStorage.getItem('pp.sidebar') === 'collapsed';
    } catch {
      return false;
    }
  };
  let collapsed = $state(load());
  function setCollapsed(v: boolean) {
    collapsed = v;
    try {
      localStorage.setItem('pp.sidebar', v ? 'collapsed' : 'open');
    } catch {
      /* storage blocked */
    }
  }
  let query = $state('');
  let expanded = $state(new Set<string>());

  interface Node {
    key: string;
    label: string;
    count: number;
    err: boolean;
    view: View;
    children: Node[];
    icon: Icon;
    /** App nodes: the app name to fetch a real icon for. */
    app?: string;
  }

  type Icon = 'mac' | 'sim' | 'android' | 'browser' | 'tool' | 'device' | 'app' | 'host' | 'domain';

  function add(map: Map<string, Node>, key: string, label: string, v: View, icon: Icon, r: Row) {
    let n = map.get(key);
    if (!n) {
      n = { key, label, count: 0, err: false, view: v, children: [], icon };
      map.set(key, n);
    }
    n.count++;
    n.err ||= isError(r);
    return n;
  }

  // One pass: device → app → host, and root domain → host.
  const tree = $derived.by(() => {
    const devices = new Map<string, Node>();
    const apps = new Map<string, Map<string, Node>>();
    const appHosts = new Map<string, Map<string, Node>>();
    const domains = new Map<string, Node>();
    const domHosts = new Map<string, Map<string, Node>>();
    for (const r of rows) {
      const d = deviceOf(r), a = appOf(r), dom = rootDomain(r.host);
      add(devices, d, d, { kind: 'tree', device: d }, deviceIcon(d), r);
      const am = apps.get(d) ?? new Map();
      apps.set(d, am);
      add(am, d + '\u0000' + a, a, { kind: 'tree', device: d, app: a }, 'app', r).app = localDevice(d) ? a : undefined;
      const hk = d + '\u0000' + a;
      const hm = appHosts.get(hk) ?? new Map();
      appHosts.set(hk, hm);
      add(hm, hk + '\u0000' + r.host, r.host, { kind: 'tree', device: d, app: a, host: r.host }, 'host', r);
      add(domains, 'dom:' + dom, dom, { kind: 'domain', value: dom }, 'domain', r);
      const dh = domHosts.get(dom) ?? new Map();
      domHosts.set(dom, dh);
      add(dh, 'dom:' + dom + '\u0000' + r.host, r.host, { kind: 'domain', value: dom, host: r.host }, 'host', r);
    }
    const sorted = (m: Map<string, Node>) => [...m.values()].sort((x, y) => y.count - x.count);
    const devNodes = sorted(devices);
    for (const dn of devNodes) {
      dn.children = sorted(apps.get(dn.label) ?? new Map());
      for (const an of dn.children) an.children = sorted(appHosts.get(dn.label + '\u0000' + an.label) ?? new Map());
    }
    const domNodes = sorted(domains);
    for (const dn of domNodes) dn.children = sorted(domHosts.get(dn.label) ?? new Map());
    return { devices: devNodes, domains: domNodes };
  });

  function deviceIcon(d: string): Icon {
    if (d === 'This Mac') return 'mac';
    if (d === 'Chrome') return 'browser';
    if (d.includes('Simulator') || d.startsWith('iPhone') || d.startsWith('iPad')) return 'sim';
    if (d.startsWith('Android') || d.startsWith('Pixel')) return 'android';
    if (d === 'ProxyPilot') return 'tool';
    return 'device';
  }
  // Only apps running on this Mac (or its simulators) have a bundle we can read an icon from.
  const localDevice = (d: string) => d === 'This Mac' || d.includes('Simulator');
  const iconURL = (app: string) => `/api/v1/app-icons/${encodeURIComponent(app)}`;
  let noIcon = $state(new Set<string>());
  function iconFailed(app: string) {
    noIcon = new Set(noIcon).add(app);
  }


  // Search keeps a node when it or any descendant matches, and opens the path.
  const q = $derived(query.trim().toLowerCase());
  function filter(nodes: Node[]): Node[] {
    if (!q) return nodes;
    const out: Node[] = [];
    for (const n of nodes) {
      const kids = filter(n.children);
      if (n.label.toLowerCase().includes(q) || kids.length) out.push({ ...n, children: kids });
    }
    return out;
  }
  const shownDevices = $derived(filter(tree.devices));
  // Proxyman-style flat sections: simulators keep device › app, this Mac lists its apps directly.
  const sims = $derived(shownDevices.filter((d) => d.label.includes('Simulator')));
  const mac = $derived(shownDevices.find((d) => d.label === 'This Mac'));
  const others = $derived(shownDevices.filter((d) => d.label !== 'This Mac' && !d.label.includes('Simulator')));
  const shownDomains = $derived(filter(tree.domains));
  const isOpen = (n: Node) => !!q || expanded.has(n.key);
  function toggle(n: Node) {
    const next = new Set(expanded);
    if (!next.delete(n.key)) next.add(n.key);
    expanded = next;
  }

  const total = $derived(rows.length);
  const railItems = $derived([
    { icon: 'all', label: t('sidebar.allTraffic'), view: { kind: 'all' } as View },
    { icon: 'pinned', label: t('sidebar.pinned'), view: { kind: 'pinned' } as View },
  ]);
</script>

{#snippet nodeIcon(icon: Icon, app: string | undefined, cls = 'size-4')}
  {#if icon === 'app' && app && !noIcon.has(app)}
    <img src={iconURL(app)} alt="" class={cn(cls, 'shrink-0 rounded-[4px]')} onerror={() => iconFailed(app)} />
  {:else if icon === 'mac'}<Laptop class={cls} />
  {:else if icon === 'sim'}<Smartphone class={cls} />
  {:else if icon === 'android'}<TabletSmartphone class={cls} />
  {:else if icon === 'browser'}<Globe class={cls} />
  {:else if icon === 'tool'}<WandSparkles class={cls} />
  {:else if icon === 'device'}<Monitor class={cls} />
  {:else if icon === 'app'}<AppWindow class={cls} />
  {:else if icon === 'domain'}<Globe class={cls} />
  {:else}<Link2 class={cn(cls, 'opacity-70')} />
  {/if}
{/snippet}

{#snippet nodeRow(n: Node, depth: number)}
  <div class="group flex items-center" style="padding-left:{depth * 14}px">
    {#if n.children.length}
      <button type="button" class="flex size-6 shrink-0 items-center justify-center rounded-md border-0 bg-transparent p-0 text-faint hover:bg-hover hover:text-foreground" aria-label={n.label} aria-expanded={isOpen(n)} onclick={() => toggle(n)}
        ><ChevronRight class={cn('size-4 transition-transform', isOpen(n) && 'rotate-90')} /></button
      >
    {:else}
      <span class="w-6 shrink-0"></span>
    {/if}
    <button type="button" class={cn(
        'flex h-7 w-full min-w-0 items-center gap-2 rounded-md border-0 bg-transparent pr-2 pl-1 text-left text-foreground group-hover:bg-hover',
        active(n.view) && 'bg-sel font-medium text-accent-foreground group-hover:bg-sel',
      )} onclick={() => go(n.view)} title={n.label}>
      <span class="flex w-[18px] shrink-0 items-center justify-center text-muted-foreground">{@render nodeIcon(n.icon, n.app, n.icon === 'host' ? 'size-3.5' : 'size-4')}</span><span
        class={cn('flex-1 truncate', n.err && depth > 0 && 'text-err')}>{n.label}</span
      ><span class="text-xs tabular-nums text-faint">{formatCount(n.count)}</span>
    </button>
  </div>
  {#if n.children.length && isOpen(n)}
    {#each n.children as c (c.key)}{@render nodeRow(c, depth + 1)}{/each}
  {/if}
{/snippet}

{#snippet section(label: string)}
  <div class="mt-3 mb-0.5 px-2 text-[11px] font-semibold tracking-[0.04em] text-faint">{label}</div>
{/snippet}

{#snippet flat(label: string, icon: typeof ArrowRightLeft, v: View, count: number)}
  {@const Icon = icon}
  <button type="button" class={cn('flex h-7 w-full min-w-0 items-center gap-2 rounded-md border-0 bg-transparent px-2 text-left text-foreground hover:bg-hover', active(v) && 'bg-sel font-medium text-accent-foreground hover:bg-sel')} onclick={() => go(v)}
    ><span class="flex w-[18px] shrink-0 items-center justify-center text-muted-foreground"><Icon class="size-4" /></span><span class="flex-1 truncate">{label}</span><span class="text-xs tabular-nums text-faint">{formatCount(count)}</span></button
  >
{/snippet}

{#if collapsed}
  <nav class="flex w-[60px] shrink-0 flex-col items-center gap-1.5 border-r border-border bg-panel pt-3" aria-label={t('sidebar.views')}>
    <img src="./icon.svg" alt="" class="mb-2 size-7 shrink-0" />
    <button type="button" class="flex size-9 items-center justify-center rounded-lg border-0 bg-transparent text-muted-foreground hover:bg-hover" title={t('sidebar.expand')} aria-label={t('sidebar.expand')} onclick={() => setCollapsed(false)}><PanelLeftOpen class="size-5" /></button>
    <span class="my-1 w-[22px] border-t border-border"></span>
    {#each nav as n (n.page)}
      {@const Icon = n.icon}
      <a href={href(n.page)} class={cn('relative flex size-9 items-center justify-center rounded-lg text-muted-foreground no-underline hover:bg-hover', page === n.page && 'bg-sel text-accent-foreground')} title={t(`nav.${n.page}`)} aria-label={t(`nav.${n.page}`)}
        ><Icon class="size-[18px]" />{#if n.page === 'breakpoints' && paused}<span class="absolute top-1 right-1 size-2 rounded-full bg-warn"></span>{/if}</a
      >
    {/each}
    <span class="my-1 w-[22px] border-t border-border"></span>
    {#each tree.devices.slice(0, 6) as d (d.key)}
      <button type="button" class={cn('flex size-9 items-center justify-center rounded-lg border-0 bg-transparent text-muted-foreground hover:bg-hover', page === 'traffic' && view.kind === 'tree' && view.device === d.label && 'bg-sel text-accent-foreground')} title={`${d.label} · ${d.count}`} onclick={() => go(d.view)}
        >{@render nodeIcon(d.icon, undefined, 'size-[18px]')}</button
      >
    {/each}
  </nav>
{:else}
  <aside class="flex min-h-0 w-[250px] shrink-0 flex-col border-r border-border bg-panel text-[13px]">
    <div class="flex h-[52px] shrink-0 items-center gap-2.5 pr-2 pl-4">
      <img src="./icon.svg" alt="" class="size-6 shrink-0" /><strong class="flex-1 text-sm font-semibold">{t('app.name')}</strong>
      <Button variant="ghost" size="icon" class="size-8 shrink-0 text-muted-foreground [&_svg:not([class*='size-'])]:size-[18px]" title={t('sidebar.collapse')} aria-label={t('sidebar.collapse')} onclick={() => setCollapsed(true)}><PanelLeftClose /></Button>
    </div>
    <nav class="flex shrink-0 flex-col gap-px px-2.5" aria-label={t('sidebar.views')}>
      {#each nav as n (n.page)}
        {@const Icon = n.icon}
        <a href={href(n.page)} class={cn('flex h-7 items-center gap-2 rounded-md px-2 text-foreground no-underline hover:bg-hover', page === n.page && 'bg-neutral-bg font-semibold')}
          ><span class="flex w-[18px] items-center justify-center text-muted-foreground"><Icon class="size-4" /></span><span class="flex-1">{t(`nav.${n.page}`)}</span>{#if n.page === 'breakpoints' && paused}<span class="rounded-full bg-warn-bg px-1.5 text-[11px] font-semibold text-warn">{paused}</span>{/if}</a
        >
      {/each}
    </nav>
    <div class="mt-2 min-h-0 flex-1 overflow-y-auto border-t border-border px-2.5 pt-1 pb-2.5">
      {@render section(t('sidebar.favorites'))}
      {@render flat(t('sidebar.allTraffic'), ArrowRightLeft, { kind: 'all' }, total)}
      {@render flat(t('sidebar.pinned'), Star, { kind: 'pinned' }, pinnedCount)}
      {#if sims.length}
        {@render section(t('sidebar.simulators'))}
        {#each sims as n (n.key)}{@render nodeRow(n, 0)}{/each}
      {/if}
      {#if mac}
        {@render section(t('sidebar.thisMac'))}
        {#each mac.children as n (n.key)}{@render nodeRow(n, 0)}{/each}
      {/if}
      {#if others.length}
        {@render section(t('sidebar.devices'))}
        {#each others as n (n.key)}{@render nodeRow(n, 0)}{/each}
      {/if}
      {#if shownDomains.length}
        {@render section(t('sidebar.domains'))}
        {#each shownDomains.slice(0, 60) as n (n.key)}{@render nodeRow(n, 0)}{/each}
      {/if}
      {#if q && !shownDevices.length && !shownDomains.length}<p class="m-0 p-2 text-faint">{t('sidebar.noMatch')}</p>{/if}
    </div>
    <div class="flex h-11 shrink-0 items-center border-t border-border px-2.5">
      <Input class="h-7 min-w-0 flex-1 text-xs" bind:value={query} placeholder={t('sidebar.search')} spellcheck="false" />
    </div>
  </aside>
{/if}
