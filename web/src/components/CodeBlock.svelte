<script lang="ts">
  import { t } from '../lib/i18n';
  import { copyText } from '../lib/detail';
  import { Button } from '$lib/components/ui/button/index.js';

  interface Props {
    code: string;
    lang?: string;
  }
  let { code, lang = '' }: Props = $props();
  let copied = $state(false);

  async function copy() {
    if (await copyText(code)) {
      copied = true;
      setTimeout(() => (copied = false), 1200);
    }
  }
</script>

<div class="overflow-hidden rounded-lg border border-border bg-panel">
  <div class="flex min-h-7 items-center border-b border-border py-1 pr-1.5 pl-2.5 text-faint">
    <span>{lang}</span>
    <span class="flex-1"></span>
    <Button variant="outline" size="xs" onclick={copy}>{copied ? t('docs.copied') : t('docs.copy')}</Button>
  </div>
  <pre class="m-0 px-3 py-2.5 font-mono text-xs leading-[1.7] break-all whitespace-pre-wrap text-foreground">{code}</pre>
</div>
