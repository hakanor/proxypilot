<script lang="ts">
  import JsonNode from './JsonNode.svelte';
  import CodeBlock from '../CodeBlock.svelte';
  import { t } from '../../lib/i18n';
  import { api } from '../../lib/api';

  interface Props {
    flowId: string;
    redact: boolean;
    version: string;
    oncopy: (text: string) => void;
  }
  let { flowId, redact, version, oncopy }: Props = $props();

  interface Op {
    query?: string;
    operationName?: string;
    variables?: unknown;
    extensions?: { persistedQuery?: unknown };
  }
  let ops = $state<Op[]>([]);
  let resp = $state<unknown>(null);

  // Re-indent a GraphQL document: braces open a level, fields go one per line;
  // whitespace inside (...) arguments stays on the line.
  function format(q: string): string {
    const src = q.replace(/#[^\n]*/g, '').replace(/\s+/g, ' ').trim();
    let out = '';
    let depth = 0;
    let parens = 0;
    let inStr = false;
    const nl = () => {
      out = out.trimEnd() + '\n' + '  '.repeat(depth);
    };
    for (let i = 0; i < src.length; i++) {
      const c = src[i];
      if (inStr) {
        out += c;
        if (c === '"' && src[i - 1] !== '\\') inStr = false;
        continue;
      }
      if (c === '"') {
        inStr = true;
        out += c;
      } else if (c === '(') {
        parens++;
        out += c;
      } else if (c === ')') {
        parens--;
        out += c;
      } else if (c === '{' && parens === 0) {
        out = out.trimEnd() + ' {';
        depth++;
        nl();
      } else if (c === '}' && parens === 0) {
        depth = Math.max(0, depth - 1);
        nl();
        out += '}';
        if (depth > 0) nl();
      } else if ((c === ' ' || c === ',') && parens === 0 && depth > 0) {
        const next = src[i + 1];
        if (next && next !== '{' && next !== '}' && next !== ' ' && next !== '(' && next !== ':' && out.trimEnd().slice(-1) !== ':') nl();
      } else {
        out += c;
      }
    }
    return out.replace(/\n\s*\n/g, '\n').trim();
  }

  $effect(() => {
    void version;
    const id = flowId, r = redact;
    let cancelled = false;
    Promise.all([api.body(id, 'req', 1 << 20, r), api.body(id, 'resp', 1 << 20, r)]).then(([rq, rs]) => {
      if (cancelled) return;
      const parse = (s?: string) => {
        try {
          return s ? JSON.parse(s) : null;
        } catch {
          return null;
        }
      };
      const body = parse(rq.data);
      let list: Op[] = Array.isArray(body) ? body : body ? [body] : [];
      if (!list.length) {
        // GET ?query=… or application/graphql
        list = [{ query: rq.data ?? '' }];
      }
      ops = list;
      resp = parse(rs.data);
    });
    return () => (cancelled = true);
  });

  const parts = $derived.by(() => {
    const arr = Array.isArray(resp) ? resp : resp ? [resp] : [];
    return arr as { data?: unknown; errors?: unknown }[];
  });
</script>

<div class="flex flex-col gap-1.5 px-gutter py-2">
  {#each ops as op, i (i)}
    {#if ops.length > 1}<div class="section-label px-0 pt-2 pb-0">#{i + 1} {op.operationName ?? ''}</div>{/if}
    {#if op.query}
      <span class="section-label px-0 pt-2 pb-0">{t('graphql.query')}</span>
      <CodeBlock lang="graphql" code={format(op.query)} />
    {:else if op.extensions?.persistedQuery}
      <p class="m-0 text-faint">{t('graphql.persisted')}</p>
    {/if}
    <span class="section-label px-0 pt-2 pb-0">{t('graphql.variables')}</span>
    {#if op.variables && Object.keys(op.variables as object).length}
      <div class="font-mono text-xs leading-[1.7]"><JsonNode value={op.variables} path="$" depth={0} query="" {oncopy} /></div>
    {:else}
      <p class="m-0 text-faint">{t('graphql.noVariables')}</p>
    {/if}
  {/each}

  {#each parts as p, i (i)}
    {#if p.errors}
      <span class="section-label px-0 pt-2 pb-0 text-err">{t('graphql.errors')}</span>
      <div class="rounded-[7px] bg-err-bg px-2 py-1.5 font-mono text-xs leading-[1.7]"><JsonNode value={p.errors} path="$.errors" depth={0} query="" {oncopy} /></div>
    {/if}
    {#if p.data !== undefined}
      <span class="section-label px-0 pt-2 pb-0">{t('graphql.data')}</span>
      <div class="font-mono text-xs leading-[1.7]"><JsonNode value={p.data} path="$.data" depth={0} query="" {oncopy} /></div>
    {/if}
  {:else}
    <p class="m-0 text-faint">{t('graphql.noResponse')}</p>
  {/each}
</div>
