<script lang="ts">
  import Pointer from '@lucide/svelte/icons/pointer';
  import type { Scene } from '../lib/guide';

  // The sketched phone: one screen of the setup slideshow, with a hand that glides to
  // the row to tap and taps it. Plain scoped CSS (no Tailwind) so the phone's own
  // certificate page can show it too.
  interface Props {
    platform: 'ios' | 'android';
    scene: Scene | undefined;
    /** Changes per step; replays the slide-in and the tap. */
    sceneKey: string;
  }
  let { platform, scene, sceneKey }: Props = $props();

  // The hand: starts low on the screen, glides to the target row, then taps.
  let screen = $state<HTMLDivElement>();
  let hand = $state({ x: 70, y: 105, tap: false, show: false }); // % of the screen
  $effect(() => {
    void sceneKey;
    if (!screen) return;
    hand = { x: 70, y: 105, tap: false, show: false };
    const timers: ReturnType<typeof setTimeout>[] = [];
    timers.push(
      setTimeout(() => {
        const el = screen?.querySelector<HTMLElement>('[data-target]');
        if (!el || !screen) return;
        const s = screen.getBoundingClientRect();
        const r = el.getBoundingClientRect();
        const cx = el.dataset.target === 'end' ? r.right - 22 : r.left + Math.min(r.width / 2, 90);
        hand = { x: ((cx - s.left) / s.width) * 100, y: ((r.top + r.height / 2 - s.top) / s.height) * 100, tap: false, show: true };
        timers.push(setTimeout(() => (hand = { ...hand, tap: true }), 750));
        timers.push(setTimeout(() => (hand = { ...hand, tap: false }), 1050));
      }, 420), // after the page slide-in, so the target is measured where it rests
    );
    return () => timers.forEach(clearTimeout);
  });

  const control = (v: string) => /^\[(on|off|>|i|check)\]$/.exec(v)?.[1];
</script>

<!-- The phone -->
<div class="phone {platform}">
  <div class="screen" bind:this={screen}>
    <div class="status">
      <span>9:41</span>
      {#if platform === 'ios'}<span class="island"></span>{:else}<span class="punch"></span>{/if}
      <span class="bars"><i></i><i></i><i></i></span>
    </div>
    {#if scene}
      {#key sceneKey}
        <div class="page">
          {#if scene.url}
            <div class="url"><span class="lock">●</span>{scene.url}</div>
          {/if}
          <div class="title">{scene.title}</div>
          <div class="list">
            {#each scene.rows as r, i (i)}
              {#if r.skeleton}
                <div class="row"><span class="sk" style:width="{38 + ((i * 23) % 40)}%"></span></div>
              {:else}
                {@const c = control(r.value)}
                <div
                  class="row"
                  class:target={r.target}
                  class:pressed={r.target && hand.tap}
                  data-target={r.target ? (c === 'on' || c === 'off' || c === 'i' ? 'end' : 'mid') : undefined}
                >
                  <span class="label">{r.label}</span>
                  {#if c === 'on' || c === 'off'}
                    <span class="toggle" class:on={(c === 'on') !== (r.target && hand.tap)}></span>
                  {:else if c === '>'}
                    <span class="chev">›</span>
                  {:else if c === 'i'}
                    <span class="info">i</span>
                  {:else if c === 'check'}
                    <span class="check">✓</span>
                  {:else if r.value}
                    <span class="value">{r.value}</span>
                  {/if}
                </div>
              {/if}
            {/each}
          </div>
        </div>
      {/key}
    {/if}
    <div
      class="hand"
      class:show={hand.show}
      class:tap={hand.tap}
      style:left="{hand.x}%"
      style:top="{hand.y}%"
      aria-hidden="true"
    >
      <span class="ripple"></span>
      <Pointer size={28} />
    </div>
  </div>
</div>

<style>
  .phone {
    width: 236px;
    height: 470px;
    flex-shrink: 0;
    padding: 9px;
    border-radius: 38px;
    background: #0c0c0c;
    border: 1px solid #3a3a3a;
    box-shadow: 0 18px 40px rgba(0, 0, 0, 0.35), inset 0 0 0 2px #1e1e1e;
  }
  .phone.android {
    border-radius: 26px;
  }
  .screen {
    position: relative;
    height: 100%;
    overflow: hidden;
    border-radius: 30px;
    background: #f2f2f7;
    color: #111;
    font-family: -apple-system, system-ui, Inter, sans-serif;
  }
  .android .screen {
    border-radius: 18px;
    background: #f7f5fa;
    font-family: Roboto, system-ui, Inter, sans-serif;
  }
  .status {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 30px;
    padding: 0 18px;
    font-size: 11px;
    font-weight: 600;
  }
  .island {
    width: 64px;
    height: 18px;
    border-radius: 10px;
    background: #000;
  }
  .punch {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: #000;
  }
  .bars {
    display: flex;
    align-items: flex-end;
    gap: 2px;
  }
  .bars i {
    width: 3px;
    background: #111;
    border-radius: 1px;
  }
  .bars i:nth-child(1) { height: 4px; }
  .bars i:nth-child(2) { height: 7px; }
  .bars i:nth-child(3) { height: 10px; }
  .page {
    padding: 4px 12px 12px;
    animation: slide 0.35s ease;
  }
  @keyframes slide {
    from { opacity: 0; transform: translateX(24px); }
    to { opacity: 1; transform: none; }
  }
  .url {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 2px 0 10px;
    padding: 7px 10px;
    border-radius: 10px;
    background: #e3e3e8;
    font-size: 11px;
    color: #333;
  }
  .lock {
    font-size: 7px;
    color: #888;
  }
  .title {
    margin: 4px 2px 10px;
    font-size: 21px;
    font-weight: 700;
    letter-spacing: -0.01em;
  }
  .android .title {
    font-size: 19px;
    font-weight: 500;
  }
  .list {
    overflow: hidden;
    border-radius: 11px;
    background: #fff;
  }
  .android .list {
    border-radius: 16px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 36px;
    padding: 0 12px;
    font-size: 12.5px;
    border-bottom: 1px solid #ececf0;
    transition: background 0.15s;
  }
  .row:last-child {
    border-bottom: 0;
  }
  .row.target {
    background: #eaf8ef;
    box-shadow: inset 3px 0 0 #1db954;
    font-weight: 600;
  }
  .row.pressed {
    background: #c9efd6;
  }
  .label {
    /* reset: the certificate page has its own .label style */
    font-size: inherit;
    font-weight: inherit;
    letter-spacing: normal;
    color: inherit;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .value {
    max-width: 55%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: #8a8a8e;
    font-family: 'JetBrains Mono', ui-monospace, monospace;
    font-size: 11px;
  }
  .chev {
    color: #b8b8bd;
    font-size: 16px;
  }
  .info {
    display: inline-flex;
    width: 17px;
    height: 17px;
    align-items: center;
    justify-content: center;
    border: 1.5px solid #0a84ff;
    border-radius: 50%;
    color: #0a84ff;
    font-size: 10px;
    font-weight: 700;
    font-style: italic;
  }
  .check {
    color: #1db954;
    font-weight: 700;
  }
  .toggle {
    position: relative;
    width: 32px;
    height: 19px;
    border-radius: 10px;
    background: #d9d9de;
    transition: background 0.2s;
  }
  .toggle::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 15px;
    height: 15px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
    transition: transform 0.2s;
  }
  .toggle.on {
    background: #34c759;
  }
  .toggle.on::after {
    transform: translateX(13px);
  }
  .sk {
    display: block;
    height: 9px;
    border-radius: 5px;
    background: linear-gradient(90deg, #e6e6eb 0%, #f1f1f4 50%, #e6e6eb 100%);
    background-size: 200% 100%;
    animation: shimmer 1.6s linear infinite;
  }
  @keyframes shimmer {
    from { background-position: 200% 0; }
    to { background-position: -200% 0; }
  }
  .hand {
    position: absolute;
    z-index: 2;
    color: #111;
    opacity: 0;
    filter: drop-shadow(0 2px 3px rgba(0, 0, 0, 0.35));
    transform: translate(-30%, -8%);
    transition: left 0.6s cubic-bezier(0.3, 0.7, 0.3, 1), top 0.6s cubic-bezier(0.3, 0.7, 0.3, 1), opacity 0.25s, transform 0.15s;
    pointer-events: none;
  }
  .hand :global(svg) {
    fill: #fff;
  }
  .hand.show {
    opacity: 1;
  }
  .hand.tap {
    transform: translate(-30%, -8%) scale(0.86);
  }
  .ripple {
    position: absolute;
    left: 30%;
    top: 8%;
    width: 30px;
    height: 30px;
    margin: -15px 0 0 -15px;
    border-radius: 50%;
    background: rgba(29, 185, 84, 0.35);
    transform: scale(0);
    opacity: 0;
  }
  .hand.tap .ripple {
    animation: ripple 0.5s ease-out;
  }
  @keyframes ripple {
    from { transform: scale(0.3); opacity: 1; }
    to { transform: scale(1.6); opacity: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    .page, .sk, .hand.tap .ripple { animation: none; }
    .hand { transition: opacity 0.2s; }
  }
</style>
