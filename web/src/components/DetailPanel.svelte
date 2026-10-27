<script lang="ts">
  import XIcon from '@lucide/svelte/icons/x';
  import BodyView from './detail/BodyView.svelte';
  import TabStrip from './detail/TabStrip.svelte';
  import MessagesView from './detail/MessagesView.svelte';
  import HeadersTable from './detail/HeadersTable.svelte';
  import TimingView from './detail/TimingView.svelte';
  import ErrorView from './detail/ErrorView.svelte';
  import GraphQLView from './detail/GraphQLView.svelte';
  import { t } from '../lib/i18n';
    import { statusPill } from '../lib/status';
  import { api, APIError, type FlowDetail } from '../lib/api';
  import { copyText, parseCookies, parseQuery, toCurl, rawHead } from '../lib/detail';
  import type { Row } from '../lib/traffic.svelte';
  import { href } from '../lib/route.svelte';
  import { cn } from '$lib/utils.js';
  import { Button } from '$lib/components/ui/button/index.js';

  const pillTone: Record<string, string> = {
    ok: 'text-ok bg-ok-bg',
    c4: 'text-c4 bg-c4-bg',
    err: 'text-err bg-err-bg',
    warn: 'text-warn bg-warn-bg',
    neutral: 'text-neutral-fg bg-neutral-bg',
  };

  interface Props {
    row: Row;
    pinned: boolean;
    onpin: () => void;
    onclose: () => void;
    onbypass: (host: string) => void;
    /** Opens a rule in the editor pop-up. */
    onrule: (id: string) => void;
  }
  let { row, pinned, onpin, onclose, onbypass, onrule }: Props = $props();

  type ReqTab = 'header' | 'query' | 'cookies' | 'body' | 'raw';
  type RespTab = 'header' | 'body' | 'raw' | 'hex' | 'timing' | 'graphql' | 'messages';
  // Body (JSON) first, everywhere: it is the first tab and every newly selected flow opens on it.
  const reqTabs: ReqTab[] = ['body', 'header', 'query', 'cookies', 'raw'];
  // A WebSocket (101) opens on its messages instead of the empty body.
  const isWS = $derived(row.status === 101);
  const respTabs = $derived<RespTab[]>(
    isWS ? ['messages', 'header', 'raw', 'timing'] : row.gql_op ? ['body', 'graphql', 'header', 'raw', 'hex', 'timing'] : ['body', 'header', 'raw', 'hex', 'timing'],
  );
  let reqTab = $state<ReqTab>('body');
  let respTabPref = $state<RespTab>('body');
  const respTab = $derived<RespTab>(respTabs.includes(respTabPref) ? respTabPref : respTabs[0]);
  function pickReq(v: ReqTab) { reqTab = v; }
  function pickResp(v: RespTab) { respTabPref = v; }
  $effect(() => {
    void row.id;
    reqTab = 'body';
    respTabPref = 'body';
  });
  const reveal = true; // the UI is local, so it always shows real values
  let flow = $state<FlowDetail | null>(null);
  let gone = $state(false);
  let toast = $state<string | null>(null);
  let toastTimer: ReturnType<typeof setTimeout>;

  // Refetch when the selection, its state or the reveal toggle change.
  const version = $derived(`${row.state}:${row.status ?? 0}:${row.bytes_down}`);
  $effect(() => {
    const id = row.id;
    const redact = !reveal;
    void version;
    let cancelled = false;
    api.flow(id, redact).then(
      (f) => {
        if (!cancelled) [flow, gone] = [f, false];
      },
      (e) => {
        if (!cancelled) [flow, gone] = [null, e instanceof APIError && e.status === 404];
      },
    );
    return () => (cancelled = true);
  });

  const p = $derived(statusPill(row));
  const failed = $derived(row.state === 'failed' || !!row.error_kind);
  const pending = $derived(row.state === 'pending' || row.state === 'paused');
  const reqHeaders = $derived((flow?.req_headers ?? []).map((h) => [h.name, h.value] as [string, string]));
  const respHeaders = $derived((flow?.resp_headers ?? []).map((h) => [h.name, h.value] as [string, string]));

  function flash(msg: string) {
    toast = msg;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = null), 1400);
  }
  async function copy(text: string) {
    flash((await copyText(text)) ? t('detail.copied') : t('detail.copyFailed'));
  }
  async function copyCurl() {
    // Always build from the unredacted flow: a masked token would make the command useless.
    const f = await api.flow(row.id, false);
    let body: string | null = null;
    if (f.bodies.req?.decoded_size) {
      const b = await api.body(row.id, 'req', 10 << 20, false);
      if (b.encoding === 'utf8') body = b.data ?? null;
    }
    copy(toCurl(f, body));
  }

  // A request-phase breakpoint for this method and URL (any query).
  async function addBreakpoint() {
    try {
      const url = row.url.split('?')[0] + '*';
      const r = await api.addRule({
        name: `${row.method} ${row.host}${row.path}`, type: 'breakpoint', enabled: true, owner: 'user', phase: 'request',
        match: { url, methods: [row.method] }, spec: {}, priority: 0,
      });
      onrule(r.id);
    } catch (e) {
      flash(e instanceof Error ? e.message : String(e));
    }
  }

  async function repeatNow() {
    try {
      const s = await api.repeat(row.id);
      const r = s.results[0];
      flash(t('detail.repeated', { status: r?.error ?? r?.status ?? '—' }));
    } catch (e) {
      flash(e instanceof Error ? e.message : String(e));
    }
  }

  async function saveMapLocal() {
    try {
      const { rule } = await api.mapLocalFromFlow(row.id);
      onrule(rule.id);
    } catch (e) {
      flash(e instanceof Error ? e.message : String(e));
    }
  }

</script>


<section class="relative flex h-full w-full min-h-0 flex-col border-t border-border bg-panel text-[13px]">
  <div class="flex h-10 shrink-0 items-center gap-2 border-b border-border px-4">
    <span class="rounded bg-muted px-1.5 py-0.5 font-mono text-[11.5px] font-semibold">{row.method}</span>
    <span class={cn('rounded px-1.5 py-0.5 font-mono text-[11.5px]', pillTone[p.cls])}>{p.label}</span>
    {#if flow?.protocol}<span class="font-mono text-[11px] text-faint" title={t('detail.protocol')}>{flow.protocol === 'HTTP/2.0' ? 'h2' : flow.protocol}</span>{/if}
    {#if pending}<span class="size-2.5 animate-spin rounded-full border-2 border-border border-t-primary" aria-label={t('detail.loading')}></span>{/if}
    <span class="min-w-0 flex-1 truncate font-mono text-[13px]" title={flow?.tags?.original_url ? t('detail.mappedFrom', { url: flow.tags.original_url }) : row.url}>{row.url}</span>
    {#if flow?.tags?.rewritten}<span class="shrink-0 text-xs text-accent-foreground">{t('detail.rewritten', { phase: flow.tags.rewritten })}</span>{/if}
    <div class="flex shrink-0 items-center gap-1">
      <Button variant="ghost" size="xs" onclick={() => copy(row.url)}>{t('actions.copyUrl')}</Button>
      <Button variant="ghost" size="xs" onclick={copyCurl}>{t('actions.copyCurl')}</Button>
      <Button variant="ghost" size="xs" disabled={row.state === 'tunneled' || pending} onclick={repeatNow}>{t('actions.repeat')}</Button>
      <Button variant="ghost" size="xs" href={href('compose', row.id)}>{t('actions.compose')}</Button>
      <Button variant="ghost" size="xs" disabled={row.state === 'pending'} onclick={saveMapLocal}>{t('actions.mapLocal')}</Button>
      <Button variant="ghost" size="xs" disabled={row.state === 'tunneled'} onclick={addBreakpoint}>{t('actions.breakpoint')}</Button>
      <Button variant={pinned ? 'secondary' : 'ghost'} size="xs" class={cn(pinned && 'bg-accent text-accent-foreground')} onclick={onpin}>{pinned ? t('actions.unpin') : t('actions.pin')}</Button>
      <Button variant="ghost" size="icon-sm" class="text-muted-foreground" aria-label={t('detail.close')} onclick={onclose}><XIcon /></Button>
    </div>
  </div>

  {#if gone}
    <p class="mx-4 my-3 text-faint">{t('detail.evicted')}</p>
  {:else}
    <div class="grid min-h-0 flex-1 grid-cols-2">
      <div class="flex min-h-0 min-w-0 flex-col border-r border-border">
        <TabStrip title={t('detail.requestPane')} list={reqTabs} active={reqTab} pick={(v) => pickReq(v as ReqTab)} />
        <div class="min-h-0 flex-1 overflow-auto py-1">
          {#if reqTab === 'header'}
            <HeadersTable rows={reqHeaders} empty={t('viewer.noHeaders')} oncopy={copy} />
          {:else if reqTab === 'query'}
            <HeadersTable rows={parseQuery(flow?.query ?? '')} empty={t('viewer.noQuery')} oncopy={copy} />
          {:else if reqTab === 'cookies'}
            <HeadersTable rows={parseCookies(flow?.req_headers, false)} empty={t('viewer.noCookies')} oncopy={copy} />
          {:else}
            <BodyView flowId={row.id} side="req" info={flow?.bodies.req} contentType={flow?.req_content_type} redact={!reveal} {version} head={flow ? rawHead(flow, 'req') : ''} forceRaw={reqTab === 'raw'} oncopy={copy} />
          {/if}
        </div>
      </div>
      <div class="flex min-h-0 min-w-0 flex-col">
        <TabStrip title={t('detail.responsePane')} list={respTabs} active={respTab} pick={(v) => pickResp(v as RespTab)} />
        <div class="min-h-0 flex-1 overflow-auto py-1">
          {#if failed && row.error_kind && !row.status && row.error_kind !== 'graphql_error'}
            <ErrorView kind={row.error_kind} message={flow?.error_msg} host={row.host} {onbypass} />
          {:else if pending && respTab !== 'timing'}
            <p class="mx-4 my-3 text-faint">{t('detail.loading')}</p>
          {:else if respTab === 'header'}
            <HeadersTable rows={respHeaders} empty={t('viewer.noHeaders')} oncopy={copy} />
            <HeadersTable title={t('viewer.cookies')} rows={parseCookies(flow?.resp_headers, true)} empty={t('viewer.noCookies')} oncopy={copy} />
          {:else if respTab === 'messages'}
            <MessagesView flowId={row.id} oncopy={copy} />
          {:else if respTab === 'graphql' && row.gql_op}
            <GraphQLView flowId={row.id} redact={!reveal} {version} oncopy={copy} />
          {:else if respTab === 'hex'}
            <BodyView flowId={row.id} side="resp" info={flow?.bodies.resp} contentType={flow?.resp_content_type} redact={!reveal} {version} forceHex oncopy={copy} />
          {:else if respTab === 'timing'}
            <TimingView timing={flow?.timing} />
          {:else}
            <BodyView flowId={row.id} side="resp" info={flow?.bodies.resp} contentType={flow?.resp_content_type} redact={!reveal} {version} head={flow ? rawHead(flow, 'resp') : ''} forceRaw={respTab === 'raw'} oncopy={copy} />
          {/if}
        </div>
      </div>
    </div>
  {/if}
  {#if toast}<div class="absolute right-3.5 bottom-3.5 rounded-lg bg-accent px-3 py-1.5 font-medium text-accent-foreground" role="status">{toast}</div>{/if}
</section>
