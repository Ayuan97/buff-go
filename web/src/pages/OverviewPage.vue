<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createTarget,
  getAccounts,
  getNodes,
  getTargets,
  setTargetDesired,
  type AccessNode,
  type Account,
  type ActualState,
  type Side,
  type Target,
} from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import { sideText } from '../utils/format'

const GAME_NAMES: Record<number, { short: string; name: string }> = {
  730: { short: 'CS2', name: 'Counter-Strike 2' },
  570: { short: 'Dota2', name: 'Dota 2' },
  440: { short: 'TF2', name: 'Team Fortress 2' },
  252490: { short: 'Rust', name: 'Rust' },
}

type SideDraft = {
  enabled: boolean
  actual: ActualState
  reason: string | null
  nodeIds: number[]
  usableNodeCount: number
  target: Target | null
}

type PlatInGame = { platform: string; bid: SideDraft; ask: SideDraft }
type GameDraft = {
  appid: number
  name: string
  shortName: string
  nodeIds: number[]
  platforms: PlatInGame[]
}

const error = ref<string | null>(null)
const stale = ref(false)
const loading = ref(true)
const loaded = ref(false)
const games = ref<GameDraft[]>([])
const accounts = ref<Account[]>([])
const nodeMap = ref<Map<number, AccessNode>>(new Map())
const newAppid = ref('')
const busy = ref(false)

const confirmClose = ref<{
  appid: number
  platform: string
  side: Side
  label: string
} | null>(null)

let poll = 0

function buildGames(nodes: AccessNode[], targets: Target[]): GameDraft[] {
  const appids = new Set<number>()
  for (const n of nodes) if (n.appid) appids.add(n.appid)
  for (const t of targets) appids.add(t.appid)
  const list = [...appids].sort((a, b) => a - b)
  return list.map((appid) => {
    const meta = GAME_NAMES[appid] ?? { short: String(appid), name: `App ${appid}` }
    const gameNodes = nodes.filter((n) => n.appid === appid)
    const platforms = ['steam']
    return {
      appid,
      shortName: meta.short,
      name: meta.name,
      nodeIds: gameNodes.map((n) => n.id),
      platforms: platforms.map((platform) => ({
        platform,
        bid: makeSide(appid, platform, 'bid', gameNodes, targets),
        ask: makeSide(appid, platform, 'ask', gameNodes, targets),
      })),
    }
  })
}

function makeSide(appid: number, platform: string, side: Side, nodes: AccessNode[], targets: Target[]): SideDraft {
  const target = targets.find((t) => t.appid === appid && t.platform === platform && t.side === side) ?? null
  const assigned = nodes.filter((n) => n.sides.some((s) => s.platform === platform && s.side === side))
  return {
    enabled: target?.desired === 'enabled',
    actual: target?.actual ?? 'stopped',
    reason: target?.reason ?? null,
    nodeIds: assigned.map((n) => n.id),
    usableNodeCount: assigned.filter((n) => n.state === 'available').length,
    target,
  }
}

async function reload() {
  const [n, t, a] = await Promise.all([getNodes(), getTargets(), getAccounts()])
  nodeMap.value = new Map(n.map((x) => [x.id, x]))
  accounts.value = a
  games.value = buildGames(n, t)
  loaded.value = true
  error.value = null
  stale.value = false
}

onMounted(async () => {
  try {
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
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

function isLive(s: SideDraft) {
  return s.enabled && (s.actual === 'running' || s.actual === 'starting')
}
function isBlocked(s: SideDraft) {
  return s.enabled && (s.actual === 'blocked' || s.actual === 'error')
}

function statusWord(s: SideDraft) {
  if (s.actual === 'stopping') return { t: '停止中', k: 'warn' as const }
  if (!s.enabled) return { t: '关', k: 'off' as const }
  if (s.actual === 'running') return { t: '采集中', k: 'live' as const }
  if (s.actual === 'starting') return { t: '启动中', k: 'live' as const }
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

function requestToggle(g: GameDraft, p: PlatInGame, side: Side) {
  const s = sideOf(p, side)
  if (s.actual === 'stopping' || busy.value) return
  if (s.enabled && isLive(s)) {
    confirmClose.value = {
      appid: g.appid,
      platform: p.platform,
      side,
      label: `${g.shortName} · ${p.platform.toUpperCase()} ${sideText(side)}`,
    }
    return
  }
  applyToggle(g, p, side)
}

async function applyToggle(g: GameDraft, p: PlatInGame, side: Side) {
  const s = sideOf(p, side)
  busy.value = true
  error.value = null
  try {
    if (s.target) {
      const next = s.target.desired === 'enabled' ? 'disabled' : 'enabled'
      await setTargetDesired(s.target.id, s.target.revision, next)
    } else {
      await createTarget(p.platform, g.appid, side, 'enabled')
    }
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '操作失败'
  } finally {
    busy.value = false
  }
}

async function addGame() {
  const appid = Number(newAppid.value)
  if (!Number.isInteger(appid) || appid < 1) return
  busy.value = true
  error.value = null
  try {
    await createTarget('steam', appid, 'ask', 'disabled')
    newAppid.value = ''
    await reload()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '添加失败'
  } finally {
    busy.value = false
  }
}

function onConfirmClose() {
  if (!confirmClose.value) return
  const g = games.value.find((x) => x.appid === confirmClose.value!.appid)
  const p = g?.platforms.find((x) => x.platform === confirmClose.value!.platform)
  if (g && p) void applyToggle(g, p, confirmClose.value.side)
  confirmClose.value = null
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
    <div v-if="error && !games.length" class="fail">{{ error }}</div>
    <div v-else-if="loading" class="loading">加载中</div>

    <template v-else>
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
        <input v-model="newAppid" class="inp" placeholder="appid" inputmode="numeric" />
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
              <span class="num">节点 {{ g.nodeIds.length }}</span>
              <span class="game-heat">{{ heatLabel(gameHeat(g)) }}</span>
            </div>
          </header>

          <div class="plat-rows">
            <div v-for="p in g.platforms" :key="p.platform" class="plat-row">
              <div class="plat-tag">{{ p.platform }}</div>
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
                  </div>
                  <div class="dir-body">
                    <div class="num cap">
                      节点 {{ sideOf(p, side).usableNodeCount }}/{{ sideOf(p, side).nodeIds.length }}
                    </div>
                    <div v-if="sideOf(p, side).nodeIds.length" class="mini-nodes">
                      <span v-for="id in sideOf(p, side).nodeIds" :key="id" class="ntag sm">
                        {{ nodeMap.get(id)?.name ?? id }}
                      </span>
                    </div>
                    <div v-else class="muted sm">未分节点</div>
                    <div v-if="sideOf(p, side).reason" class="reason">{{ sideOf(p, side).reason }}</div>
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
      v-if="confirmClose"
      title="关闭采集"
      :impact="confirmClose.label"
      consequence="先进入停止中：不再发新请求。在途结束后才是已停止。已保存数据保留。"
      confirm-text="关闭"
      @confirm="onConfirmClose"
      @cancel="confirmClose = null"
    />
  </div>
</template>

<style scoped>
.ov {
  width: 100%;
  max-width: 1100px;
  margin: 0 auto;
  padding: clamp(12px, 2.5vw, 28px) clamp(12px, 2.5vw, 32px) 40px;
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
.reason { font-size: 12px; color: var(--warn); word-break: break-word; }
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

.empty-cta { margin-top: 16px; border: 1px solid var(--line); }
.add-game { display: flex; gap: 8px; margin-bottom: 14px; }
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
