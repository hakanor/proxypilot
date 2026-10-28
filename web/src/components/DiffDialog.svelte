<script lang="ts">
  import { onMount } from 'svelte';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import { cn } from '$lib/utils.js';
  import { t } from '../lib/i18n';
  import { api, type DiffChange, type DiffSide, type FlowDiff } from '../lib/api';

  interface Props {
    a: string;
    b: string;
    /** Row numbers shown in the title (#12 → #15). */
    labels: [string, string];
    onclose: () => void;
  }
  let { a, b, labels, onclose }: Props = $props();

  let d = $state<FlowDiff | null>(null);
  let error = $state<string | null>(null);
  onMount(() => {
    api.diff(a, b).then((r) => (d = r), (e) => (error = e instanceof Error ? e.message : String(e)));
  });

  const kindCls: Record<DiffChange['kind'], string> = { added: 'text-ok', removed: 'text-err', changed: 'text-warn' };
  const lineCls: Record<string, string> = { '-': 'bg-err-bg text-err', '+': 'bg-ok-bg text-ok', ' ': 'text-muted-foreground', '…': 'text-faint' };
  const count = (s: DiffSide) => (s.line?.length ?? 0) + (s.headers?.length ?? 0) + (s.body.changes?.length ?? 0) + (s.body.lines?.filter((l) => l.op === '-' || l.op === '+').length ?? 0);
</script>

{#snippet changes(title: string, list: DiffChange[] | null | undefined)}
  {#if list?.length}
    <div class="mb-3">
      <div class="mb-1 text-xs font-medium text-muted-foreground">{title}</div>
      <div class="overflow-hidden rounded-lg border border-border font-mono text-xs">
        <div class="grid grid-cols-[minmax(120px,1fr)_2fr_2fr] gap-2 border-b border-border bg-panel px-2.5 py-1.5 font-sans text-faint">
          <span></span><span>{labels[0]}</span><span>{labels[1]}</span>
        </div>
        {#each list as c (c.path)}
          <div class="grid grid-cols-[minmax(120px,1fr)_2fr_2fr] gap-2 border-b border-border px-2.5 py-1.5 last:border-b-0">
            <span class={cn('break-all', kindCls[c.kind])} title={t(`diff.kinds.${c.kind}`)}>{c.path}</span>
            <span class={cn('break-all whitespace-pre-wrap', c.kind !== 'added' && 'text-err')}>{c.a ?? '—'}</span>
            <span class={cn('break-all whitespace-pre-wrap', c.kind !== 'removed' && 'text-ok')}>{c.b ?? '—'}</span>
          </div>
        {/each}
      </div>
    </div>
  {/if}
{/snippet}

{#snippet side(title: string, s: DiffSide)}
  <section class="mb-5">
    <h3 class="mb-2 flex items-baseline gap-2 text-sm font-semibold">
      {title}
      <span class="text-xs font-normal text-faint">{count(s) ? t('diff.changes', { n: count(s) }) : t('diff.noChanges')}</span>
    </h3>
    {@render changes(t('diff.line'), s.line)}
    {@render changes(t('diff.headers'), s.headers)}
    {#if s.body.mode === 'json' || s.body.mode === 'binary'}
      {@render changes(t('diff.body'), s.body.changes)}
    {:else if s.body.mode === 'text'}
      <div class="mb-1 text-xs font-medium text-muted-foreground">{t('diff.body')}</div>
      <pre class="overflow-auto rounded-lg border border-border font-mono text-xs leading-[1.6]">{#each s.body.lines ?? [] as l, i (i)}<div class={cn('px-2.5', lineCls[l.op])}><span class="mr-2 inline-block w-3 select-none">{l.op === ' ' ? '' : l.op}</span>{l.text ?? ''}</div>{/each}</pre>
    {/if}
    {#if s.body.truncated}<p class="mt-1 text-xs text-faint">{t('diff.truncated')}</p>{/if}
  </section>
{/snippet}

<Dialog.Root open onOpenChange={(o) => !o && onclose()}>
  <Dialog.Content class="flex max-h-[calc(100%-64px)] w-[min(1000px,calc(100%-32px))] flex-col gap-3 overflow-auto sm:max-w-none">
    <Dialog.Title>{t('diff.title', { a: labels[0], b: labels[1] })}</Dialog.Title>
    {#if error}
      <div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>
    {:else if !d}
      <p class="text-muted-foreground">{t('diff.loading')}</p>
    {:else}
      <div class="grid grid-cols-2 gap-3 font-mono text-xs">
        {#each [[labels[0], d.a], [labels[1], d.b]] as const as [label, r] (label)}
          <div class="min-w-0 rounded-lg border border-border px-2.5 py-2">
            <span class="font-sans text-faint">{label}</span>
            <div class="truncate" title={r.url}>{r.method} {r.url}</div>
            {#if r.status}<div class="text-muted-foreground">{r.status}</div>{/if}
          </div>
        {/each}
      </div>
      {#if d.same}
        <p class="text-muted-foreground">{t('diff.same')}</p>
      {:else}
        {@render side(t('diff.request'), d.request)}
        {@render side(t('diff.response'), d.response)}
      {/if}
    {/if}
  </Dialog.Content>
</Dialog.Root>
