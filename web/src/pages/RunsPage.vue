<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getWorkers, type Worker, type WorkerItem, type WorkerWait } from '../api'
import EmptyState from '../components/EmptyState.vue'
import {
  apiErrorText,
  fenToYuan,
  fmtAgo,
  gameName,
  platformLabel,
  fmtWaitRetry,
  regionText,
  sessionStateText,
  sideText,
  waitCategoryText,
} from '../utils/format'

const workers = ref<Worker[] | null>(null)
const error = ref<string | null>(null)
const stale = ref(false)
const open = ref<number | null>(null)
let poll = 0
let seq = 0

const ordered = computed(() => [...(workers.value ?? [])].sort((a, b) => workerRank(a) - workerRank(b) || a.combination_id - b.combination_id))

function workerRank(worker: Worker): number {
  if (worker.claim?.active) return 0
  if (worker.active_waits.length) return 1
  if (worker.claim) return 2
  return 3
}

function pageItems(worker: Worker): WorkerItem[] {
  return worker.last_page?.items ?? []
}

function pageMeta(worker: Worker): { appid: number; side: 'bid' | 'ask'; platform: string; at?: string } | null {
  if (worker.last_page) {
    return {
      appid: worker.last_page.appid,
      side: worker.last_page.side,
      platform: worker.last_page.platform,
      at: worker.last_page.committed_at,
    }
  }
  return null
}

function idleWhy(worker: Worker): string {
  if (worker.session_state === 'invalid') return '会话失效，不会领任务'
  if (worker.session_state === 'unverified') return '会话未验证'
  const wait = worker.active_waits[0]
  if (wait) return `${waitCategoryText(wait.reason)} · ${fmtWaitRetry(wait.retry_after_sec, wait.retry_at)}`
  if (worker.claim && !worker.claim.active) return '任务已认领，等待执行'
  if (worker.last_page) return '两枪之间空闲'
  return '还没写下过页'
}

function endpointLabel(endpoint?: string, side?: 'bid' | 'ask'): string {
  if (endpoint === 'market_summary') return '出售搜索'
  if (endpoint === 'market_orderbook') return '求购订单簿'
  return side ? `${sideText(side)}接口` : '当前接口'
}

function waitLabel(wait: WorkerWait): string {
  const category = waitCategoryText(wait.reason)
  if (wait.scope === 'account_exit_endpoint') {
    return `${category} · ${endpointLabel(wait.endpoint, wait.side)}`
  }
  if (wait.scope === 'node_platform') return `${category} · 节点`
  return `${category} · 组合`
}

async function reload() {
  const n = ++seq
  try {
    const rows = await getWorkers()
    if (n !== seq) return
    workers.value = rows
    error.value = null
    stale.value = false
  } catch (err) {
    if (n !== seq) return
    error.value = err instanceof Error ? apiErrorText(err.message) : '加载失败'
    if (workers.value) stale.value = true
  }
}

onMounted(() => {
  void reload()
  poll = window.setInterval(() => { void reload() }, 2000)
})
onUnmounted(() => {
  window.clearInterval(poll)
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>运行</h1>
      <p class="hero-sub">
        每一行是一个工人：账号加出口 IP。认领只持续几秒，所以详情保留最近一次提交，展开可看全部商品。
      </p>
    </header>

    <div v-if="stale" class="stale-banner" role="status" aria-live="polite">数据可能过期，已保留上次成功结果</div>
    <div v-if="error && !workers" class="fail" role="alert">{{ error }}</div>
    <EmptyState v-else-if="!workers" kind="empty" text="读取中" />
    <EmptyState v-else-if="!workers.length" kind="empty" text="还没有账号绑到节点，先去资源页绑组合" />

    <template v-else>
      <table class="data">
        <thead>
          <tr><th>工人</th><th>会话</th><th>当前认领</th><th>最近提交</th></tr>
        </thead>
        <tbody>
          <template v-for="worker in ordered" :key="worker.combination_id">
            <tr class="row" :class="{ on: open === worker.combination_id }">
              <td>
                <button
                  class="worker-toggle"
                  type="button"
                  :aria-expanded="open === worker.combination_id"
                  :aria-controls="`worker-detail-${worker.combination_id}`"
                  @click="open = open === worker.combination_id ? null : worker.combination_id"
                >
                  <span class="strong">{{ worker.account_alias }} @ {{ worker.exit_address || '出口未填' }}</span>
                  <span class="muted">{{ platformLabel(worker.platform) }} · {{ worker.node_name }} · {{ regionText(worker.region as 'domestic' | 'foreign' | 'hongkong') }}</span>
                </button>
              </td>
              <td>
                <span class="badge" :class="worker.session_state">{{ sessionStateText(worker.session_state as 'unverified' | 'valid' | 'invalid') }}</span>
                <div class="muted sm">{{ worker.claim?.active ? '采集中' : idleWhy(worker) }}</div>
              </td>
              <td>
                <template v-if="worker.claim">
                  <div class="strong">{{ gameName(worker.claim.appid) }} · {{ sideText(worker.claim.side) }}</div>
                  <div class="muted">
                    {{ platformLabel(worker.claim.platform) }}
                    <span v-if="worker.claim.task_id"> · 任务 #{{ worker.claim.task_id }}</span>
                    <span v-if="worker.claim.claimed_at"> · {{ fmtAgo(worker.claim.claimed_at) }}</span>
                  </div>
                </template>
                <span v-else class="muted">没有任务</span>
              </td>
              <td>
                <template v-if="pageMeta(worker)">
                  <div class="strong">{{ gameName(pageMeta(worker)!.appid) }} · {{ sideText(pageMeta(worker)!.side) }}</div>
                  <div class="muted">
                    {{ pageItems(worker).length }} 件
                    <span v-if="pageMeta(worker)!.at"> · {{ fmtAgo(pageMeta(worker)!.at!) }}</span>
                  </div>
                </template>
                <span v-else class="muted">还没有提交记录</span>
              </td>
            </tr>
            <tr v-if="open === worker.combination_id" :id="`worker-detail-${worker.combination_id}`" class="detail">
              <td colspan="4">
                <div v-if="worker.active_waits.length" class="wait-list">
                  <div v-for="wait in worker.active_waits" :key="`${wait.scope}:${wait.endpoint || wait.node_id || wait.combination_id}`" class="wait-row">
                    <span class="wait-mark">等待</span>
                    <span>{{ waitLabel(wait) }}</span>
                    <span class="muted" :title="wait.retry_at">{{ fmtWaitRetry(wait.retry_after_sec, wait.retry_at) }}</span>
                  </div>
                </div>
                <div v-if="pageItems(worker).length" class="items">
                  <div v-for="item in pageItems(worker)" :key="item.product_id" class="item">
                    <span>{{ item.name }}</span>
                    <span class="muted">{{ item.present_cents != null ? fenToYuan(item.present_cents) : item.status }}</span>
                  </div>
                </div>
                <div v-else class="muted">本次提交没有商品明细。刚开启方向、还没有成功提交时这里也是空的。</div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </template>
  </div>
</template>

<style scoped>
.pg { width: 100%; max-width: none; margin: 0; padding: 20px 28px 40px; box-sizing: border-box; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-2); }
.fail { color: #ff6b6b; }
.pg .muted { color: var(--text-2); }
.sm { font-size: 11px; }
.badge { font-size: 11px; }
.badge.valid { color: var(--ok); }
.badge.unverified { color: var(--warn); }
.badge.invalid { color: #ff6b6b; }
.row.on { background: var(--surface-2); }
.worker-toggle {
  display: grid;
  gap: 2px;
  width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.worker-toggle:focus-visible { outline: 2px solid var(--text-2); outline-offset: 4px; }
.detail td { padding: 10px 12px 14px; background: var(--surface-2); }
.wait-list { display: grid; gap: 5px; margin-bottom: 10px; }
.wait-row { display: flex; align-items: baseline; gap: 8px; font-size: 12px; }
.wait-mark { color: var(--warn); font-family: var(--mono); font-size: 11px; }
.items { display: grid; gap: 4px; }
.item { display: flex; justify-content: space-between; gap: 12px; font-family: var(--mono); font-size: 12px; }
</style>
