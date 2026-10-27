<script lang="ts">
  import { t } from '../lib/i18n';
  import { guideScenes } from '../lib/guide';
  import PhoneScreen from '../components/PhoneScreen.svelte';

  // The setup slideshow on the phone's own certificate page (served by the proxy at
  // http://proxypilot.cert). Same phone as the Setup page; touch-sized controls styled
  // like that page, no Tailwind.
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
    playing = false;
  });
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

<div class="guide">
  <div class="count">{t('setup.guide.stepOf', { n: step + 1, total: scenes.length })}</div>
  <p class="caption">{scene?.caption}</p>
  <div class="stage"><PhoneScreen {platform} {scene} sceneKey={`${platform}-${step}`} /></div>
  <div class="controls">
    <button type="button" class="nav" aria-label={t('setup.guide.back')} disabled={step === 0} onclick={() => go(-1)}>‹</button>
    <div class="dots" role="tablist">
      {#each scenes as _, i (i)}
        <button
          type="button"
          role="tab"
          class="dot"
          class:on={i === step}
          aria-selected={i === step}
          aria-label={t('setup.guide.stepOf', { n: i + 1, total: scenes.length })}
          onclick={() => {
            playing = false;
            step = i;
          }}
        ></button>
      {/each}
    </div>
    <button type="button" class="nav" aria-label={t('setup.guide.next')} disabled={step >= scenes.length - 1} onclick={() => go(1)}>›</button>
  </div>
  <button
    type="button"
    class="play"
    onclick={() => {
      if (!playing && step >= scenes.length - 1) step = 0;
      playing = !playing;
    }}>{playing ? t('setup.guide.pause') : t('setup.guide.play')}</button
  >
</div>

<style>
  .guide {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .count {
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.05em;
    color: var(--muted, #a7a7a7);
  }
  .caption {
    margin: 0;
    min-height: 66px;
    font-size: 15px;
    line-height: 1.45;
  }
  .stage {
    display: flex;
    justify-content: center;
    padding: 4px 0;
  }
  .controls {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .nav {
    flex-shrink: 0;
    width: 44px;
    height: 44px;
    border: 1px solid var(--border, #282828);
    border-radius: 12px;
    background: var(--panel, #181818);
    color: var(--text, #e8e8e8);
    font: 600 24px/1 -apple-system, system-ui, sans-serif;
  }
  .nav:disabled {
    opacity: 0.35;
  }
  .dots {
    flex: 1;
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 6px;
  }
  .dot {
    width: 8px;
    height: 8px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: var(--border, #282828);
    transition: width 0.2s;
  }
  .dot.on {
    width: 22px;
    background: var(--accent, #1db954);
  }
  .play {
    align-self: center;
    min-height: 36px;
    padding: 0 16px;
    border: 0;
    border-radius: 10px;
    background: transparent;
    color: var(--muted, #a7a7a7);
    font: 600 14px -apple-system, system-ui, sans-serif;
  }
</style>
