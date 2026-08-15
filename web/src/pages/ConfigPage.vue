<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  createProvider,
  deleteProvider,
  getProviders,
  getSteamFacets,
  getTargets,
  getWatermarks,
  replaceProviderCredential,
  setTargetPriceRange,
  setTargetSort,
  setTargetSteamFacets,
  setWatermark,
  updateProvider,
  type Provider,
  type Region,
  type SortColumn,
  type SortDirection,
  type SteamFacetsVocab,
  type Target,
  type Watermark,
} from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import SteamFacetDialog from '../components/SteamFacetDialog.vue'
import { useEscape } from '../utils/escape'
import { apiErrorText, gameName, platformLabel, regionText, RUST_APPID, sideText } from '../utils/format'

const REGIONS: Region[] = ['domestic', 'foreign', 'hongkong']

// 每一项对应 Steam 的 sort_column 与 sort_dir 组合
const SORT_OPTIONS: { value: string; label: string }[] = [
  { value: 'price:desc', label: '价格从高到低' },
  { value: 'price:asc', label: '价格从低到高' },
  { value: 'quantity:desc', label: '在售数量从多到少' },
  { value: 'quantity:asc', label: '在售数量从少到多' },
  { value: 'name:asc', label: '按名字' },
]

const watermarks = ref<Watermark[] | null>(null)
const providers = ref<Provider[] | null>(null)
const targets = ref<Target[] | null>(null)
const sortDraft = ref<Record<number, string>>({})
const rangeDraft = ref<Record<number, { min: string; max: string }>>({})
const steamVocab = ref<SteamFacetsVocab>({ categories: [], item_classes: [] })
const facetEdit = ref<Target | null>(null)
const loadError = ref<string | null>(null)
const actionError = ref<string | null>(null)
const busy = ref(false)
const confirm = ref<{ title: string; impact: string; consequence: string; run: () => Promise<unknown> } | null>(null)
const draftMin = ref<Record<string, string>>({})
const showAdd = ref(false)
const credEdit = ref<{ provider: Provider; credential: string } | null>(null)
const providerEdit = ref<{
  provider: Provider
  name: string
  priority: string
  enabled: boolean
  regions: Record<Region, boolean>
} | null>(null)
const editError = ref<string | null>(null)
const newProvider = ref({
  name: '',
  priority: '1',
  enabled: true,
  regions: { foreign: true, hongkong: false, domestic: false } as Record<Region, boolean>,
  credential: '',
})

onMounted(() => { void reload() })

useEscape(() => {
  if (confirm.value) return
  if (facetEdit.value) { facetEdit.value = null; return }
  if (credEdit.value) { credEdit.value = null; return }
  if (providerEdit.value) { providerEdit.value = null; return }
  if (showAdd.value) showAdd.value = false
})

function sortKey(target: Target): string {
  return `${target.sort_column}:${target.sort_dir}`
}

function sortLabel(key: string): string {
  return SORT_OPTIONS.find((option) => option.value === key)?.label ?? key
}

function centsToYuan(cents: number | undefined): string {
  if (cents == null) return ''
  return (cents / 100).toFixed(2)
}

function yuanToCents(raw: string): number | null | undefined {
  const text = raw.trim()
  if (!text) return null
  const value = Number(text)
  if (!Number.isFinite(value) || value < 0) return undefined
  return Math.round(value * 100)
}

function rangeKey(target: Target): string {
  return `${target.price_min_cents ?? ''}:${target.price_max_cents ?? ''}`
}

function draftRangeKey(target: Target): string {
  const draft = rangeDraft.value[target.id]
  if (!draft) return rangeKey(target)
  const min = yuanToCents(draft.min)
  const max = yuanToCents(draft.max)
  if (min === undefined || max === undefined) return 'invalid'
  return `${min ?? ''}:${max ?? ''}`
}

function rangeDirty(target: Target): boolean {
  return draftRangeKey(target) !== rangeKey(target)
}

function rangeOf(id: number): { min: string; max: string } {
  if (!rangeDraft.value[id]) rangeDraft.value[id] = { min: '', max: '' }
  return rangeDraft.value[id]
}

function isRust(target: Target): boolean {
  return target.appid === RUST_APPID
}

function facetName(slug: string, kind: 'cat' | 'class'): string {
  if (kind === 'cat') return steamVocab.value.categories.find((item) => item.slug === slug)?.name ?? slug
  return steamVocab.value.item_classes.find((item) => item.slug === slug)?.name ?? slug
}

function facetSummary(target: Target): string {
  const cats = target.steam_cats ?? []
  const classes = target.item_classes ?? []
  if (!cats.length && !classes.length) return '不限'
  const catText = cats.length ? cats.map((slug) => facetName(slug, 'cat')).join('、') : '分类不限'
  if (!classes.length) return catText
  if (classes.length <= 2) return `${catText} · ${classes.map((slug) => facetName(slug, 'class')).join('、')}`
  return `${catText} · ${classes.length} 个类型`
}

// 保存顺序会推进开关版本并清空该方向队列，所以走确认框
function requestSortChange(target: Target) {
  const key = sortDraft.value[target.id]
  if (!key || key === sortKey(target) || busy.value) return
  confirm.value = {
    title: '改采集顺序',
    impact: `${gameName(target.appid)} · ${sideText(target.side)} → ${sortLabel(key)}`,
    consequence:
      '顺序一换，补货游标没有意义了：该方向未完成任务会丢掉，补货从第一页重新开始。' +
      '已经存下来的行情不会丢。',
    run: async () => {
      const [column, direction] = key.split(':')
      await setTargetSort(target.id, target.revision, column as SortColumn, direction as SortDirection)
    },
  }
}

function requestRangeChange(target: Target) {
  const draft = rangeDraft.value[target.id]
  if (!draft || !rangeDirty(target) || busy.value) return
  const min = yuanToCents(draft.min)
  const max = yuanToCents(draft.max)
  if (min === undefined || max === undefined) {
    actionError.value = '价格区间必须是不小于 0 的数字'
    return
  }
  if (min != null && max != null && min > max) {
    actionError.value = '最低价不能高于最高价'
    return
  }
  const label = `${min == null ? '不限' : `¥${(min / 100).toFixed(2)}`} ~ ${max == null ? '不限' : `¥${(max / 100).toFixed(2)}`}`
  confirm.value = {
    title: '改采集价格区间',
    impact: `${gameName(target.appid)} · ${sideText(target.side)} → ${label}`,
    consequence:
      target.side === 'ask'
        ? '区间会直接传给 Steam 搜索。该方向未完成任务会丢掉，补货从第一页重新开始。已经存下来的行情不会丢。'
        : '求购没有 Steam 搜索区间，这项存了也不会打出去。该方向未完成任务仍会丢掉。',
    run: async () => {
      await setTargetPriceRange(target.id, target.revision, min, max)
    },
  }
}

function saveFacets(cats: string[], classes: string[]) {
  const target = facetEdit.value
  if (!target || busy.value) return
  void runAction(async () => {
    await setTargetSteamFacets(target.id, target.revision, cats, classes)
    facetEdit.value = null
  })
}

async function reload() {
  try {
    const [marks, list, targetList, vocab] = await Promise.all([
      getWatermarks(),
      getProviders(),
      getTargets(),
      getSteamFacets(RUST_APPID).catch(() => steamVocab.value),
    ])
    watermarks.value = marks
    providers.value = list
    targets.value = targetList
    steamVocab.value = vocab
    const next: Record<string, string> = {}
    for (const mark of marks) next[mark.region] = String(mark.min_usable)
    draftMin.value = next
    const sorts: Record<number, string> = {}
    const ranges: Record<number, { min: string; max: string }> = {}
    for (const target of targetList) {
      sorts[target.id] = sortKey(target)
      ranges[target.id] = {
        min: centsToYuan(target.price_min_cents),
        max: centsToYuan(target.price_max_cents),
      }
    }
    sortDraft.value = sorts
    rangeDraft.value = ranges
    loadError.value = null
  } catch (e) {
    loadError.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
    if (watermarks.value == null) watermarks.value = []
    if (providers.value == null) providers.value = []
    if (targets.value == null) targets.value = []
  }
}

async function runAction(fn: () => Promise<unknown>) {
  if (busy.value) return
  actionError.value = null
  busy.value = true
  try {
    await fn()
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '操作失败'
  } finally {
    busy.value = false
  }
}

function selectedRegions(flags: Record<Region, boolean>): Region[] {
  return REGIONS.filter((region) => flags[region])
}

async function saveWatermark(mark: Watermark) {
  const min = Number(draftMin.value[mark.region])
  if (!Number.isInteger(min) || min < 0) {
    actionError.value = '最少可用必须是 0 或正整数'
    return
  }
  await runAction(() => setWatermark(mark.region, mark.revision, min))
}

async function addProvider() {
  const name = newProvider.value.name.trim()
  const priority = Number(newProvider.value.priority)
  const regions = selectedRegions(newProvider.value.regions)
  const credential = newProvider.value.credential.trim()
  if (!name || !credential || !regions.length || !Number.isInteger(priority) || priority < 1) {
    actionError.value = '名称、优先级、可供地域和凭证都要填'
    return
  }
  await runAction(async () => {
    await createProvider({ name, enabled: newProvider.value.enabled, priority, regions, credential })
    newProvider.value = {
      name: '',
      priority: '1',
      enabled: true,
      regions: { foreign: true, hongkong: false, domestic: false },
      credential: '',
    }
    showAdd.value = false
  })
}

function openProviderEdit(provider: Provider) {
  editError.value = null
  providerEdit.value = {
    provider,
    name: provider.name,
    priority: String(provider.priority),
    enabled: provider.enabled,
    regions: {
      domestic: provider.regions.includes('domestic'),
      foreign: provider.regions.includes('foreign'),
      hongkong: provider.regions.includes('hongkong'),
    },
  }
}

async function saveProvider() {
  const draft = providerEdit.value
  if (!draft || busy.value) return
  const name = draft.name.trim()
  const priority = Number(draft.priority)
  const regions = selectedRegions(draft.regions)
  if (!name) {
    editError.value = '名称不能为空'
    return
  }
  if (!regions.length) {
    editError.value = '至少选一个可供地域'
    return
  }
  if (!Number.isInteger(priority) || priority < 1 || priority > 10000) {
    editError.value = '优先级必须是 1 到 10000 的整数'
    return
  }
  editError.value = null
  busy.value = true
  try {
    await updateProvider(draft.provider.id, {
      expected_revision: draft.provider.revision,
      name,
      enabled: draft.enabled,
      priority,
      regions,
    })
    providerEdit.value = null
    await reload()
  } catch (e) {
    // 弹窗还开着，错误留在弹窗里；页面顶部的提示会被遮罩挡住
    editError.value = e instanceof Error ? apiErrorText(e.message) : '保存失败'
  } finally {
    busy.value = false
  }
}

async function toggleEnabled(provider: Provider) {
  await runAction(() => updateProvider(provider.id, {
    expected_revision: provider.revision,
    name: provider.name,
    enabled: !provider.enabled,
    priority: provider.priority,
    regions: provider.regions,
  }))
}

async function saveCredential() {
  const draft = credEdit.value
  if (!draft) return
  const credential = draft.credential.trim()
  if (!credential) {
    actionError.value = '请填写代理商凭证'
    return
  }
  await runAction(async () => {
    await replaceProviderCredential(draft.provider.id, draft.provider.revision, credential)
    credEdit.value = null
  })
}
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>配置</h1>
      <p class="hero-sub">这里存的是以后自动补短效代理要用的参数。程序现在不会向代理商拉号，填了也不会多出节点。</p>
    </header>

    <div v-if="actionError" class="panel fail">
      {{ actionError }}
      <button class="btn sm" @click="actionError = null">知道了</button>
    </div>
    <div v-if="loadError" class="panel"><EmptyState kind="empty" :text="loadError" /></div>

    <section class="panel">
      <div class="panel-head">采集配置</div>
      <div class="panel-body">
        <p class="note">
          顺序决定平台先返回哪一头。出售价格区间会原样传给 Steam 搜索
          （price_min / price_max / price_currency=23），区间外的商品 Steam 根本不返回。
          Rust 的分类和物品类型在页面上是中文，交给 Steam 的仍是原来的英文参数。
          求购没有这些参数，填了也不会少打。
        </p>
        <p class="note warn">
          改顺序、价格区间或分类会丢掉该方向未完成任务，补货从头开始。已经存下来的行情不会丢。
        </p>
        <EmptyState v-if="!targets" kind="empty" text="读取中" />
        <EmptyState v-else-if="!targets.length" kind="unconfigured" text="还没有采集目标，先去概览页加游戏" />
        <table v-else class="data">
          <thead>
            <tr><th>方向</th><th>顺序</th><th>价格区间（元）</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="target in targets" :key="target.id">
              <td>
                <div class="strong">{{ gameName(target.appid) }} · {{ sideText(target.side) }}</div>
                <div class="muted">{{ platformLabel(target.platform) }}</div>
                <div v-if="isRust(target)" class="muted">筛选：{{ facetSummary(target) }}</div>
              </td>
              <td>
                <select v-model="sortDraft[target.id]" class="mini wide">
                  <option v-for="option in SORT_OPTIONS" :key="option.value" :value="option.value">
                    {{ option.label }}
                  </option>
                </select>
              </td>
              <td>
                <div class="range">
                  <input
                    v-model="rangeOf(target.id).min"
                    class="mini"
                    inputmode="decimal"
                    placeholder="最低"
                  />
                  <span class="muted">~</span>
                  <input
                    v-model="rangeOf(target.id).max"
                    class="mini"
                    inputmode="decimal"
                    placeholder="最高"
                  />
                </div>
                <div v-if="target.side === 'bid'" class="muted">求购打不出去</div>
              </td>
              <td>
                <span v-if="target.desired === 'enabled'">已开</span>
                <span v-else class="muted">已关</span>
              </td>
              <td>
                <div class="ops">
                  <button
                    class="btn sm"
                    :disabled="busy || sortDraft[target.id] === sortKey(target)"
                    @click="requestSortChange(target)"
                  >保存顺序</button>
                  <button
                    class="btn sm"
                    :disabled="busy || !rangeDirty(target)"
                    @click="requestRangeChange(target)"
                  >保存区间</button>
                  <button
                    v-if="isRust(target)"
                    class="btn sm"
                    :disabled="busy || !steamVocab.categories.length"
                    @click="facetEdit = target"
                  >编辑筛选</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">短效代理水位</div>
      <div class="panel-body">
        <p class="note">按地域设定「至少要有几个可用的短效代理」。自动补号接入后才会按这个数去拉号。</p>
        <EmptyState v-if="!watermarks" kind="empty" text="读取中" />
        <EmptyState v-else-if="!watermarks.length" kind="unconfigured" text="还没设置任何地域水位" />
        <table v-else class="data">
          <thead>
            <tr><th>地域</th><th>最少可用</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="mark in watermarks" :key="mark.region">
              <td>{{ regionText(mark.region) }}</td>
              <td><input v-model="draftMin[mark.region]" class="mini" inputmode="numeric" /></td>
              <td>
                <button class="btn sm" :disabled="busy" @click="saveWatermark(mark)">保存</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">
        代理商
        <span class="spacer" />
        <button class="btn sm" @click="showAdd = true">添加代理商</button>
      </div>
      <div class="panel-body">
        <p class="note">优先级数字小的先用。凭证只写进数据库，页面不回显。</p>
        <EmptyState v-if="!providers" kind="empty" text="读取中" />
        <EmptyState v-else-if="!providers.length" kind="unconfigured" text="还没有代理商，点右上角添加" />
        <table v-else class="data">
          <thead>
            <tr><th>名称</th><th>优先级</th><th>可供地域</th><th>启用</th><th>凭证</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="provider in providers" :key="provider.id">
              <td>{{ provider.name }}</td>
              <td class="num">{{ provider.priority }}</td>
              <td>{{ provider.regions.map((r) => regionText(r)).join(' / ') }}</td>
              <td>{{ provider.enabled ? '已启用' : '已停用' }}</td>
              <td>{{ provider.has_credential ? '已保存' : '未填' }}</td>
              <td class="ops">
                <button class="btn sm" @click="openProviderEdit(provider)">编辑</button>
                <button class="btn sm" :disabled="busy" @click="toggleEnabled(provider)">
                  {{ provider.enabled ? '停用' : '启用' }}
                </button>
                <button class="btn sm" @click="credEdit = { provider, credential: '' }">换凭证</button>
                <button
                  class="btn sm danger"
                  @click="confirm = {
                    title: '删除代理商',
                    impact: provider.name,
                    consequence: '只删这条配置，已有节点不受影响。',
                    run: () => deleteProvider(provider.id),
                  }"
                >
                  删除
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <div v-if="showAdd" class="modal-mask" @click.self="showAdd = false">
      <form class="modal" @submit.prevent="addProvider">
        <div class="modal-head">添加代理商</div>
        <div class="modal-body">
          <div class="field">
            <label>名称</label>
            <input v-model="newProvider.name" />
          </div>
          <div class="field">
            <label>优先级（越小越先用）</label>
            <input v-model="newProvider.priority" inputmode="numeric" />
          </div>
          <div class="field">
            <label>可供地域</label>
            <div class="checks">
              <label v-for="region in REGIONS" :key="region" class="check">
                <input v-model="newProvider.regions[region]" type="checkbox" />
                {{ regionText(region) }}
              </label>
            </div>
          </div>
          <label class="check solo">
            <input v-model="newProvider.enabled" type="checkbox" />
            启用
          </label>
          <div class="field">
            <label>API 凭证</label>
            <textarea v-model="newProvider.credential" rows="4" />
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="showAdd = false">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">添加</button>
        </div>
      </form>
    </div>

    <div v-if="providerEdit" class="modal-mask" @click.self="providerEdit = null">
      <form class="modal" @submit.prevent="saveProvider">
        <div class="modal-head">编辑代理商 · {{ providerEdit.provider.name }}</div>
        <div class="modal-body">
          <p v-if="editError" class="err">{{ editError }}</p>
          <div class="field">
            <label>名称</label>
            <input v-model="providerEdit.name" />
          </div>
          <div class="field">
            <label>优先级（越小越先用）</label>
            <input v-model="providerEdit.priority" inputmode="numeric" />
          </div>
          <div class="field">
            <label>可供地域</label>
            <div class="checks">
              <label v-for="region in REGIONS" :key="region" class="check">
                <input v-model="providerEdit.regions[region]" type="checkbox" />
                {{ regionText(region) }}
              </label>
            </div>
          </div>
          <label class="check solo">
            <input v-model="providerEdit.enabled" type="checkbox" />
            启用
          </label>
          <p class="note">凭证不在这里改，用列表里的「换凭证」。</p>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="providerEdit = null">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">保存</button>
        </div>
      </form>
    </div>

    <div v-if="credEdit" class="modal-mask" @click.self="credEdit = null">
      <form class="modal" @submit.prevent="saveCredential">
        <div class="modal-head">换凭证 · {{ credEdit.provider.name }}</div>
        <div class="modal-body">
          <p class="note">已存的凭证不回显，贴新的就会覆盖。</p>
          <div class="field">
            <label>新凭证</label>
            <textarea v-model="credEdit.credential" rows="4" />
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="credEdit = null">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">保存</button>
        </div>
      </form>
    </div>

    <SteamFacetDialog
      v-if="facetEdit"
      :target="facetEdit"
      :vocab="steamVocab"
      :busy="busy"
      @save="saveFacets"
      @cancel="facetEdit = null"
    />

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.title"
      :impact="confirm.impact"
      :consequence="confirm.consequence"
      confirm-text="确认"
      @confirm="runAction(confirm!.run); confirm = null"
      @cancel="confirm = null"
    />
  </div>
</template>

<style scoped>
.pg { width: 100%; max-width: none; margin: 0; padding: 20px 28px 40px; box-sizing: border-box; }
.hero { margin-bottom: 16px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); font-size: 12px; }
.panel { margin-bottom: 14px; }
.panel-head .spacer { flex: 1; }
.panel-body { padding: 0 0 4px; }
.note { margin: 8px 12px; color: var(--text-3); font-size: 12px; }
.fail { color: var(--danger); padding: 8px 12px; display: flex; gap: 8px; align-items: center; }
.err { margin: 0 0 10px; color: var(--danger); font-size: 12px; }
.ops { display: flex; gap: 6px; flex-wrap: wrap; }
.checks { display: flex; gap: 14px; flex-wrap: wrap; }
.check { display: flex; gap: 6px; align-items: center; font-size: 12px; }
.check.solo { margin-bottom: 12px; }
input.mini, select.mini {
  width: 96px; height: 28px; padding: 0 8px;
  background: var(--bg); color: var(--text);
  border: 1px solid var(--line-strong); font-family: var(--mono); font-size: 12px;
}
select.mini.wide { width: 180px; }
.range { display: flex; gap: 6px; align-items: center; }
.note.warn { color: var(--warn); }
</style>
