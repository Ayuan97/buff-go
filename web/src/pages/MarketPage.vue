<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import {
  getQuoteFacets,
  getQuotes,
  productImage,
  type Quote,
  type QuoteFilter,
  type QuoteSort,
  type Side,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import { apiErrorText, fenToYuan, fmtAgo, fmtTime, gameName, platformLabel, quoteReasonText, quoteStatusText, sideText } from '../utils/format'

// 一页正好四行。列数与样式里 .grid 的 repeat(6, ...) 必须一致，改一处就要改另一处。
const GRID_COLUMNS = 6
const PAGE_SIZE = GRID_COLUMNS * 4

const page = ref<{ total: number; quotes: Quote[] } | null>(null)
const facets = ref<{ appids: number[]; item_types: string[] }>({ appids: [], item_types: [] })
const error = ref<string | null>(null)
const stale = ref(false)

// 筛选条件全部交给后端，前端不再对截断结果二次过滤
const appid = ref<number | ''>('')
const side = ref<Side | ''>('ask')
const keyword = ref('')
const itemType = ref('')
const minYuan = ref('')
const maxYuan = ref('')
const sort = ref<QuoteSort>('price_desc')
const offset = ref(0)

let poll = 0
let seq = 0

const totalPages = computed(() => (page.value ? Math.ceil(page.value.total / PAGE_SIZE) : 0))
const currentPage = computed(() => Math.floor(offset.value / PAGE_SIZE) + 1)
const filtering = computed(
  () => !!keyword.value.trim() || !!itemType.value || !!minYuan.value.trim() || !!maxYuan.value.trim(),
)

// 输入的是元，后端按分筛选；填了非数字就当没填，不发必失败的请求
function yuanToCents(raw: string): number | undefined {
  const trimmed = raw.trim()
  if (!trimmed) return undefined
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return undefined
  return Math.round(value * 100)
}

function currentFilter(): QuoteFilter {
  return {
    appid: appid.value === '' ? undefined : appid.value,
    side: side.value || undefined,
    keyword: keyword.value.trim() || undefined,
    item_type: itemType.value || undefined,
    min_cents: yuanToCents(minYuan.value),
    max_cents: yuanToCents(maxYuan.value),
    sort: sort.value,
    limit: PAGE_SIZE,
    offset: offset.value,
  }
}

async function load() {
  const token = ++seq
  try {
    const result = await getQuotes(currentFilter())
    if (token !== seq) return // 切筛选时丢掉在途旧请求，避免旧结果盖掉新结果
    page.value = result
    error.value = null
    stale.value = false
  } catch (e) {
    if (token !== seq) return
    if (page.value) stale.value = true
    else error.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
  }
}

async function loadFacets() {
  try {
    facets.value = await getQuoteFacets(appid.value === '' ? undefined : appid.value)
  } catch {
    // 筛选项拿不到不影响列表本身，保留上一次的选项
  }
}

// 换游戏要重新取分类，因为分类是按游戏统计的
watch(appid, () => {
  itemType.value = ''
  offset.value = 0
  void loadFacets()
  void load()
})
watch([side, itemType, sort], () => {
  offset.value = 0
  void load()
})
watch(offset, () => void load())

function applyFilters() {
  offset.value = 0
  void load()
}

function resetFilters() {
  keyword.value = ''
  itemType.value = ''
  minYuan.value = ''
  maxYuan.value = ''
  offset.value = 0
  void load()
}

function turnPage(delta: number) {
  const next = offset.value + delta * PAGE_SIZE
  if (next < 0 || next >= (page.value?.total ?? 0)) return
  offset.value = next
}

onMounted(() => {
  void loadFacets()
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
      <h1>行情</h1>
      <p class="hero-sub">
        价格是最近一次采到的有效价，待售数量来自同一次采集。图片和分类要等这一轮采集覆盖到该商品才会出现。
      </p>
    </header>

    <div class="layout">
      <aside class="side">
        <div class="group">
          <div class="group-title">游戏</div>
          <select v-model="appid" class="inp block">
            <option value="">全部游戏</option>
            <option v-for="id in facets.appids" :key="id" :value="id">{{ gameName(id) }}</option>
          </select>
        </div>

        <div class="group">
          <div class="group-title">方向</div>
          <div class="segmented">
            <button class="seg" :class="{ on: side === 'ask' }" type="button" @click="side = 'ask'">出售</button>
            <button class="seg" :class="{ on: side === 'bid' }" type="button" @click="side = 'bid'">求购</button>
            <button class="seg" :class="{ on: side === '' }" type="button" @click="side = ''">全部</button>
          </div>
        </div>

        <div class="group">
          <div class="group-title">关键字</div>
          <input v-model="keyword" class="inp block" placeholder="商品名包含" @keyup.enter="applyFilters" />
        </div>

        <div class="group">
          <div class="group-title">价格（元）</div>
          <div class="range">
            <input v-model="minYuan" class="inp" placeholder="最低" @keyup.enter="applyFilters" />
            <span class="dash">—</span>
            <input v-model="maxYuan" class="inp" placeholder="最高" @keyup.enter="applyFilters" />
          </div>
          <p class="hint">只筛有有效价的商品</p>
        </div>

        <div v-if="facets.item_types.length" class="group">
          <div class="group-title">分类</div>
          <select v-model="itemType" class="inp block">
            <option value="">全部分类</option>
            <option v-for="type in facets.item_types" :key="type" :value="type">{{ type }}</option>
          </select>
        </div>

        <div class="group ops">
          <button class="btn primary sm" type="button" @click="applyFilters">筛选</button>
          <button class="btn sm" type="button" @click="resetFilters">重置</button>
        </div>
      </aside>

      <section class="main">
        <div class="bar">
          <span class="count">
            共 <b>{{ page?.total ?? 0 }}</b> 个商品
            <span v-if="totalPages > 1" class="muted">· 第 {{ currentPage }}/{{ totalPages }} 页</span>
          </span>
          <span class="spacer" />
          <select v-model="sort" class="inp">
            <option value="price_desc">价格从高到低</option>
            <option value="price_asc">价格从低到高</option>
            <option value="listings_desc">待售数量最多</option>
            <option value="name">按名字</option>
          </select>
        </div>

        <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
        <div v-if="error && !page" class="fail">{{ error }}</div>
        <EmptyState v-else-if="!page" kind="empty" text="读取中" />
        <EmptyState
          v-else-if="!page.quotes.length && filtering"
          kind="filtered"
          text="换个关键字、价格区间或分类试试"
        />
        <EmptyState v-else-if="!page.quotes.length" kind="empty" text="还没有采到行情" />

        <div v-else class="grid">
          <article v-for="q in page.quotes" :key="`${q.product_id}-${q.platform}-${q.side}`" class="card">
            <div class="stripe" :style="{ background: q.name_color ? `#${q.name_color}` : 'var(--line-strong)' }" />
            <div class="card-top">
              <span class="tag">{{ q.item_type || platformLabel(q.platform) }}</span>
              <span class="tag dim">{{ sideText(q.side) }}</span>
            </div>
            <h3 class="name" :title="q.name">{{ q.name }}</h3>
            <div class="shot">
              <img v-if="q.icon_path" :src="productImage(q.icon_path)" :alt="q.name" loading="lazy" />
              <span v-else class="no-shot">暂无图片</span>
            </div>
            <div class="card-foot">
              <div class="listings">
                待售数量：{{ q.present_order_count ?? '—' }}
              </div>
              <div v-if="q.present_cents != null" class="price" :title="fmtTime(q.present_collected_at ?? null)">
                {{ fenToYuan(q.present_cents) }}
              </div>
              <div v-else class="price none">无有效价</div>
              <div class="state">
                {{ quoteStatusText(q.status) }}
                <span class="muted">· {{ fmtAgo(q.collected_at) }}</span>
              </div>
              <div v-if="q.reason_code && (q.status === 'failed' || q.status === 'unavailable')" class="reason">
                {{ quoteReasonText(q.reason_code) }}
              </div>
            </div>
          </article>
        </div>

        <div v-if="totalPages > 1" class="pager">
          <button class="btn sm" type="button" :disabled="offset === 0" @click="turnPage(-1)">上一页</button>
          <span class="muted">第 {{ currentPage }} / {{ totalPages }} 页</span>
          <button
            class="btn sm"
            type="button"
            :disabled="offset + PAGE_SIZE >= (page?.total ?? 0)"
            @click="turnPage(1)"
          >下一页</button>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.pg { max-width: 1400px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); }

.layout { display: grid; grid-template-columns: 210px 1fr; gap: 20px; align-items: start; }
.side { border: 1px solid var(--line); padding: 12px; position: sticky; top: 12px; }
.group { margin-bottom: 14px; }
.group:last-child { margin-bottom: 0; }
.group-title {
  margin-bottom: 6px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.inp {
  height: 28px;
  padding: 0 8px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--line-strong);
  font-family: var(--mono);
  font-size: 12px;
}
.inp.block { width: 100%; }
.range { display: flex; align-items: center; gap: 6px; }
.range .inp { width: 100%; min-width: 0; }
.dash { color: var(--text-3); }
.hint { margin: 6px 0 0; color: var(--text-3); font-size: 11px; }
.segmented { display: flex; }
.seg {
  flex: 1;
  height: 26px;
  border: 1px solid var(--line-strong);
  border-right: 0;
  background: var(--bg);
  color: var(--text-3);
  cursor: pointer;
  font-family: var(--mono);
  font-size: 11px;
}
.seg:last-child { border-right: 1px solid var(--line-strong); }
.seg.on { background: var(--bg-elev); color: var(--text); }
.ops { display: flex; gap: 8px; }

.bar { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.count { font-size: 12px; color: var(--text-2); }
.spacer { flex: 1; }
.fail { color: var(--danger); }

/* 列数固定才能保证一页正好四行；窄屏降列，此时按可读性优先 */
.grid { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 10px; }
@media (max-width: 1180px) { .grid { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
@media (max-width: 700px) { .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.card {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--line);
  background: var(--surface);
  overflow: hidden;
}
.stripe { height: 3px; }
.card-top { display: flex; gap: 6px; padding: 8px 8px 0; }
.tag {
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 10px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tag.dim { margin-left: auto; }
.name {
  margin: 6px 8px 0;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.35;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 32px;
}
.shot { display: flex; align-items: center; justify-content: center; height: 108px; padding: 6px; }
.shot img { max-width: 100%; max-height: 100%; object-fit: contain; }
.no-shot { color: var(--text-3); font-size: 11px; }
.card-foot { padding: 0 8px 8px; }
.listings { color: var(--text-3); font-family: var(--mono); font-size: 10px; }
.price { margin-top: 2px; font-family: var(--mono); font-size: 14px; font-weight: 700; }
.price.none { color: var(--text-3); font-size: 11px; font-weight: 400; }
.state { margin-top: 2px; color: var(--text-3); font-size: 10px; }
.reason { margin-top: 2px; color: var(--warn); font-size: 10px; }

.pager { display: flex; align-items: center; gap: 10px; margin-top: 16px; }

@media (max-width: 820px) {
  .layout { grid-template-columns: 1fr; }
  .side { position: static; }
}
</style>
