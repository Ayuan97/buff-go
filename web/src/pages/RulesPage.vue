<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getCapabilities } from '../api'

const detail = ref<string>('')
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const cap = await getCapabilities()
    detail.value = cap.detail
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  }
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>规则</h1>
      <p class="hero-sub">无详情能力时只记 detail_unavailable，不造空任务。</p>
    </header>
    <div v-if="error" class="fail">{{ error }}</div>
    <p v-else-if="detail === 'detail_unavailable'" class="muted">
      当前详情能力：<span class="num">detail_unavailable</span>。规则引擎未接入，不会创建详情运行。
    </p>
  </div>
</template>

<style scoped>
.pg { max-width: 1100px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 14px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); }
.fail { color: var(--danger); }
</style>
