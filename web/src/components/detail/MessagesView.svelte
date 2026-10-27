<script lang="ts">
  import { t } from '../../lib/i18n';
  import { api, type WSMessage } from '../../lib/api';
  import { formatBytes } from '../../lib/format';
  import { cn } from '$lib/utils.js';

  // WebSocket messages of one connection, oldest first. Polls for new ones while the
  // connection is open; click a message to see it in full (JSON pretty-printed).
  interface Props {
    flowId: string;
    oncopy: (text: string) => void;
  }
  let { flowId, oncopy }: Props = $props();

  const KEEP = 5000; // newest messages kept in the view
  let messages = $state<WSMessage[]>([]);
  let total = $state(0);
  let open = $state<number | null>(null);
  let query = $state('');
  let dir = $state<'all' | 'up' | 'down'>('all');
  let follow = $state(true);
  let list = $state<HTMLDivElement>();

  $effect(() => {
    const id = flowId;
    let after = 0;
    let stop = false;
    messages = [];
    open = null;
    async function pull() {
      while (!stop) {
        const p = await api.messages(id, after, 1000).catch(() => null);
        if (stop || !p) return;
        total = p.total;
        if (!p.messages.length) return;
        messages = [...messages, ...p.messages].slice(-KEEP);
        after = p.next;
        if (p.messages.length < 1000) return;
      }
    }
    pull();
    const timer = setInterval(pull, 1000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  });

  const text = (m: WSMessage) => m.text ?? (m.base64 ? `base64: ${m.base64}` : '');
  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return messages.filter((m) => (dir === 'all' || m.dir === dir) && (!q || text(m).toLowerCase().includes(q)));
  });

  // Stay at the bottom as messages arrive unless the user scrolled up.
  $effect(() => {
    void shown.length;
    if (follow && list) queueMicrotask(() => list && (list.scrollTop = list.scrollHeight));
  });
  function onscroll() {
    if (list) follow = list.scrollHeight - list.scrollTop - list.clientHeight < 40;
  }

  function pretty(m: WSMessage): string {
    const s = m.text ?? '';
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return text(m);
    }
  }
  const time = (iso: string) => {
    const d = new Date(iso);
    return d.toLocaleTimeString([], { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0');
  };
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="flex shrink-0 items-center gap-2 px-4 pb-1.5 pt-1">
    <input
      class="h-7 min-w-0 flex-1 rounded-md border border-input bg-field px-2.5 text-xs text-foreground outline-none placeholder:text-faint focus:border-primary"
      placeholder={t('ws.filter')}
      bind:value={query}
    />
    {#each ['all', 'up', 'down'] as const as d (d)}
      <button
        type="button"
        class={cn('h-7 rounded-md px-2 text-xs', dir === d ? 'bg-accent font-medium text-accent-foreground' : 'text-muted-foreground hover:text-foreground')}
        onclick={() => (dir = d)}>{t(`ws.dir.${d}`)}</button
      >
    {/each}
    <span class="shrink-0 text-xs text-faint">{t('ws.count', { n: shown.length, total })}</span>
  </div>
  <div class="min-h-0 flex-1 overflow-auto font-mono text-xs" bind:this={list} {onscroll}>
    {#each shown as m (m.seq)}
      <button
        type="button"
        class={cn('grid w-full grid-cols-[92px_16px_52px_64px_minmax(0,1fr)] items-baseline gap-2 border-b border-border/60 px-4 py-1 text-left hover:bg-hover', open === m.seq && 'bg-sel')}
        onclick={() => (open = open === m.seq ? null : m.seq)}
      >
        <span class="text-faint">{time(m.at)}</span>
        <span class={m.dir === 'up' ? 'text-ok' : 'text-c4'} title={t(`ws.dir.${m.dir}`)}>{m.dir === 'up' ? '↑' : '↓'}</span>
        <span class="text-muted-foreground">{m.opcode}</span>
        <span class="text-right text-faint">{formatBytes(m.size)}</span>
        <span class="truncate text-foreground">{text(m)}</span>
      </button>
      {#if open === m.seq}
        <div class="border-b border-border bg-panel px-4 py-2">
          <div class="mb-1.5 flex items-center gap-3 font-sans text-xs text-faint">
            <span>{t(`ws.dir.${m.dir}`)} · {m.opcode} · {formatBytes(m.size)}</span>
            {#if m.truncated}<span class="text-warn">{t('ws.truncated')}</span>{/if}
            <span class="grow"></span>
            <button type="button" class="text-accent-foreground hover:underline" onclick={() => oncopy(m.text ?? m.base64 ?? '')}>{t('ws.copy')}</button>
          </div>
          <pre class="m-0 max-h-[320px] overflow-auto whitespace-pre-wrap break-all text-foreground">{pretty(m)}</pre>
        </div>
      {/if}
    {:else}
      <p class="mx-4 my-3 font-sans text-faint">{total ? t('ws.noMatch') : t('ws.empty')}</p>
    {/each}
  </div>
</div>
