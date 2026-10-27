<script lang="ts">
  import { t } from '../lib/i18n';
  import { parseQuery, protoTypes, contentTypes, type TypeFilter } from '../lib/filter';
  import { Kbd } from '$lib/components/ui/kbd/index.js';
  import { cn } from '$lib/utils.js';

  interface Props {
    typeFilter: TypeFilter;
    query: string;
    errorsOnly: boolean;
    autoScroll: boolean;
    filterInput?: HTMLInputElement;
  }
  let {
    typeFilter = $bindable(),
    query = $bindable(),
    errorsOnly = $bindable(),
    autoScroll = $bindable(),
    filterInput = $bindable(),
  }: Props = $props();

  let parsed = $derived(parseQuery(query));

  function removeChip(i: number) {
    const c = parsed.chips[i];
    query = query
      .split(/\s+/)
      .filter((tok) => tok.toLowerCase() !== `${c.key}:${c.value}`.toLowerCase())
      .join(' ');
    filterInput?.focus();
  }
</script>

{#snippet seg(k: TypeFilter)}
  <button
    type="button"
    aria-pressed={typeFilter === k}
    onclick={() => (typeFilter = k)}
    class={cn(
      'whitespace-nowrap rounded-[7px] border-0 px-2.5 py-1 text-xs',
      typeFilter === k ? 'bg-neutral-bg font-semibold text-foreground' : 'bg-transparent text-muted-foreground hover:text-foreground',
    )}>{t(`typeStrip.${k}`)}</button
  >
{/snippet}

<div class="flex h-[38px] min-w-0 shrink-0 items-center gap-0.5 border-b border-border px-gutter text-[13px]" role="toolbar" aria-label={t('toolbar.typeFilter')}>
  <div class="flex min-w-0 items-center gap-0.5 overflow-x-auto">
    {#each protoTypes as k (k)}{@render seg(k)}{/each}
    <span class="mx-1.5 h-4 w-px shrink-0 bg-border"></span>
    {#each contentTypes as k (k)}{@render seg(k)}{/each}
  </div>
  <span class="flex-1"></span>
  {#each [{ on: errorsOnly, label: 'toolbar.errorsOnly', flip: () => (errorsOnly = !errorsOnly) }, { on: autoScroll, label: 'toolbar.autoScroll', flip: () => (autoScroll = !autoScroll) }] as p (p.label)}
    <button
      type="button"
      aria-pressed={p.on}
      onclick={p.flip}
      class={cn(
        'mr-1 whitespace-nowrap rounded-full border-0 px-2.5 py-1 text-xs',
        p.on ? 'bg-accent text-accent-foreground' : 'bg-transparent text-muted-foreground hover:text-foreground',
      )}>{t(p.label)}</button
    >
  {/each}
  <label
    class="ml-1 flex h-7 w-[280px] min-w-[160px] shrink items-center gap-1.5 overflow-hidden rounded-[8px] border border-input bg-field px-2.5 text-faint focus-within:border-primary"
  >
    <svg class="shrink-0" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"
      ><circle cx="11" cy="11" r="7"></circle><path d="m20 20-3.5-3.5"></path></svg
    >
    {#each parsed.chips as c, i (c.key + c.value + i)}
      <button
        type="button"
        class="whitespace-nowrap rounded-full border-0 bg-accent px-2 py-px text-[11px] font-medium text-accent-foreground"
        title={t('toolbar.removeChip', { chip: `${c.key}:${c.value}` })}
        onclick={() => removeChip(i)}>{c.key}: {c.value} ×</button
      >
    {/each}
    <input
      class="min-w-[40px] flex-1 border-0 bg-transparent text-xs text-foreground outline-none"
      bind:this={filterInput}
      bind:value={query}
      placeholder={t('toolbar.filterPlaceholder')}
      spellcheck="false"
      autocomplete="off"
      onkeydown={(e) => e.key === 'Escape' && (e.currentTarget as HTMLInputElement).blur()}
    />
    <Kbd>/</Kbd>
  </label>
</div>
