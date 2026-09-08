<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createTarget,
  deleteTarget,
  getAccounts,
  getCombinations,
  getNodes,
  getTargets,
  setTargetDesired,
  type AccessNode,
  type Account,
  type ActualState,
  type Combination,
  type Side,
  type Target,
} from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import {
  apiErrorText,
  fmtTime,
  fmtUntil,
  gameFullName,
  gameName,
  isPacingBeat,
  KNOWN_GAMES,
  platformLabel,
  sideText,
  targetReasonHint,
  type ReasonAction,
} from '../utils/format'
import { nodeAllowsPlatform, nodeIsUsableAt } from '../utils/platformRegion'

type SideDraft = {
  enabled: boolean
  actual: ActualState
  reason: string | null
  recheckAt: string | null
  nodeIds: number[]
  usableNodeIds: number[]
  combinationCount: number
  usableCombinationCount: number
  target: Target | null
}

type PlatInGame = { platform: string; bid: SideDraft; ask: SideDraft }
type GameDraft = {
  appid: number
  name: string
  shortName: string
  nodeIds: number[]
  usableNodeIds: number[]
  platforms: PlatInGame[]
}

// 加载失败和写操作失败分开存：轮询成功只清加载错误，操作失败的提示要一直留到用户看到
const loadError = ref<string | null>(null)
const actionError = ref<string | null>(null)
const stale = ref(false)
const loading = ref(true)
const loaded = ref(false)
const games = ref<GameDraft[]>([])
const accounts = ref<Account[]>([])
const nodeMap = ref<Map<number, AccessNode>>(new Map())
const newAppid = ref('')
const busy = ref(false)

// 所有危险操作共用一个确认框，run 里放真正要执行的动作
const confirm = ref<{
  title: string
  impact: string
  consequence: string
  confirmText: string
  run: () => Promise<void>
} | null>(null)

let poll = 0
let reloadSeq = 0

type PlatformCapacity = {
  nodeIds: number[]
  usableNodeIds: number[]
  combinationCount: number
  usableCombinationCount: number
}

function platformCapacity(
  platform: string,
  nodes: AccessNode[],
  accounts: Account[],
  combinations: Combination[],
  now: number,
): PlatformCapacity {
  const accountById = new Map(accounts.map((account) => [account.id, account]))
  const nodeById = new Map(nodes.map((node) => [node.id, node]))
  const configured = combinations.filter((combination) => combination.platform === platform)
  const usable = configured.filter((combination) => {
    const account = accountById.get(combination.account_id)
    const node = nodeById.get(combination.node_id)
    return !!account &&
      account.platform === platform &&
      account.session_state !== 'invalid' &&
      !!node &&
      nodeIsUsableAt(node, now) &&
      nodeAllowsPlatform(node.region, platform)
  })
  return {
    nodeIds: [...new Set(configured.map((combination) => combination.node_id))],
    usableNodeIds: [...new Set(usable.map((combination) => combination.node_id))],
    combinationCount: configured.length,
    usableCombinationCount: usable.length,
  }
}

function buildGames(
  nodes: AccessNode[],
  targets: Target[],
  accounts: Account[],
  combinations: Combination[],
): GameDraft[] {
  const appids = new Set<number>()
  for (const t of targets) appids.add(t.appid)
  const list = [...appids].sort((a, b) => a - b)
  const steamCapacity = platformCapacity('steam', nodes, accounts, combinations, Date.now())
  return list.map((appid) => {
    const platforms = ['steam']
    return {
      appid,
      shortName: gameName(appid),
      name: gameFullName(appid),
      nodeIds: steamCapacity.nodeIds,
      usableNodeIds: steamCapacity.usableNodeIds,
      platforms: platforms.map((platform) => ({
        platform,
        bid: makeSide(appid, platform, 'bid', targets, steamCapacity),
        ask: makeSide(appid, platform, 'ask', targets, steamCapacity),
      })),
    }
  })
}

function makeSide(
  appid: number,
  platform: string,
  side: Side,
  targets: Target[],
  capacity: PlatformCapacity,
): SideDraft {
  const target = targets.find((t) => t.appid === appid && t.platform === platform && t.side === side) ?? null
  return {
    enabled: target?.desired === 'enabled',
    actual: target?.actual ?? 'stopped',
    reason: target?.reason ?? null,
    recheckAt: target?.recheck_at ?? null,
    nodeIds: capacity.nodeIds,
    usableNodeIds: capacity.usableNodeIds,
    combinationCount: capacity.combinationCount,
    usableCombinationCount: capacity.usableCombinationCount,
    target,
  }
}

async function reload() {
  const request = ++reloadSeq
  let result: [AccessNode[], Target[], Account[], Combination[]]
  try {
    result = await Promise.all([getNodes(), getTargets(), getAccounts(), getCombinations()])
  } catch (error) {
    if (request !== reloadSeq) return false
    throw error
  }
  if (request !== reloadSeq) return false
  const [n, t, a, c] = result
  nodeMap.value = new Map(n.map((x) => [x.id, x]))
  accounts.value = a
  games.value = buildGames(n, t, a, c)
  loaded.value = true
  loadError.value = null
  stale.value = false
  return true
}

onMounted(async () => {
  try {
    await reload()
  } catch (e) {
    loadError.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
  } finally {
    loading.value = false
  }
  poll = window.setInterval(() => {
    reload().catch(() => {
      if (loaded.value) stale.value = true
    })
  }, 3000)
})

onUnmounted(() => {
  if (poll) window.clearInterval(poll)
})

const liveCount = computed(() =>
  games.value.filter((g) => g.platforms.some((p) => isLive(p.bid) || isLive(p.ask))).length,
)
const blockedCount = computed(() =>
  games.value.filter((g) => g.platforms.some((p) => isBlocked(p.bid) || isBlocked(p.ask))).length,
)
const openSides = computed(() => {
  let n = 0
  for (const g of games.value) {
    for (const p of g.platforms) {
      if (p.bid.enabled) n++
      if (p.ask.enabled) n++
    }
  }
  return n
})
const hasExpired = computed(() => accounts.value.some((a) => a.session_state === 'invalid'))
const validAccounts = computed(() => accounts.value.filter((a) => a.session_state === 'valid').length)

// 节流节拍在语义上就是正在采集，否则顶部指标和热度也会跟着每两秒闪一次
function isLive(s: SideDraft) {
  if (!s.enabled) return false
  return s.actual === 'running' || s.actual === 'starting' || isPacingBeat(s.reason, s.recheckAt)
}
function isBlocked(s: SideDraft) {
  if (!s.enabled || isPacingBeat(s.reason, s.recheckAt)) return false
  return s.actual === 'blocked' || s.actual === 'error'
}

function statusWord(s: SideDraft) {
  if (s.actual === 'stopping') return { t: '停止中', k: 'warn' as const }
  if (!s.enabled) return { t: '关', k: 'off' as const }
  if (s.actual === 'running') return { t: '采集中', k: 'live' as const }
  if (s.actual === 'starting') return { t: '启动中', k: 'live' as const }
  // 本地节流每两秒就把目标短暂标成阻塞，那是正常节拍，不该让状态一直闪
  if (isPacingBeat(s.reason, s.recheckAt)) return { t: '采集中', k: 'live' as const }
  if (s.actual === 'blocked') return { t: '阻塞', k: 'warn' as const }
  if (s.actual === 'error') return { t: '错误', k: 'bad' as const }
  if (s.actual === 'waiting') return { t: '等待', k: 'idle' as const }
  return { t: '开', k: 'idle' as const }
}

function gameHeat(g: GameDraft) {
  const sides = g.platforms.flatMap((p) => [p.bid, p.ask])
  const en = sides.filter((s) => s.enabled || s.actual === 'stopping')
  if (!en.length) return 'off'
  const live = en.some((s) => isLive(s))
  const blocked = en.some((s) => isBlocked(s) || s.actual === 'stopping')
  if (live && blocked) return 'mixed'
  if (blocked) return 'blocked'
  if (live) return 'live'
  return 'idle'
}

function heatLabel(h: string) {
  return ({ live: '在采', blocked: '阻塞', mixed: '在采·阻塞', idle: '空闲', off: '未开' } as Record<string, string>)[h] ?? h
}

function sideOf(p: PlatInGame, side: Side) {
  return side === 'bid' ? p.bid : p.ask
}

function sideLabel(g: GameDraft, p: PlatInGame, side: Side) {
  return `${g.shortName} · ${platformLabel(p.platform)} ${sideText(side)}`
}

// 阻塞/等待方向固定三层：状态徽标 → 短句 → 行动入口。节拍冷却不当阻塞。
function sideBlockHint(s: SideDraft): { text: string; action?: ReasonAction } | null {
  if (!s.enabled || isPacingBeat(s.reason, s.recheckAt)) return null
  if (s.actual !== 'blocked' && s.actual !== 'error' && s.actual !== 'waiting') return null
  const hint = targetReasonHint(s.reason)
  if (!hint.text && s.actual !== 'blocked' && s.actual !== 'error') return null
  return {
    text: hint.text || '采集受阻',
    action: hint.action ?? (s.actual === 'blocked' || s.actual === 'error'
      ? { label: '去资源页', to: '/resources' }
      : undefined),
  }
}

// 后端只允许移除已停用且已停止的目标。跑过没跑过前端看不出来，那种情况按后端返回的原因提示。
function removeBlockReason(p: PlatInGame, side: Side): string {
  const s = sideOf(p, side)
  if (!s.target) return ''
  if (s.target.desired !== 'disabled') return '先关掉这个方向才能移除'
  if (s.actual !== 'stopped') return '还没完全停下来，等状态变成已停止再移除'
  return ''
}

function requestToggle(g: GameDraft, p: PlatInGame, side: Side) {
  const s = sideOf(p, side)
  if (s.actual === 'stopping' || busy.value) return
  if (s.enabled && isLive(s)) {
    confirm.value = {
      title: '关闭采集',
      impact: sideLabel(g, p, side),
      consequence: '先进入停止中：不再派发新任务并清理未完成任务。已发出的旧结果不会写入，已保存数据保留。',
      confirmText: '关闭',
      run: () => toggleLatest(g.appid, p.platform, side),
    }
    return
  }
  void applyToggle(g, p, side)
}

// 轮询会重建 games，确认时重新取当前对象，避免用到过期的 revision
async function toggleLatest(appid: number, platform: string, side: Side) {
  const g = games.value.find((x) => x.appid === appid)
  const p = g?.platforms.find((x) => x.platform === platform)
  if (g && p) await applyToggle(g, p, side)
}

async function applyToggle(g: GameDraft, p: PlatInGame, side: Side) {
  const s = sideOf(p, side)
  busy.value = true
  actionError.value = null
  try {
    if (s.target) {
      const next = s.target.desired === 'enabled' ? 'disabled' : 'enabled'
      await setTargetDesired(s.target.id, s.target.revision, next)
    } else {
      await createTarget(p.platform, g.appid, side, 'enabled')
    }
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '操作失败'
  } finally {
    busy.value = false
  }
}

function requestRemove(g: GameDraft, p: PlatInGame, side: Side) {
  const s = sideOf(p, side)
  if (!s.target || busy.value || removeBlockReason(p, side)) return
  const id = s.target.id
  confirm.value = {
    title: '移除采集目标',
    impact: sideLabel(g, p, side),
    consequence:
      '移除的是这个采集目标本身，不是把开关关掉：这个方向会从概览消失，想再采只能重新加回来。' +
      '跑过采集的目标不能移除，服务会拒绝，采集历史不会被删。',
    confirmText: '移除',
    run: () => removeTarget(id),
  }
}

async function removeTarget(id: number) {
  busy.value = true
  actionError.value = null
  try {
    await deleteTarget(id)
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '移除失败'
  } finally {
    busy.value = false
  }
}

async function addGameByAppid(appid: number) {
  if (busy.value || games.value.some((game) => game.appid === appid)) return
  busy.value = true
  actionError.value = null
  try {
    await createTarget('steam', appid, 'ask', 'disabled')
    newAppid.value = ''
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '添加失败'
  } finally {
    busy.value = false
  }
}

async function addGame() {
  const raw = newAppid.value.trim()
  const appid = Number(raw)
  if (!raw || !Number.isInteger(appid) || appid < 1) {
    actionError.value = '游戏编号要填正整数的 Steam appid，例如 730'
    return
  }
  await addGameByAppid(appid)
}

function onConfirm() {
  const action = confirm.value
  confirm.value = null
  if (action) void action.run()
}
</script>

<template>
  <div class="ov">
    <header class="hero">
      <h1>概览</h1>
      <div class="hero-actions">
        <RouterLink class="btn sm" to="/resources">资源</RouterLink>
        <RouterLink class="btn sm" to="/runs">运行</RouterLink>
      </div>
    </header>

    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="loadError" class="fail">{{ loadError }}</div>
    <div v-if="actionError" class="fail act-err">
      <span>{{ actionError }}</span>
      <button class="btn sm" type="button" @click="actionError = null">知道了</button>
    </div>
    <div v-if="loading" class="loading">加载中</div>

    <template v-else-if="loaded">
      <section class="metrics">
        <div class="metric">
          <div class="m-label">在采游戏</div>
          <div class="m-val">{{ liveCount }}</div>
        </div>
        <div class="metric" :class="{ alert: blockedCount > 0 }">
          <div class="m-label">阻塞</div>
          <div class="m-val">{{ blockedCount }}</div>
        </div>
        <div class="metric">
          <div class="m-label">开启方向</div>
          <div class="m-val">{{ openSides }}</div>
        </div>
        <div class="metric" :class="{ alert: hasExpired }">
          <div class="m-label">账号</div>
          <div class="m-val" :class="{ bad: hasExpired }">
            {{ hasExpired ? accounts.filter((a) => a.session_state === 'invalid').length : validAccounts }}
          </div>
          <div class="m-unit">
            {{ hasExpired ? `失效 / ${accounts.length}` : '有效' }}
          </div>
        </div>
      </section>

      <form class="add-game" @submit.prevent="addGame">
        <div class="presets">
          <button
            v-for="game in KNOWN_GAMES"
            :key="game.appid"
            class="btn sm"
            type="button"
            :disabled="busy || games.some((row) => row.appid === game.appid)"
            @click="addGameByAppid(game.appid)"
          >{{ game.short }}</button>
        </div>
        <input v-model="newAppid" class="inp" placeholder="或其他 Steam appid" inputmode="numeric" />
        <button class="btn sm" type="submit" :disabled="busy">加入游戏</button>
      </form>

      <section class="game-list">
        <article
          v-for="g in games"
          :key="g.appid"
          class="game"
          :data-heat="gameHeat(g)"
        >
          <header class="game-head">
            <div class="game-titles">
              <div class="game-short">{{ g.shortName }}</div>
              <div class="game-full">{{ g.name }} · {{ g.appid }}</div>
            </div>
            <div class="game-meta">
              <span class="num">可用节点 {{ g.usableNodeIds.length }}/{{ g.nodeIds.length }}</span>
              <span class="game-heat">{{ heatLabel(gameHeat(g)) }}</span>
            </div>
          </header>

          <div class="plat-rows">
            <div v-for="p in g.platforms" :key="p.platform" class="plat-row">
              <div class="plat-tag">{{ platformLabel(p.platform) }}</div>
              <div class="dir-grid">
                <div
                  v-for="side in (['bid', 'ask'] as const)"
                  :key="side"
                  class="dir"
                  :data-k="statusWord(sideOf(p, side)).k"
                >
                  <div class="dir-top">
                    <span class="dir-name">{{ sideText(side) }}</span>
                    <span
                      v-if="statusWord(sideOf(p, side)).k !== 'off'"
                      class="side-flag"
                    >{{ statusWord(sideOf(p, side)).t }}</span>
                    <button
                      class="sw"
                      :class="{ on: sideOf(p, side).enabled }"
                      type="button"
                      :disabled="sideOf(p, side).actual === 'stopping'"
                      :title="sideOf(p, side).actual === 'stopping' ? '停止中，停干净后再开' : ''"
                      @click="requestToggle(g, p, side)"
                    >
                      <span class="sw-knob" />
                      <span class="sw-txt">{{
                        sideOf(p, side).actual === 'stopping'
                          ? '停'
                          : sideOf(p, side).enabled
                            ? '开'
                            : '关'
                      }}</span>
                    </button>
                    <button
                      v-if="sideOf(p, side).target"
                      class="rm"
                      type="button"
                      :disabled="busy || !!removeBlockReason(p, side)"
                      :title="removeBlockReason(p, side) || '移除这个采集目标'"
                      @click="requestRemove(g, p, side)"
                    >移除</button>
                  </div>
                  <div class="dir-body">
                    <div class="num cap">
                      健康绑定 {{ sideOf(p, side).usableCombinationCount }}/{{ sideOf(p, side).combinationCount }}
                      · 节点 {{ sideOf(p, side).usableNodeIds.length }}/{{ sideOf(p, side).nodeIds.length }}
                    </div>
                    <div v-if="sideOf(p, side).usableNodeIds.length" class="mini-nodes">
                      <span v-for="id in sideOf(p, side).usableNodeIds" :key="id" class="ntag sm">
                        {{ nodeMap.get(id)?.name ?? id }}
                      </span>
                    </div>
                    <div v-else class="muted sm">
                      {{ sideOf(p, side).nodeIds.length ? '暂无可用节点' : '未分节点' }}
                    </div>
                    <template v-for="hint in [sideBlockHint(sideOf(p, side))]" :key="`${side}-hint`">
                      <div
                        v-if="hint"
                        class="reason"
                        :title="fmtTime(sideOf(p, side).recheckAt)"
                      >
                        <span>{{ hint.text }}</span>
                        <RouterLink
                          v-if="hint.action"
                          class="reason-act"
                          :to="hint.action.to"
                        >{{ hint.action.label }}</RouterLink>
                        <span v-if="fmtUntil(sideOf(p, side).recheckAt)" class="muted">
                          · {{ fmtUntil(sideOf(p, side).recheckAt) }}
                        </span>
                      </div>
                    </template>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </article>
      </section>

      <div v-if="games.length === 0" class="empty-cta">
        <EmptyState kind="unconfigured" text="无游戏" />
      </div>
    </template>

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.title"
      :impact="confirm.impact"
      :consequence="confirm.consequence"
      :confirm-text="confirm.confirmText"
      @confirm="onConfirm"
      @cancel="confirm = null"
    />
  </div>
</template>

<style scoped>
.ov {
  width: 100%;
  max-width: none;
  margin: 0;
  padding: 20px 28px 40px;
  box-sizing: border-box;
}
.hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  padding-bottom: 10px;
  border-bottom: 1px solid var(--line-strong);
}
.hero h1 { margin: 0; font-size: clamp(18px, 2.5vw, 22px); font-weight: 800; }
.hero-actions { display: flex; gap: 8px; }

.fail, .loading {
  font-family: var(--mono);
  font-size: 12px;
  padding: 10px 12px;
  margin-bottom: 12px;
  border: 1px solid var(--line-strong);
}
.fail { color: var(--danger); border-color: var(--danger); background: var(--danger-dim); }
.act-err { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.loading { color: var(--text-3); }

.metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 1px;
  background: var(--line);
  border: 1px solid var(--line-strong);
  margin-bottom: 16px;
}
.metric {
  background: var(--surface);
  padding: 10px;
  min-height: 72px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  text-decoration: none;
  color: inherit;
  border: none;
}
.metric.alert { background: linear-gradient(180deg, var(--danger-dim), var(--surface)); }
.m-label {
  font-family: var(--mono);
  font-size: 9px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-3);
}
.m-val {
  font-family: var(--mono);
  font-size: clamp(22px, 3.5vw, 30px);
  font-weight: 800;
  line-height: 1;
  margin: 6px 0 2px;
}
.metric.alert .m-val, .m-val.bad { color: var(--danger); }
.m-unit { font-family: var(--mono); font-size: 10px; color: var(--text-3); }

.game-list { display: flex; flex-direction: column; gap: 12px; }
.game {
  border: 1px solid var(--line-strong);
  background: var(--surface);
  min-width: 0;
}
.game[data-heat='live'] { border-color: color-mix(in srgb, var(--ok) 50%, var(--line-strong)); }
.game[data-heat='blocked'] { border-color: color-mix(in srgb, var(--warn) 50%, var(--line-strong)); }
.game[data-heat='mixed'] { border-color: color-mix(in srgb, var(--warn) 40%, var(--ok) 30%); }

.game-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--line);
  background: var(--bg-elev);
}
.game-short {
  font-size: clamp(16px, 2.2vw, 18px);
  font-weight: 900;
  font-family: var(--mono);
}
.game-full { margin-top: 2px; font-size: 11px; font-family: var(--mono); color: var(--text-3); }
.game-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--text-3);
}
.game-heat { text-transform: uppercase; letter-spacing: 0.1em; }
.game[data-heat='live'] .game-heat { color: var(--ok); }
.game[data-heat='blocked'] .game-heat,
.game[data-heat='mixed'] .game-heat { color: var(--warn); }
.game[data-heat='off'] .game-heat { color: var(--text-3); }

.plat-rows { display: flex; flex-direction: column; }
.plat-row {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  border-bottom: 1px solid var(--line);
}
.plat-row:last-child { border-bottom: none; }
.plat-tag {
  padding: 12px 8px;
  font-family: var(--mono);
  font-weight: 800;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--text-2);
  background: var(--bg);
  border-right: 1px solid var(--line);
  word-break: break-all;
}

.dir-grid { display: grid; grid-template-columns: 1fr 1fr; min-width: 0; }
.dir {
  padding: 10px 12px;
  border-right: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.dir:last-child { border-right: none; }
.dir[data-k='live'] { background: color-mix(in srgb, var(--ok-dim) 45%, transparent); }
.dir[data-k='warn'] { background: color-mix(in srgb, var(--warn-dim) 40%, transparent); }
.dir[data-k='bad'] { background: color-mix(in srgb, var(--danger-dim) 45%, transparent); }

.dir-top { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.dir-name { font-weight: 700; font-size: 12px; }
.side-flag {
  font-family: var(--mono);
  font-size: 10px;
  font-weight: 800;
  padding: 1px 5px;
  border: 1px solid currentColor;
}
.dir[data-k='live'] .side-flag { color: var(--ok); }
.dir[data-k='warn'] .side-flag { color: var(--warn); }
.dir[data-k='bad'] .side-flag { color: var(--danger); }
.dir[data-k='idle'] .side-flag { color: var(--text-2); }
.dir[data-k='off'] .side-flag { color: var(--text-3); }

.dir-body { display: flex; flex-direction: column; gap: 4px; }
.cap { font-size: 11px; color: var(--text-2); }
.mini-nodes { display: flex; flex-wrap: wrap; gap: 4px; }
.ntag {
  font-family: var(--mono);
  font-size: 10px;
  padding: 3px 7px;
  border: 1px solid var(--line-strong);
  color: var(--text-2);
}
.ntag.sm { font-size: 9px; padding: 2px 5px; }
.freq { font-family: var(--mono); font-size: 11px; color: var(--text-2); }
.reason { font-size: 12px; color: var(--warn); word-break: break-word; display: flex; flex-wrap: wrap; align-items: baseline; gap: 6px; }
.reason-act { color: var(--warn); border-bottom-color: color-mix(in srgb, var(--warn) 50%, transparent); font-size: 12px; }
.muted { color: var(--text-3); }
.sm { font-size: 11px; }

.sw {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  margin-left: auto;
  padding: 0 6px 0 3px;
  border: 1px solid var(--line-strong);
  background: var(--bg);
  color: var(--text-3);
  cursor: pointer;
  font-family: var(--mono);
  font-size: 10px;
}
.sw-knob {
  width: 26px;
  height: 14px;
  border: 1px solid var(--line-strong);
  background: #2a2a2a;
  position: relative;
  flex-shrink: 0;
}
.sw-knob::after {
  content: "";
  position: absolute;
  top: 1px;
  left: 1px;
  width: 10px;
  height: 10px;
  background: var(--text-3);
  transition: transform 0.12s ease;
}
.sw.on { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 50%, var(--line-strong)); }
.sw.on .sw-knob { border-color: var(--ok); background: var(--ok-dim); }
.sw.on .sw-knob::after { transform: translateX(12px); background: var(--ok); }
.sw:disabled { opacity: 0.5; cursor: not-allowed; }

.rm {
  height: 26px;
  padding: 0 6px;
  border: 1px solid var(--line-strong);
  background: var(--bg);
  color: var(--text-3);
  cursor: pointer;
  font-family: var(--mono);
  font-size: 10px;
}
.rm:hover:not(:disabled) { color: var(--danger); border-color: var(--danger); background: var(--danger-dim); }
.rm:disabled { opacity: 0.5; cursor: not-allowed; }

.empty-cta { margin-top: 16px; border: 1px solid var(--line); }
.add-game { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 14px; }
.presets { display: flex; flex-wrap: wrap; gap: 6px; }
.inp {
  height: 28px;
  padding: 0 8px;
  background: var(--bg);
  color: var(--text);
  border: 1px solid var(--line-strong);
  font-family: var(--mono);
  font-size: 12px;
}

@media (max-width: 960px) {
  .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 720px) {
  .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .plat-row { grid-template-columns: 1fr; }
  .plat-tag { border-right: none; border-bottom: 1px solid var(--line); padding: 8px 12px; }
  .dir-grid { grid-template-columns: 1fr; }
  .dir { border-right: none; border-bottom: 1px solid var(--line); }
  .dir:last-child { border-bottom: none; }
}
@media (max-width: 420px) {
  .sw-txt { display: none; }
}
</style>
