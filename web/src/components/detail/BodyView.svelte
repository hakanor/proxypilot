<script lang="ts">
  import JsonNode from './JsonNode.svelte';
  import HexView from './HexView.svelte';
  import HeadersTable from './HeadersTable.svelte';
  import { t } from '../../lib/i18n';
  import { api, type Body, type BodyInfo } from '../../lib/api';
  import { formatBytes } from '../../lib/format';
  import { base64ToBytes, bodyKind, parseMultipart, parseQuery, type BodyKind } from '../../lib/detail';
  import { highlight, prettyJSON, type Lang } from '../../lib/highlight';
  import { parseJSON } from '../../lib/json';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';

  interface Props {
    flowId: string;
    side: 'req' | 'resp';
    info: BodyInfo | undefined;
    contentType: string | undefined;
    redact: boolean;
    /** Changes when the flow updates (pending → completed). */
    version: string;
    forceHex?: boolean;
    /** Lock the view to the raw HTTP message (no mode tabs). */
    forceRaw?: boolean;
    /** Start line + headers; the Raw view shows them above the body like the wire format. */
    head?: string;
    oncopy: (text: string) => void;
  }
  let { flowId, side, info, contentType, redact, version, forceHex = false, forceRaw = false, head = '', oncopy }: Props = $props();

  const PREVIEW = 2 << 20; // first 2 MB
  const FULL = 10 << 20; // server cap
  type Mode = 'tree' | 'pretty' | 'raw' | 'preview' | 'table' | 'hex';

  let body = $state<Body | null>(null);
  let loading = $state(false);
  let failed = $state(false);
  let max = $state(PREVIEW);
  let mode = $state<Mode | null>(null);
  let parsed = $state<{ value: unknown } | null>(null);
  let parseError = $state(false);
  let parsing = $state(false);
  let query = $state('');
  let img = $state<{ w: number; h: number } | null>(null);

  $effect(() => {
    void version;
    const id = flowId, s = side, m = max, r = redact;
    let cancelled = false;
    loading = true;
    failed = false;
    api.body(id, s, m, r).then(
      (b) => {
        if (cancelled) return;
        body = b;
        loading = false;
      },
      () => {
        if (cancelled) return;
        failed = true;
        loading = false;
      },
    );
    return () => (cancelled = true);
  });

  const isB64 = $derived(body?.encoding === 'base64');
  const text = $derived(body?.data && !isB64 ? body.data : '');
  const kind = $derived<BodyKind>(bodyKind(body?.content_type ?? contentType, text.slice(0, 64), isB64));
  const bytes = $derived(
    body?.data ? (isB64 ? base64ToBytes(body.data) : new TextEncoder().encode(body.data)) : new Uint8Array(),
  );
  const modes = $derived.by((): Mode[] => {
    if (!body?.size) return ['raw'];
    switch (kind) {
      case 'json':
        return ['tree', 'pretty', 'raw'];
      case 'html':
      case 'xml':
      case 'js':
      case 'css':
        return ['pretty', 'raw'];
      case 'image':
        return ['preview', 'hex', 'raw'];
      case 'form':
      case 'multipart':
        return ['table', 'raw'];
      case 'binary':
        return ['hex', 'raw'];
      default:
        return ['raw'];
    }
  });
  const active = $derived<Mode>(forceHex ? 'hex' : forceRaw ? 'raw' : mode && modes.includes(mode) ? mode : modes[0]);

  // JSON parse (Web Worker for big bodies). Truncated JSON will not parse: fall back to raw.
  $effect(() => {
    if (kind !== 'json' || !text) {
      parsed = null;
      return;
    }
    const src = text;
    let cancelled = false;
    parsing = true;
    parseError = false;
    parseJSON(src).then(
      (v) => {
        if (!cancelled) [parsed, parsing] = [{ value: v }, false];
      },
      () => {
        if (!cancelled) [parsed, parsing, parseError] = [null, false, true];
      },
    );
    return () => (cancelled = true);
  });

  const lang = $derived<Lang>(kind === 'json' ? 'json' : kind === 'html' || kind === 'xml' || kind === 'js' || kind === 'css' ? kind : 'json');
  const pretty = $derived.by(() => {
    if (active !== 'pretty') return '';
    const src = kind === 'json' && parsed ? prettyJSON(parsed.value) : text;
    return highlight(src, lang);
  });
  const formRows = $derived.by((): [string, string][] => {
    if (active !== 'table') return [];
    if (kind === 'form') return parseQuery(text);
    return parseMultipart(text, body?.content_type ?? contentType ?? '').map((p) => [p.name, p.info]);
  });
  const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const reEsc = (v: string) => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Wraps matches in <mark>, only inside text between tags so highlight spans stay intact.
  function markHTML(html: string, q: string): string {
    if (!q) return html;
    const re = new RegExp(reEsc(esc(q)), 'gi');
    return html.replace(/(^|>)([^<]+)/g, (_, a: string, txt: string) => a + txt.replace(re, (m) => `<mark>${m}</mark>`));
  }
  const rawText = $derived((head ? head + '\n\n' : '') + (isB64 ? (body?.size ? `<binary body, ${formatBytes(body.size)}>` : '') : text));
  const rawShown = $derived(active === 'raw' ? markHTML(esc(rawText), query) : '');
  const prettyShown = $derived(active === 'pretty' ? markHTML(pretty, query) : '');

  // Enter / Shift+Enter walks through the matches.
  let viewEl = $state<HTMLDivElement | null>(null);
  let hitIndex = $state(0);
  let hitTotal = $state(0);
  function hits(): HTMLElement[] {
    return viewEl ? [...viewEl.querySelectorAll<HTMLElement>('mark, .hit > .line')] : [];
  }
  function focusHit(i: number) {
    const list = hits();
    hitTotal = list.length;
    if (!list.length) return;
    hitIndex = (i + list.length) % list.length;
    list.forEach((el, j) => el.classList.toggle('current', j === hitIndex));
    list[hitIndex].scrollIntoView({ block: 'center' });
  }
  $effect(() => {
    void query;
    void active;
    void prettyShown;
    void rawShown;
    void parsed;
    // Wait for the tree to expand and render before looking for matches.
    const id = setTimeout(() => (query ? focusHit(0) : (hitTotal = 0)), 60);
    return () => clearTimeout(id);
  });
  function onSearchKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      focusHit(hitIndex + (e.shiftKey ? -1 : 1));
    } else if (e.key === 'Escape') {
      query = '';
    }
  }
  const empty = $derived(!loading && !failed && (!body || !body.size) && !head);
</script>

{#if loading && !body}
  <p class="mx-gutter my-2.5 text-faint">…</p>
{:else if head && (failed || !body)}
  <div class="flex items-center gap-2 border-b border-border px-gutter py-2">
    <span class="text-faint">{t('viewer.raw')}</span><span class="flex-1"></span>
    <Button variant="outline" size="sm" onclick={() => oncopy(head)}>{t('viewer.copyRaw')}</Button>
  </div>
  <div class="body-view px-gutter py-2"><pre class="m-0 font-mono text-[13px] leading-[1.65] break-all whitespace-pre-wrap">{head}</pre></div>
{:else if failed}
  <p class="mx-gutter my-2.5 text-faint">{t('viewer.noBody')}</p>
{:else if empty}
  <p class="mx-gutter my-2.5 text-faint">{t('viewer.noBody')}</p>
{:else if body}
  <div class="flex flex-wrap items-center gap-2 border-b border-border px-gutter py-2">
    {#if !forceHex && !forceRaw && modes.length > 1}
      <Tabs.Root value={active} onValueChange={(v) => (mode = v as Mode)}>
        <Tabs.List>
          {#each modes as m (m)}
            <Tabs.Trigger value={m} class="px-2.5">{t(`viewer.${m}`)}</Tabs.Trigger>
          {/each}
        </Tabs.List>
      </Tabs.Root>
    {/if}
    {#if active === 'tree' || active === 'pretty' || active === 'raw'}
      <Input class="h-7 w-40" placeholder={t('viewer.search')} bind:value={query} spellcheck="false" onkeydown={onSearchKey} title={t('viewer.searchHint')} />
      {#if query}<span class="text-faint tabular-nums">{hitTotal ? `${hitIndex + 1}/${hitTotal}` : t('viewer.noMatches')}</span>{/if}
    {/if}
    <span class="flex-1"></span>
    {#if info?.encoding}<span class="text-faint" title={t('viewer.encoded', { encoding: info.encoding, size: formatBytes(info.raw_size) })}>{info.encoding}</span>{/if}
    <span class="font-mono text-xs text-faint">{formatBytes(body.size)}</span>
    {#if active === 'raw' && head}<Button variant="outline" size="sm" onclick={() => oncopy(rawText)}>{t('viewer.copyRaw')}</Button>{/if}
    <Button variant="outline" size="sm" href={api.bodyURL(flowId, side)} download>{t('viewer.download')}</Button>
  </div>
  {#if body.truncated}
    <div class="flex flex-wrap items-center gap-2 bg-warn-bg px-gutter py-1.5 text-warn">
      <span>{t('viewer.truncated', { shown: formatBytes(body.returned), total: formatBytes(body.size) })}</span>
      {#if max < FULL && body.size > body.returned}
        <Button variant="outline" size="sm" onclick={() => (max = FULL)}>{t('viewer.loadFull')}</Button>
      {:else if body.size > FULL}
        <span>{t('viewer.tooLarge', { limit: formatBytes(FULL) })}</span>
      {/if}
    </div>
  {/if}

  <div class="body-view px-gutter py-2" bind:this={viewEl}>
    {#if active === 'tree'}
      {#if parsing}
        <p class="my-2.5 text-faint">{t('viewer.parsing')}</p>
      {:else if parsed}
        <div class="font-mono text-[13px] leading-[1.7]"><JsonNode value={parsed.value} path="$" depth={0} {query} {oncopy} /></div>
      {:else}
        {#if parseError}<p class="my-2.5 text-faint">{body.truncated ? t('viewer.truncatedJson') : t('viewer.invalidJson')}</p>{/if}
        <pre class="m-0 font-mono text-[13px] leading-[1.65] break-all whitespace-pre-wrap">{text}</pre>
      {/if}
    {:else if active === 'pretty'}
      <pre class="m-0 font-mono text-[13px] leading-[1.65] break-all whitespace-pre-wrap">{@html prettyShown}</pre>
    {:else if active === 'raw'}
      <pre class="m-0 font-mono text-[13px] leading-[1.65] break-all whitespace-pre-wrap">{@html rawShown}</pre>
    {:else if active === 'table'}
      <HeadersTable rows={formRows} empty={t('viewer.noBody')} {oncopy} />
    {:else if active === 'preview'}
      <div class="flex flex-col items-start gap-2">
        <img
          class="checker max-h-[420px] max-w-full rounded-md"
          src={api.bodyURL(flowId, side)}
          alt=""
          onload={(e) => {
            const el = e.currentTarget as HTMLImageElement;
            img = { w: el.naturalWidth, h: el.naturalHeight };
          }}
        />
        {#if img}<span class="text-faint">{t('viewer.imageSize', { w: img.w, h: img.h, size: formatBytes(body.size) })}</span>{/if}
      </div>
    {:else if active === 'hex'}
      <HexView {bytes} />
    {/if}
  </div>
{/if}

<style>
  .checker {
    background: repeating-conic-gradient(var(--zebra) 0 25%, var(--background) 0 50%) 0 0 / 16px 16px;
  }
  /* Syntax highlight tokens emitted by lib/highlight and JsonNode. */
  /* Readable on the dark panel: keys near-white, punctuation muted rather than faint. */
  .body-view {
    color: var(--text);
  }
  .body-view :global(mark) {
    background: var(--warn-bg);
    color: var(--warn);
    border-radius: 2px;
    box-shadow: 0 0 0 1px var(--warn);
  }
  .body-view :global(mark.current),
  .body-view :global(.line.current) {
    background: var(--warn);
    color: #111;
  }
  :global(.h-key) {
    color: #d6d6d9;
  }
  :global(.h-str) {
    color: var(--ok);
  }
  :global(.h-num),
  :global(.h-bool) {
    color: var(--warn);
  }
  :global(.h-null),
  :global(.h-com) {
    color: var(--pp-muted);
  }
  :global(.h-tag) {
    color: var(--accent-foreground);
  }
  :global(.h-attr),
  :global(.h-kw) {
    color: var(--warn);
  }
</style>
