<script lang="ts">
  import { untrack } from 'svelte';
  import JsonNode from './JsonNode.svelte';
  import { t } from '../../lib/i18n';
  import { pathSeg } from '../../lib/detail';

  interface Props {
    k?: string | number;
    value: unknown;
    path: string;
    depth: number;
    query: string;
    last?: boolean;
    oncopy: (text: string) => void;
  }
  let { k, value, path, depth, query, last = true, oncopy }: Props = $props();

  const PAGE = 200;
  const isArr = $derived(Array.isArray(value));
  const isObj = $derived(value !== null && typeof value === 'object');
  const entries = $derived(
    isObj ? (isArr ? (value as unknown[]).map((v, i) => [i, v] as [number, unknown]) : Object.entries(value as object)) : [],
  );
  let open = $state(untrack(() => depth < 2));
  let limit = $state(PAGE);

  // Search expands every branch that contains a match.
  const q = $derived(query.toLowerCase());
  const matchesSelf = $derived(
    !!q && ((k !== undefined && String(k).toLowerCase().includes(q)) || (!isObj && String(value).toLowerCase().includes(q))),
  );
  $effect(() => {
    if (q && isObj && contains(value, q)) open = true;
  });

  function contains(v: unknown, s: string, budget = { n: 20000 }): boolean {
    if (budget.n-- <= 0) return false;
    if (v === null || typeof v !== 'object') return String(v).toLowerCase().includes(s);
    for (const [kk, vv] of Object.entries(v)) {
      if (kk.toLowerCase().includes(s) || contains(vv, s, budget)) return true;
    }
    return false;
  }

  const cls = (v: unknown) =>
    v === null ? 'h-null' : typeof v === 'string' ? 'h-str' : typeof v === 'number' ? 'h-num' : typeof v === 'boolean' ? 'h-bool' : '';
  const show = (v: unknown) => (typeof v === 'string' ? JSON.stringify(v) : String(v));
  // Click copies what you see: a leaf as `"key": value`, an object or array as its JSON.
  // Alt+click copies the JSON path instead.
  function copyNode(e: MouseEvent) {
    if (e.altKey) return oncopy(path);
    if (isObj) return oncopy(JSON.stringify(value, null, 2));
    const v = JSON.stringify(value) ?? String(value);
    oncopy(typeof k === 'string' ? `${JSON.stringify(k)}: ${v}` : v);
  }
  const keyLabel = $derived(k === undefined ? '' : typeof k === 'number' ? `${k}: ` : `${JSON.stringify(k)}: `);
</script>

<div class="node" class:hit={matchesSelf}>
  {#if isObj}
    <button type="button" class="tw" onclick={() => (open = !open)} aria-expanded={open}>{open ? '▾' : '▸'}</button>
    <span
      class="line"
      role="button"
      tabindex="-1"
      title={t('viewer.copyJson')}
      onclick={copyNode}
      onkeydown={() => {}}
    ><span class="h-key">{keyLabel}</span>{isArr ? '[' : '{'}
      {#if !open}
        <span class="h-null">… {isArr ? t('viewer.items', { n: entries.length }) : t('viewer.keys', { n: entries.length })}</span>{isArr ? ']' : '}'}{last ? '' : ','}
      {:else}
        <span class="h-null">{isArr ? t('viewer.items', { n: entries.length }) : ''}</span>
      {/if}
    </span>
    {#if open}
      <div class="kids">
        {#each entries.slice(0, limit) as [ck, cv], i (ck)}
          <JsonNode k={ck} value={cv} path={path + pathSeg(ck)} depth={depth + 1} {query} last={i === entries.length - 1} {oncopy} />
        {/each}
        {#if entries.length > limit}
          <button type="button" class="more" onclick={() => (limit += PAGE * 5)}>
            {t('viewer.showMore', { n: Math.min(PAGE * 5, entries.length - limit) })}
          </button>
        {/if}
      </div>
      <span class="close">{isArr ? ']' : '}'}{last ? '' : ','}</span>
    {/if}
  {:else}
    <span
      class="line leaf"
      role="button"
      tabindex="-1"
      title={t('viewer.copyKeyValue')}
      onclick={copyNode}
      onkeydown={() => {}}
    ><span class="h-key">{keyLabel}</span><span class={cls(value)}>{show(value)}</span>{last ? '' : ','}
    </span>
  {/if}
</div>

<style>
  .line,
  .close {
    white-space: pre;
  }
  .close {
    display: block;
  }
  /* One 18px gutter everywhere: the toggle, a leaf's indent and the closing bracket
     all start at the same column, so keys and brackets line up at every depth. */
  .kids {
    padding-left: 9px;
    border-left: 1px solid var(--border);
    margin-left: 8px;
  }
  .node {
    position: relative;
  }
  /* The toggle sits in the gutter (absolutely, so no inline whitespace shifts the line). */
  .tw {
    position: absolute;
    left: 0;
    top: 0;
    width: 18px;
    border: 0;
    background: transparent;
    color: var(--faint);
    padding: 0;
    font-size: 12px;
    line-height: inherit;
    text-align: left;
    cursor: pointer;
  }
  .line {
    padding-left: 18px;
  }
  .close {
    padding-left: 18px;
  }
  .line {
    cursor: copy;
    border-radius: 3px;
  }
  .line:hover {
    background: var(--neutral-bg);
  }
  .hit > .line {
    background: var(--warn-bg);
    box-shadow: inset 0 0 0 1px var(--warn);
  }
  .more {
    border: 0;
    background: transparent;
    color: var(--accent-foreground);
    padding: 2px 14px;
  }
</style>
