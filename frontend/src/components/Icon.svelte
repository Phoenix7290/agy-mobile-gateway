<script lang="ts">
  import { resolveIconShapes, type IconName } from '../lib/icons';

  let {
    name,
    size = 18,
    color,
    strokeWidth = 1.85,
    class: className = '',
  }: {
    name: IconName;
    size?: number;
    color?: string;
    strokeWidth?: number;
    class?: string;
  } = $props();

  let shapes = $derived(resolveIconShapes(name));
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke={color ?? 'currentColor'}
  stroke-width={strokeWidth}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  class="inline-block align-middle shrink-0 {className}"
>
  {#each shapes as shape}
    {#if shape.tag === 'path'}
      <path {...shape.attrs} />
    {:else if shape.tag === 'circle'}
      <circle {...shape.attrs} />
    {:else if shape.tag === 'rect'}
      <rect {...shape.attrs} />
    {:else if shape.tag === 'line'}
      <line {...shape.attrs} />
    {:else if shape.tag === 'polyline'}
      <polyline {...shape.attrs} />
    {:else if shape.tag === 'polygon'}
      <polygon {...shape.attrs} />
    {/if}
  {/each}
</svg>
