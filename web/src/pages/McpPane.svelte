<script lang="ts">
  import CodeBlock from '../components/CodeBlock.svelte';
  import * as Card from '$lib/components/ui/card/index.js';
  import { t } from '../lib/i18n';

  const cardCls = 'gap-0 overflow-hidden rounded-xl border border-border bg-card py-0 ring-0';
  const headCls = 'flex items-center gap-2.5 border-b border-border px-3.5 py-2.5';
  const cursorConfig = `{
  "mcpServers": {
    "proxypilot": { "command": "proxypilot", "args": ["mcp", "--agent-name", "cursor"] }
  }
}`;
</script>

<h1 class="mb-1 text-lg font-semibold">{t('setup.mcp.title')}</h1>
<p class="mb-[18px] max-w-[720px] leading-normal text-muted-foreground">{t('setup.mcp.body')}</p>

<div class="flex max-w-[720px] flex-col gap-[18px]">
  <Card.Root class={cardCls}>
    <div class={headCls}><strong>Claude Code</strong></div>
    <div class="flex flex-col gap-2.5 px-3.5 py-2.5">
      <CodeBlock lang="bash" code="claude mcp add -s user proxypilot -- proxypilot mcp --agent-name claude" />
      <span class="text-faint">{t('setup.mcp.claudeNote')}</span>
    </div>
  </Card.Root>

  <Card.Root class={cardCls}>
    <div class={headCls}><strong>{t('setup.mcp.otherTitle')}</strong></div>
    <div class="flex flex-col gap-2.5 px-3.5 py-2.5">
      <CodeBlock lang="json" code={cursorConfig} />
      <span class="text-faint">{t('setup.mcp.otherNote')}</span>
    </div>
  </Card.Root>

  <Card.Root class={cardCls}>
    <div class={headCls}><strong>{t('setup.mcp.tryTitle')}</strong></div>
    <p class="px-3.5 py-2.5 leading-normal text-muted-foreground">{t('setup.mcp.tryBody')}</p>
  </Card.Root>
</div>
