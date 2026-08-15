<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getWorkers, type Worker, type WorkerItem } from '../api'
import EmptyState from '../components/EmptyState.vue'
import {
  apiErrorText,
  fenToYuan,
  fmtAgo,
  gameName,
  platformLabel,
  regionText,
  sessionStateText,
  sideText,
} from '../utils/format'

const workers = ref<Worker[] | null>(null)
const error = ref<string | null>(null)
const stale = ref(false)
const open = ref<number | null>(null)
let poll = 0
let seq = 0

const busy = computed(() => (workers.value ?? []).filter((w) => !w.idle))
const idle = computed(() => (workers.value ?? []).filter((w) => w.idle))

function pageItems(worker: Worker): WorkerItem[] {
  if (worker.claim?.items.length) return worker.claim.items
  return worker.last_page?.items ?? []
}

function pageMeta(worker: Worker): { appid: number; side: 'bid' | 'ask'; platform: string; at?: string } | null {
  if (worker.claim) {
    return { appid: worker.claim.appid, side: worker.claim.side, platform: worker.claim.platform }
  }
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
  if (worker.last_page) return '两枪之间空闲'
  return '还没写下过页'
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
        每一行是一个工人：账号加出口 IP。认领只持续几秒，所以详情看它最近写下的那一页，点开看全部商品。
      </p>
    </header>

    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="error && !workers" class="fail">{{ error }}</div>
    <EmptyState v-else-if="!workers" kind="empty" text="读取中" />
    <EmptyState v-else-if="!workers.length" kind="empty" text="还没有账号绑到节点，先去资源页绑组合" />

    <template v-else>
      <table class="data">
        <thead>
          <tr><th>工人</th><th>会话</th><th>正在采</th><th>最近一页</th></tr>
        </thead>
        <tbody>
          <template v-for="worker in [...busy, ...idle]" :key="worker.combination_id">
            <tr class="row" :class="{ on: open === worker.combination_id }" @click="open = open === worker.combination_id ? null : worker.combination_id">
              <td>
                <div class="strong">{{ worker.account_alias }} @ {{ worker.exit_address || '出口未填' }}</div>
                <div class="muted">{{ platformLabel(worker.platform) }} · {{ worker.node_name }} · {{ regionText(worker.region as 'domestic' | 'foreign' | 'hongkong') }}</div>
              </td>
              <td>
                <span class="badge" :class="worker.session_state">{{ sessionStateText(worker.session_state as 'unverified' | 'valid' | 'invalid') }}</span>
                <div class="muted sm">{{ worker.idle ? idleWhy(worker) : '采集中' }}</div>
              </td>
              <td>
                <template v-if="worker.claim">
                  <div class="strong">{{ gameName(worker.claim.appid) }} · {{ sideText(worker.claim.side) }}</div>
                  <div class="muted">{{ platformLabel(worker.claim.platform) }}</div>
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
                <span v-else class="muted">还没写下过页</span>
              </td>
            </tr>
            <tr v-if="open === worker.combination_id" class="detail">
              <td colspan="4">
                <div v-if="pageItems(worker).length" class="items">
                  <div v-for="item in pageItems(worker)" :key="item.product_id" class="item">
                    <span>{{ item.name }}</span>
                    <span class="muted">{{ item.present_cents != null ? fenToYuan(item.present_cents) : item.status }}</span>
                  </div>
                </div>
                <div v-else class="muted">这一页还没有商品明细。工人空闲时看最近提交的那一页；刚开方向、还没写下过页就是空的。</div>
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
.hero-sub { margin: 6px 0 0; color: var(--text-3); }
.fail { color: var(--danger); }
.sm { font-size: 11px; }
.badge { font-size: 11px; }
.badge.valid { color: var(--ok); }
.badge.unverified { color: var(--warn); }
.badge.invalid { color: var(--danger); }
.row { cursor: pointer; }
.row.on { background: var(--surface-2); }
.detail td { padding: 10px 12px 14px; background: var(--surface-2); }
.items { display: grid; gap: 4px; }
.item { display: flex; justify-content: space-between; gap: 12px; font-family: var(--mono); font-size: 12px; }
</style>
