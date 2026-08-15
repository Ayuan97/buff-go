<script setup lang="ts">
import { computed } from 'vue'
import { type PriceTick, type Quote, type Target } from '../api'
import { dropPctText, dropWindowLabel, fenToYuan, fmtAgo, gameName, platformItemURL, platformLabel, quoteReasonText, quoteStatusText, sideText, targetReasonText } from '../utils/format'
import TickRows from './TickRows.vue'

const emit = defineEmits<{
  enter: []
  leave: []
}>()

const props = defineProps<{
  quote: Quote
  related: Quote[] | null
  ticks: PriceTick[] | null
  targets: Target[]
  dropWindow?: string
  x: number
  y: number
}>()

const dropSummary = computed(() => {
  const quote = props.quote
  if (!quote.drop_cents) return ''
  const bits = [`${dropWindowLabel(props.dropWindow ?? '24h')}高 ${fenToYuan(quote.high_cents)}`]
  bits.push(`已降 ${fenToYuan(quote.drop_cents)}`)
  if (quote.drop_pct_bp) bits.push(dropPctText(quote.drop_pct_bp))
  if (quote.drop_count) bits.push(`${quote.drop_count} 次`)
  if (quote.last_drop_at) bits.push(fmtAgo(quote.last_drop_at))
  return bits.join(' · ')
})

const order = ['steam', 'buff', 'igxe'] as const

const groups = computed(() => {
  const map = new Map<string, { bid?: Quote; ask?: Quote }>()
  for (const row of props.related ?? []) {
    const current = map.get(row.platform) ?? {}
    current[row.side] = row
    map.set(row.platform, current)
  }
  for (const target of props.targets) {
    if (target.appid !== props.quote.appid) continue
    if (!map.has(target.platform)) map.set(target.platform, {})
  }
  return [...map.entries()]
    .sort((a, b) => rank(a[0]) - rank(b[0]))
    .map(([platform, sides]) => ({
      platform,
      href: platformItemURL(platform, props.quote.appid, props.quote.name),
      ...sides,
    }))
})

function rank(platform: string): number {
  const index = order.indexOf(platform as (typeof order)[number])
  return index < 0 ? 99 : index
}

function targetOf(platform: string, side: 'bid' | 'ask'): Target | undefined {
  return props.targets.find(
    (target) => target.appid === props.quote.appid && target.platform === platform && target.side === side,
  )
}

// 现行情只认这一次 attempt 是 present 的价。last_present 在最新一次不是 present 时不是现行情。
function priceText(row: Quote | undefined, platform: string, side: 'bid' | 'ask'): string {
  if (row?.status === 'present' && row.present_cents != null) return fenToYuan(row.present_cents)
  if (row) return quoteStatusText(row.status)
  const target = targetOf(platform, side)
  if (!target || target.desired !== 'enabled') return '未开'
  if (target.actual === 'blocked' || target.actual === 'error') return '开了但采不动'
  return '还没扫到'
}

function note(row: Quote | undefined, platform: string, side: 'bid' | 'ask'): string {
  if (!row) {
    const target = targetOf(platform, side)
    if (target?.desired === 'enabled' && target.reason) return targetReasonText(target.reason)
    return ''
  }
  const bits: string[] = []
  if (row.status === 'present' && row.present_order_count != null) {
    bits.push(side === 'ask' ? `待售 ${row.present_order_count}` : `求购 ${row.present_order_count}`)
  }
  bits.push(fmtAgo(row.collected_at))
  if (row.drop_cents) {
    bits.push(`降 ${fenToYuan(row.drop_cents)}${row.drop_pct_bp ? ` ${dropPctText(row.drop_pct_bp)}` : ''}`)
  }
  if (row.status !== 'present') {
    if (row.reason_code) bits.push(quoteReasonText(row.reason_code))
    if (row.present_cents != null) bits.push(`上次有价 ${fenToYuan(row.present_cents)}`)
  }
  return bits.join(' · ')
}
</script>

<template>
  <aside class="peek" :style="{ left: `${x}px`, top: `${y}px` }" @mouseenter="emit('enter')" @mouseleave="emit('leave')">
    <div class="head">
      <RouterLink class="name" :to="`/market/${quote.product_id}`">{{ quote.name }}</RouterLink>
      <div class="muted">
        {{ gameName(quote.appid) }}
        <span v-if="quote.item_type"> · {{ quote.item_type }}</span>
      </div>
      <div v-if="dropSummary" class="drop-sum">{{ dropSummary }}</div>
    </div>
    <div v-if="related === null" class="muted">读取各平台价格…</div>
    <div v-else class="plats">
      <section v-for="group in groups" :key="group.platform" class="plat">
        <div class="plat-h">
          <b>{{ platformLabel(group.platform) }}</b>
        </div>
        <div v-for="side in (['bid', 'ask'] as const)" :key="side" class="line">
          <span class="side">{{ sideText(side) }}</span>
          <a
            v-if="group.href"
            class="price link"
            :href="group.href"
            target="_blank"
            rel="noopener noreferrer"
          >{{ priceText(group[side], group.platform, side) }}</a>
          <span v-else class="price">{{ priceText(group[side], group.platform, side) }}</span>
          <span class="muted">{{ note(group[side], group.platform, side) }}</span>
        </div>
      </section>
    </div>
    <div v-if="ticks && ticks.length" class="hist">
      <div class="hist-h">最近变价</div>
      <TickRows :ticks="ticks" compact />
    </div>
  </aside>
</template>

<style scoped>
.peek {
  position: fixed;
  z-index: 200;
  width: 520px;
  max-height: min(78vh, 640px);
  overflow: auto;
  padding: 12px 14px;
  border: 1px solid var(--line-strong);
  background: var(--surface);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  pointer-events: auto;
}
.head { margin-bottom: 10px; }
.drop-sum { margin-top: 6px; color: var(--ok); font-family: var(--mono); font-size: 11px; }
.name {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font-size: 13px;
  font-weight: 600;
  line-height: 1.35;
  color: inherit;
  text-decoration: none;
}
.name:hover { text-decoration: underline; }
.plats { display: grid; gap: 10px; }
.plat-h { margin-bottom: 4px; font-size: 12px; }
.line { display: grid; grid-template-columns: 36px 88px 1fr; gap: 8px; align-items: start; font-family: var(--mono); font-size: 11px; }
.side { color: var(--text-3); }
.price { font-weight: 700; }
.price.link { color: inherit; text-decoration: none; cursor: pointer; }
.price.link:hover { text-decoration: underline; }
.muted { color: var(--text-3); font-size: 11px; }
.hist {
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px solid var(--line);
  max-height: 280px;
  overflow: auto;
}
.hist-h {
  margin-bottom: 4px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
</style>
