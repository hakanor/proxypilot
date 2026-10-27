<script lang="ts">
  import { onMount } from 'svelte';
  import { inline } from '../lib/md';
  import { t } from '../lib/i18n';
  import { api, APIError, type BPHeader, type Paused } from '../lib/api';
  import { href } from '../lib/route.svelte';
  import { fromRaw, jsonError, prettyJSON, toRaw } from '../lib/httpraw';
  import { notify, enableNotifications, sendNotification } from '../lib/notify.svelte';
  import TabStrip from '../components/detail/TabStrip.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Textarea } from '$lib/components/ui/textarea/index.js';
  import { Kbd } from '$lib/components/ui/kbd/index.js';

  interface Props {
    /** Bumped on every breakpoint.* event. */
    version: number;
    /** #/breakpoints/<id>: select this one (set when a request just paused). */
    focusId?: string;
    onnotice: (msg: string) => void;
    /** Shown as a pop-up over another page: closes itself when the queue empties. */
    modal?: boolean;
    onclose?: () => void;
  }
  let { version, focusId = '', onnotice, modal = false, onclose }: Props = $props();

  type Mode = 'body' | 'header' | 'raw';
  let mode = $state<Mode>('body');
  let rawText = $state('');
  let rawError = $state('');
  let jsonText = $state('');
  let jsonErr = $state('');

  interface Draft {
    method: string;
    url: string;
    status: number;
    headers: BPHeader[];
    body: string;
    dirty: boolean;
  }

  let items = $state<Paused[]>([]);
  let selId = $state<string | null>(null);
  let draft = $state<Draft | null>(null);
  let now = $state(Date.now());
  let last = $state<string | null>(null);
  let busy = $state(false);
  const notifyOn = $derived(notify.permission === 'granted');

  const sel = $derived(items.find((p) => p.id === selId) ?? null);
  const agentHeld = $derived(!!sel && sel.owner.startsWith('agent:'));

  async function load() {
    try {
      const list = (await api.breakpoints()).breakpoints;
      const known = new Set(items.map((p) => p.id));
      for (const p of list) if (!known.has(p.id)) announce(p);
      items = list;
      const wanted = focusId && list.find((p) => p.id === focusId);
      if (wanted && selId !== wanted.id) pick(wanted);
      else if (!sel && list.length) pick(list[0]);
      if (selId && !list.some((p) => p.id === selId)) {
        const next = list[0];
        if (next) pick(next);
        else [selId, draft] = [null, null];
      }
      if (modal && !list.length) onclose?.();
    } catch {
      /* banner covers connection errors */
    }
  }

  function announce(p: Paused) {
    const left = Math.round((new Date(p.expires_at).getTime() - Date.now()) / 1000);
    sendNotification(t('bp.notifyTitle', { method: p.request.method, path: pathOf(p.request.url) }), t('bp.notifyBody', { left }), p.id);
  }

  function pick(p: Paused) {
    selId = p.id;
    const src = p.phase === 'response' && p.response ? p.response : null;
    draft = {
      method: p.request.method,
      url: p.request.url,
      status: src?.status ?? 0,
      headers: (src ? src.headers : p.request.headers).map((h) => ({ ...h })),
      body: src ? src.body : p.request.body,
      dirty: false,
    };
    setMode(mode);
  }

  const phase = $derived(sel?.phase ?? 'request');
  const isJSON = $derived(!!draft && prettyJSON(draft.body) !== null);

  // Every tab edits the same draft; switching re-renders the text from it.
  function setMode(m: Mode) {
    mode = m;
    if (!draft) return;
    if (m === 'raw') [rawText, rawError] = [toRaw(draft, phase), ''];
    if (m === 'body') [jsonText, jsonErr] = [prettyJSON(draft.body) ?? draft.body, ''];
  }
  function onRaw() {
    if (!draft) return;
    const { msg, error } = fromRaw(rawText, phase);
    rawError = error ? t(`bp.rawError.${error}`) : '';
    if (!msg) return;
    if (phase === 'request') [draft.method, draft.url] = [msg.method, msg.url];
    else draft.status = msg.status;
    [draft.headers, draft.body, draft.dirty] = [msg.headers, msg.body, true];
  }
  function onJSON() {
    if (!draft) return;
    jsonErr = isJSON || jsonText.trim().startsWith('{') || jsonText.trim().startsWith('[') ? jsonError(jsonText) : '';
    [draft.body, draft.dirty] = [jsonText, true];
  }

  $effect(() => {
    void version;
    load();
  });
  onMount(() => {
    const id = setInterval(() => (now = Date.now()), 250);
    return () => clearInterval(id);
  });

  const left = (p: Paused) => Math.max(0, (new Date(p.expires_at).getTime() - now) / 1000);
  const total = (p: Paused) => Math.max(1, (new Date(p.expires_at).getTime() - new Date(p.created_at).getTime()) / 1000);
  const pathOf = (u: string) => u.replace(/^https?:\/\/[^/]+/, '') || '/';
  const touch = () => draft && (draft.dirty = true);

  async function decide(action: 'continue' | 'modify' | 'abort') {
    if (!sel || !draft || busy) return;
    busy = true;
    const p = sel;
    try {
      if (action === 'modify') {
        if (p.phase === 'request') {
          await api.resume(p.id, {
            action,
            request: { method: draft.method, url: draft.url, headers: draft.headers.filter((h) => h.name), body: draft.body },
          });
        } else {
          await api.resume(p.id, {
            action,
            response: { status: Number(draft.status), headers: draft.headers.filter((h) => h.name), body: draft.body, encoding: p.response?.encoding },
          });
        }
      } else {
        await api.resume(p.id, { action });
      }
      last = t('bp.last', { method: p.request.method, path: pathOf(p.request.url), result: t(`bp.results.${action}`) });
    } catch (e) {
      onnotice(e instanceof APIError && e.code === 'already_resolved' ? t('bp.alreadyResolved') : String(e));
    } finally {
      busy = false;
      await load();
    }
  }

  async function extend() {
    if (sel) await api.extend(sel.id, 30).catch(() => {});
    load();
  }
  async function takeOver() {
    if (sel) await api.takeOver(sel.id).catch(() => {});
    load();
  }
  const enableNotify = enableNotifications;

  function onkeydown(e: KeyboardEvent) {
    const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement;
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      if (!agentHeld) decide(draft?.dirty ? 'modify' : 'continue');
      return;
    }
    if (typing || e.metaKey || e.ctrlKey || agentHeld) return;
    if (e.key === 'c') decide('continue');
    else if (e.key === 'x') decide('abort');
    else if (e.key === 'n' && items.length) {
      const i = items.findIndex((p) => p.id === selId);
      pick(items[(i + 1) % items.length]);
    }
  }
</script>

<svelte:window {onkeydown} />

<div class="page" class:modal>
  <aside class="queue">
    <div class="qhead">
      <span class="section-label">{t('bp.queue')}</span>
      <span class="grow"></span>
      <button type="button" class="link" onclick={enableNotify} disabled={notifyOn}>{notifyOn ? t('bp.notifyOn') : t('bp.notifyEnable')}</button>
    </div>
    {#each items as p (p.id)}
      {@const l = left(p)}
      <button type="button" class="item" class:on={p.id === selId} onclick={() => pick(p)}>
        <span class="line">
          <span class="phase {p.phase}">{p.phase === 'request' ? t('bp.request') : t('bp.response')}</span>
          <span class="mono method">{p.request.method}</span>
          <span class="mono ellipsis">{pathOf(p.request.url)}</span>
        </span>
        <span class="line small">
          <span class="faint ellipsis">{p.owner.startsWith('agent:') ? p.owner : p.rule_name}</span>
          <span class="grow"></span>
          <span class="mono timer" class:urgent={l <= 10}>{Math.ceil(l)}s</span>
        </span>
        <span class="bar"><span class:urgent={l <= 10} style="width:{(l / total(p)) * 100}%"></span></span>
      </button>
    {/each}
  </aside>

  <main>
    {#if !sel || !draft}
      <div class="empty">
        <h2>{t('bp.emptyTitle')}</h2>
        <p>{@html inline(t('bp.emptyBody'))}</p>
        <Button variant="outline" href={href('rules')}>{t('bp.manageRules')}</Button>
        {#if last}<p class="faint">{last}</p>{/if}
      </div>
    {:else}
      {@const l = left(sel)}
      <div class="head">
        <span class="phase {sel.phase}">{sel.phase === 'request' ? t('bp.requestLong') : t('bp.responseLong')}</span>
        <span class="mono ellipsis min-w-0"><strong>{sel.request.method}</strong> {sel.request.url}</span>
        <span class="grow"></span>
        <span class="faint ellipsis rulename" title={sel.rule_name}>{t('bp.matchedRule')}: {sel.rule_name}</span>
        {#if modal}<button type="button" class="x" aria-label={t('bp.close')} title={t('bp.close')} onclick={() => onclose?.()}>×</button>{/if}
      </div>
      <div class="countdown">
        <span class="mono timer" class:urgent={l <= 10}>{Math.ceil(l)}s</span>
        <span class="bar wide"><span class:urgent={l <= 10} style="width:{(l / total(sel)) * 100}%"></span></span>
        <Button variant="outline" size="sm" onclick={extend}>{t('bp.extend')}</Button>
      </div>
      {#if agentHeld}
        <div class="agent">
          <span>{t('bp.agentHolding', { owner: sel.owner })}</span>
          <Button variant="outline" size="sm" onclick={takeOver}>{t('bp.takeOver')}</Button>
        </div>
      {/if}

      <TabStrip title={sel.phase === 'request' ? t('detail.requestPane') : t('detail.responsePane')} list={['body', 'header', 'raw']} active={mode} pick={(v) => setMode(v as Mode)}>
        {#snippet end()}
          {#if mode === 'body' && isJSON}
            <button type="button" class="link" onclick={() => { jsonText = prettyJSON(jsonText) ?? jsonText; onJSON(); }} disabled={!!jsonErr}>{t('bp.format')}</button>
          {/if}
        {/snippet}
      </TabStrip>

      {#if mode === 'raw'}
        <fieldset class="form" disabled={agentHeld}>
          <div class="section grow-area">
            {#if rawError}<p class="warn">{rawError}</p>{/if}
            <Textarea class="min-w-0 font-mono text-xs" bind:value={rawText} oninput={onRaw} spellcheck="false" aria-label={t('bp.modes.raw')} />
          </div>
        </fieldset>
      {:else if mode === 'body'}
        <fieldset class="form" disabled={agentHeld}>
          <div class="section grow-area">
            {#if sel.truncated}<p class="warn">{t('bp.truncated')}</p>{/if}
            {#if sel.response?.encoding}<p class="faint">{t('bp.encoded', { encoding: sel.response.encoding })}</p>{/if}
            {#if jsonErr}<p class="warn">{t('bp.jsonInvalid', { error: jsonErr })}</p>{/if}
            <Textarea class="min-w-0 font-mono text-xs" bind:value={jsonText} oninput={onJSON} readonly={sel.truncated} spellcheck="false" aria-label={t('detail.tabs.body')} />
          </div>
        </fieldset>
      {:else}
      <fieldset class="form" disabled={agentHeld}>
        {#if sel.phase === 'request'}
          <div class="two">
            <label class="narrow">{t('bp.method')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.method} oninput={touch} /></label>
            <label class="min-w-0">{t('bp.url')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.url} oninput={touch} spellcheck="false" /></label>
          </div>
        {:else}
          <label class="narrow">{t('bp.status')}<Input class="min-w-0 font-mono text-xs" type="number" bind:value={draft.status} oninput={touch} /></label>
          <details>
            <summary class="faint">{t('bp.original')}</summary>
            <div class="mono orig">{sel.request.method} {sel.request.url}</div>
          </details>
        {/if}

        <div class="section">
          <span>{t('bp.headers')}</span>
          {#each draft.headers as h, i (i)}
            <div class="hrow">
              <Input class="min-w-0 font-mono text-xs" bind:value={h.name} oninput={touch} spellcheck="false" />
              <Input class="min-w-0 font-mono text-xs" bind:value={h.value} oninput={touch} spellcheck="false" />
              <button
                type="button"
                class="x"
                aria-label={t('bp.removeHeader')}
                onclick={() => {
                  draft?.headers.splice(i, 1);
                  touch();
                }}>×</button
              >
            </div>
          {/each}
          <button
            type="button"
            class="link"
            onclick={() => {
              draft?.headers.push({ name: '', value: '' });
              touch();
            }}>+ {t('bp.addHeader')}</button
          >
        </div>

      </fieldset>
      {/if}

      <div class="foot">
        <span class="faint">{draft.dirty ? t('bp.edited') : t('bp.onTimeout', { action: t(`bp.timeoutActions.${sel.on_timeout}`) })}</span>
        <span class="grow"></span>
        <Button variant="destructive" disabled={agentHeld || busy} onclick={() => decide('abort')}>{t('bp.abort')} <Kbd>x</Kbd></Button>
        <Button variant="outline" disabled={agentHeld || busy} onclick={() => decide('continue')}>{t('bp.continue')} <Kbd>c</Kbd></Button>
        <Button disabled={agentHeld || busy || !draft.dirty} onclick={() => decide('modify')}>{t('bp.execute')} <Kbd>⌘↵</Kbd></Button>
      </div>
    {/if}
  </main>
</div>

<style>
  .page {
    flex: 1;
    display: flex;
    min-height: 0;
    min-width: 0;
  }
  .rulename {
    max-width: 35%;
    flex-shrink: 1;
  }
  .modal .queue {
    width: 240px;
  }
  .queue {
    width: 300px;
    flex-shrink: 0;
    background: var(--panel);
    border-right: 1px solid var(--border);
    padding: 8px 0;
    overflow-y: auto;
    overflow-x: hidden;
  }
  .qhead {
    display: flex;
    align-items: center;
    padding: 4px 12px 8px;
  }
  .item {
    display: flex;
    flex-direction: column;
    gap: 6px;
    width: calc(100% - 24px);
    margin: 0 12px 6px;
    padding: 10px 12px;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--bg);
    text-align: left;
  }
  .item.on {
    border-color: var(--pp-accent);
    background: var(--sel);
  }
  .line {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }
  .small {
    font-size: 11px;
  }
  .phase {
    padding: 1px 6px;
    border-radius: 5px;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }
  .phase.request {
    background: var(--pp-accent-bg);
    color: var(--pp-accent-text);
  }
  .phase.response {
    background: var(--warn-bg);
    color: var(--warn);
  }
  .method {
    font-weight: 600;
  }
  .ellipsis {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }
  .grow {
    flex: 1;
  }
  .timer {
    font-weight: 600;
    color: var(--warn);
  }
  .timer.urgent {
    color: var(--err);
  }
  .bar {
    position: relative;
    height: 3px;
    border-radius: 2px;
    background: var(--neutral-bg);
    overflow: hidden;
  }
  .bar.wide {
    flex: 1;
    height: 6px;
  }
  .bar span {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    background: var(--warn);
    transition: width 0.25s linear;
  }
  .bar span.urgent {
    background: var(--err);
  }
  main {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px var(--gutter);
    border-bottom: 1px solid var(--border);
  }
  .countdown {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px var(--gutter);
  }
  .agent {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 var(--gutter) 8px;
    padding: 8px 12px;
    border-radius: 8px;
    background: var(--ai-bg);
    border: 1px solid var(--border);
  }
  /* fieldset defaults to min-content width; without this long headers push past the edge. */
  .form {
    min-width: 0;
    flex: 1;
    min-height: 0;
    overflow: auto;
    border: 0;
    margin: 0;
    padding: 8px var(--gutter);
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .form:disabled {
    opacity: 0.6;
  }
  label,
  .section {
    display: flex;
    flex-direction: column;
    gap: 5px;
    color: var(--pp-muted);
  }
  .two {
    display: flex;
    gap: 10px;
  }
  .two label:last-child {
    flex: 1;
    min-width: 0;
  }
  .narrow {
    width: 110px;
  }
  .hrow {
    display: grid;
    grid-template-columns: 200px minmax(0, 1fr) 24px;
    gap: 6px;
  }
  .x {
    border: 0;
    background: transparent;
    color: var(--faint);
  }
  .grow-area {
    flex: 1;
    min-height: 160px;
  }
  .grow-area :global(textarea) {
    flex: 1;
    min-height: 160px;
    resize: vertical;
  }
  .orig {
    color: var(--pp-muted);
    padding: 4px 0;
  }
  .link {
    align-self: flex-start;
    border: 0;
    background: transparent;
    color: var(--pp-accent-text);
    padding: 2px 0;
  }
  .link:disabled {
    color: var(--faint);
  }
  .foot {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px var(--gutter);
    border-top: 1px solid var(--border);
  }
  .empty {
    margin: auto;
    max-width: 460px;
    text-align: center;
    display: flex;
    flex-direction: column;
    gap: 8px;
    align-items: center;
    color: var(--pp-muted);
  }
  .empty h2 {
    margin: 0;
    font-size: 14px;
    color: var(--text);
  }
  .empty p {
    margin: 0;
    line-height: 1.5;
  }
  .empty :global(code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    color: var(--text);
  }
  .faint {
    color: var(--faint);
  }
  .warn {
    color: var(--warn);
    margin: 0;
  }
</style>
