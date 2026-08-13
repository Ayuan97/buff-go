<script setup lang="ts">
import { useEscape } from '../utils/escape'

// 危险操作确认框：必须写明具体影响对象和后续状态
defineProps<{
  title: string
  impact: string
  consequence: string
  confirmText?: string
}>()

const emit = defineEmits<{ confirm: []; cancel: [] }>()

useEscape(() => emit('cancel'))
</script>

<template>
  <div class="modal-mask" @click.self="emit('cancel')">
    <div class="modal" role="dialog" :aria-label="title">
      <div class="modal-head">{{ title }}</div>
      <div class="modal-body">
        <dl class="kv">
          <dt>影响对象</dt>
          <dd>{{ impact }}</dd>
          <dt>后续状态</dt>
          <dd>{{ consequence }}</dd>
        </dl>
      </div>
      <div class="modal-foot">
        <button class="btn" autofocus @click="emit('cancel')">取消</button>
        <button class="btn danger" @click="emit('confirm')">
          {{ confirmText ?? '确认执行' }}
        </button>
      </div>
    </div>
  </div>
</template>
