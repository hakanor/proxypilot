<script lang="ts">
  // Full text on hover, only when the cell cuts it off. One shared element, so rows stay light.
  let tip = $state<{ url: string; left: number; top?: number; bottom?: number; el: HTMLElement } | null>(null);
  let timer: ReturnType<typeof setTimeout> | undefined;
  export function show(e: PointerEvent, url: string) {
    const cell = e.currentTarget as HTMLElement;
    clearTimeout(timer);
    if (!url || ![...cell.children].some((c) => c.scrollWidth > c.clientWidth)) return;
    timer = setTimeout(() => {
      const b = cell.getBoundingClientRect();
      const left = Math.max(8, Math.min(b.left, innerWidth - 728));
      tip = b.bottom > innerHeight / 2 ? { url, left, bottom: innerHeight - b.top + 4, el: cell } : { url, left, top: b.bottom + 4, el: cell };
    }, 250);
  }
  export function hide() {
    clearTimeout(timer);
    tip = null;
  }
  /** A list can drop the hovered row without a pointerleave. */
  export function hideIfGone() {
    if (tip && !tip.el.isConnected) hide();
  }
  // A container query ancestor would capture position: fixed, so the tooltip lives on body.
  function portal(node: HTMLElement) {
    document.body.append(node);
    return { destroy: () => node.remove() };
  }
</script>

{#if tip}
  {@const m = tip.url.match(/^([a-z]+:\/\/[^/?#]+)(.*)$/is)}
  <div
    use:portal
    role="tooltip"
    class="pointer-events-none fixed z-50 line-clamp-[12] w-max max-w-[min(720px,calc(100vw-16px))] rounded-lg border border-border bg-popover px-3 py-2 font-mono text-xs leading-[1.6] break-all text-muted-foreground shadow-xl ring-1 ring-black/10"
    style:left="{tip.left}px"
    style:top={tip.top === undefined ? undefined : `${tip.top}px`}
    style:bottom={tip.bottom === undefined ? undefined : `${tip.bottom}px`}
  >
    {#if m}<span class="font-semibold text-foreground">{m[1]}</span>{m[2]}{:else}<span class="text-foreground">{tip.url}</span>{/if}
  </div>
{/if}
