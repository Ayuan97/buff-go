<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import {
  getPriceTicks,
  getQuoteFacets,
  getQuotes,
  getSteamFacets,
  getTargets,
  productImage,
  type DropWindow,
  type PriceTick,
  type Quote,
  type QuoteFilter,
  type QuoteSort,
  type Side,
  type SteamFacetsVocab,
  type Target,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import QuotePeek from '../components/QuotePeek.vue'
import { apiErrorText, dropPctText, dropWindowLabel, fenToYuan, fmtAgo, fmtTime, gameName, platformLabel, quoteReasonText, quoteStatusText, RUST_APPID, sideText } from '../utils/format'

const router = useRouter()
const CHUNK = 24
const REFRESH_LIMIT = 200
const PEEK_TICKS = 12
const PEEK_WIDTH = 520
const PEEK_MAX_HEIGHT = 640

const quotes = ref<Quote[]>([])
const total = ref(0)
const loaded = ref(false)
const loadingMore = ref(false)
const refreshing = ref(false)
const serverOffset = ref(0)
const facets = ref<{ appids: number[]; item_types: string[] }>({ appids: [], item_types: [] })
const steamVocab = ref<SteamFacetsVocab>({ categories: [], item_classes: [] })
const error = ref<string | null>(null)
const loadMoreError = ref<string | null>(null)
const stale = ref(false)
const sentinel = ref<HTMLElement | null>(null)
const board = ref<HTMLElement | null>(null)

// 离开行情页会卸掉组件，筛选记在本机，回来不用重选。
const FILTER_KEY = 'buffgo.market.filters'

type SavedFilters = {
  appid: number | ''
  side: Side | ''
  keyword: string
  itemTypes: string[]
  steamCats: string[]
  minYuan: string
  maxYuan: string
  dropped: boolean
  dropWindow: DropWindow
  minDropYuan: string
  sort: QuoteSort
  refreshSec: number
}

type AppliedFilters = Omit<SavedFilters, 'refreshSec'>

function readStringList(value: unknown, legacy: string): string[] {
  const out: string[] = []
  if (Array.isArray(value)) {
    for (const item of value) {
      if (typeof item === 'string' && item.trim()) out.push(item)
    }
  }
  if (!out.length && legacy.trim()) out.push(legacy)
  return out
}

function readSavedFilters(): SavedFilters {
  const fallback: SavedFilters = {
    appid: '',
    side: 'ask',
    keyword: '',
    itemTypes: [],
    steamCats: [],
    minYuan: '',
    maxYuan: '',
    dropped: false,
    dropWindow: '24h',
    minDropYuan: '',
    sort: 'price_desc',
    refreshSec: 10,
  }
  try {
    const raw = localStorage.getItem(FILTER_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as Partial<SavedFilters> & { itemType?: string }
    const app = Number(parsed.appid)
    const sorts: QuoteSort[] = ['price_desc', 'price_asc', 'listings_desc', 'name', 'drop_desc', 'drop_pct_desc']
    const windows: DropWindow[] = ['24h', '7d', '30d']
    const refreshSecs = [0, 10, 30, 60]
    return {
      appid: Number.isInteger(app) && app > 0 ? app : '',
      side: parsed.side === 'ask' || parsed.side === 'bid' || parsed.side === '' ? parsed.side : 'ask',
      keyword: typeof parsed.keyword === 'string' ? parsed.keyword : '',
      itemTypes: readStringList(parsed.itemTypes, typeof parsed.itemType === 'string' ? parsed.itemType : ''),
      steamCats: readStringList(parsed.steamCats, ''),
      minYuan: typeof parsed.minYuan === 'string' ? parsed.minYuan : '',
      maxYuan: typeof parsed.maxYuan === 'string' ? parsed.maxYuan : '',
      dropped: parsed.dropped === true,
      dropWindow: parsed.dropWindow && windows.includes(parsed.dropWindow) ? parsed.dropWindow : '24h',
      minDropYuan: typeof parsed.minDropYuan === 'string' ? parsed.minDropYuan : '',
      sort: parsed.sort && sorts.includes(parsed.sort) ? parsed.sort : 'price_desc',
      refreshSec: refreshSecs.includes(Number(parsed.refreshSec)) ? Number(parsed.refreshSec) : 10,
    }
  } catch {
    return fallback
  }
}

const saved = readSavedFilters()

// 筛选条件全部交给后端，前端不再对截断结果二次过滤
const appid = ref<number | ''>(saved.appid)
const side = ref<Side | ''>(saved.side)
const keyword = ref(saved.keyword)
const itemTypes = ref<string[]>(saved.itemTypes)
const steamCats = ref<string[]>(saved.steamCats)
const minYuan = ref(saved.minYuan)
const maxYuan = ref(saved.maxYuan)
const dropped = ref(saved.dropped)
const dropWindow = ref<DropWindow>(saved.dropWindow)
const minDropYuan = ref(saved.minDropYuan)
const sort = ref<QuoteSort>(saved.sort)
const refreshSec = ref(saved.refreshSec)
const appliedFilters = ref<AppliedFilters>({
  appid: saved.appid,
  side: saved.side,
  keyword: saved.keyword,
  itemTypes: [...saved.itemTypes],
  steamCats: [...saved.steamCats],
  minYuan: saved.minYuan,
  maxYuan: saved.maxYuan,
  dropped: saved.dropped,
  dropWindow: saved.dropWindow,
  minDropYuan: saved.minDropYuan,
  sort: saved.sort,
})
const peek = ref<{ quote: Quote; related: Quote[] | null; ticks: PriceTick[] | null; x: number; y: number } | null>(null)
const peekCache = new Map<number, { related: Quote[]; ticks: PriceTick[] }>()
const targets = ref<Target[]>([])

let poll = 0
let seq = 0
let peekSeq = 0
let hidePeekTimer = 0
let moreObs: IntersectionObserver | null = null

const hasMore = computed(() => loaded.value && serverOffset.value < total.value)
const rustSelected = computed(() => appid.value === RUST_APPID)
const filtering = computed(
  () =>
    !!appliedFilters.value.keyword.trim() ||
    appliedFilters.value.itemTypes.length > 0 ||
    appliedFilters.value.steamCats.length > 0 ||
    !!appliedFilters.value.minYuan.trim() ||
    !!appliedFilters.value.maxYuan.trim() ||
    appliedFilters.value.dropped ||
    !!appliedFilters.value.minDropYuan.trim(),
)

function quoteKey(quote: Quote): string {
  return `${quote.product_id}-${quote.platform}-${quote.side}`
}

// 输入的是元，后端按分筛选；填了非数字就当没填，不发必失败的请求
function yuanToCents(raw: string): number | undefined {
  const trimmed = raw.trim()
  if (!trimmed) return undefined
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value < 0) return undefined
  return Math.round(value * 100)
}

function currentFilter(offset: number, limit = CHUNK): QuoteFilter {
  const applied = appliedFilters.value
  return {
    appid: applied.appid === '' ? undefined : applied.appid,
    side: applied.side || undefined,
    keyword: applied.keyword.trim() || undefined,
    item_types: applied.itemTypes.length ? applied.itemTypes : undefined,
    steam_cats: applied.steamCats.length ? applied.steamCats : undefined,
    min_cents: yuanToCents(applied.minYuan),
    max_cents: yuanToCents(applied.maxYuan),
    dropped: applied.dropped || undefined,
    drop_window: applied.dropWindow,
    min_drop_cents: yuanToCents(applied.minDropYuan),
    sort: applied.sort,
    limit,
    offset,
  }
}

async function load(reset: boolean) {
  if (reset) {
    loaded.value = false
    loadingMore.value = false
    refreshing.value = false
    quotes.value = []
    total.value = 0
    serverOffset.value = 0
    loadMoreError.value = null
  } else if (!loaded.value || loadingMore.value || refreshing.value || !hasMore.value || loadMoreError.value) {
    return
  } else {
    loadingMore.value = true
  }
  const token = ++seq
  const offset = reset ? 0 : serverOffset.value
  try {
    const result = await getQuotes(currentFilter(offset))
    if (token !== seq) return
    total.value = result.total
    serverOffset.value = result.quotes.length ? offset + result.quotes.length : result.total
    if (reset) {
      quotes.value = result.quotes
    } else if (!result.quotes.length) {
      serverOffset.value = result.total
    } else {
      const seen = new Set(quotes.value.map(quoteKey))
      quotes.value = [...quotes.value, ...result.quotes.filter((row) => !seen.has(quoteKey(row)))]
    }
    loaded.value = true
    error.value = null
    loadMoreError.value = null
    stale.value = false
  } catch (e) {
    if (token !== seq) return
    const message = e instanceof Error ? apiErrorText(e.message) : '加载失败'
    if (!reset) {
      loadMoreError.value = message
    } else if (quotes.value.length) {
      stale.value = true
    } else {
      error.value = message
    }
  } finally {
    if (token === seq) loadingMore.value = false
  }
}

async function refreshSilent() {
  if (!loaded.value || loadingMore.value || refreshing.value) return
  if (serverOffset.value > REFRESH_LIMIT) return
  const token = ++seq
  const keep = Math.max(serverOffset.value, CHUNK)
  refreshing.value = true
  try {
    const refreshed: Quote[] = []
    const seen = new Set<string>()
    let offset = 0
    let refreshedTotal = total.value
    while (offset < keep) {
      const limit = Math.min(REFRESH_LIMIT, keep - offset)
      const result = await getQuotes(currentFilter(offset, limit))
      if (token !== seq || !loaded.value) return
      refreshedTotal = result.total
      for (const quote of result.quotes) {
        const key = quoteKey(quote)
        if (!seen.has(key)) {
          seen.add(key)
          refreshed.push(quote)
        }
      }
      if (!result.quotes.length) {
        offset = result.total
        break
      }
      offset += result.quotes.length
      if (offset >= result.total) break
    }
    if (token !== seq || !loaded.value) return
    total.value = refreshedTotal
    serverOffset.value = Math.min(offset, refreshedTotal)
    quotes.value = refreshed
    stale.value = false
    error.value = null
    peekCache.clear()
  } catch {
    if (token === seq && quotes.value.length) stale.value = true
  } finally {
    if (token === seq) refreshing.value = false
  }
}

function refreshNow() {
  if (serverOffset.value > REFRESH_LIMIT) {
    resetAndLoad()
    return
  }
  void refreshSilent()
}

function startPoll() {
  if (poll) window.clearInterval(poll)
  poll = 0
  if (refreshSec.value <= 0) return
  poll = window.setInterval(() => {
    void loadTargets()
    if (document.hidden) return
    void refreshSilent()
  }, refreshSec.value * 1000)
}

function resetAndLoad() {
  if (board.value) board.value.scrollTop = 0
  void load(true)
}

function draftFilters(): AppliedFilters {
  return {
    appid: appid.value,
    side: side.value,
    keyword: keyword.value,
    itemTypes: [...itemTypes.value],
    steamCats: [...steamCats.value],
    minYuan: minYuan.value,
    maxYuan: maxYuan.value,
    dropped: dropped.value,
    dropWindow: dropWindow.value,
    minDropYuan: minDropYuan.value,
    sort: sort.value,
  }
}

function applyDraftFilters(force: boolean) {
  const next = draftFilters()
  if (!force && JSON.stringify(next) === JSON.stringify(appliedFilters.value)) return
  appliedFilters.value = next
  resetAndLoad()
}

function retryLoadMore() {
  loadMoreError.value = null
  void load(false)
}

async function loadFacets() {
  try {
    facets.value = await getQuoteFacets(appid.value === '' ? undefined : appid.value)
  } catch {
    // 筛选项拿不到不影响列表本身，保留上一次的选项
  }
}

async function loadSteamVocab() {
  if (!rustSelected.value) return
  try {
    steamVocab.value = await getSteamFacets(RUST_APPID)
  } catch {
    // 词表拿不到就先不显示 Rust 勾选
  }
}

function toggleSteamCat(slug: string) {
  steamCats.value = steamCats.value.includes(slug)
    ? steamCats.value.filter((value) => value !== slug)
    : [...steamCats.value, slug]
}

function toggleItemType(label: string) {
  itemTypes.value = itemTypes.value.includes(label)
    ? itemTypes.value.filter((value) => value !== label)
    : [...itemTypes.value, label]
}

function rustClassesOf(slug: string) {
  return steamVocab.value.item_classes.filter((item) => item.category === slug)
}

async function loadTargets() {
  try {
    targets.value = await getTargets()
  } catch {
    // 开关只用来区分未开和还没扫到，拿不到就当没有开关信息
  }
}

// 换游戏要重新取分类，因为分类是按游戏统计的
watch(appid, () => {
  itemTypes.value = []
  steamCats.value = []
  void loadFacets()
  void loadSteamVocab()
})
watch(dropWindow, () => peekCache.clear())
watch([appid, side, itemTypes, steamCats, dropped, dropWindow, sort], () => applyDraftFilters(false))
watch([loaded, hasMore, loadingMore, refreshing], () => {
  if (!loaded.value || !hasMore.value || loadingMore.value || refreshing.value || loadMoreError.value || !sentinel.value || !board.value) return
  if (sentinel.value.getBoundingClientRect().top < board.value.getBoundingClientRect().bottom + 600) {
    void load(false)
  }
})
watch(refreshSec, () => startPoll())
watch([appid, side, keyword, itemTypes, steamCats, minYuan, maxYuan, dropped, dropWindow, minDropYuan, sort, refreshSec], () => {
  try {
    localStorage.setItem(
      FILTER_KEY,
      JSON.stringify({
        appid: appid.value,
        side: side.value,
        keyword: keyword.value,
        itemTypes: itemTypes.value,
        steamCats: steamCats.value,
        minYuan: minYuan.value,
        maxYuan: maxYuan.value,
        dropped: dropped.value,
        dropWindow: dropWindow.value,
        minDropYuan: minDropYuan.value,
        sort: sort.value,
        refreshSec: refreshSec.value,
      } satisfies SavedFilters),
    )
  } catch {
    // 隐私模式写不进去就当没记住
  }
})

function applyFilters() {
  applyDraftFilters(true)
}

function resetFilters() {
  keyword.value = ''
  itemTypes.value = []
  steamCats.value = []
  minYuan.value = ''
  maxYuan.value = ''
  dropped.value = false
  dropWindow.value = '24h'
  minDropYuan.value = ''
  applyDraftFilters(true)
}

function dropTitle(quote: Quote): string {
  const bits = [`相对${dropWindowLabel(appliedFilters.value.dropWindow)}最高价`]
  if (quote.high_cents != null) bits.push(`高 ${fenToYuan(quote.high_cents)}`)
  if (quote.drop_count) bits.push(`降了 ${quote.drop_count} 次`)
  if (quote.last_drop_at) bits.push(`最近 ${fmtAgo(quote.last_drop_at)}`)
  return bits.join(' · ')
}

function placePeek(event: MouseEvent): { x: number; y: number } {
  const box = (event.currentTarget as HTMLElement).getBoundingClientRect()
  const x = box.right + 8 + PEEK_WIDTH > window.innerWidth ? Math.max(8, box.left - PEEK_WIDTH - 8) : box.right + 8
  const y = Math.max(8, Math.min(box.top, window.innerHeight - PEEK_MAX_HEIGHT - 8))
  return { x, y }
}

function cancelHidePeek() {
  if (hidePeekTimer) {
    window.clearTimeout(hidePeekTimer)
    hidePeekTimer = 0
  }
}

async function showPeek(quote: Quote, event: MouseEvent) {
  cancelHidePeek()
  const pos = placePeek(event)
  const cached = peekCache.get(quote.product_id)
  peek.value = { quote, related: cached?.related ?? null, ticks: cached?.ticks ?? null, ...pos }
  if (cached) return
  const token = ++peekSeq
  try {
    const [result, tickPage] = await Promise.all([
      getQuotes({ product_id: quote.product_id, drop_window: appliedFilters.value.dropWindow, limit: 12 }),
      getPriceTicks({ product_id: quote.product_id, limit: PEEK_TICKS }),
    ])
    peekCache.set(quote.product_id, { related: result.quotes, ticks: tickPage.ticks })
    if (token !== peekSeq || peek.value?.quote.product_id !== quote.product_id) return
    peek.value = { ...peek.value, related: result.quotes, ticks: tickPage.ticks }
  } catch {
    if (token !== peekSeq || peek.value?.quote.product_id !== quote.product_id) return
    peek.value = { ...peek.value, related: [quote], ticks: peek.value.ticks ?? [] }
  }
}

function openProduct(quote: Quote) {
  hidePeek()
  void router.push(`/market/${quote.product_id}`)
}

function hidePeek() {
  cancelHidePeek()
  peekSeq += 1
  peek.value = null
}

function scheduleHidePeek() {
  cancelHidePeek()
  hidePeekTimer = window.setTimeout(hidePeek, 160)
}

onMounted(() => {
  moreObs = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void load(false)
    },
    { root: board.value, rootMargin: '600px 0px' },
  )
  if (sentinel.value) moreObs.observe(sentinel.value)
  void loadFacets()
  void loadSteamVocab()
  void loadTargets()
  void load(true)
  startPoll()
})
onUnmounted(() => {
  seq += 1
  loaded.value = false
  refreshing.value = false
  if (poll) window.clearInterval(poll)
  moreObs?.disconnect()
  hidePeek()
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>行情</h1>
      <span class="count">
        已显示 <b>{{ quotes.length }}</b> / {{ total }} 个商品
      </span>
      <span class="spacer" />
      <select v-model.number="refreshSec" class="inp" title="按当前筛选刷新，不重置条件">
        <option :value="0">关闭自动刷新</option>
        <option :value="10">每 10 秒刷新</option>
        <option :value="30">每 30 秒刷新</option>
        <option :value="60">每 60 秒刷新</option>
      </select>
      <button
        class="btn sm"
        type="button"
        :disabled="!loaded || loadingMore || refreshing"
        :title="serverOffset > REFRESH_LIMIT ? '已加载超过 200 条，刷新会回到首屏' : '刷新已加载范围'"
        @click="refreshNow"
      >
        {{ refreshing ? '刷新中' : '刷新' }}
      </button>
      <select v-model="sort" class="inp">
        <option value="price_desc">价格从高到低</option>
        <option value="price_asc">价格从低到高</option>
        <option value="listings_desc">{{ side === 'bid' ? '求购数量最多' : side === 'ask' ? '待售数量最多' : '挂单数量最多' }}</option>
        <option value="drop_desc">降价最多</option>
        <option value="drop_pct_desc">降幅最大</option>
        <option value="name">按名字</option>
      </select>
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

        <div class="group">
          <div class="group-title">降价窗口</div>
          <div class="segmented">
            <button class="seg" :class="{ on: dropWindow === '24h' }" type="button" @click="dropWindow = '24h'">24 小时</button>
            <button class="seg" :class="{ on: dropWindow === '7d' }" type="button" @click="dropWindow = '7d'">7 天</button>
            <button class="seg" :class="{ on: dropWindow === '30d' }" type="button" @click="dropWindow = '30d'">30 天</button>
          </div>
          <label class="check">
            <input v-model="dropped" type="checkbox" />
            只看降价
          </label>
          <div class="range">
            <input v-model="minDropYuan" class="inp" placeholder="至少降" @keyup.enter="applyFilters" />
            <span class="dash">元</span>
          </div>
          <p class="hint">相对窗口内最高价，不是只看最后一跳</p>
        </div>

        <div v-if="rustSelected && steamVocab.categories.length" class="group">
          <div class="group-title">分类</div>
          <div class="checks">
            <label v-for="cat in steamVocab.categories" :key="cat.slug" class="check">
              <input
                type="checkbox"
                :checked="steamCats.includes(cat.slug)"
                @change="toggleSteamCat(cat.slug)"
              />
              {{ cat.name || cat.label }}
            </label>
          </div>
        </div>

        <div v-if="rustSelected && steamVocab.item_classes.length" class="group">
          <div class="group-title">物品类型</div>
          <div class="checks tall">
            <template v-for="cat in steamVocab.categories" :key="cat.slug">
              <template v-if="rustClassesOf(cat.slug).length">
                <div class="sub">{{ cat.name || cat.label }}</div>
                <label v-for="cls in rustClassesOf(cat.slug)" :key="cls.slug" class="check">
                  <input
                    type="checkbox"
                    :checked="itemTypes.includes(cls.label)"
                    @change="toggleItemType(cls.label)"
                  />
                  {{ cls.name || cls.label }}
                </label>
              </template>
            </template>
          </div>
        </div>

        <div v-else-if="appid && facets.item_types.length" class="group">
          <div class="group-title">分类</div>
          <div class="checks tall">
            <label v-for="type in facets.item_types" :key="type" class="check">
              <input
                type="checkbox"
                :checked="itemTypes.includes(type)"
                @change="toggleItemType(type)"
              />
              {{ type }}
            </label>
          </div>
        </div>

        <div class="group ops">
          <button class="btn primary sm" type="button" @click="applyFilters">筛选</button>
          <button class="btn sm" type="button" @click="resetFilters">重置</button>
        </div>
      </aside>

      <section ref="board" class="main" @scroll="hidePeek">
        <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
        <div v-if="error && !quotes.length" class="fail">{{ error }}</div>
        <EmptyState v-else-if="!loaded" kind="empty" text="读取中" />
        <EmptyState
          v-else-if="!quotes.length && filtering"
          kind="filtered"
          text="换个关键字、价格区间或勾选试试"
        />
        <EmptyState v-else-if="!quotes.length" kind="empty" text="还没有采到行情" />
        <div v-else class="grid">
          <article
            v-for="q in quotes"
            :key="`${q.product_id}-${q.platform}-${q.side}`"
            class="card"
            @click="openProduct(q)"
            @mouseenter="showPeek(q, $event)"
            @mouseleave="scheduleHidePeek"
          >
            <div class="stripe" :style="{ background: q.name_color ? `#${q.name_color}` : 'var(--line-strong)' }" />
            <div class="card-top">
              <span class="tag">{{ q.item_type || platformLabel(q.platform) }}</span>
              <span v-if="q.drop_cents" class="tag drop" :title="dropTitle(q)">
                降 {{ fenToYuan(q.drop_cents) }}
                <template v-if="q.drop_pct_bp"> {{ dropPctText(q.drop_pct_bp) }}</template>
              </span>
              <span class="tag dim">{{ sideText(q.side) }}</span>
            </div>
            <h3 class="name" :title="q.name">{{ q.name }}</h3>
            <div class="shot">
              <img v-if="q.icon_path" :src="productImage(q.icon_path)" :alt="q.name" loading="lazy" />
              <span v-else class="no-shot">暂无图片</span>
            </div>
            <div class="card-foot">
              <div class="listings">
                <template v-if="q.status === 'present'">
                  {{ q.side === 'bid' ? '求购数量' : '待售数量' }}：{{ q.present_order_count ?? '—' }}
                </template>
                <template v-else>当前挂单数：—</template>
              </div>
              <div
                v-if="q.status === 'present' && q.present_cents != null"
                class="price"
                :title="fmtTime(q.present_collected_at ?? null)"
              >
                {{ fenToYuan(q.present_cents) }}
              </div>
              <div
                v-else-if="q.status !== 'present' && q.present_cents != null"
                class="price none"
                :title="fmtTime(q.present_collected_at ?? null)"
              >
                历史有效价 {{ fenToYuan(q.present_cents) }}
              </div>
              <div v-else class="price none">{{ q.status === 'present' ? '无有效价' : '无历史有效价' }}</div>
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
        <div ref="sentinel" class="more">
          <span v-if="loadingMore">加载中</span>
          <span v-else-if="loadMoreError" class="fail">
            {{ loadMoreError }}
            <button class="btn sm" type="button" @click="retryLoadMore">重试</button>
          </span>
          <span v-else-if="hasMore">继续下滚加载</span>
          <span v-else-if="loaded && quotes.length">已显示全部</span>
        </div>

        <QuotePeek
          v-if="peek"
          :quote="peek.quote"
          :related="peek.related"
          :ticks="peek.ticks"
          :targets="targets"
          :drop-window="appliedFilters.dropWindow"
          :x="peek.x"
          :y="peek.y"
          @enter="cancelHidePeek"
          @leave="scheduleHidePeek"
        />
      </section>
    </div>
  </div>
</template>

<style scoped>
.pg {
  width: 100%;
  max-width: none;
  height: 100vh;
  margin: 0;
  padding: 16px 20px;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.hero {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--line-strong);
  flex-shrink: 0;
}
.hero h1 { margin: 0; font-size: 20px; }
.count { font-size: 12px; color: var(--text-2); }
.spacer { flex: 1; }

.layout {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: 248px minmax(0, 1fr);
  gap: 20px;
}
.side {
  border: 1px solid var(--line);
  padding: 14px;
  min-height: 0;
  overflow: auto;
}
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
.segmented + .check,
.check + .range { margin-top: 8px; }
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
.checks { display: flex; flex-direction: column; gap: 6px; }
.checks.tall { max-height: 220px; overflow: auto; }
.check { display: flex; gap: 6px; align-items: center; font-size: 12px; }
.sub {
  margin-top: 4px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 10px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.main {
  min-width: 0;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: var(--line-strong) transparent;
}
.fail { color: var(--danger); padding: 12px; }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 14px;
}
.card {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--line);
  background: var(--surface);
  overflow: hidden;
  min-height: 340px;
  cursor: pointer;
}
.stripe { height: 3px; }
.card-top { display: flex; gap: 6px; padding: 10px 10px 0; }
.tag {
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tag.dim { margin-left: auto; }
.tag.drop {
  color: var(--ok);
  border: 1px solid color-mix(in srgb, var(--ok) 40%, transparent);
  background: var(--ok-dim);
  padding: 0 5px;
  flex-shrink: 0;
}
.name {
  margin: 8px 10px 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.35;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 38px;
}
.shot { display: flex; align-items: center; justify-content: center; height: 168px; padding: 8px 10px; }
.shot img { max-width: 100%; max-height: 100%; object-fit: contain; }
.no-shot { color: var(--text-3); font-size: 12px; }
.card-foot { padding: 0 10px 12px; }
.listings { color: var(--text-3); font-family: var(--mono); font-size: 11px; }
.price { margin-top: 4px; font-family: var(--mono); font-size: 18px; font-weight: 700; }
.price.none { color: var(--text-3); font-size: 12px; font-weight: 400; }
.state { margin-top: 4px; color: var(--text-3); font-size: 11px; }
.reason { margin-top: 2px; color: var(--warn); font-size: 11px; }
.more {
  margin-top: 16px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

@media (max-width: 820px) {
  .pg { height: auto; overflow: visible; }
  .layout { grid-template-columns: 1fr; }
  .main { overflow: visible; }
}
</style>
