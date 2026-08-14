<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  getAccounts,
  getCombinations,
  getNodes,
  getPageAttempts,
  getPagePayload,
  getRunPages,
  getRuns,
  getTargets,
  type AccessNode,
  type Account,
  type CollectionPage,
  type Combination,
  type PageAttempt,
  type Run,
  type Target,
} from '../api'
import EmptyState from '../components/EmptyState.vue'
import { useEscape } from '../utils/escape'
import {
  apiErrorText,
  fenToYuan,
  fmtAgo,
  fmtTime,
  fmtUntil,
  gameName,
  isPacingBeat,
  platformLabel,
  quoteReasonText,
  quoteStatusText,
  runReasonText,
  runStateText,
  sideText,
  targetReasonText,
} from '../utils/format'

/** 一个采集方向的当前状态。批次只提供页数进度，编号不呈现给使用者。 */
interface Lane {
  target: Target
  /** 负责这个方向的账号与出口，来自节点分配与组合 */
  workers: { account: string; exit: string; nodeName: string; usable: boolean }[]
  pages: number
  run: Run | null
}

const lanes = ref<Lane[] | null>(null)
const history = ref<Run[]>([])
const error = ref<string | null>(null)
const stale = ref(false)
let poll = 0
let seq = 0

const activeLanes = computed(() => (lanes.value ?? []).filter((lane) => lane.target.desired === 'enabled'))
const idleLanes = computed(() => (lanes.value ?? []).filter((lane) => lane.target.desired !== 'enabled'))

function buildLanes(
  targets: Target[],
  runs: Run[],
  nodes: AccessNode[],
  accounts: Account[],
  combinations: Combination[],
): Lane[] {
  const accountName = new Map(accounts.map((a) => [a.id, a.alias]))
  const nodeByID = new Map(nodes.map((n) => [n.id, n]))
  return targets.map((target) => {
    // 一个方向由「分到这个游戏且划了这个平台方向」的节点负责，账号来自该节点的组合
    const workers: Lane['workers'] = []
    for (const combination of combinations) {
      const node = nodeByID.get(combination.node_id)
      if (!node || node.appid !== target.appid) continue
      if (!node.sides.some((s) => s.platform === target.platform && s.side === target.side)) continue
      workers.push({
        account: accountName.get(combination.account_id) ?? `#${combination.account_id}`,
        exit: node.exit?.address ?? '',
        nodeName: node.name,
        usable: node.state === 'available',
      })
    }
    const run = runs.find((r) => r.target_id === target.id && (r.state === 'running' || r.state === 'pending')) ?? null
    return { target, workers, pages: run?.last_page_sequence ?? 0, run }
  })
}

async function load() {
  const token = ++seq
  try {
    const [targets, runs, nodes, accounts, combinations] = await Promise.all([
      getTargets(),
      getRuns(100),
      getNodes(),
      getAccounts(),
      getCombinations(),
    ])
    if (token !== seq) return
    lanes.value = buildLanes(targets, runs, nodes, accounts, combinations)
    // 只留失败和被作废的，正常跑完的批次没有诊断价值
    history.value = runs.filter((r) => r.state === 'failed' || (r.state === 'stopped' && r.reason))
    error.value = null
    stale.value = false
  } catch (e) {
    if (token !== seq) return
    if (lanes.value) stale.value = true
    else error.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
  }
}

// 下钻：页列表与单页详情。批次在这里只是个句柄，界面上不显示它的编号。
const drill = ref<{ lane: Lane; pages: CollectionPage[] | null } | null>(null)
const drillError = ref<string | null>(null)
const picked = ref<CollectionPage | null>(null)
const attempts = ref<PageAttempt[] | null>(null)
const payload = ref<string | null>(null)
const payloadError = ref<string | null>(null)
const loadingPage = ref(false)

useEscape(() => {
  if (payload.value !== null) {
    payload.value = null
    return
  }
  if (picked.value) {
    picked.value = null
    return
  }
  drill.value = null
})

async function openDrill(lane: Lane) {
  if (!lane.run) return
  drill.value = { lane, pages: null }
  drillError.value = null
  picked.value = null
  attempts.value = null
  payload.value = null
  try {
    const pages = await getRunPages(lane.run.id)
    if (drill.value?.lane.target.id === lane.target.id) drill.value = { lane, pages }
  } catch (e) {
    drillError.value = e instanceof Error ? apiErrorText(e.message) : '读取页列表失败'
  }
}

async function pickPage(page: CollectionPage) {
  const run = drill.value?.lane.run
  if (!run) return
  picked.value = page
  attempts.value = null
  payload.value = null
  payloadError.value = null
  loadingPage.value = true
  try {
    const rows = await getPageAttempts(run.id, page.page_sequence)
    if (picked.value?.page_sequence === page.page_sequence) attempts.value = rows
  } catch (e) {
    drillError.value = e instanceof Error ? apiErrorText(e.message) : '读取这一页的行情失败'
  } finally {
    loadingPage.value = false
  }
}

async function showPayload() {
  const run = drill.value?.lane.run
  if (!run || !picked.value) return
  payloadError.value = null
  try {
    const raw = await getPagePayload(run.id, picked.value.page_sequence)
    // 原始响应通常是 JSON，能解析就缩进显示；解析不了（比如抓到登录页）就原样呈现
    try {
      payload.value = JSON.stringify(JSON.parse(raw), null, 2)
    } catch {
      payload.value = raw
    }
  } catch (e) {
    payloadError.value = e instanceof Error ? apiErrorText(e.message) : '读取原始响应失败'
  }
}

function laneState(lane: Lane) {
  const target = lane.target
  if (target.desired !== 'enabled') return { text: '已关', kind: 'off' }
  if (target.actual === 'running') return { text: '采集中', kind: 'live' }
  if (target.actual === 'starting') return { text: '启动中', kind: 'live' }
  // 秒级的本地节拍不叫冷却，否则状态每两秒就在采集和冷却之间闪
  if (isPacingBeat(target.reason, target.recheck_at)) return { text: '采集中', kind: 'live' }
  if (target.actual === 'blocked') return { text: targetReasonText(target.reason ?? null), kind: 'warn' }
  if (target.actual === 'waiting') return { text: targetReasonText(target.reason ?? null), kind: 'idle' }
  if (target.actual === 'error') return { text: targetReasonText(target.reason ?? null), kind: 'bad' }
  if (target.actual === 'stopping') return { text: '停止中', kind: 'warn' }
  return { text: '已停止', kind: 'off' }
}

onMounted(() => {
  void load()
  poll = window.setInterval(() => void load(), 3000)
})
onUnmounted(() => {
  if (poll) window.clearInterval(poll)
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>运行</h1>
      <p class="hero-sub">
        每一行是一个采集方向：哪个账号、从哪个出口、在采哪个游戏的哪个方向，以及这一轮翻到第几页。
      </p>
    </header>

    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="error && !lanes" class="fail">{{ error }}</div>
    <EmptyState v-else-if="!lanes" kind="empty" text="读取中" />
    <EmptyState v-else-if="!lanes.length" kind="empty" text="还没有采集方向，先去概览页加游戏" />

    <template v-else>
      <table v-if="activeLanes.length" class="data">
        <thead>
          <tr><th>采什么</th><th>谁在采</th><th>状态</th><th>这一轮进度</th></tr>
        </thead>
        <tbody>
          <tr v-for="lane in activeLanes" :key="lane.target.id">
            <td>
              <div class="strong">{{ gameName(lane.target.appid) }} · {{ sideText(lane.target.side) }}</div>
              <div class="muted">{{ platformLabel(lane.target.platform) }}</div>
            </td>
            <td>
              <div v-if="!lane.workers.length" class="muted">没有节点负责这个方向</div>
              <div v-for="worker in lane.workers" :key="worker.nodeName" class="worker">
                <span class="strong">{{ worker.account }}</span>
                <span class="muted"> @ </span>
                <span>{{ worker.exit || '出口未填' }}</span>
                <span class="muted"> · {{ worker.nodeName }}</span>
                <span v-if="!worker.usable" class="muted"> · 节点不可用</span>
              </div>
            </td>
            <td>
              <span class="badge" :class="laneState(lane).kind">{{ laneState(lane).text }}</span>
              <div v-if="lane.target.recheck_at && laneState(lane).kind !== 'live'" class="muted sm">
                {{ fmtUntil(lane.target.recheck_at) }}
              </div>
            </td>
            <td class="num">
              <button v-if="lane.run" class="link" type="button" @click="openDrill(lane)">
                已翻 {{ lane.pages }} 页
              </button>
              <span v-else class="muted">还没开始</span>
              <div v-if="lane.run?.cursor" class="muted sm">续点 {{ lane.run.cursor }}</div>
            </td>
          </tr>
        </tbody>
      </table>

      <section v-if="idleLanes.length" class="off-block">
        <div class="off-head">已关闭的方向</div>
        <div class="off-list">
          <span v-for="lane in idleLanes" :key="lane.target.id" class="off-item">
            {{ gameName(lane.target.appid) }} · {{ platformLabel(lane.target.platform) }} {{ sideText(lane.target.side) }}
          </span>
        </div>
      </section>

      <section v-if="history.length" class="off-block">
        <div class="off-head">出过问题的轮次</div>
        <table class="data">
          <thead>
            <tr><th>方向</th><th>结果</th><th>已翻页数</th><th>结束</th></tr>
          </thead>
          <tbody>
            <tr v-for="run in history" :key="run.id">
              <td>
                {{ gameName(run.appid) }} · {{ platformLabel(run.platform) }} {{ sideText(run.side ?? null) }}
              </td>
              <td>
                {{ runStateText(run.state) }}
                <span v-if="run.reason" class="muted"> · {{ runReasonText(run.reason) }}</span>
              </td>
              <td class="num">{{ run.last_page_sequence }}</td>
              <td class="num" :title="fmtTime(run.finished_at ?? null)">{{ fmtAgo(run.finished_at ?? null) }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>

    <div v-if="drill" class="modal-mask" @click.self="drill = null">
      <div class="modal drill">
        <div class="modal-head">
          {{ gameName(drill.lane.target.appid) }} · {{ platformLabel(drill.lane.target.platform) }}
          {{ sideText(drill.lane.target.side) }} · 这一轮的翻页
        </div>
        <!-- 左右分栏，两侧各自滚动：页多了也不用翻到底才能看详情 -->
        <div class="drill-body">
          <div class="pane pages">
            <p v-if="drillError" class="err">{{ drillError }}</p>
            <EmptyState v-if="!drill.pages && !drillError" kind="empty" text="读取中" />
            <EmptyState
              v-else-if="drill.pages && !drill.pages.length"
              kind="empty"
              text="这一轮还没有提交过页"
            />
            <ul v-else-if="drill.pages" class="page-list">
              <li v-for="pg in drill.pages" :key="pg.page_sequence">
                <button
                  class="page-item"
                  :class="{ on: picked?.page_sequence === pg.page_sequence }"
                  type="button"
                  @click="pickPage(pg)"
                >
                  <span class="seq">第 {{ pg.page_sequence }} 页</span>
                  <span class="when">{{ fmtAgo(pg.committed_at) }}</span>
                  <span class="who">{{ pg.account_alias || (pg.account_id ? `#${pg.account_id}` : '未记录归属') }}</span>
                </button>
              </li>
            </ul>
          </div>

          <div class="pane detail">
            <div v-if="!picked" class="pick-hint">
              <p class="note">左边选一页，这里显示它的归属、游标和写入的行情。</p>
              <p class="note">账号和出口是抓这一页时占用的租约，归属能力上线之前提交的页没有记录。</p>
            </div>
            <template v-else>
              <dl class="kv">
                <dt>账号</dt>
                <dd>{{ picked.account_alias || (picked.account_id ? `#${picked.account_id}` : '未记录') }}</dd>
                <dt>出口 IP</dt>
                <dd>{{ picked.exit_address || '未记录' }}</dd>
                <dt>游标</dt>
                <dd>{{ picked.cursor_before || '起点' }} → {{ picked.cursor_after || '—' }}</dd>
                <dt>采集</dt>
                <dd>{{ fmtTime(picked.collected_at) }}</dd>
                <dt>提交</dt>
                <dd>{{ fmtTime(picked.committed_at) }}</dd>
              </dl>

              <div class="ops">
                <button
                  class="btn sm"
                  type="button"
                  :disabled="!picked.payload_bytes"
                  :title="picked.payload_bytes ? '看平台返回的原文' : '这一页没有保留原始响应'"
                  @click="showPayload"
                >
                  看原始响应{{ picked.payload_bytes ? `（${(picked.payload_bytes / 1024).toFixed(1)} KB）` : '' }}
                </button>
                <button v-if="payload !== null" class="btn sm" type="button" @click="payload = null">收起原文</button>
              </div>
              <p v-if="payloadError" class="err">{{ payloadError }}</p>

              <pre v-if="payload !== null" class="raw">{{ payload }}</pre>
              <template v-else>
                <p class="note">
                  下面只列当前仍归属这一页的商品。后面的轮次重新采到同一商品时会把它带走，
                  所以越旧的页看到的行数越少。
                </p>
                <EmptyState v-if="loadingPage" kind="empty" text="读取中" />
                <EmptyState
                  v-else-if="attempts && !attempts.length"
                  kind="empty"
                  text="这一页的商品都已被后续轮次重新采过"
                />
                <table v-else-if="attempts" class="data">
                  <thead>
                    <tr><th>商品</th><th>状态</th><th>最近有效价</th></tr>
                  </thead>
                  <tbody>
                    <tr v-for="a in attempts" :key="`${a.product_id}-${a.side}`">
                      <td>
                        <div class="strong">{{ a.name }}</div>
                        <div class="muted">{{ sideText(a.side) }}</div>
                      </td>
                      <td>
                        {{ quoteStatusText(a.status) }}
                        <div v-if="a.reason_code" class="muted">{{ quoteReasonText(a.reason_code) }}</div>
                      </td>
                      <td class="num">{{ a.present_cents != null ? fenToYuan(a.present_cents) : '—' }}</td>
                    </tr>
                  </tbody>
                </table>
              </template>
            </template>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="drill = null">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pg { max-width: 1100px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); }
.fail { color: var(--danger); }
.sm { font-size: 11px; }
.worker { font-family: var(--mono); font-size: 12px; }

.badge { font-size: 11px; }
.badge.live { color: var(--ok); }
.badge.warn { color: var(--warn); }
.badge.bad { color: var(--danger); }
.badge.idle, .badge.off { color: var(--text-3); }

.off-block { margin-top: 22px; padding-top: 12px; border-top: 1px solid var(--line); }
.off-head {
  margin-bottom: 8px;
  color: var(--text-3);
  font-family: var(--mono);
  font-size: 11px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.off-list { display: flex; gap: 10px; flex-wrap: wrap; }
.off-item { color: var(--text-3); font-size: 12px; }

.link {
  border: 0;
  padding: 0;
  background: none;
  color: var(--text);
  cursor: pointer;
  font-family: var(--mono);
  font-size: inherit;
  text-decoration: underline;
}

/* 定高弹窗：两栏各自滚动，整体不出现外层滚动条 */
.modal.drill {
  display: flex;
  flex-direction: column;
  width: 1000px;
  max-width: 100%;
  height: 78vh;
  overflow: hidden;
}
.drill-body { display: grid; grid-template-columns: 216px 1fr; flex: 1; min-height: 0; }
.pane { min-height: 0; overflow: auto; padding: 12px; }
.pane.pages { border-right: 1px solid var(--line); }
.pane.detail { display: flex; flex-direction: column; gap: 10px; }

.page-list { margin: 0; padding: 0; list-style: none; }
.page-item {
  display: grid;
  gap: 1px;
  width: 100%;
  padding: 6px 8px;
  border: 0;
  border-left: 2px solid transparent;
  background: none;
  color: var(--text-2);
  cursor: pointer;
  text-align: left;
}
.page-item:hover { background: var(--bg-elev); }
.page-item.on { background: var(--bg-elev); border-left-color: var(--accent); color: var(--text); }
.seq { font-family: var(--mono); font-size: 12px; }
.when, .who { color: var(--text-3); font-size: 10px; }

.pick-hint { color: var(--text-3); }
.note { margin: 0; color: var(--text-3); font-size: 12px; }
.err { margin: 0; color: var(--danger); }
.ops { display: flex; gap: 8px; }
.raw {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 10px;
  overflow: auto;
  background: var(--bg);
  border: 1px solid var(--line-strong);
  font-family: var(--mono);
  font-size: 11px;
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 760px) {
  .drill-body { grid-template-columns: 1fr; grid-template-rows: 160px 1fr; }
  .pane.pages { border-right: 0; border-bottom: 1px solid var(--line); }
}
</style>
