<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getRuns, type Run } from '../api'
import EmptyState from '../components/EmptyState.vue'
import { fmtAgo, fmtTime, sideText } from '../utils/format'

const runs = ref<Run[] | null>(null)
const error = ref<string | null>(null)
const stale = ref(false)
const filterPlatform = ref('')
const viewMode = ref<'active' | 'all'>('active')
let poll = 0

const visible = computed(() => {
  const list = runs.value ?? []
  return list.filter((r) => {
    if (filterPlatform.value && r.platform !== filterPlatform.value) return false
    if (viewMode.value === 'active') return r.state === 'running' || r.state === 'pending' || r.state === 'failed'
    return true
  })
})

async function load() {
  try {
    runs.value = await getRuns(100)
    error.value = null
    stale.value = false
  } catch (e) {
    if (runs.value) stale.value = true
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
      <h1>运行</h1>
    </header>
    <div class="toolbar">
      <button class="btn sm" :class="{ primary: viewMode === 'active' }" @click="viewMode = 'active'">活动/失败</button>
      <button class="btn sm" :class="{ primary: viewMode === 'all' }" @click="viewMode = 'all'">全部</button>
      <input v-model="filterPlatform" class="inp" placeholder="平台" />
    </div>
    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="error && !runs" class="fail">{{ error }}</div>
    <EmptyState v-else-if="!runs" kind="empty" text="加载中" />
    <EmptyState v-else-if="!visible.length" kind="empty" text="无运行" />
    <table v-else class="data">
      <thead>
        <tr>
          <th>ID</th><th>平台</th><th>游戏</th><th>方向</th><th>状态</th><th>页</th><th>游标</th><th>开始</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in visible" :key="r.id">
          <td class="num">#{{ r.id }}</td>
          <td>{{ r.platform }}</td>
          <td class="num">{{ r.appid }}</td>
          <td>{{ sideText(r.side ?? null) }}</td>
          <td>
            {{ r.state }}
            <span v-if="r.completeness" class="muted"> · {{ r.completeness }}</span>
            <span v-if="r.reason" class="muted"> · {{ r.reason }}</span>
          </td>
          <td class="num">{{ r.last_page_sequence }}</td>
          <td class="num">{{ r.cursor || '—' }}</td>
          <td class="num" :title="fmtTime(r.started_at ?? r.created_at)">{{ fmtAgo(r.started_at ?? r.created_at) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.pg { max-width: 1100px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.toolbar { display: flex; gap: 8px; margin-bottom: 12px; }
.inp { height: 28px; background: var(--bg); color: var(--text); border: 1px solid var(--line-strong); font-family: var(--mono); padding: 0 8px; }
.fail { color: var(--danger); }
</style>
