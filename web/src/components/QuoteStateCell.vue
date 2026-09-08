<script setup lang="ts">
import { computed } from 'vue'
import type { Quote } from '../api/types'
import { fenToYuan, fmtAgo, fmtTime } from '../utils/format'

const props = defineProps<{ quote: Quote | undefined }>()

// 主行是这次尝试；失败/空/不可用不能盖掉历史有效价，那是另一条线。
const view = computed(() => {
  const q = props.quote
  if (!q) return { kind: 'none' as const }
  if (q.status === 'present' && q.present_cents != null)
    return {
      kind: 'present' as const,
      price: fenToYuan(q.present_cents),
      counts: q.present_order_count != null ? `${q.present_order_count} 单` : null,
      collectedAt: q.collected_at,
      sourceTime: q.source_time,
    }
  const label = q.status === 'empty' ? '空行情' : q.status === 'unavailable' ? '不可用' : '采集失败'
  const cls = q.status === 'failed' ? 'danger' : 'info'
  return {
    kind: 'stale' as const,
    label,
    cls,
    history:
      q.present_cents != null
        ? `历史有效 ${fenToYuan(q.present_cents)}（${fmtAgo(q.present_collected_at ?? null)}）`
        : '无历史有效价',
    collectedAt: q.collected_at,
    sourceTime: q.source_time,
  }
})
</script>

<template>
  <div v-if="view.kind === 'none'" class="muted">—</div>
  <div v-else class="cell">
    <div class="line">
      <span v-if="view.kind === 'present'" class="num strong">{{ view.price }}</span>
      <template v-else>
        <span class="badge" :class="view.cls">{{ view.label }}</span>
        <span class="muted history">{{ view.history }}</span>
      </template>
    </div>
    <div class="meta muted">
      <span v-if="view.kind === 'present' && view.counts" class="num">{{ view.counts }} · </span>
      <span class="num" :title="`采集 ${fmtTime(view.collectedAt)}`">采 {{ fmtAgo(view.collectedAt) }}</span>
      <span v-if="view.sourceTime" class="num" :title="`平台 ${fmtTime(view.sourceTime)}`">· 源 {{ fmtAgo(view.sourceTime) }}</span>
    </div>
  </div>
</template>

<style scoped>
.cell { display: flex; flex-direction: column; gap: 1px; }
.line { display: inline-flex; align-items: center; gap: 6px; }
.history { font-size: 12px; }
.meta { font-size: 11px; display: inline-flex; align-items: center; gap: 4px; }
</style>
