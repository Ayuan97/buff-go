<script setup lang="ts">
import { computed } from 'vue'
import type { ActualState } from '../api/types'

const props = defineProps<{ state: ActualState; reason?: string | null }>()

const map: Record<ActualState, { text: string; cls: string }> = {
  starting: { text: '启动中', cls: 'info' },
  waiting: { text: '等待中', cls: 'info' },
  running: { text: '运行中', cls: 'ok' },
  blocked: { text: '已阻塞', cls: 'warn' },
  stopping: { text: '停止中', cls: 'warn' },
  stopped: { text: '已停止', cls: 'neutral' },
  error: { text: '错误', cls: 'danger' },
}

const view = computed(() => map[props.state])
</script>

<template>
  <span class="badge" :class="view.cls" :title="reason ?? undefined">
    <span class="dot" />{{ view.text }}
  </span>
</template>
