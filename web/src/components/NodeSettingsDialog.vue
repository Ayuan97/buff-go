<script setup lang="ts">
import { computed, ref } from 'vue'
import type { AccessNode, Region } from '../api'
import { useEscape } from '../utils/escape'
import { egressModeText, fmtTime, nodeKindText, nodeStateText, regionText } from '../utils/format'

const props = defineProps<{
  node: AccessNode
  busy?: boolean
}>()

const emit = defineEmits<{
  close: []
  exit: [address: string, validUntil: string, done: (saved: boolean) => void]
  rename: [name: string]
  connection: [payload: {
    kind: 'direct' | 'proxy'
    region: Region
    egress_mode: 'static' | 'sticky'
    proxy_credential?: string
    sticky_session_valid_until?: string
  }, done: (saved: boolean) => void]
}>()

useEscape(() => emit('close'))

const REGIONS: Region[] = ['foreign', 'hongkong', 'domestic']

const exitAddress = ref('')
const exitValidUntil = ref('')
const nameDraft = ref(props.node.name)
const connDraft = ref({
  kind: props.node.kind,
  region: props.node.region,
  egress_mode: props.node.egress_mode,
  proxy_credential: '',
  sticky_until: '',
})
const localError = ref<string | null>(null)

const line = computed(() => {
  const parts = [nodeKindText(props.node.kind), regionText(props.node.region)]
  if (props.node.kind === 'proxy') parts.push(egressModeText(props.node.egress_mode))
  return parts.join(' · ')
})

function localToISO(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString()
}

function presetDays(days: number) {
  const date = new Date(Date.now() + days * 86_400_000)
  const pad = (n: number) => String(n).padStart(2, '0')
  exitValidUntil.value = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function submitExit() {
  localError.value = null
  const address = exitAddress.value.trim()
  const until = localToISO(exitValidUntil.value)
  if (!address) {
    localError.value = '请填出口 IP'
    return
  }
  if (!until) {
    localError.value = '请填出口有效期'
    return
  }
  if (new Date(until).getTime() <= Date.now()) {
    localError.value = '有效期必须晚于现在'
    return
  }
  emit('exit', address, until, (saved) => {
    if (!saved) return
    exitAddress.value = ''
    exitValidUntil.value = ''
  })
}

function submitName() {
  localError.value = null
  const name = nameDraft.value.trim()
  if (!name) {
    localError.value = '节点名不能为空'
    return
  }
  // 服务端按 UTF-8 字节数限制 128，中文一个字算三字节，这里用同一个口径先拦下来
  if (new TextEncoder().encode(name).length > 128) {
    localError.value = '节点名太长，最多 128 个字节，中文一个字算三个字节'
    return
  }
  if (/[\u0000-\u001f\u007f]/.test(name)) {
    localError.value = '节点名不能有控制字符'
    return
  }
  if (name === props.node.name) {
    localError.value = '名字没有变化'
    return
  }
  emit('rename', name)
}

function submitConnection() {
  localError.value = null
  const draft = connDraft.value
  const credential = draft.proxy_credential.trim()
  if (draft.kind === 'direct' && credential) {
    localError.value = '本机直连不能填代理地址'
    return
  }
  const payload = {
    kind: draft.kind,
    region: draft.region,
    // 直连没有会话保持一说，出口模式固定。
    egress_mode: draft.kind === 'proxy' ? draft.egress_mode : ('static' as const),
    proxy_credential: undefined as string | undefined,
    sticky_session_valid_until: undefined as string | undefined,
  }
  if (draft.kind === 'proxy') {
    if (!credential) {
      localError.value = '代理节点必须填代理地址；已存的地址不回显，换连接要重新填一次'
      return
    }
    payload.proxy_credential = credential
    if (draft.egress_mode === 'sticky') {
      const until = localToISO(draft.sticky_until)
      if (!until) {
        localError.value = '会话保持必须填会话到期时间'
        return
      }
      if (new Date(until).getTime() <= Date.now()) {
        localError.value = '会话到期必须晚于现在'
        return
      }
      payload.sticky_session_valid_until = until
    }
  }
  emit('connection', payload, (saved) => {
    if (!saved) return
    draft.proxy_credential = ''
    draft.sticky_until = ''
  })
}
</script>

<template>
  <div class="modal-mask" @click.self="emit('close')">
    <div class="modal wide" role="dialog" :aria-label="`节点设置 ${node.name}`">
      <div class="modal-head">节点设置 · {{ node.name }}</div>
      <div class="modal-body">
        <dl class="kv">
          <dt>线路</dt>
          <dd>{{ line }}</dd>
          <dt>状态</dt>
          <dd>{{ nodeStateText(node.state) }}</dd>
          <dt>当前出口</dt>
          <dd>
            {{ node.exit?.address ?? '还没填' }}
            <span v-if="node.exit" class="muted"> · 有效期至 {{ fmtTime(node.exit.valid_until) }}</span>
          </dd>
        </dl>

        <p v-if="localError" class="err">{{ localError }}</p>

        <section class="sec">
          <h3>名称</h3>
          <p class="note">只改显示名，不影响线路、归属和已填的出口 IP。</p>
          <div class="ops">
            <input v-model="nameDraft" placeholder="local 或 hk-1" />
            <span class="spacer" />
            <button class="btn sm" type="button" :disabled="busy" @click="submitName">保存名称</button>
          </div>
        </section>

        <section class="sec">
          <h3>出口 IP</h3>
          <p class="note">
            节点必须有一个已填写的公网出口 IP 才能参与采集，本机直连也一样。
            限频按「接口 + 账号 + 出口 IP」计算：三者都相同才共用预算，不同接口、账号或平台相互独立。
          </p>
          <div class="grid">
            <div class="field">
              <label>公网 IP</label>
              <input v-model="exitAddress" placeholder="203.0.113.9" />
            </div>
            <div class="field">
              <label>有效期至</label>
              <input v-model="exitValidUntil" type="datetime-local" />
            </div>
          </div>
          <div class="ops">
            <button class="btn sm" type="button" @click="presetDays(30)">+30 天</button>
            <button class="btn sm" type="button" @click="presetDays(180)">+180 天</button>
            <span class="spacer" />
            <button class="btn primary sm" type="button" :disabled="busy" @click="submitExit">保存出口</button>
          </div>
        </section>

        <section class="sec">
          <h3>连接</h3>
          <p class="note">
            换连接会清掉已填的出口 IP，节点回到待填状态，采集会跳过它直到重新填一次。
            已存的代理地址不回显，所以改代理节点的任何一项都要重新填一次地址。
          </p>
          <div class="grid">
            <div class="field">
              <label>线路</label>
              <select v-model="connDraft.kind">
                <option value="direct">本机直连</option>
                <option value="proxy">代理</option>
              </select>
            </div>
            <div class="field">
              <label>地域</label>
              <select v-model="connDraft.region">
                <option v-for="option in REGIONS" :key="option" :value="option">
                  {{ regionText(option) }}
                </option>
              </select>
            </div>
          </div>
          <template v-if="connDraft.kind === 'proxy'">
            <div class="field">
              <label>出口模式</label>
              <select v-model="connDraft.egress_mode">
                <option value="static">固定出口</option>
                <option value="sticky">会话保持</option>
              </select>
            </div>
            <div class="field">
              <label>代理地址</label>
              <input v-model="connDraft.proxy_credential" placeholder="http://user:pass@host:port" />
            </div>
            <div v-if="connDraft.egress_mode === 'sticky'" class="field">
              <label>会话到期</label>
              <input v-model="connDraft.sticky_until" type="datetime-local" />
            </div>
          </template>
          <div class="ops">
            <span class="spacer" />
            <button class="btn sm" type="button" :disabled="busy" @click="submitConnection">替换连接</button>
          </div>
        </section>
      </div>
      <div class="modal-foot">
        <button class="btn" type="button" @click="emit('close')">关闭</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal.wide { width: 620px; }
.sec { border-top: 1px solid var(--line); padding-top: 12px; margin-top: 14px; }
.sec h3 {
  margin: 0 0 6px;
  font-size: 11px;
  font-family: var(--mono);
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--text-2);
}
.note { margin: 0 0 8px; color: var(--text-3); font-size: 12px; }
.note.warn { color: var(--warn); }
.err { margin: 10px 0 0; color: var(--danger); font-size: 12px; }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.grid .field { margin-bottom: 8px; }
.ops { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.ops .spacer { flex: 1; }
.plat { display: flex; gap: 10px; align-items: center; padding: 5px 0; flex-wrap: wrap; }
.plat-name { min-width: 64px; font-family: var(--mono); font-size: 12px; }
/* 当前生效的方向按钮禁用后仍要看得出是选中态 */
.plat .btn.primary:disabled { opacity: 1; }
input.mini {
  width: 96px; height: 28px; padding: 0 8px;
  background: var(--bg); color: var(--text);
  border: 1px solid var(--line-strong); font-family: var(--mono); font-size: 12px;
}
input.mini:disabled { opacity: 0.4; }
</style>
