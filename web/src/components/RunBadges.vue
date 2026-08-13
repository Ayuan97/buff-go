<script setup lang="ts">
import { computed } from 'vue'
import type { Completeness, RunState } from '../api/types'

const props = defineProps<{ status: RunState; coverage: Completeness }>()

const statusMap: Record<RunState, { text: string; cls: string }> = {
  pending: { text: '待执行', cls: 'info' },
  running: { text: '运行中', cls: 'ok' },
  succeeded: { text: '成功', cls: 'ok' },
  failed: { text: '失败', cls: 'danger' },
  stopped: { text: '已停止', cls: 'neutral' },
}

// 运行状态与覆盖完整性是两个独立维度，必须同时展示
const s = computed(() => statusMap[props.status])
const covText = computed(() => (props.coverage === 'complete' ? '完整' : props.coverage === 'partial' ? '部分' : '—'))
const covCls = computed(() => (props.coverage === 'complete' ? 'ok' : props.coverage === 'partial' ? 'warn' : 'neutral'))
</script>

<template>
  <span class="run-badges">
    <span class="badge" :class="s.cls"><span class="dot" />{{ s.text }}</span>
    <span class="badge" :class="covCls">{{ covText }}覆盖</span>
  </span>
</template>

<style scoped>
.run-badges { display: inline-flex; gap: 6px; }
</style>
