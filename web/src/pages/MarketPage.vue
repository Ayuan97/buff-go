<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getQuotes, type Quote } from '../api'
import QuoteStateCell from '../components/QuoteStateCell.vue'
import EmptyState from '../components/EmptyState.vue'

const quotes = ref<Quote[] | null>(null)
const error = ref<string | null>(null)
const stale = ref(false)
const filterPlatform = ref('')
const filterText = ref('')
const appid = ref('730')
let poll = 0

interface Row {
  productId: number
  appid: number
  name: string
  byKey: Map<string, Quote>
}

const rows = computed<Row[]>(() => {
  const map = new Map<number, Row>()
  for (const q of quotes.value ?? []) {
    if (filterPlatform.value && q.platform !== filterPlatform.value) continue
    if (filterText.value && !q.name.toLowerCase().includes(filterText.value.toLowerCase())) continue
    let row = map.get(q.product_id)
    if (!row) {
      row = { productId: q.product_id, appid: q.appid, name: q.name, byKey: new Map() }
      map.set(q.product_id, row)
    }
    row.byKey.set(`${q.platform}-${q.side}`, q)
  }
  return [...map.values()]
})

const platforms = computed(() => [...new Set((quotes.value ?? []).map((q) => q.platform))].sort())
const columns = computed(() =>
  platforms.value
    .filter((p) => !filterPlatform.value || p === filterPlatform.value)
    .flatMap((p) => [
      { platform: p, side: 'bid' as const, label: `${p.toUpperCase()} 求购` },
      { platform: p, side: 'ask' as const, label: `${p.toUpperCase()} 出售` },
    ]),
)

async function load() {
  try {
    quotes.value = await getQuotes(Number(appid.value) || undefined)
    error.value = null
    stale.value = false
  } catch (e) {
    if (quotes.value) stale.value = true
    else error.value = e instanceof Error ? e.message : '加载失败'
  }
}

onMounted(() => {
  void load()
  poll = window.setInterval(() => { void load() }, 5000)
})
onUnmounted(() => {
  if (poll) window.clearInterval(poll)
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <div>
        <h1>行情</h1>
        <p class="hero-sub">最新尝试状态 · 历史有效价来自 last present</p>
      </div>
    </header>
    <div class="toolbar">
      <input v-model="appid" class="inp" placeholder="appid" @change="load" />
      <select v-model="filterPlatform">
        <option value="">全部平台</option>
        <option v-for="p in platforms" :key="p" :value="p">{{ p }}</option>
      </select>
      <input v-model="filterText" class="inp" placeholder="商品名" />
      <button class="btn sm" @click="load">刷新</button>
    </div>
    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="error && !quotes" class="fail">{{ error }}</div>
    <EmptyState v-else-if="!quotes" kind="empty" text="加载中" />
    <EmptyState v-else-if="!rows.length" kind="empty" text="暂无行情" />
    <table v-else class="data">
      <thead>
        <tr>
          <th>商品</th>
          <th v-for="c in columns" :key="c.label">{{ c.label }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.productId">
          <td>
            <div class="strong">{{ row.name }}</div>
            <div class="muted num">{{ row.appid }} · #{{ row.productId }}</div>
          </td>
          <td v-for="c in columns" :key="c.label">
            <QuoteStateCell :quote="row.byKey.get(`${c.platform}-${c.side}`)" />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.pg { max-width: 1200px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); }
.toolbar { display: flex; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.inp, select { height: 28px; background: var(--bg); color: var(--text); border: 1px solid var(--line-strong); font-family: var(--mono); padding: 0 8px; }
.fail { color: var(--danger); margin-bottom: 12px; }
</style>
