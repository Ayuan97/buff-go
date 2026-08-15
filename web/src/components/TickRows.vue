<script setup lang="ts">
import type { PriceTick } from '../api'
import { fenToYuan, fmtAgo, platformLabel, sideText, tickDelta, tickDeltaText } from '../utils/format'

defineProps<{
  ticks: PriceTick[]
  compact?: boolean
}>()
</script>

<template>
  <ul class="ticks" :class="{ compact }">
    <li v-for="tick in ticks" :key="tick.tick_id" class="tick">
      <span class="plat">{{ platformLabel(tick.platform) }}</span>
      <span class="side">{{ sideText(tick.side) }}</span>
      <span class="change">
        <span v-if="tick.prev_cents != null" class="old">{{ fenToYuan(tick.prev_cents) }}</span>
        <span class="now">{{ fenToYuan(tick.price_cents) }}</span>
        <span
          class="delta"
          :class="{ up: (tickDelta(tick.prev_cents, tick.price_cents) ?? 0) > 0, down: (tickDelta(tick.prev_cents, tick.price_cents) ?? 0) < 0 }"
        >{{ tickDeltaText(tick.prev_cents, tick.price_cents) }}</span>
      </span>
      <span class="when">{{ fmtAgo(tick.collected_at) }}</span>
    </li>
  </ul>
</template>

<style scoped>
.ticks { list-style: none; margin: 0; padding: 0; }
.tick {
  display: grid;
  grid-template-columns: 44px 28px minmax(0, 1fr) auto;
  gap: 6px;
  align-items: baseline;
  padding: 4px 0;
  font-family: var(--mono);
  font-size: 11px;
}
.compact .tick { padding: 2px 0; font-size: 10px; }
.plat { color: var(--text-2); }
.side { color: var(--text-3); }
.change { display: flex; gap: 6px; align-items: baseline; min-width: 0; }
.old { color: var(--text-3); text-decoration: line-through; }
.now { font-weight: 700; }
.delta { color: var(--text-3); }
.delta.up { color: var(--ok); }
.delta.down { color: var(--danger); }
.when { color: var(--text-3); }
</style>
