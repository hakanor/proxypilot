<script lang="ts">
  import { t } from '../lib/i18n';
  import { guideScenes } from '../lib/guide';
  import PhoneScreen from './PhoneScreen.svelte';
  import { Button } from '$lib/components/ui/button/index.js';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Play from '@lucide/svelte/icons/play';
  import Pause from '@lucide/svelte/icons/pause';
  import { cn } from '$lib/utils.js';

  // The setup slideshow on the Setup page: the sketched phone plus step controls.
  interface Props {
    platform: 'ios' | 'android';
    ip: string;
    port: string;
  }
  let { platform, ip, port }: Props = $props();

  const scenes = $derived(guideScenes(platform, ip, port));

  let step = $state(0);
  let playing = $state(false);
  const scene = $derived(scenes[Math.min(step, scenes.length - 1)]);
  $effect(() => {
    void platform;
    step = 0;
  });

  // Autoplay walks through and stops at the end.
  $effect(() => {
    if (!playing) return;
    const id = setInterval(() => {
      if (step >= scenes.length - 1) playing = false;
      else step++;
    }, 2600);
    return () => clearInterval(id);
  });

  const go = (d: number) => {
    playing = false;
    step = Math.max(0, Math.min(scenes.length - 1, step + d));
  };
</script>

<div class="flex flex-wrap items-start gap-6">
  <PhoneScreen {platform} {scene} sceneKey={`${platform}-${step}`} />

  <!-- Caption and controls -->
  <div class="flex min-w-[220px] flex-1 flex-col gap-3 pt-2">
    <div class="text-xs font-medium tracking-wide text-faint">{t('setup.guide.stepOf', { n: step + 1, total: scenes.length })}</div>
    <p class="m-0 min-h-[66px] text-[14px] leading-relaxed text-foreground">{scene?.caption}</p>
    <div class="flex items-center gap-1.5">
      <Button variant="outline" size="icon-sm" aria-label={t('setup.guide.back')} disabled={step === 0} onclick={() => go(-1)}><ChevronLeft /></Button>
      <Button variant="outline" size="icon-sm" aria-label={t('setup.guide.next')} title={t('setup.guide.next')} onclick={() => go(1)} disabled={step >= scenes.length - 1}><ChevronRight /></Button>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={playing ? t('setup.guide.pause') : t('setup.guide.play')}
        title={playing ? t('setup.guide.pause') : t('setup.guide.play')}
        onclick={() => {
          if (!playing && step >= scenes.length - 1) step = 0;
          playing = !playing;
        }}>{#if playing}<Pause />{:else}<Play />{/if}</Button
      >
    </div>
    <div class="flex flex-wrap gap-1.5" role="tablist">
      {#each scenes as _, i (i)}
        <button
          type="button"
          role="tab"
          aria-selected={i === step}
          aria-label={t('setup.guide.stepOf', { n: i + 1, total: scenes.length })}
          class={cn('h-1.5 rounded-full transition-all', i === step ? 'w-5 bg-primary' : 'w-1.5 bg-border hover:bg-faint')}
          onclick={() => {
            playing = false;
            step = i;
          }}
        ></button>
      {/each}
    </div>
  </div>
</div>
