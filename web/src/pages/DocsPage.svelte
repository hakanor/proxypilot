<script lang="ts">
  import CodeBlock from '../components/CodeBlock.svelte';
  import { Input } from '$lib/components/ui/input/index.js';
  import { cn } from '$lib/utils.js';
  import { lang, t } from '../lib/i18n';
  import { loadDocs } from '../lib/docs';
  import { href } from '../lib/route.svelte';
  import type { Status } from '../lib/api';

  interface Props {
    slug: string;
    status: Status | null;
  }
  let { slug, status }: Props = $props();

  // Live values are substituted into the docs ({proxy}, {api}, {dataDir}).
  const docs = $derived(
    loadDocs(lang(), {
      proxy: status?.proxy_addr ?? '127.0.0.1:9090',
      api: status?.api_addr ?? '127.0.0.1:9091',
      dataDir: status?.data_dir ?? '~/Library/Application Support/ProxyPilot',
    }),
  );
  let query = $state('');
  const visible = $derived(query ? docs.filter((d) => d.text.includes(query.toLowerCase())) : docs);
  const doc = $derived(docs.find((d) => d.slug === slug) ?? docs[0]);
  const sections = $derived.by(() => {
    const out: { name: string; items: typeof docs }[] = [];
    for (const d of visible) {
      const last = out.at(-1);
      if (last && last.name === d.section) last.items.push(d);
      else out.push({ name: d.section, items: [d] });
    }
    return out;
  });
  const outline = $derived(doc?.blocks.filter((b) => b.type === 'h2') ?? []);
  let content = $state<HTMLElement>();
  $effect(() => {
    void doc;
    content?.scrollTo(0, 0);
  });
</script>

<div class="flex min-h-0 flex-1">
  <nav class="flex w-[220px] shrink-0 flex-col overflow-y-auto border-r border-border bg-panel px-1.5 py-2.5">
    <Input class="mx-1 mb-1.5 h-8 w-auto" placeholder={t('docs.search')} bind:value={query} spellcheck="false" />
    {#each sections as s (s.name)}
      <div class="section-label !pt-3">{s.name.toUpperCase()}</div>
      {#each s.items as d (d.slug)}
        <a
          class={cn(
            'rounded-[5px] px-2 py-1 text-foreground no-underline hover:bg-neutral-bg',
            d.slug === doc?.slug && 'bg-accent font-medium text-accent-foreground hover:bg-accent',
          )}
          href={href('docs', d.slug)}>{d.title}</a
        >
      {/each}
    {:else}
      <p class="px-2 text-faint">{t('docs.noResults')}</p>
    {/each}
  </nav>
  <main bind:this={content} class="flex min-w-0 flex-1 items-start gap-10 overflow-auto px-gutter py-5">
    {#if doc}
      <article class="docs-prose flex max-w-[760px] flex-1 flex-col gap-3 text-[13px]">
        <span class="section-label !p-0">{doc.section.toUpperCase()}</span>
        {#each doc.blocks as b, i (i)}
          {#if b.type === 'h1'}
            <h1>{@html b.html}</h1>
          {:else if b.type === 'h2'}
            <h2 id={b.id}>{@html b.html}</h2>
          {:else if b.type === 'p'}
            <p>{@html b.html}</p>
          {:else if b.type === 'code'}
            <CodeBlock code={b.code} lang={b.lang} />
          {:else if b.type === 'ul'}
            <ul>{#each b.items as it, j (j)}<li>{@html it}</li>{/each}</ul>
          {:else if b.type === 'table'}
            <table>
              <thead><tr>{#each b.head as h, j (j)}<th>{@html h}</th>{/each}</tr></thead>
              <tbody>
                {#each b.rows as r, j (j)}<tr>{#each r as c, k (k)}<td>{@html c}</td>{/each}</tr>{/each}
              </tbody>
            </table>
          {/if}
        {/each}
      </article>
      {#if outline.length}
        <aside class="sticky top-0 hidden w-[200px] shrink-0 flex-col gap-1 min-[1101px]:flex">
          <span class="section-label !p-0 !pb-1">{t('docs.onThisPage')}</span>
          {#each outline as h (h.type === 'h2' ? h.id : '')}
            {#if h.type === 'h2'}
              <button
                type="button"
                class="border-0 bg-transparent py-0.5 text-left text-muted-foreground hover:text-accent-foreground"
                onclick={() => content?.querySelector(`#${h.id}`)?.scrollIntoView({ behavior: 'smooth' })}>{@html h.html}</button
              >
            {/if}
          {/each}
        </aside>
      {/if}
    {/if}
  </main>
</div>

<style>
  /* Preflight resets element styles; restore readable prose for rendered markdown. */
  :global(.docs-prose h1) {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
    line-height: 1.3;
    color: var(--text);
  }
  :global(.docs-prose h2) {
    margin: 14px 0 0;
    font-size: 15px;
    font-weight: 600;
    line-height: 1.4;
    color: var(--text);
  }
  :global(.docs-prose p),
  :global(.docs-prose ul) {
    margin: 0;
    color: var(--pp-muted);
    line-height: 1.65;
  }
  :global(.docs-prose ul) {
    list-style: disc;
    padding-left: 20px;
  }
  :global(.docs-prose li + li) {
    margin-top: 2px;
  }
  :global(.docs-prose li::marker) {
    color: var(--faint);
  }
  :global(.docs-prose :not(pre) > code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    font-size: 11.5px;
    color: var(--text);
    background: var(--neutral-bg);
    padding: 1px 5px;
    border-radius: 4px;
  }
  :global(.docs-prose strong) {
    color: var(--text);
    font-weight: 600;
  }
  :global(.docs-prose em) {
    font-style: italic;
  }
  :global(.docs-prose a) {
    color: var(--pp-accent-text);
    text-decoration: underline;
    text-underline-offset: 2px;
  }
  :global(.docs-prose table) {
    border-collapse: separate;
    border-spacing: 0;
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
    font-size: 12px;
  }
  :global(.docs-prose th),
  :global(.docs-prose td) {
    text-align: left;
    padding: 6px 10px;
    vertical-align: top;
    color: var(--pp-muted);
  }
  :global(.docs-prose th) {
    background: var(--panel);
    font-weight: 500;
    border-bottom: 1px solid var(--border);
  }
  :global(.docs-prose tbody tr:nth-child(even) td) {
    background: var(--zebra);
  }
</style>
