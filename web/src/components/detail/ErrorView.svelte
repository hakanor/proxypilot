<script lang="ts">
  import { has, t, tlist } from '../../lib/i18n';
  import { cn } from '$lib/utils.js';
  import { Button } from '$lib/components/ui/button/index.js';
  import { badgeVariants } from '$lib/components/ui/badge/index.js';

  interface Props {
    kind: string;
    message?: string;
    host: string;
    onbypass: (host: string) => void;
  }
  let { kind, message, host, onbypass }: Props = $props();

  const key = $derived(has(`error.kinds.${kind}.title`) ? kind : 'unknown');
  const tlsKind = $derived(kind === 'client_tls_rejected' || kind === 'client_tls_error' || kind === 'upstream_tls_error');
  const bareHost = $derived(host.replace(/:\d+$/, ''));
</script>

<div class="px-gutter py-3">
  <div class="mb-2 flex flex-col items-start gap-2 rounded-lg border border-err bg-err-bg p-3">
    <div class="text-[13px] font-semibold text-err">{t(`error.kinds.${key}.title`)}</div>
    <span class={cn(badgeVariants({ variant: 'secondary' }), 'bg-err-bg font-mono text-err')}>{kind}</span>
    <p class="m-0 leading-normal text-foreground">{t(`error.kinds.${key}.body`)}</p>
  </div>
  {#if tlist(`error.kinds.${key}.causes`).length}
    <div class="section-label">{t('error.causes')}</div>
    <ul class="mt-0 mb-2 list-disc pl-5.5 leading-relaxed text-muted-foreground">
      {#each tlist(`error.kinds.${key}.causes`) as c (c)}<li>{c}</li>{/each}
    </ul>
  {/if}
  <div class="section-label">{t('error.fixes')}</div>
  <div class="flex flex-wrap gap-2 px-2 pt-1 pb-2">
    {#if tlsKind}
      <Button variant="secondary" class="bg-accent text-accent-foreground" onclick={() => onbypass(bareHost)}>{t('error.fixBypass')}</Button>
    {/if}
    <Button variant="outline" href="#/setup">{t('error.fixSetup')}</Button>
  </div>
  {#if message}
    <div class="section-label">{t('error.details')}</div>
    <pre class="mx-2 my-0 rounded-md bg-panel p-2 font-mono text-xs break-all whitespace-pre-wrap text-muted-foreground">{message}</pre>
  {/if}
</div>
