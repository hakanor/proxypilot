<script lang="ts">
  import CodeBlock from '../components/CodeBlock.svelte';
  import { inline } from '../lib/md';
  import { t } from '../lib/i18n';
  import { api, type ComposeReq, type SendSummary } from '../lib/api';
  import { formatDuration } from '../lib/format';
  import { statusPill } from '../lib/status';
  import { href } from '../lib/route.svelte';
  import { generate, langs, type Lang } from '../lib/codegen';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Textarea } from '$lib/components/ui/textarea/index.js';
  import * as Select from '$lib/components/ui/select/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';
  import { Checkbox } from '$lib/components/ui/checkbox/index.js';

  interface Props {
    fromFlow: string;
    onnotice: (msg: string) => void;
  }
  let { fromFlow, onnotice }: Props = $props();

  const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];
  let req = $state<ComposeReq>({ method: 'GET', url: 'https://', headers: [{ name: '', value: '' }], body: '' });
  let curl = $state('');
  let count = $state(1);
  let concurrency = $state(1);
  let bypass = $state(false);
  let busy = $state(false);
  let error = $state<string | null>(null);
  let result = $state<SendSummary | null>(null);
  let lang = $state<Lang>('curl');

  // Prefill from a recorded flow (#/compose/<id>): the unredacted request, ready to edit.
  $effect(() => {
    const id = fromFlow;
    if (!id) return;
    api.editableRequest(id).then(
      (r) => (req = { ...r, headers: r.headers?.length ? r.headers : [{ name: '', value: '' }], body: r.body ?? '' }),
      (e) => (error = String(e)),
    );
  });

  async function importCurl() {
    error = null;
    try {
      const r = await api.parseCurl(curl);
      req = { ...r, headers: r.headers?.length ? r.headers : [{ name: '', value: '' }], body: r.body ?? '' };
      curl = '';
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function send() {
    busy = true;
    error = null;
    try {
      result = await api.compose({
        method: req.method,
        url: req.url.trim(),
        headers: req.headers.filter((h) => h.name.trim()),
        body: req.body || undefined,
        count: Number(count) || 1,
        concurrency: Number(concurrency) || 1,
        bypass_rules: bypass || undefined,
      });
      onnotice(t('compose.summary', { n: result.results.length, ms: Math.round(result.avg_ms) }));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const code = $derived(generate({ ...req, headers: req.headers.filter((h) => h.name.trim()) }, lang));
  const pill = (s?: number) => statusPill({ status: s, state: s ? 'completed' : 'failed' } as never);
</script>

<div class="flex min-h-0 flex-1">
  <main class="flex min-w-0 flex-1 flex-col gap-3.5 overflow-auto px-gutter py-5">
    <div class="flex items-baseline gap-2.5">
      <h1 class="m-0 text-lg font-semibold">{t('compose.title')}</h1>
      {#if fromFlow}<span class="text-faint">{t('compose.fromFlow', { id: fromFlow })}</span>{/if}
    </div>

    <section class="flex flex-col gap-2 rounded-[10px] border border-border bg-panel px-3.5 py-3">
      <label class="flex flex-col gap-1.5 text-muted-foreground"
        >{t('compose.import')}
        <Textarea class="min-h-0 resize-y font-mono text-xs" rows={3} bind:value={curl} placeholder="curl 'https://…' -H 'accept: */*'" spellcheck="false" />
      </label>
      <div class="flex flex-wrap items-center gap-2.5">
        <span class="text-faint">{t('compose.importHint')}</span>
        <span class="flex-1"></span>
        <Button variant="outline" disabled={!curl.trim()} onclick={importCurl}>{t('compose.importBtn')}</Button>
      </div>
    </section>

    <section class="flex flex-col gap-2 rounded-[10px] border border-border bg-panel px-3.5 py-3">
      <div class="grid grid-cols-[110px_minmax(0,1fr)_auto] gap-2">
        <Select.Root type="single" bind:value={req.method}>
          <Select.Trigger class="w-full" aria-label={t('compose.method')}>{req.method}</Select.Trigger>
          <Select.Content>
            {#each methods as m (m)}<Select.Item value={m} label={m}>{m}</Select.Item>{/each}
          </Select.Content>
        </Select.Root>
        <Input class="min-w-0 font-mono text-xs" bind:value={req.url} aria-label={t('compose.url')} spellcheck="false" />
        <Button disabled={busy || !req.url.trim()} onclick={send}>{busy ? t('compose.sending') : t('compose.send')}</Button>
      </div>
      <span class="section-label pt-1.5">{t('compose.headers')}</span>
      {#each req.headers as h, i (i)}
        <div class="grid grid-cols-[220px_minmax(0,1fr)_24px] gap-1.5">
          <Input class="min-w-0 font-mono text-xs" bind:value={h.name} spellcheck="false" />
          <Input class="min-w-0 font-mono text-xs" bind:value={h.value} spellcheck="false" />
          <button type="button" class="border-0 bg-transparent text-faint" aria-label="×" onclick={() => req.headers.splice(i, 1)}>×</button>
        </div>
      {/each}
      <button type="button" class="self-start border-0 bg-transparent px-0 py-0.5 text-accent-foreground" onclick={() => req.headers.push({ name: '', value: '' })}>+ {t('compose.addHeader')}</button>
      <span class="section-label pt-1.5">{t('compose.body')}</span>
      <Textarea class="resize-y font-mono text-xs" rows={8} bind:value={req.body} spellcheck="false" />
      <div class="flex flex-wrap items-center gap-2.5">
        <label class="flex items-center gap-1.5 text-muted-foreground">{t('compose.count')}<Input class="w-16 font-mono text-xs" type="number" min="1" max="50" bind:value={count} /></label>
        <label class="flex items-center gap-1.5 text-muted-foreground">{t('compose.concurrency')}<Input class="w-16 font-mono text-xs" type="number" min="1" max="10" bind:value={concurrency} /></label>
        <label class="flex items-center gap-1.5 text-muted-foreground"><Checkbox bind:checked={bypass} /> {t('compose.bypass')}</label>
        <span class="text-faint">{t('compose.limit', { count: 50, conc: 10 })}</span>
      </div>
      {#if error}<div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>{/if}
    </section>

    {#if result}
      <section class="flex flex-col gap-2 rounded-[10px] border border-border bg-panel px-3.5 py-3">
        <div class="flex flex-wrap items-center gap-2.5">
          <strong>{t('compose.results')}</strong>
          <span class="text-faint">{t('compose.summary', { n: result.results.length, ms: Math.round(result.avg_ms) })}</span>
        </div>
        {#each result.results as r, i (i)}
          {@const p = pill(r.status)}
          <div class="flex items-center gap-2.5 border-t border-border py-1">
            <span class="pill {p.cls}">{r.error ? t('status.error') : r.aborted ? t('compose.aborted') : p.label}</span>
            <span class="font-mono text-xs text-faint">{formatDuration(r.ms)}</span>
            {#if r.error}<span class="text-err">{r.error}</span>{/if}
            <span class="flex-1"></span>
            {#if r.flow_id}<a class="text-accent-foreground no-underline" href={href('traffic', r.flow_id)}>{t('compose.open')}</a>{/if}
          </div>
        {/each}
      </section>
    {/if}
  </main>

  <aside class="side flex w-[420px] shrink-0 flex-col gap-2.5 overflow-auto border-l border-border px-gutter py-5">
    <span class="section-label p-0">{t('compose.export')}</span>
    <Tabs.Root bind:value={lang}>
      <Tabs.List class="w-full">
        {#each langs as l (l)}<Tabs.Trigger value={l}>{l}</Tabs.Trigger>{/each}
      </Tabs.List>
    </Tabs.Root>
    <CodeBlock code={code} lang={lang} />
    <p class="small m-0 text-[11px] text-faint">{@html inline(t('compose.binary'))}</p>
  </aside>
</div>

<style>
  .small :global(code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
  }
  @media (max-width: 1100px) {
    .side {
      display: none;
    }
  }
</style>
