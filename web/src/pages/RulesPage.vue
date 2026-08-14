<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { APIError, getCapabilities } from '../api'
import EmptyState from '../components/EmptyState.vue'
import { apiErrorText } from '../utils/format'

const detail = ref('')
const loadError = ref<string | null>(null)
const loading = ref(true)

// 后端目前只声明 detail_unavailable；真出现别的值说明后端已扩展，原样显示，不替它编解释。
const detailNote = computed(() =>
  detail.value === 'detail_unavailable'
    ? '详情采集不可用：后端没有详情适配器，命中行情也不会创建详情任务。'
    : '后端返回了这一页还没有对应说明的能力值。',
)

onMounted(async () => {
  try {
    detail.value = (await getCapabilities()).detail
  } catch (e) {
    loadError.value = e instanceof APIError ? apiErrorText(e.code) : '接口请求失败'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>规则</h1>
      <p class="hero-sub">这一页只反映采集能力状态，没有规则配置。</p>
    </header>

    <section class="panel">
      <div class="panel-head">采集能力</div>
      <div class="panel-body padded">
        <EmptyState v-if="loading" kind="empty" text="读取中" />
        <div v-else-if="loadError" class="fail">读取能力失败：{{ loadError }}</div>
        <template v-else>
          <dl class="kv">
            <dt>详情能力</dt>
            <dd class="num">{{ detail }}</dd>
          </dl>
          <p class="note">{{ detailNote }}</p>
        </template>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">为什么没有规则列表</div>
      <div class="panel-body padded">
        <p class="note">
          规则引擎属于采集侧，要靠规则在命中行情后创建详情任务。后端没有规则表，也没有规则相关接口，
          所以这一页没有规则列表，也没有新增、修改、删除。
        </p>
        <p class="note">当前没有任何规则数据可显示，这一页的作用就是上面那条能力状态。</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.pg { max-width: 1100px; margin: 0 auto; padding: 20px 24px 48px; }
.hero { margin-bottom: 16px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); font-size: 12px; }
.panel { margin-bottom: 14px; }
.note { margin: 8px 0 0; color: var(--text-3); font-size: 12px; }
.note + .note { margin-top: 6px; }
.fail { color: var(--danger); font-family: var(--mono); font-size: 12px; }
</style>
