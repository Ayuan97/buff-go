<script setup lang="ts">
// 空状态：区分“尚未配置 / 确实没有数据 / 筛选后无结果”
defineProps<{
  kind: 'unconfigured' | 'empty' | 'filtered'
  text?: string
}>()
</script>

<template>
  <div class="empty">
    <div class="title">
      {{ kind === 'unconfigured' ? '尚未配置' : kind === 'empty' ? '暂无数据' : '筛选无结果' }}
    </div>
    <div>{{ text }}</div>
  </div>
</template>
