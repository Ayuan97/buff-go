<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { APIError, getPriceTicks, getQuotes, getTargets, productImage, type DropWindow, type PriceTick, type Quote, type Target } from '../api'
import EmptyState from '../components/EmptyState.vue'
import TickRows from '../components/TickRows.vue'
import {
  apiErrorText,
  dropPctText,
  dropWindowLabel,
  fenToYuan,
  fmtAgo,
  fmtTime,
  gameName,
  platformItemURL,
  platformLabel,
  quoteReasonText,
  quoteStatusText,
  sideText,
  targetReasonText,
} from '../utils/format'

const route = useRoute()
const productId = computed(() => Number(route.params.productId))

const quotes = ref<Quote[]>([])
const ticks = ref<PriceTick[]>([])
const targets = ref<Target[]>([])
const loaded = ref(false)
const error = ref<string | null>(null)
const stale = ref(false)
const dropWindow = ref<DropWindow>('7d')

let poll = 0
let seq = 0

const order = ['steam', 'buff', 'igxe'] as const

const head = computed(() => quotes.value[0] ?? null)

const groups = computed(() => {
  const map = new Map<string, { bid?: Quote; ask?: Quote }>()
  for (const row of quotes.value) {
    const current = map.get(row.platform) ?? {}
    current[row.side] = row
    map.set(row.platform, current)
  }
  if (head.value) {
    for (const target of targets.value) {
      if (target.appid !== head.value.appid) continue
      if (!map.has(target.platform)) map.set(target.platform, {})
    }
  }
  return [...map.entries()]
    .sort((a, b) => rank(a[0]) - rank(b[0]))
    .map(([platform, sides]) => ({
      platform,
      href: head.value ? platformItemURL(platform, head.value.appid, head.value.name) : null,
      ...sides,
    }))
})

function rank(platform: string): number {
  const index = order.indexOf(platform as (typeof order)[number])
  return index < 0 ? 99 : index
}

function targetOf(platform: string, side: 'bid' | 'ask'): Target | undefined {
  if (!head.value) return undefined
  return targets.value.find(
    (target) => target.appid === head.value!.appid && target.platform === platform && target.side === side,
  )
}

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
    bits.push(`${dropWindowLabel(dropWindow.value)}降 ${fenToYuan(row.drop_cents)}${row.drop_pct_bp ? ` ${dropPctText(row.drop_pct_bp)}` : ''}`)
  }
  if (row.present_collected_at) bits.push(fmtTime(row.present_collected_at))
  if (row.status !== 'present') {
    if (row.reason_code) bits.push(quoteReasonText(row.reason_code))
    if (row.present_cents != null) bits.push(`上次有价 ${fenToYuan(row.present_cents)}`)
  }
  return bits.join(' · ')
}

async function load() {
  if (!Number.isInteger(productId.value) || productId.value < 1) {
    error.value = '商品不存在或不合法'
    loaded.value = true
    return
  }
  const token = ++seq
  try {
    const [quotePage, tickPage, nextTargets] = await Promise.all([
      getQuotes({ product_id: productId.value, drop_window: dropWindow.value, limit: 12 }),
      getPriceTicks({ product_id: productId.value, limit: 200 }),
      getTargets().catch(() => [] as Target[]),
    ])
    if (token !== seq) return
    quotes.value = quotePage.quotes
    ticks.value = tickPage.ticks
    targets.value = nextTargets
    loaded.value = true
    error.value = null
    stale.value = false
  } catch (e) {
    if (token !== seq) return
    const text = e instanceof APIError ? apiErrorText(e.code) : '加载失败'
    if (quotes.value.length || ticks.value.length) stale.value = true
    else error.value = text
    loaded.value = true
  }
}

watch(dropWindow, () => { void load() })
watch(productId, () => {
  loaded.value = false
  quotes.value = []
  ticks.value = []
  void load()
})

onMounted(() => {
  void load()
  poll = window.setInterval(() => void load(), 10_000)
})
onUnmounted(() => {
  if (poll) window.clearInterval(poll)
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <RouterLink class="back" to="/market">← 行情</RouterLink>
      <h1>{{ head?.name ?? (loaded ? '商品' : '读取中') }}</h1>
    </header>

    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="error" class="fail">{{ error }}</div>
    <EmptyState v-else-if="!loaded" kind="empty" text="读取中" />
    <EmptyState v-else-if="!head && !ticks.length" kind="empty" text="没有这个商品的行情" />

    <template v-else>
      <section v-if="head" class="ident">
        <div class="shot">
          <img v-if="head.icon_path" :src="productImage(head.icon_path)" :alt="head.name" />
          <span v-else class="no-shot">暂无图片</span>
        </div>
        <div>
          <div class="muted">
            {{ gameName(head.appid) }}
            <span v-if="head.item_type"> · {{ head.item_type }}</span>
          </div>
          <p class="note">现价只认这次采集是有价的结果。变价只记人民币分，挂单数变了不记，留 30 天。</p>
        </div>
      </section>

      <section class="panel">
        <div class="panel-head">
          现价
          <div class="windows">
            <button class="win" :class="{ on: dropWindow === '24h' }" type="button" @click="dropWindow = '24h'">24 小时</button>
            <button class="win" :class="{ on: dropWindow === '7d' }" type="button" @click="dropWindow = '7d'">7 天</button>
            <button class="win" :class="{ on: dropWindow === '30d' }" type="button" @click="dropWindow = '30d'">30 天</button>
          </div>
        </div>
        <div class="panel-body">
          <EmptyState v-if="!groups.length" kind="empty" text="还没有采到这个商品" />
          <div v-else class="plats">
            <section v-for="group in groups" :key="group.platform" class="plat">
              <div class="plat-h">
                <b>{{ platformLabel(group.platform) }}</b>
                <a
                  v-if="group.href"
                  class="ext"
                  :href="group.href"
                  target="_blank"
                  rel="noopener noreferrer"
                >平台页</a>
              </div>
              <div v-for="side in (['bid', 'ask'] as const)" :key="side" class="line">
                <span class="side">{{ sideText(side) }}</span>
                <span class="price">{{ priceText(group[side], group.platform, side) }}</span>
                <span class="muted">{{ note(group[side], group.platform, side) }}</span>
              </div>
            </section>
          </div>
        </div>
      </section>

      <section class="panel">
        <div class="panel-head">变价 · 30 天</div>
        <div class="panel-body">
          <EmptyState v-if="!ticks.length" kind="empty" text="还没有分值变动" />
          <TickRows v-else :ticks="ticks" />
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.pg { width: 100%; max-width: 880px; margin: 0; padding: 20px 28px 40px; box-sizing: border-box; }
.hero { margin-bottom: 16px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 6px 0 0; font-size: 18px; line-height: 1.35; }
.back { color: var(--text-3); font-family: var(--mono); font-size: 11px; text-decoration: none; }
.back:hover { color: var(--text); }
.ident { display: grid; grid-template-columns: 96px minmax(0, 1fr); gap: 16px; align-items: center; margin-bottom: 16px; }
.shot {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 96px;
  border: 1px solid var(--line);
  background: var(--surface);
}
.shot img { max-width: 100%; max-height: 100%; object-fit: contain; }
.no-shot, .muted, .note { color: var(--text-3); font-size: 12px; }
.note { margin: 8px 0 0; }
.panel { margin-bottom: 14px; }
.windows { display: flex; margin-left: auto; letter-spacing: 0; text-transform: none; }
.win {
  height: 22px;
  padding: 0 8px;
  border: 1px solid var(--line-strong);
  border-right: 0;
  background: var(--bg);
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  cursor: pointer;
}
.win:last-child { border-right: 1px solid var(--line-strong); }
.win.on { background: var(--surface); color: var(--text); }
.fail { color: var(--danger); padding: 12px 0; }
.plats { display: grid; gap: 12px; padding: 10px 12px; }
.plat-h { display: flex; align-items: center; gap: 10px; margin-bottom: 4px; font-size: 12px; }
.ext { color: var(--text-3); font-family: var(--mono); font-size: 11px; }
.line { display: grid; grid-template-columns: 36px 88px 1fr; gap: 8px; align-items: start; font-family: var(--mono); font-size: 12px; }
.side { color: var(--text-3); }
.price { font-weight: 700; }
</style>
