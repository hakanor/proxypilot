<script lang="ts">
  import { onMount } from 'svelte';
  import CodeBlock from '../components/CodeBlock.svelte';
  import { inline } from '../lib/md';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Badge } from '$lib/components/ui/badge/index.js';
  import * as Card from '$lib/components/ui/card/index.js';
  import { cn } from '$lib/utils.js';
  import { t } from '../lib/i18n';
  import { api, type UninstallStep } from '../lib/api';

  interface Props {
    onnotice: (msg: string) => void;
  }
  let { onnotice }: Props = $props();

  let steps = $state<UninstallStep[] | null>(null);
  let error = $state<string | null>(null);
  let running = $state(false);

  const autoCount = $derived(steps?.filter((s) => s.auto && s.status === 'pending').length ?? 0);
  const how = (s: UninstallStep) =>
    s.auto ? 'auto' : s.id === 'mac_trust' ? 'admin' : s.id === 'devices' ? 'device' : 'terminal';
  const icon = { pending: '!', clean: '✓', manual: '·', unavailable: '–' } as const;
  const cls = {
    pending: 'bg-warn-bg text-warn',
    clean: 'bg-ok-bg text-ok',
    manual: 'bg-neutral-bg text-neutral-fg',
    unavailable: 'bg-neutral-bg text-neutral-fg',
  } as const;
  const body = (s: UninstallStep) => {
    const key = `uninstall.steps.${s.id}.${s.status === 'unavailable' ? 'clean' : s.status}`;
    const v = t(key);
    return v === key ? '' : v;
  };

  async function load() {
    try {
      [steps, error] = [(await api.uninstallPlan()).steps, null];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(load);

  async function run() {
    running = true;
    try {
      const r = await api.uninstall();
      steps = r.steps;
      const done = r.results.flatMap((x) => x.done ?? []);
      const failed = r.results.filter((x) => !x.ok).map((x) => x.error);
      if (done.length) onnotice(t('uninstall.done', { items: done.join(', ') }));
      if (failed.length) error = failed.join('\n');
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      running = false;
    }
  }
</script>

<main class="flex max-w-[980px] flex-1 flex-col gap-3.5 overflow-auto px-gutter py-5">
  <div class="flex items-end gap-4">
    <div class="flex-1">
      <h1 class="mb-1 text-xl font-semibold">{t('uninstall.title')}</h1>
      <p class="leading-normal text-muted-foreground">{t('uninstall.body')}</p>
    </div>
    <Button disabled={!autoCount || running} onclick={run}>
      {running ? t('uninstall.running') : autoCount ? t('uninstall.run', { n: autoCount }) : t('uninstall.nothing')}
    </Button>
  </div>
  {#if error}<div class="rounded-[7px] bg-err-bg px-2.5 py-2 font-mono text-xs whitespace-pre-wrap text-err">{error}</div>{/if}
  {#each steps ?? [] as s (s.id)}
    <Card.Root class="flex flex-col gap-2 rounded-xl border border-border bg-card px-3.5 py-3 ring-0">
      <div class="flex items-center gap-2.5">
        <Badge class={cn('size-5 rounded-full p-0', cls[s.status])}>{icon[s.status]}</Badge>
        <strong>{t(`uninstall.steps.${s.id}.title`)}</strong>
        <Badge class={cls[s.status]}>{t(`uninstall.states.${s.status}`)}</Badge>
        <span class="flex-1"></span>
        <span class="text-faint">{t(`uninstall.how.${how(s)}`)}</span>
      </div>
      {#if body(s)}<p class="pl-[30px] leading-normal text-muted-foreground">{body(s)}</p>{/if}
      {#if s.details?.length}
        <div class="pl-[30px] font-mono text-xs leading-[1.7] text-muted-foreground">{#each s.details as d (d)}<div>{d}</div>{/each}</div>
      {/if}
      {#if s.commands?.length && (s.status === 'pending' || s.status === 'manual') && !s.auto}
        <div class="pl-[30px]"><CodeBlock lang="bash" code={s.commands.join('\n')} /></div>
      {/if}
    </Card.Root>
  {/each}
  <p class="cli leading-normal text-faint">{@html inline(t('uninstall.cli'))}</p>
</main>

<style>
  .cli :global(code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    color: var(--text);
  }
</style>
