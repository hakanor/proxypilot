<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import Toolbar from './components/Toolbar.svelte';
  import Sidebar from './components/Sidebar.svelte';
  import TrafficTable from './components/TrafficTable.svelte';
  import DetailPanel from './components/DetailPanel.svelte';
  import BottomBar from './components/BottomBar.svelte';
  import TypeStrip from './components/TypeStrip.svelte';
  import Banner from './components/Banner.svelte';
  import { t } from './lib/i18n';
  import { api, type Rule, type SSLPolicy, type Status } from './lib/api';
  import { Stream, type StreamState } from './lib/stream';
  import { Traffic } from './lib/traffic.svelte';
  import { compileFilter, parseQuery, typeMatcher, type TypeFilter } from './lib/filter';
  import { viewMatcher, type View } from './lib/view';
  import { formatCount } from './lib/format';
  import { route, href } from './lib/route.svelte';
  import ContextMenu from './components/ContextMenu.svelte';
  import type { Item } from './lib/menu';
  import { nextSort, sortRows, type SortKey } from './lib/sort';
  import { copyText } from './lib/detail';
  import SetupPage from './pages/SetupPage.svelte';
  import RulesPage from './pages/RulesPage.svelte';
  import { sendNotification } from './lib/notify.svelte';
  import BreakpointsPage from './pages/BreakpointsPage.svelte';
  import ComposePage from './pages/ComposePage.svelte';
  import DocsPage from './pages/DocsPage.svelte';
  import UninstallPage from './pages/UninstallPage.svelte';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import SSLListDialog from './components/SSLListDialog.svelte';
  import DiffDialog from './components/DiffDialog.svelte';
  import { Button } from '$lib/components/ui/button/index.js';

  const store = (k: string, v?: string) => {
    try {
      if (v === undefined) return localStorage.getItem(k);
      localStorage.setItem(k, v);
    } catch {
      /* storage blocked */
    }
    return null;
  };

  const traffic = new Traffic();
  let status = $state<Status | null>(null);
  let ssl = $state<SSLPolicy | null>(null);
  let sslListOpen = $state(false);
  // Compare: the first request picked from the context menu, then the open dialog.
  let compareBase = $state<string | null>(null);
  let compare = $state<{ a: string; b: string; labels: [string, string] } | null>(null);
  let conn = $state<StreamState>({ kind: 'connecting' });
  let notice = $state<string | null>(null);
  let ruleVersion = $state(0);
  let bpVersion = $state(0);
  let ruleModal = $state<string | null>(null); // rule id or new~type~url, edited in a pop-up
  let bpModal = $state<string | null>(null); // paused flow shown in the pop-up
  const seenPauses = new Set<string>(); // breakpoint.paused repeats on extend/take-over
  let deviceVersion = $state(0);

  let query = $state('');
  let errorsOnly = $state(false);
  let autoScroll = $state(true);
  let typeFilter = $state<TypeFilter>('all');
  let ruleCounts = $state<Partial<Record<Rule['type'], number>>>({});
  let detailH = $state(Number(store('pp.detailH')) || 330);
  const clampH = (h: number) => Math.round(Math.max(160, Math.min(window.innerHeight * 0.75, h)));

  function startResize(e: PointerEvent) {
    e.preventDefault();
    const y0 = e.clientY;
    const h0 = detailH;
    const onmove = (m: PointerEvent) => (detailH = clampH(h0 + (y0 - m.clientY)));
    const onup = () => {
      window.removeEventListener('pointermove', onmove);
      window.removeEventListener('pointerup', onup);
      store('pp.detailH', String(detailH));
    };
    window.addEventListener('pointermove', onmove);
    window.addEventListener('pointerup', onup);
  }

  async function refreshRules() {
    try {
      const counts: Partial<Record<Rule['type'], number>> = {};
      for (const r of (await api.rules()).rules) if (r.enabled) counts[r.type] = (counts[r.type] ?? 0) + 1;
      ruleCounts = counts;
    } catch {
      /* banner covers it */
    }
  }
  let view = $state<View>({ kind: 'all' });
  let selected = $state<string | null>(null);
  let detailOpen = $state(false);
  let pinned = $state(new Set<string>(JSON.parse(store('pp.pinned') ?? '[]')));
  let theme = $state<'dark' | 'light'>(
    (store('pp.theme') as 'dark' | 'light' | null) ??
      (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'),
  );
  let filterInput = $state<HTMLInputElement>();
  let table = $state<ReturnType<typeof TrafficTable>>();

  $effect(() => {
    document.documentElement.dataset.theme = theme;
  });
  // #/flow/<id> selects a flow and opens its detail (Composer results, notifications).
  $effect(() => {
    const { page, sub } = route.current;
    if (page === 'traffic' && sub && traffic.get(sub)) {
      untrack(() => {
        selected = sub;
        detailOpen = true;
        autoScroll = false;
        table?.reveal(sub);
      });
    }
  });
  // Paused flows show in the tab title so they are noticed from other tabs.
  $effect(() => {
    const n = status?.pending_breakpoints ?? 0;
    document.title = n > 0 ? `(${n}) ${t('app.name')}` : t('app.name');
  });

  let sort = $state<{ key: SortKey; dir: 1 | -1 } | null>(null);
  let menu = $state<{ id: string; x: number; y: number } | null>(null);
  let sysProxy = $state<boolean | null>(null);

  let visible = $derived.by(() => {
    const { chips, text } = parseQuery(query);
    const f = compileFilter(chips, text, errorsOnly);
    const v = viewMatcher(view, pinned);
    const tf = typeMatcher(typeFilter);
    const rows = traffic.rows.filter((r) => v(r) && tf(r) && f(r));
    return sort ? sortRows(rows, sort.key, sort.dir) : rows;
  });
  const selectedRow = $derived(selected ? traffic.get(selected) : undefined);
  const filtering = $derived(query.trim() !== '' || errorsOnly || view.kind !== 'all' || typeFilter !== 'all');

  async function refreshSysProxy() {
    try {
      sysProxy = (await api.systemProxy()).status.enabled;
    } catch {
      sysProxy = null; // not macOS
    }
  }

  async function toggleSysProxy() {
    try {
      sysProxy = (await api.setSystemProxy(!sysProxy)).status.enabled;
      // Turning the proxy on means "capture": make sure recording is running.
      if (sysProxy && status && !status.recording) {
        const r = await api.setRecording(true);
        status = { ...status, recording: r.recording };
      }
    } catch (e) {
      const d = (e as { body?: { details?: { on_commands?: string[]; off_commands?: string[] } } }).body?.details;
      const cmds = (sysProxy ? d?.off_commands : d?.on_commands) ?? [];
      notice = cmds.length ? t('toolbar.systemProxyAdmin', { cmd: 'sudo ' + cmds[0] + (cmds.length > 1 ? ' …' : '') }) : String(e);
    }
  }

  // Toolbar "No caching": flips at once, the server confirms.
  async function toggleNoCaching() {
    if (!status) return;
    const on = !status.no_caching;
    status = { ...status, no_caching: on };
    try {
      await api.setNoCaching(on);
      notice = t(on ? 'toolbar.noCachingOn' : 'toolbar.noCachingOff');
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    }
    refreshStatus();
  }

  async function refreshStatus() {
    try {
      const prev = status?.active_rules;
      status = await api.status();
      if (status.max_entries > 0) traffic.maxRows = status.max_entries;
      if (prev !== status.active_rules) refreshRules();
    } catch {
      /* banner covers it */
    }
  }

  const stream = new Stream({
    onOpen: (reconnect) => {
      traffic.load(!reconnect).catch(() => {});
      refreshStatus();
      api.sslPolicy().then((p) => (ssl = p), () => {});
    },
    onBatch: (events) => {
      traffic.apply(events);
      for (const e of events) {
        if (e.type === 'recording.changed' && status) status = { ...status, recording: e.data?.recording };
        if (e.type === 'device.connected' || e.type === 'chrome.state') deviceVersion++;
        if (e.type === 'proxy.state') refreshStatus(); // e.g. network access switched in Setup
        if (e.type.startsWith('breakpoint.')) {
          bpVersion++;
          refreshStatus();
        }
        // A request just stopped at one of your breakpoints: bring the editor up with it.
        if (e.type === 'breakpoint.paused' && e.data?.id && !seenPauses.has(e.data.id)) {
          seenPauses.add(e.data.id);
          if (!String(e.data.owner ?? '').startsWith('agent:')) {
            const req = e.data.request ?? {};
            sendNotification(t('bp.notifyTitle', { method: req.method ?? '', path: String(req.url ?? '').replace(/^[a-z]+:\/\/[^/]+/i, '') }), t('bp.notifyShort'), e.data.id);
            if (route.current.page !== 'breakpoints') bpModal = e.data.id;
            window.focus();
          }
        }
        if (e.type === 'rule.changed') {
          ruleVersion++;
          refreshStatus();
          refreshRules();
        }
      }
    },
    onDropped: (count) => {
      notice = t('banner.dropped', { count: formatCount(count) });
      traffic.load(false).catch(() => {});
    },
    onState: (s) => (conn = s),
  });

  onMount(() => {
    stream.start();
    refreshSysProxy();
    // A request paused before the page loaded still gets the pop-up.
    api
      .breakpoints()
      .then(({ breakpoints }) => {
        const mine = breakpoints.filter((p) => !p.owner.startsWith('agent:'));
        mine.forEach((p) => seenPauses.add(p.id));
        if (mine.length && route.current.page !== 'breakpoints') bpModal = mine[0].id;
      })
      .catch(() => {});
    const id = setInterval(() => {
      refreshStatus();
      refreshSysProxy();
    }, 5000);
    return () => {
      clearInterval(id);
      stream.stop();
    };
  });

  async function toggleRecording() {
    if (!status) return;
    const r = await api.setRecording(!status.recording);
    status = { ...status, recording: r.recording };
  }

  // Clear the screen at once; the server-side delete finishes in the background
  // (the trash icon spins until it does).
  let clearing = $state(false);
  async function clearAll() {
    traffic.clear();
    selected = null;
    detailOpen = false;
    if (clearing) return;
    clearing = true;
    try {
      await api.clear();
    } catch (e) {
      notice = String(e);
    } finally {
      clearing = false;
    }
  }

  async function exportFlows(kind: 'har' | 'session', all: boolean, secrets: boolean) {
    try {
      await api.exportFile(kind, all ? null : visible.map((r) => r.id).slice(-20000), !secrets);
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    }
  }

  async function importFile(file: File) {
    try {
      const r = await api.importFile(file, (p) => (notice = t('toolbar.importing', { pct: Math.round(p * 100) })));
      notice = t('toolbar.imported', { n: r.imported });
      await traffic.load(false);
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    }
  }

  async function setSSL(mode: SSLPolicy['mode']) {
    if (!ssl) return;
    ssl = await api.setSSLPolicy({ ...ssl, mode });
  }

  async function addBypass(host: string) {
    const p = ssl ?? (await api.sslPolicy());
    if (p.denylist.includes(host)) return;
    ssl = await api.setSSLPolicy({ ...p, denylist: [...p.denylist, host] });
    notice = t('error.fixBypassDone', { host });
  }

  // Right-click Tools: act on one URL directly (Proxyman-style).
  async function act(fn: () => Promise<string | void>) {
    try {
      const msg = await fn();
      if (msg) notice = msg;
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    }
  }
  function menuItems(id: string): Item[] {
    const r = traffic.get(id);
    if (!r) return [];
    const base = r.url.split('?')[0];
    const pattern = base + '*';
    const name = `${r.method} ${r.host}${r.path}`;
    const tunnel = r.state === 'tunneled' || r.method === 'CONNECT';
    const bp = (phase: 'request' | 'response') =>
      act(async () => {
        const rule = await api.addRule({ name, type: 'breakpoint', enabled: true, owner: 'user', phase, match: { url: pattern, methods: [r.method] }, spec: {}, priority: 0 });
        ruleModal = rule.id;
        return t('menu.done', { action: t(phase === 'request' ? 'menu.breakpointReq' : 'menu.breakpointResp'), name: rule.name });
      });
    const newRule = (type: string) => (ruleModal = `new~${type}~${pattern}`);
    const first = compareBase && compareBase !== id ? traffic.get(compareBase) : undefined;
    const compareItem: Item = first
      ? {
          label: t('menu.compareWith', { n: first.n }), sep: true, disabled: tunnel,
          action: () => {
            compare = { a: first.id, b: id, labels: [`#${first.n}`, `#${r.n}`] };
            compareBase = null;
          },
        }
      : { label: t('menu.compareMark'), sep: true, disabled: tunnel, action: () => ((compareBase = id), (notice = t('menu.compareMarked', { n: r.n }))) };
    return [
      { label: t('menu.copyUrl'), action: () => act(async () => ((await copyText(r.url)) ? t('menu.copied') : undefined)) },
      { label: t('menu.copyCurl'), disabled: tunnel, action: () => act(async () => ((await copyText((await api.curl(id)).curl)) ? t('menu.copied') : undefined)) },
      { label: t('menu.repeat'), sep: true, disabled: tunnel, action: () => act(async () => { const s = await api.repeat(id); return t('detail.repeated', { status: s.results[0]?.status ?? '—' }); }) },
      { label: t('menu.compose'), disabled: tunnel, action: () => (location.hash = href('compose', id)) },
      compareItem,
      {
        label: t('menu.tools'), sep: true, disabled: tunnel,
        children: [
          { label: t('menu.breakpointReq'), action: () => bp('request') },
          { label: t('menu.breakpointResp'), action: () => bp('response') },
          { label: t('menu.mapLocal'), sep: true, action: () => act(async () => { const m = await api.mapLocalFromFlow(id); ruleModal = m.rule.id; }) },
          { label: t('menu.mapRemote'), action: () => newRule('map_remote') },
          { label: t('menu.rewrite'), action: () => newRule('rewrite') },
          {
            label: t('menu.block'), sep: true,
            action: () => act(async () => {
              await api.addRule({ name: `Block ${name}`, type: 'map_local', enabled: true, owner: 'user', match: { url: pattern }, spec: { status: 403, body: 'Blocked by ProxyPilot' }, priority: 10 });
              return t('menu.blocked', { url: pattern });
            }),
          },
        ],
      },
      { label: pinned.has(id) ? t('menu.unpin') : t('menu.pin'), sep: true, action: () => togglePin(id) },
      { label: t('menu.filterHost', { host: r.host }), action: () => (query = `host:${r.host}`) },
      { label: t('menu.bypass', { host: r.host }), action: () => act(async () => addBypass(r.host)) },
    ];
  }

  function togglePin(id: string) {
    const next = new Set(pinned);
    if (!next.delete(id)) next.add(id);
    pinned = next;
    store('pp.pinned', JSON.stringify([...next]));
  }

  function select(id: string) {
    selected = id;
    table?.reveal(id);
  }

  function move(delta: number) {
    if (!visible.length) return;
    const i = visible.findIndex((r) => r.id === selected);
    const next = i < 0 ? (delta > 0 ? 0 : visible.length - 1) : Math.max(0, Math.min(visible.length - 1, i + delta));
    autoScroll = untrack(() => autoScroll) && next === visible.length - 1;
    select(visible[next].id);
  }

  function onkeydown(e: KeyboardEvent) {
    if (bpModal) {
      if (e.key === 'Escape') bpModal = null;
      return; // the pop-up's own shortcuts (c, x, ⌘↵) handle the rest
    }
    if (route.current.page !== 'traffic') return;
    const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement;
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      clearAll();
      return;
    }
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
    switch (e.key) {
      case '/':
        e.preventDefault();
        filterInput?.focus();
        break;
      case 'j':
      case 'ArrowDown':
        e.preventDefault();
        move(1);
        break;
      case 'k':
      case 'ArrowUp':
        e.preventDefault();
        move(-1);
        break;
      case 'Enter':
        if (selected) detailOpen = true;
        break;
      case 'Escape':
        detailOpen = false;
        break;
      case 'p':
        if (selected) togglePin(selected);
        break;
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="flex h-full">
  <Sidebar rows={traffic.rows} pinnedCount={pinned.size} {view} onview={(v) => (view = v)} page={route.current.page} paused={status?.pending_breakpoints ?? 0} />
  <div class="flex min-w-0 flex-1 flex-col">
  <Toolbar
    {status}
    {ssl}
    {theme}
    onrecord={toggleRecording}
    onclear={clearAll}
    {clearing}
    {sysProxy}
    ontogglesysproxy={toggleSysProxy}
    ontogglenocaching={toggleNoCaching}
    onexport={exportFlows}
    onimport={importFile}
    visibleCount={visible.length}
    onssl={setSSL}
    onssllist={() => (sslListOpen = true)}
    ontheme={() => {
      theme = theme === 'dark' ? 'light' : 'dark';
      store('pp.theme', theme);
    }}
  />
  <Banner {conn} {notice} onretry={() => stream.retryNow()} ondismiss={() => (notice = null)} />
  {#if route.current.page === 'compose'}
    <ComposePage fromFlow={route.current.sub} onnotice={(m) => (notice = m)} />
  {:else if route.current.page === 'breakpoints'}
    <BreakpointsPage version={bpVersion} focusId={route.current.sub} onnotice={(m) => (notice = m)} />
  {:else if route.current.page === 'rules'}
    <RulesPage selectedId={route.current.sub} version={ruleVersion} onnotice={(m) => (notice = m)} />
  {:else if route.current.page === 'setup'}
    <SetupPage onnotice={(m) => (notice = m)} version={deviceVersion} />
  {:else if route.current.page === 'docs'}
    <DocsPage slug={route.current.sub} {status} />
  {:else if route.current.page === 'uninstall'}
    <UninstallPage onnotice={(m) => (notice = m)} />
  {:else}
  <TypeStrip bind:typeFilter bind:query bind:errorsOnly bind:autoScroll bind:filterInput />
  <div class="relative flex min-h-0 flex-1 flex-col">
    <section class="flex min-w-0 flex-1 flex-col">
      <TrafficTable
        bind:this={table}
        rows={visible}
        {selected}
        {pinned}
        {autoScroll}
        onselect={(id) => {
          selected = id;
          detailOpen = true;
        }}
        {sort}
        onsort={(k) => (sort = nextSort(sort, k))}
        oncontext={(id, x, y) => (menu = { id, x, y })}
        onopen={(id) => {
          selected = id;
          detailOpen = true;
        }}
      >
        {#snippet empty()}
          <div class="absolute inset-0 z-[1] flex flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground [&_p]:mb-2">
            {#if filtering && traffic.rows.length}
              <p>{t('empty.noMatch')}</p>
              <Button
                variant="outline"
                onclick={() => {
                  query = '';
                  errorsOnly = false;
                  view = { kind: 'all' };
                  typeFilter = 'all';
                }}>{t('empty.clearFilter')}</Button
              >
            {:else}
              <h2 class="text-sm font-semibold text-foreground">{t('empty.title')}</h2>
              <p>{t('empty.body', { addr: status?.proxy_addr ?? '127.0.0.1:9090' })}</p>
              <Button variant="secondary" class="bg-accent text-accent-foreground" href="#/setup">{t('empty.setup')}</Button>
            {/if}
          </div>
        {/snippet}
      </TrafficTable>
    </section>
    {#if detailOpen && selectedRow}
      <div class="relative flex min-h-0 shrink-0 z-[1] border-t border-border shadow-[0_-10px_14px_-8px_rgba(0,0,0,0.55)]" style:height="{detailH}px">
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="absolute inset-x-0 -top-[3px] z-10 h-[6px] cursor-row-resize"
          title={t('traffic.resize')}
          onpointerdown={startResize}
        ></div>
      <DetailPanel
        row={selectedRow}
        pinned={pinned.has(selectedRow.id)}
        onpin={() => togglePin(selectedRow.id)}
        onclose={() => (detailOpen = false)}
        onbypass={addBypass}
        onrule={(id) => (ruleModal = id)}
      />
      </div>
    {/if}
  </div>
  {/if}
  {#if bpModal && route.current.page !== 'breakpoints'}
    <Dialog.Root open onOpenChange={(o) => !o && (bpModal = null)}>
      <Dialog.Content
        showCloseButton={false}
        onOpenAutoFocus={(e) => e.preventDefault()}
        class="flex h-[min(760px,calc(100%-64px))] w-[min(1100px,calc(100%-64px))] max-w-none gap-0 overflow-hidden rounded-[14px] bg-background p-0 sm:max-w-none"
      >
        <Dialog.Title class="sr-only">{t('bp.title')}</Dialog.Title>
        <BreakpointsPage modal version={bpVersion} focusId={bpModal} onnotice={(m) => (notice = m)} onclose={() => (bpModal = null)} />
      </Dialog.Content>
    </Dialog.Root>
  {/if}
  {#if compare}
    <DiffDialog a={compare.a} b={compare.b} labels={compare.labels} onclose={() => (compare = null)} />
  {/if}
  {#if sslListOpen && ssl}
    <SSLListDialog {ssl} onchange={(p) => (ssl = p)} onclose={() => (sslListOpen = false)} />
  {/if}
  {#if ruleModal}
    <Dialog.Root open onOpenChange={(o) => !o && (ruleModal = null)}>
      <Dialog.Content
        showCloseButton={false}
        onOpenAutoFocus={(e) => e.preventDefault()}
        class="flex h-[min(820px,calc(100%-64px))] w-[min(760px,calc(100%-64px))] max-w-none gap-0 overflow-hidden rounded-[14px] bg-background p-0 sm:max-w-none"
      >
        <Dialog.Title class="sr-only">{t('rules.edit')}</Dialog.Title>
        <RulesPage modal selectedId={ruleModal} version={ruleVersion} onnotice={(m) => (notice = m)} onclose={() => (ruleModal = null)} />
      </Dialog.Content>
    </Dialog.Root>
  {/if}
  {#if menu}
    <ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.id)} onclose={() => (menu = null)} />
  {/if}
  <BottomBar
    totalDown={traffic.totalDown}
    totalUp={traffic.totalUp}
    rateDown={traffic.rateDown}
    rateUp={traffic.rateUp}
    shown={visible.length}
    total={traffic.rows.length}
    selected={!!selectedRow}
    {ruleCounts}
    sslMode={ssl?.mode}
  />
  </div>
</div>
