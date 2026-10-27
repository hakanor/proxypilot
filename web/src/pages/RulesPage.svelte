<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '../lib/i18n';
  import { api, type RuleContent, type Rule, type RuleInput, type RewriteStep } from '../lib/api';
  import { formatCount } from '../lib/format';
  import { href } from '../lib/route.svelte';
  import { inline } from '../lib/md';
  import { Button } from '$lib/components/ui/button/index.js';
  import { Input } from '$lib/components/ui/input/index.js';
  import { Textarea } from '$lib/components/ui/textarea/index.js';
  import * as Select from '$lib/components/ui/select/index.js';
  import * as Tabs from '$lib/components/ui/tabs/index.js';
  import { Switch } from '$lib/components/ui/switch/index.js';
  import { Checkbox } from '$lib/components/ui/checkbox/index.js';
  import * as Dialog from '$lib/components/ui/dialog/index.js';
  import ContextMenu from '../components/ContextMenu.svelte';
  import type { Item } from '../lib/menu';
  import Trash2Icon from '@lucide/svelte/icons/trash-2';

  interface Props {
    selectedId: string;
    /** Bumped on every rule.changed event. */
    version: number;
    onnotice: (msg: string) => void;
    /** Pop-up mode: only the editor; Save and Cancel close it. */
    modal?: boolean;
    onclose?: () => void;
  }
  let { selectedId, version, onnotice, modal = false, onclose }: Props = $props();

  type Source = 'file' | 'dir' | 'inline';
  interface Draft {
    type: Rule['type'];
    to: string;
    preserveHost: boolean;
    steps: RewriteStep[];
    phase: 'request' | 'response' | 'both';
    timeout: string;
    onTimeout: 'continue' | 'abort';
    name: string;
    url: string;
    regex: boolean;
    methods: string;
    matchHeaders: string;
    gqlOp: string;
    priority: number;
    owner: string;
    ttl: string;
    source: Source;
    path: string;
    body: string;
    status: number;
    headers: string;
    mode: 'full' | 'body_only';
    enabled: boolean;
    expires_at?: string;
  }

  let rules = $state<Rule[]>([]);
  let draft = $state<Draft | null>(null);
  let editing = $state<string | null>(null); // rule id, or '' for a new rule
  let error = $state<string | null>(null);
  // Map Local file rules: the file's response, shown and edited right in the editor.
  let fileInfo = $state<RuleContent | null>(null);
  let fileText = $state('');
  let fileOrig = '';
  async function loadContent(id: string) {
    fileInfo = null;
    try {
      const c = await api.ruleContent(id);
      if (editing !== id) return;
      fileInfo = c;
      fileOrig = c.content;
      fileText = prettyIfJSON(c.content);
    } catch {
      /* rule gone or not map_local */
    }
  }
  // JSON first: show JSON pretty-printed; saving keeps that formatting.
  function prettyIfJSON(v: string): string {
    try {
      return JSON.stringify(JSON.parse(v), null, 2);
    } catch {
      return v;
    }
  }
  function jsonErr(v: string): string {
    const s = v.trim();
    if (!s || !(s.startsWith('{') || s.startsWith('['))) return '';
    try {
      JSON.parse(s);
      return '';
    } catch (e) {
      return e instanceof Error ? e.message : String(e);
    }
  }
  let now = $state(Date.now());

  async function load() {
    try {
      rules = (await api.rules()).rules;
      // The open rule was deleted or expired elsewhere (CLI, agent, TTL).
      if (editing && !rules.some((r) => r.id === editing)) close();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  onMount(() => {
    const id = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(id);
  });
  $effect(() => {
    void version;
    load();
  });
  // #/rules/new~<type>~<url>: a new rule prefilled from a right-click in Traffic.
  $effect(() => {
    if (!selectedId.startsWith('new~')) return;
    const [, type, ...rest] = selectedId.split('~');
    newRule();
    if (draft) {
      draft.type = (['map_local', 'breakpoint', 'rewrite', 'map_remote'].includes(type) ? type : 'map_local') as Draft['type'];
      draft.url = rest.join('~');
      if (draft.type === 'rewrite') draft.phase = 'response';
    }
  });
  // Open the rule named in the URL (#/rules/<id>), e.g. after "Save as Map Local".
  $effect(() => {
    if (selectedId && !selectedId.startsWith('new~') && editing !== selectedId) {
      const r = rules.find((x) => x.id === selectedId);
      if (r) open(r);
    }
  });

  const shown = $derived(rules);

  const lines = (m?: Record<string, string>) =>
    Object.entries(m ?? {})
      .map(([k, v]) => `${k}: ${v}`)
      .join('\n');
  const parseLines = (s: string) => {
    const out: Record<string, string> = {};
    for (const line of s.split('\n')) {
      const i = line.indexOf(':');
      if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
    }
    return Object.keys(out).length ? out : undefined;
  };
  const b64decode = (s: string) => {
    try {
      return new TextDecoder().decode(Uint8Array.from(atob(s), (c) => c.charCodeAt(0)));
    } catch {
      return '';
    }
  };

  function open(r: Rule) {
    editing = r.id;
    error = null;
    const s = r.spec;
    fileInfo = null;
    if (r.type === 'map_local' && s.file) loadContent(r.id);
    draft = {
      type: r.type,
      to: s.to ?? '',
      preserveHost: !!s.preserve_host_header,
      steps: (s.steps ?? []).map((x) => ({ ...x })),
      phase: (r.phase as Draft['phase']) ?? 'request',
      timeout: s.timeout ?? '',
      onTimeout: s.on_timeout ?? 'continue',
      name: r.name,
      url: r.match.url,
      regex: !!r.match.regex,
      methods: (r.match.methods ?? []).join(', '),
      matchHeaders: lines(r.match.headers),
      gqlOp: r.match.graphql_operation ?? '',
      priority: r.priority,
      owner: r.owner,
      ttl: '',
      source: s.file ? 'file' : s.dir ? 'dir' : 'inline',
      path: s.file ?? s.dir ?? '',
      body: prettyIfJSON(s.body ?? (s.body_base64 ? b64decode(s.body_base64) : '')),
      status: s.status ?? 200,
      headers: lines(s.headers),
      mode: s.mode ?? 'full',
      enabled: r.enabled,
      expires_at: r.expires_at,
    };
  }

  function newRule() {
    editing = '';
    error = null;
    draft = {
      type: 'map_local', phase: 'request', timeout: '', onTimeout: 'continue', to: '', preserveHost: false,
      steps: [{ type: 'set_header', name: '', value: '' }],
      name: '', url: '', regex: false, methods: '', matchHeaders: '', gqlOp: '', priority: 0, owner: 'user', ttl: '',
      source: 'inline', path: '', body: '{\n  "ok": true\n}', status: 200, headers: '', mode: 'full', enabled: true,
    };
  }

  function close() {
    editing = null;
    draft = null;
    if (modal) return onclose?.();
    if (selectedId) location.hash = href('rules');
  }

  const stepTypes = ['set_header', 'add_header', 'remove_header', 'set_query', 'remove_query', 'replace_body', 'json_set', 'json_remove', 'set_status', 'set_url', 'set_path'];
  // Which fields each step type uses.
  const stepFields: Record<string, ('name' | 'value' | 'pattern' | 'regex' | 'path' | 'status')[]> = {
    set_header: ['name', 'value'], add_header: ['name', 'value'], remove_header: ['name'],
    set_query: ['name', 'value'], remove_query: ['name'],
    replace_body: ['pattern', 'value', 'regex'], json_set: ['path', 'value'], json_remove: ['path'],
    set_status: ['status'], set_url: ['value'], set_path: ['value'],
  };
  function cleanStep(s: RewriteStep): RewriteStep {
    const out: RewriteStep = { type: s.type };
    for (const f of stepFields[s.type] ?? []) {
      const v = s[f];
      if (f === 'status') out.status = Number(v) || undefined;
      else if (f === 'regex') out.regex = !!v || undefined;
      else if (v !== undefined && v !== '') (out as unknown as Record<string, unknown>)[f] = v;
    }
    return out;
  }

  function toInput(d: Draft, permanent = false): RuleInput {
    let spec: Rule['spec'];
    if (d.type === 'breakpoint') {
      spec = { timeout: d.timeout.trim() || undefined, on_timeout: d.onTimeout };
    } else if (d.type === 'map_remote') {
      spec = { to: d.to.trim(), preserve_host_header: d.preserveHost || undefined };
    } else if (d.type === 'rewrite') {
      spec = { steps: d.steps.map(cleanStep) };
    } else {
      spec = { status: Number(d.status) || 200, headers: parseLines(d.headers), mode: d.mode };
      if (d.source === 'file') spec.file = d.path.trim();
      else if (d.source === 'dir') spec.dir = d.path.trim();
      else spec.body = d.body;
    }
    return {
      name: d.name.trim(),
      type: d.type,
      phase: d.type === 'breakpoint' || d.type === 'rewrite' ? d.phase : 'request',
      enabled: d.enabled,
      owner: d.owner,
      expires_at: permanent ? undefined : d.expires_at,
      match: {
        url: d.url.trim(),
        regex: d.regex,
        methods: d.methods.split(',').map((m) => m.trim().toUpperCase()).filter(Boolean),
        headers: parseLines(d.matchHeaders),
        graphql_operation: d.gqlOp.trim() || undefined,
      },
      spec,
      priority: Number(d.priority) || 0,
      ttl: d.ttl.trim() || undefined,
    };
  }

  async function save(permanent = false) {
    if (!draft) return;
    try {
      const input = toInput(draft, permanent);
      const r = editing ? await api.updateRule(editing, input) : await api.addRule(input);
      if (editing && fileInfo?.editable && draft.source === 'file' && fileText !== prettyIfJSON(fileOrig)) {
        await api.setRuleContent(r.id, fileText);
      }
      onnotice(t('rules.saved', { name: r.name }));
      if (modal) return close();
      await load();
      open(r);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  // Resizable columns. Each divider sits between two columns and follows the pointer: the
  // neighbours trade width (the flexible Rule column just absorbs its side). Double-click resets.
  const COLS = ['type', 'owner', 'expires', 'hits'] as const;
  const DIV = ['rule', ...COLS] as const; // column order; a divider follows each but the last
  const DEF: Record<string, number> = { type: 96, owner: 130, expires: 100, hits: 70 };
  const MIN_W = 24;
  let widths = $state<Record<string, number>>(
    (() => {
      try {
        return { ...DEF, ...JSON.parse(localStorage.getItem('pp.cols.rules') ?? '{}') };
      } catch {
        return { ...DEF };
      }
    })(),
  );
  const gridCols = $derived(`44px minmax(60px, 1fr) ${COLS.map((c) => widths[c] + 'px').join(' ')} 36px`);
  function saveWidths() {
    try {
      localStorage.setItem('pp.cols.rules', JSON.stringify(widths));
    } catch {
      /* per-viewer convenience only */
    }
  }
  function startResize(e: PointerEvent, i: number) {
    e.preventDefault();
    const [a, b] = [DIV[i], DIV[i + 1]];
    const x0 = e.clientX;
    const [wa, wb] = [widths[a] ?? 0, widths[b]];
    const move = (m: PointerEvent) => {
      let dx = Math.round(m.clientX - x0);
      if (a !== 'rule') dx = Math.max(dx, MIN_W - wa);
      dx = Math.min(dx, wb - MIN_W);
      if (a !== 'rule') widths[a] = wa + dx;
      widths[b] = wb - dx;
    };
    const up = () => {
      removeEventListener('pointermove', move);
      removeEventListener('pointerup', up);
      document.body.style.cursor = '';
      saveWidths();
    };
    document.body.style.cursor = 'col-resize';
    addEventListener('pointermove', move);
    addEventListener('pointerup', up);
  }
  function resetPair(i: number) {
    for (const c of [DIV[i], DIV[i + 1]]) if (c in DEF) widths[c] = DEF[c];
    saveWidths();
  }

  // Right-click on a rule row.
  let menu = $state<{ r: Rule; x: number; y: number } | null>(null);
  function menuItems(r: Rule): Item[] {
    return [
      { label: t('rules.edit'), action: () => open(r) },
      { label: r.enabled ? t('rules.disable') : t('rules.enable'), action: () => toggle(r) },
      { label: t('rules.duplicate'), action: () => duplicate(r) },
      { label: t('rules.copyPattern'), action: () => navigator.clipboard?.writeText(r.match.url) },
      { label: t('rules.delete'), sep: true, action: () => removeRow(r) },
    ];
  }
  async function duplicate(r: Rule) {
    try {
      const { id: _id, hits: _h, created_at: _c, expires_at: _e, ...rest } = r as Rule & Record<string, unknown>;
      const copy = await api.addRule({ ...(rest as unknown as RuleInput), name: t('rules.copyName', { name: r.name }) });
      await load();
      open(copy);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  // Row trash button: deletes right away, no confirm.
  async function removeRow(r: Rule) {
    try {
      await api.deleteRule(r.id);
      if (editing === r.id) close();
      onnotice(t('rules.deleted', { name: r.name }));
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function remove() {
    if (!editing || !draft || !confirm(t('rules.confirmDelete', { name: draft.name }))) return;
    await api.deleteRule(editing);
    onnotice(t('rules.deleted', { name: draft.name }));
    close();
    load();
  }

  async function toggle(r: Rule) {
    await api.toggleRule(r.id, !r.enabled);
    load();
  }



  function left(iso?: string): string {
    if (!iso) return t('rules.never');
    const s = Math.max(0, Math.round((new Date(iso).getTime() - now) / 1000));
    const m = Math.floor(s / 60);
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m ${String(s % 60).padStart(2, '0')}s`;
  }
  const soon = (iso?: string) => !!iso && new Date(iso).getTime() - now < 60_000;
  const matchText = (r: Rule) =>
    `${r.match.methods?.length ? r.match.methods.join(',') + ' ' : ''}${r.match.url || '*'}${r.spec.dir ? `  →  ${r.spec.dir}` : ''}${r.spec.to ? `  →  ${r.spec.to}` : ''}${r.spec.steps ? `  (${r.spec.steps.length} steps)` : ''}`;
</script>

{#if menu}<ContextMenu x={menu.x} y={menu.y} items={menuItems(menu.r)} onclose={() => (menu = null)} />{/if}
{#snippet editorView()}
{#if draft}
    <aside class="editor">
      <div class="head">
        <strong>{editing ? t('rules.edit') : t('rules.create')}</strong>
        <span class="grow"></span>
        <span class="pill neutral">{t(`rules.types.${draft.type}`)}</span>
      </div>
      <div class="form">
        {#if !editing}
          <label
            >{t('rules.fields.type')}
            <Select.Root type="single" bind:value={draft.type}>
              <Select.Trigger class="w-full">{t(`rules.types.${draft.type}`)}</Select.Trigger>
              <Select.Content>
                {#each [['map_local', t('rules.types.map_local')], ['breakpoint', t('rules.types.breakpoint')], ['rewrite', t('rules.types.rewrite')], ['map_remote', t('rules.types.map_remote')]] as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
          </label>
        {/if}
        <label>{t('rules.fields.name')}<Input class="min-w-0" bind:value={draft.name} /></label>
        <label>{t('rules.fields.url')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.url} placeholder="https://api.example.com/v2/feed*" spellcheck="false" /></label>
        <label class="check"><Checkbox bind:checked={draft.regex} /> {t('rules.fields.regex')}</label>
        <div class="two">
          <label>{t('rules.fields.methods')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.methods} placeholder="GET, POST" /></label>
          <label>{t('rules.fields.priority')}<Input class="min-w-0 font-mono text-xs" type="number" bind:value={draft.priority} /></label>
        </div>
        <label>{t('rules.fields.gqlOp')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.gqlOp} placeholder="FeedQuery" spellcheck="false" /></label>
        <label>{t('rules.fields.matchHeaders')}<Textarea class="min-h-0 resize-y font-mono text-xs" rows={2} bind:value={draft.matchHeaders} spellcheck="false" /></label>

        {#if draft.type === 'map_remote'}
          <label>{t('rules.fields.to')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.to} placeholder="https://staging.example.com/*" spellcheck="false" /></label>
          <label class="check"><Checkbox bind:checked={draft.preserveHost} /> {t('rules.fields.preserveHost')}</label>
        {:else if draft.type === 'rewrite'}
          <label
            >{t('rules.fields.phase')}
            <Select.Root type="single" bind:value={draft.phase}>
              <Select.Trigger class="w-full">{t(`rules.phases.${draft.phase}`)}</Select.Trigger>
              <Select.Content>
                {#each (['request', 'response', 'both'] as const).map((ph) => [ph, t(`rules.phases.${ph}`)]) as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
          </label>
          <div class="field">
            <span>{t('rules.fields.steps')}</span>
            {#each draft.steps as st, i (i)}
              <div class="step">
                <div class="steprow">
                  <span class="faint num">{i + 1}</span>
                  <Select.Root type="single" bind:value={st.type}>
              <Select.Trigger class="w-full">{t(`rules.stepTypes.${st.type}`)}</Select.Trigger>
              <Select.Content>
                {#each stepTypes.map((ty) => [ty, t(`rules.stepTypes.${ty}`)]) as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
                  <button type="button" class="x" aria-label="×" onclick={() => draft?.steps.splice(i, 1)}>×</button>
                </div>
                {#each stepFields[st.type] ?? [] as f (f)}
                  {#if f === 'regex'}
                    <label class="check"><Checkbox bind:checked={st.regex} /> {t('rules.stepFields.regex')}</label>
                  {:else if f === 'status'}
                    <Input class="min-w-0 font-mono text-xs" type="number" bind:value={st.status} placeholder={t('rules.stepFields.status')} />
                  {:else}
                    <Input class="min-w-0 font-mono text-xs"
                      bind:value={st[f]}
                      spellcheck="false"
                      placeholder={f === 'value' && st.type === 'json_set'
                        ? t('rules.stepFields.jsonValue')
                        : f === 'value' && st.type === 'replace_body'
                          ? t('rules.stepFields.replacement')
                          : t(`rules.stepFields.${f}`)}
                    />
                  {/if}
                {/each}
              </div>
            {/each}
            <button type="button" class="link" onclick={() => draft?.steps.push({ type: 'set_header', name: '', value: '' })}>+ {t('rules.fields.addStep')}</button>
            <span class="faint">{t('rules.rwHint')}</span>
          </div>
        {:else if draft.type === 'breakpoint'}
          <label
            >{t('rules.fields.phase')}
            <Select.Root type="single" bind:value={draft.phase}>
              <Select.Trigger class="w-full">{t(`rules.phases.${draft.phase}`)}</Select.Trigger>
              <Select.Content>
                {#each (['request', 'response', 'both'] as const).map((ph) => [ph, t(`rules.phases.${ph}`)]) as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
          </label>
          <div class="two">
            <label>{t('rules.fields.timeout')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.timeout} placeholder="60s" /></label>
            <label
              >{t('rules.fields.onTimeout')}
              <Select.Root type="single" bind:value={draft.onTimeout}>
              <Select.Trigger class="w-full">{t(`bp.timeoutActions.${draft.onTimeout}`)}</Select.Trigger>
              <Select.Content>
                {#each [['continue', t('bp.timeoutActions.continue')], ['abort', t('bp.timeoutActions.abort')]] as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
            </label>
          </div>
          <span class="faint">{@html inline(t('rules.bpHint'))}</span>
        {:else}
        <div class="field">
          <span>{t('rules.fields.source')}</span>
          <Tabs.Root value={draft.source} onValueChange={(v) => draft && (draft.source = v as Source)}>
            <Tabs.List class="w-full">
              {#each ['file', 'dir', 'inline'] as const as s (s)}
                <Tabs.Trigger value={s}>{t(`rules.fields.${s}`)}</Tabs.Trigger>
              {/each}
            </Tabs.List>
          </Tabs.Root>
          {#if draft.source !== 'inline'}
            <Input class="min-w-0 font-mono text-xs" bind:value={draft.path} placeholder={draft.source === 'file' ? '~/mocks/feed.json' : '~/project/dist'} spellcheck="false" />
          {/if}
          <span class="faint">{t(`rules.sourceHint.${draft.source}`)}</span>
        </div>
        {#if draft.source === 'inline' || (draft.source === 'file' && fileInfo)}
          {@const isFile = draft.source === 'file'}
          {@const text = isFile ? fileText : draft.body}
          {@const err = jsonErr(text)}
          <div class="field">
            <div class="flex items-center gap-2">
              <span class="font-semibold text-foreground">{t('rules.response')}</span>
              {#if isFile}<span class="truncate font-mono text-[11px] text-faint" title={fileInfo?.path}>{fileInfo?.path}</span>{/if}
              <span class="flex-1"></span>
              {#if err}<span class="text-[11px] text-err" title={err}>{t('rules.jsonInvalid')}</span>{:else if text.trim().startsWith('{') || text.trim().startsWith('[')}<span class="text-[11px] text-ok">JSON</span>{/if}
              <Button variant="ghost" size="xs" disabled={!!err} onclick={() => (isFile ? (fileText = prettyIfJSON(fileText)) : draft && (draft.body = prettyIfJSON(draft.body)))}>{t('rules.formatJson')}</Button>
            </div>
            {#if isFile && fileInfo && !fileInfo.editable}
              <p class="m-0 text-warn">{fileInfo.reason}</p>
            {:else if isFile}
              <Textarea class="min-h-[320px] resize-y font-mono text-[12.5px] leading-[1.6]" rows={18} bind:value={fileText} spellcheck="false" />
              <span class="faint">{t('rules.fileSaveHint')}</span>
            {:else if draft}
              <Textarea class="min-h-[320px] resize-y font-mono text-[12.5px] leading-[1.6]" rows={18} bind:value={draft.body} spellcheck="false" />
            {/if}
          </div>
        {/if}
        <div class="two">
          <label>{t('rules.fields.status')}<Input class="min-w-0 font-mono text-xs" type="number" bind:value={draft.status} /></label>
          <label
            >{t('rules.fields.mode')}
            <Select.Root type="single" bind:value={draft.mode}>
              <Select.Trigger class="w-full">{t(`rules.modes.${draft.mode}`)}</Select.Trigger>
              <Select.Content>
                {#each [['full', t('rules.modes.full')], ['body_only', t('rules.modes.body_only')]] as o (o[0])}<Select.Item value={o[0]} label={o[1]}>{o[1]}</Select.Item>{/each}
              </Select.Content>
            </Select.Root>
          </label>
        </div>
        <label>{t('rules.fields.headers')}<Textarea class="min-h-0 resize-y font-mono text-xs" rows={2} bind:value={draft.headers} spellcheck="false" /></label>
        {/if}
        {#if draft.owner.startsWith('agent:') && draft.expires_at}
          <div class="ownerline">
            <span class="muted">{t('rules.ownerLine', { owner: draft.owner, left: left(draft.expires_at) })}</span>
            <Button variant="outline" size="sm" onclick={() => save(true)}>{t('rules.makePermanent')}</Button>
          </div>
        {:else if !editing}
          <label>{t('rules.fields.ttl')}<Input class="min-w-0 font-mono text-xs" bind:value={draft.ttl} placeholder="30m" /></label>
        {/if}
        {#if error}<div class="error mono">{error}</div>{/if}
      </div>
      <div class="foot">
        {#if editing}<Button variant="destructive" onclick={remove}>{t('rules.delete')}</Button>{/if}
        <span class="grow"></span>
        <Button variant="outline" onclick={close}>{t('rules.cancel')}</Button>
        <Button onclick={() => save()}>{t('rules.save')}</Button>
      </div>
    </aside>
{/if}
{/snippet}

<div class="page" class:modal>
  {#if !modal}
  <section class="list" style:--rule-cols={gridCols}>
    <div class="head">
      <strong>{t('rules.title')}</strong>
      <span class="faint">{t('rules.summary', { total: rules.length, enabled: rules.filter((r) => r.enabled).length })}</span>
      <span class="grow"></span>
      <Button onclick={newRule}>{t('rules.new')}</Button>
    </div>
    <div class="row hdr">
      <span></span>
      {#each DIV as c, i (c)}
        <span class="rel" class:num={c === 'hits'}
          >{t(`rules.cols.${c}`)}{#if i < DIV.length - 1}<!-- svelte-ignore a11y_no_static_element_interactions --><span
              class="grip"
              title={t('table.resize')}
              onpointerdown={(e) => startResize(e, i)}
              ondblclick={() => resetPair(i)}
            ></span>{/if}</span
        >
      {/each}<span></span>
    </div>
    <div class="rows">
      {#each shown as r, i (r.id)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="row" class:zebra={i % 2 === 1} class:sel={editing === r.id} class:off={!r.enabled} role="button" tabindex="0" onclick={() => open(r)} oncontextmenu={(e) => { e.preventDefault(); menu = { r, x: e.clientX, y: e.clientY }; }}>
          <span>
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="inline-flex" onclick={(e) => e.stopPropagation()}>
              <Switch
                checked={r.enabled}
                aria-label={r.enabled ? t('rules.disable') : t('rules.enable')}
                onCheckedChange={() => toggle(r)}
              />
            </span>
          </span>
          <span class="rule"><span class="name">{r.name}</span><span class="mono muted ellipsis">{matchText(r)}</span></span>
          <span><span class="pill neutral">{t(`rules.types.${r.type}`)}</span></span>
          <span>
            {#if r.owner.startsWith('agent:')}<span class="agent" title={t('rules.byAgent')}>{r.owner.slice(6)}</span>{/if}
          </span>
          <span class="mono" class:err={soon(r.expires_at)} class:faint={!r.expires_at}>{left(r.expires_at)}</span>
          <span class="mono muted num">{formatCount(r.hits)}</span>
          <span class="inline-flex justify-end">
            <Button variant="ghost" size="icon-sm" class="text-faint hover:text-err" aria-label={t('rules.delete')} title={t('rules.delete')} onclick={(e) => { e.stopPropagation(); removeRow(r); }}><Trash2Icon /></Button>
          </span>
        </div>
      {:else}
        <p class="empty">{t('rules.empty')}</p>
      {/each}
    </div>
  </section>
  {/if}

  {#if draft}
    {#if modal}
      {@render editorView()}
    {:else}
      <!-- Same pop-up as Traffic's Map Local / Breakpoint: the editor never squeezes the list. -->
      <Dialog.Root open onOpenChange={(o) => !o && close()}>
        <Dialog.Content
          showCloseButton={false}
          onOpenAutoFocus={(e) => e.preventDefault()}
          class="flex h-[min(820px,calc(100%-64px))] w-[min(760px,calc(100%-64px))] max-w-none gap-0 overflow-hidden rounded-[14px] bg-background p-0 sm:max-w-none"
        >
          <Dialog.Title class="sr-only">{editing ? t('rules.edit') : t('rules.create')}</Dialog.Title>
          <div class="modal flex min-h-0 flex-1">{@render editorView()}</div>
        </Dialog.Content>
      </Dialog.Root>
    {/if}
  {/if}
</div>

<style>
  .page {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  .list {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    container-type: inline-size;
  }
  /* With the editor open the list is narrow: keep the rule and its switch, drop the extras. */
  @container (max-width: 640px) {
    .list .row {
      grid-template-columns: 44px minmax(0, 1fr) 96px 0 0 44px 36px;
    }
    .list .row > :nth-child(4),
    .list .row > :nth-child(5) {
      visibility: hidden;
      overflow: hidden;
    }
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px var(--gutter);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .head strong {
    font-size: 14px;
  }
  .grow {
    flex: 1;
  }
  .rows {
    flex: 1;
    overflow: auto;
  }
  .row {
    display: grid;
    grid-template-columns: var(--rule-cols);
    align-items: center;
    padding: 8px var(--gutter);
    gap: 14px;
    cursor: default;
  }
  .rel {
    position: relative;
  }
  /* Column divider: a short faint line centred in the 14px gap; on hover it grows into a green handle. */
  .grip {
    position: absolute;
    top: -6px;
    bottom: -6px;
    right: -13px;
    width: 12px;
    cursor: col-resize;
  }
  .grip::after {
    content: '';
    position: absolute;
    top: 6px;
    bottom: 6px;
    left: 50%;
    width: 1px;
    transform: translateX(-50%);
    border-radius: 2px;
    background: color-mix(in srgb, var(--faint) 35%, transparent);
    transition: all 0.12s;
  }
  .grip:hover::after {
    top: 0;
    bottom: 0;
    width: 3px;
    background: var(--pp-accent-text);
  }
  .row.hdr {
    color: var(--pp-muted);
    font-weight: 500;
    border-bottom: 1px solid var(--border);
    padding: 6px var(--gutter);
  }
  .zebra {
    background: var(--zebra);
  }
  .row.sel {
    background: var(--sel);
    box-shadow: inset 2px 0 0 var(--pp-accent);
  }
  .row.off > :not(:first-child) {
    opacity: 0.55;
  }
  .rule {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }
  .name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ellipsis {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .num {
    text-align: right;
  }
  .agent {
    padding: 1px 8px;
    border-radius: 9px;
    background: var(--ai-bg);
    border: 1px solid var(--border);
    color: var(--ai);
    font-weight: 600;
    font-size: 11px;
  }
  .err {
    color: var(--err);
  }
  .muted {
    color: var(--pp-muted);
  }
  .faint {
    color: var(--faint);
  }
  .faint :global(code) {
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    color: var(--text);
  }
  .empty {
    padding: 24px var(--gutter);
    color: var(--faint);
  }
  .modal {
    width: 100%;
    min-width: 0;
  }
  .modal .editor {
    flex: 1;
    min-width: 0;
    width: auto;
    border-left: 0;
  }
  .editor {
    width: 600px;
    flex-shrink: 0;
    border-left: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .form {
    flex: 1;
    overflow: auto;
    padding: 14px var(--gutter);
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  label,
  .field {
    display: flex;
    flex-direction: column;
    gap: 5px;
    color: var(--pp-muted);
  }
  label.check {
    width: auto;
    flex-direction: row;
    align-items: center;
    gap: 6px;
  }
  .two {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 10px;
  }
  .step {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 8px;
    border: 1px solid var(--border);
    border-radius: 7px;
  }
  .steprow {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) 24px;
    gap: 6px;
    align-items: center;
  }
  .x {
    border: 0;
    background: transparent;
    color: var(--faint);
  }
  .link {
    align-self: flex-start;
    border: 0;
    background: transparent;
    color: var(--pp-accent-text);
    padding: 2px 0;
  }
  .ownerline {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .foot {
    display: flex;
    gap: 8px;
    padding: 12px var(--gutter);
    border-top: 1px solid var(--border);
  }
  .error {
    padding: 8px 10px;
    border-radius: 7px;
    background: var(--err-bg);
    color: var(--err);
    white-space: pre-wrap;
  }
</style>
