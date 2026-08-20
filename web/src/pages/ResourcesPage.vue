<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  createAccount,
  createCombination,
  createNode,
  deleteAccount,
  deleteCombination,
  deleteNode,
  getAccounts,
  getCombinations,
  getNodes,
  recordNodeExit,
  renameAccount,
  renameNode,
  replaceAccountSession,
  replaceNodeConnection,
  type AccessNode,
  type Account,
  type Combination,
  type Region,
} from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import NodeSettingsDialog from '../components/NodeSettingsDialog.vue'
import { useEscape } from '../utils/escape'
import {
  apiErrorText,
  egressModeText,
  fmtAgo,
  fmtTime,
  nodeKindText,
  nodeStateText,
  platformLabel,
  regionText,
  sessionStateText,
} from '../utils/format'
import { nodeAllowsPlatform, nodeIsUsableAt } from '../utils/platformRegion'

const accounts = ref<Account[] | null>(null)
const nodes = ref<AccessNode[] | null>(null)
const combinations = ref<Combination[]>([])
const loadError = ref<string | null>(null)
const stale = ref(false)
const actionError = ref<string | null>(null)
const busy = ref(false)
const showAllGaps = ref(false)
const now = ref(Date.now())

const confirm = ref<{ title: string; impact: string; consequence: string; run: () => Promise<unknown> } | null>(null)
const showAddAccount = ref(false)
const showAddNode = ref(false)
const accountEdit = ref<{ account: Account; alias: string; session: string } | null>(null)
const settingsNodeId = ref<number | null>(null)
const bindDraft = ref<{ node: AccessNode; accountId: number } | null>(null)

const newAccount = ref({ platform: 'steam', alias: '', session: '' })
const newNode = ref({
  name: '',
  kind: 'direct' as 'direct' | 'proxy',
  region: 'foreign' as Region,
  egress_mode: 'static' as 'static' | 'sticky',
  proxy_credential: '',
  sticky_until: '',
})

let clock = 0
onMounted(() => {
  void reload()
  clock = window.setInterval(() => { now.value = Date.now() }, 30_000)
})
onUnmounted(() => {
  if (clock) window.clearInterval(clock)
})

useEscape(() => {
  if (confirm.value) return
  if (bindDraft.value) { bindDraft.value = null; return }
  if (settingsNodeId.value !== null) { settingsNodeId.value = null; return }
  if (accountEdit.value) { accountEdit.value = null; return }
  if (showAddNode.value) { showAddNode.value = false; return }
  if (showAddAccount.value) showAddAccount.value = false
})

async function reload() {
  try {
    const [a, n, c] = await Promise.all([getAccounts(), getNodes(), getCombinations()])
    accounts.value = a
    nodes.value = n
    combinations.value = c
    loadError.value = null
    stale.value = false
    return true
  } catch (e) {
    if (accounts.value && nodes.value) stale.value = true
    else loadError.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
    return false
  }
}

async function runAction(fn: () => Promise<unknown>): Promise<boolean> {
  if (busy.value) return false
  actionError.value = null
  busy.value = true
  try {
    await fn()
    await reload()
    return true
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '操作失败'
    return false
  } finally {
    busy.value = false
  }
}

const accountById = computed(() => new Map((accounts.value ?? []).map((a) => [a.id, a])))
const nodeById = computed(() => new Map((nodes.value ?? []).map((n) => [n.id, n])))
const settingsNode = computed(() =>
  settingsNodeId.value === null ? null : nodeById.value.get(settingsNodeId.value) ?? null,
)

const combosByNode = computed(() => {
  const map = new Map<number, Combination[]>()
  for (const combo of combinations.value) {
    const list = map.get(combo.node_id) ?? []
    list.push(combo)
    map.set(combo.node_id, list)
  }
  return map
})

// 限频按「平台 + 出口 IP」计，同一个 IP 上的多个节点共用预算，必须让使用者看见。
const exitPeers = computed(() => {
  const byAddress = new Map<string, string[]>()
  for (const node of nodes.value ?? []) {
    const address = node.exit?.address
    if (!address) continue
    const list = byAddress.get(address) ?? []
    list.push(node.name)
    byAddress.set(address, list)
  }
  const peers = new Map<number, string[]>()
  for (const node of nodes.value ?? []) {
    const address = node.exit?.address
    if (!address) continue
    const others = (byAddress.get(address) ?? []).filter((name) => name !== node.name)
    if (others.length) peers.set(node.id, others)
  }
  return peers
})

function accountsOfNode(node: AccessNode): Account[] {
  return (combosByNode.value.get(node.id) ?? [])
    .map((combo) => accountById.value.get(combo.account_id))
    .filter((account): account is Account => account !== undefined)
}

function comboOf(node: AccessNode, accountId: number): Combination | undefined {
  return (combosByNode.value.get(node.id) ?? []).find((combo) => combo.account_id === accountId)
}

function bindCandidates(node: AccessNode): Account[] {
  const bound = new Set((combosByNode.value.get(node.id) ?? []).map((c) => c.account_id))
  return (accounts.value ?? []).filter((a) => nodeAllowsPlatform(node.region, a.platform) && !bound.has(a.id))
}

function lineOf(node: AccessNode): string {
  const parts = [nodeKindText(node.kind), regionText(node.region)]
  if (node.kind === 'proxy') parts.push(egressModeText(node.egress_mode))
  return parts.join(' · ')
}

function stateClass(node: AccessNode): string {
  if (node.state === 'available') return nodeIsUsableAt(node, now.value) ? 'ok' : 'danger'
  return node.state === 'unavailable' ? 'danger' : 'warn'
}

function nodeStateLabel(node: AccessNode): string {
  if (node.state !== 'available') return nodeStateText(node.state)
  if (!node.exit) return '出口不可用'
  if (!Number.isFinite(Date.parse(node.exit.valid_until)) || now.value >= Date.parse(node.exit.valid_until)) {
    return '出口过期'
  }
  if (node.sticky_session_valid_until) {
    const until = Date.parse(node.sticky_session_valid_until)
    if (!Number.isFinite(until) || now.value >= until) return '会话过期'
  }
  return nodeStateText(node.state)
}

function sessionClass(account: Account): string {
  if (account.session_state === 'valid') return 'ok'
  return account.session_state === 'invalid' ? 'danger' : 'info'
}

interface Gap {
  text: string
  nodeId?: number
  optional?: boolean
}

const gaps = computed<Gap[]>(() => {
  if (accounts.value === null || nodes.value === null) return []
  const list: Gap[] = []
  const accountList = accounts.value
  const nodeList = nodes.value
  if (!accountList.length) list.push({ text: '还没有账号，先加一个 Steam 账号并粘贴 Cookie' })
  for (const account of accountList) {
    if (account.session_state === 'invalid') {
      list.push({ text: `账号 ${account.alias} 的登录已失效，换一份 Cookie` })
    }
  }
  if (!nodeList.length) list.push({ text: '还没有节点，加一个本机直连或代理节点' })
  for (const node of nodeList) {
    if (nodeIsUsableAt(node, now.value)) continue
    const expired = node.exit && now.value >= Date.parse(node.exit.valid_until)
    const stickyExpired = node.sticky_session_valid_until && now.value >= Date.parse(node.sticky_session_valid_until)
    const reason = expired
      ? '出口 IP 已过期'
      : stickyExpired
        ? '代理会话已过期'
        : '还没有可用出口 IP'
    list.push({ text: `节点 ${node.name} ${reason}，不会被采集使用`, nodeId: node.id })
  }
  for (const node of nodeList) {
    if (accountsOfNode(node).length) continue
    list.push({
      text: `节点 ${node.name} 还没绑账号`,
      nodeId: node.id,
    })
  }
  return list
})

const visibleGaps = computed(() => (showAllGaps.value ? gaps.value : gaps.value.slice(0, 3)))

function ask(title: string, impact: string, consequence: string, run: () => Promise<unknown>) {
  confirm.value = { title, impact, consequence, run }
}

function localToISO(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toISOString()
}

async function submitAccount() {
  const alias = newAccount.value.alias.trim()
  const session = newAccount.value.session.trim()
  if (!alias || !session) {
    actionError.value = '别名和 Cookie 都要填'
    return
  }
  await runAction(async () => {
    await createAccount(newAccount.value.platform, alias, session)
    newAccount.value = { platform: 'steam', alias: '', session: '' }
    showAddAccount.value = false
  })
}

async function submitAccountEdit() {
  const draft = accountEdit.value
  if (!draft || busy.value) return
  const alias = draft.alias.trim()
  const session = draft.session.trim()
  if (!alias) {
    actionError.value = '别名不能为空'
    return
  }
  if (alias === draft.account.alias && !session) {
    accountEdit.value = null
    return
  }
  actionError.value = null
  busy.value = true
  let renamed = false
  try {
    if (alias !== draft.account.alias) {
      await renameAccount(draft.account.id, alias)
      renamed = true
    }
    if (session) await replaceAccountSession(draft.account.id, draft.account.session_revision, session)
    accountEdit.value = null
    await reload()
  } catch (e) {
    const detail = e instanceof Error ? apiErrorText(e.message) : '操作失败'
    if (renamed) {
      const reloaded = await reload()
      accountEdit.value = null
      actionError.value = `别名已保存，但 Cookie 更新失败：${detail}。${reloaded ? '页面已重新加载实际状态' : '重新加载也失败，当前显示可能过期'}`
    } else {
      actionError.value = detail
    }
  } finally {
    busy.value = false
  }
}

async function submitNode() {
  const draft = newNode.value
  const name = draft.name.trim()
  if (!name) {
    actionError.value = '节点名不能为空'
    return
  }
  const proxy = draft.kind === 'proxy'
  const credential = draft.proxy_credential.trim()
  if (proxy && !credential) {
    actionError.value = '代理节点必须填代理地址'
    return
  }
  const sticky = proxy && draft.egress_mode === 'sticky'
  const stickyUntil = sticky ? localToISO(draft.sticky_until) : ''
  if (sticky && !stickyUntil) {
    actionError.value = '会话保持节点必须填会话到期时间'
    return
  }
  await runAction(async () => {
    await createNode({
      name,
      kind: draft.kind,
      region: draft.region,
      egress_mode: proxy ? draft.egress_mode : 'static',
      proxy_credential: proxy ? credential : undefined,
      sticky_session_valid_until: stickyUntil || undefined,
    })
    newNode.value = {
      name: '',
      kind: 'direct',
      region: 'foreign',
      egress_mode: 'static',
      proxy_credential: '',
      sticky_until: '',
    }
    showAddNode.value = false
  })
}

async function submitBind() {
  const draft = bindDraft.value
  if (!draft) return
  if (!draft.accountId) {
    actionError.value = '请选择账号'
    return
  }
  await runAction(async () => {
    await createCombination(draft.accountId, draft.node.id)
    bindDraft.value = null
  })
}
</script>

<template>
  <div class="pg">
    <header class="hero">
      <h1>资源</h1>
      <p class="hero-sub">采集要跑起来，需要一个账号、一个填了出口 IP 的节点，再把账号绑到节点上。工人路由只看平台和地域。</p>
    </header>

    <div v-if="stale" class="stale-banner">数据可能过期，页面保留的是上次成功读到的结果</div>
    <div v-if="actionError" class="panel fail">
      {{ actionError }}
      <button class="btn sm" @click="actionError = null">知道了</button>
    </div>
    <div v-if="loadError" class="panel"><EmptyState kind="empty" :text="loadError" /></div>

    <section v-if="gaps.length" class="panel gaps">
      <div class="panel-head">现在还差</div>
      <div class="panel-body padded">
        <ul class="gap-list">
          <li v-for="(gap, index) in visibleGaps" :key="index" :class="{ optional: gap.optional }">
            <span>{{ gap.text }}</span>
            <button v-if="gap.nodeId" class="btn sm" @click="settingsNodeId = gap.nodeId ?? null">去设置</button>
          </li>
        </ul>
        <button v-if="gaps.length > 3" class="btn sm" @click="showAllGaps = !showAllGaps">
          {{ showAllGaps ? '收起' : `还有 ${gaps.length - 3} 项` }}
        </button>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">
        账号
        <span class="spacer" />
        <button class="btn sm" @click="showAddAccount = true">新增账号</button>
      </div>
      <div class="panel-body">
        <p class="note">账号是平台的登录身份，不属于某个游戏。Cookie 存进数据库，页面不回显。</p>
        <EmptyState v-if="!accounts" kind="empty" text="读取中" />
        <EmptyState v-else-if="!accounts.length" kind="unconfigured" text="还没有账号，点右上角新增" />
        <table v-else class="data">
          <thead>
            <tr><th>别名</th><th>平台</th><th>登录</th><th>最近检查</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="account in accounts" :key="account.id">
              <td>{{ account.alias }}</td>
              <td>{{ platformLabel(account.platform) }}</td>
              <td><span class="badge" :class="sessionClass(account)">{{ sessionStateText(account.session_state) }}</span></td>
              <td class="num">{{ fmtAgo(account.last_checked_at ?? null) }}</td>
              <td class="ops">
                <button class="btn sm" @click="accountEdit = { account, alias: account.alias, session: '' }">编辑</button>
                <button
                  class="btn sm danger"
                  @click="ask('删除账号', account.alias, '账号正在采集或还绑着节点时会删除失败。', () => deleteAccount(account.id))"
                >
                  删除
                </button>
              </td>
            </tr>
          </tbody>
        </table>
        <p class="note">未验证的账号也能开始采集，第一次请求成功后会变成正常。</p>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">
        节点
        <span class="spacer" />
        <button class="btn sm" @click="showAddNode = true">新增节点</button>
      </div>
      <div class="panel-body">
        <EmptyState v-if="!nodes" kind="empty" text="读取中" />
        <p v-else-if="!nodes.length" class="note">还没有节点。组合就是账号绑到节点。</p>
        <table v-else class="data">
          <thead>
            <tr><th>节点</th><th>线路</th><th>状态</th><th>出口 IP</th><th>账号</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="node in nodes" :key="node.id">
              <td>{{ node.name }}</td>
              <td>{{ lineOf(node) }}</td>
              <td><span class="badge" :class="stateClass(node)">{{ nodeStateLabel(node) }}</span></td>
              <td>
                <span class="num">{{ node.exit?.address ?? '未填' }}</span>
                <div v-if="node.exit" class="muted">有效期至 {{ fmtTime(node.exit.valid_until) }}</div>
                <div v-if="exitPeers.get(node.id)" class="muted">
                  与 {{ exitPeers.get(node.id)!.join('、') }} 同出口，共用该平台 IP 预算
                </div>
              </td>
              <td>
                <span v-for="account in accountsOfNode(node)" :key="account.id" class="bound">
                  {{ account.alias }}
                  <button
                    class="link-x"
                    title="解绑"
                    @click="ask('解绑账号', `${account.alias} × ${node.name}`, '采集中会解绑失败。', () => deleteCombination(comboOf(node, account.id)!.id))"
                  >
                    ×
                  </button>
                </span>
                <button
                  v-if="bindCandidates(node).length"
                  class="btn sm"
                  @click="bindDraft = { node, accountId: 0 }"
                >
                  绑账号
                </button>
                <span v-else-if="!accountsOfNode(node).length" class="muted">没有可用账号</span>
              </td>
              <td class="ops">
                <button class="btn sm" @click="settingsNodeId = node.id">设置</button>
                <button
                  class="btn sm danger"
                  @click="ask('删除节点', node.name, '节点正在采集或还绑着账号时会删除失败。', () => deleteNode(node.id))"
                >
                  删除
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <div v-if="showAddAccount" class="modal-mask" @click.self="showAddAccount = false">
      <form class="modal" @submit.prevent="submitAccount">
        <div class="modal-head">新增账号</div>
        <div class="modal-body">
          <div class="field">
            <label>平台</label>
            <select v-model="newAccount.platform">
              <option value="steam">Steam</option>
              <option value="buff">BUFF</option>
              <option value="igxe">IGXE</option>
            </select>
          </div>
          <p class="note">目前只有 Steam 接入了采集，BUFF 和 IGXE 加了账号也不会跑。</p>
          <div class="field">
            <label>别名</label>
            <input v-model="newAccount.alias" placeholder="自己认得出就行" />
          </div>
          <div class="field">
            <label>Cookie</label>
            <textarea v-model="newAccount.session" rows="5" placeholder="浏览器 Cookie 串或导出的 Chrome JSON" />
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="showAddAccount = false">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">创建</button>
        </div>
      </form>
    </div>

    <div v-if="accountEdit" class="modal-mask" @click.self="accountEdit = null">
      <form class="modal" @submit.prevent="submitAccountEdit">
        <div class="modal-head">编辑账号 · {{ accountEdit.account.alias }}</div>
        <div class="modal-body">
          <div class="field">
            <label>别名</label>
            <input v-model="accountEdit.alias" />
          </div>
          <div class="field">
            <label>新 Cookie</label>
            <textarea v-model="accountEdit.session" rows="5" placeholder="不换就留空" />
          </div>
          <p class="note">换了 Cookie 后登录状态回到未验证，下一次采集成功会变成正常。</p>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="accountEdit = null">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">保存</button>
        </div>
      </form>
    </div>

    <div v-if="showAddNode" class="modal-mask" @click.self="showAddNode = false">
      <form class="modal" @submit.prevent="submitNode">
        <div class="modal-head">新增节点</div>
        <div class="modal-body">
          <div class="field">
            <label>名称</label>
            <input v-model="newNode.name" placeholder="local 或 hk-1" />
          </div>
          <div class="field">
            <label>线路</label>
            <select v-model="newNode.kind">
              <option value="direct">本机直连</option>
              <option value="proxy">代理</option>
            </select>
          </div>
          <div class="field">
            <label>地域</label>
            <select v-model="newNode.region">
              <option value="foreign">国外</option>
              <option value="hongkong">香港</option>
              <option value="domestic">国内</option>
            </select>
          </div>
          <p class="note">
            Steam 走国外站，只能用国外或香港节点。
            <span v-if="!nodeAllowsPlatform(newNode.region, 'steam')" class="warn">当前选的地域不能采 Steam。</span>
          </p>
          <template v-if="newNode.kind === 'proxy'">
            <div class="field">
              <label>出口模式</label>
              <select v-model="newNode.egress_mode">
                <option value="static">固定出口</option>
                <option value="sticky">会话保持</option>
              </select>
            </div>
            <div class="field">
              <label>代理地址</label>
              <input v-model="newNode.proxy_credential" placeholder="http://user:pass@host:port" />
            </div>
            <div v-if="newNode.egress_mode === 'sticky'" class="field">
              <label>会话到期</label>
              <input v-model="newNode.sticky_until" type="datetime-local" />
            </div>
          </template>
          <p class="note">创建后还要填出口 IP，节点才会变成可用。</p>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="showAddNode = false">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">创建</button>
        </div>
      </form>
    </div>

    <div v-if="bindDraft" class="modal-mask" @click.self="bindDraft = null">
      <form class="modal" @submit.prevent="submitBind">
        <div class="modal-head">给 {{ bindDraft.node.name }} 绑账号</div>
        <div class="modal-body">
          <p class="note">账号和节点绑在一起才能采。采集时一页请求独占这一对，抓完立刻释放。</p>
          <div class="field">
            <label>账号</label>
            <select v-model.number="bindDraft.accountId">
              <option :value="0">请选择</option>
              <option v-for="account in bindCandidates(bindDraft.node)" :key="account.id" :value="account.id">
                {{ account.alias }} · {{ platformLabel(account.platform) }}
              </option>
            </select>
          </div>
        </div>
        <div class="modal-foot">
          <button class="btn" type="button" @click="bindDraft = null">取消</button>
          <button class="btn primary" type="submit" :disabled="busy">绑定</button>
        </div>
      </form>
    </div>

    <NodeSettingsDialog
      v-if="settingsNode"
      :node="settingsNode"
      :busy="busy"
      @close="settingsNodeId = null"
      @exit="async (address, validUntil, done) => done(await runAction(() => recordNodeExit(settingsNode!.id, settingsNode!.egress_revision, address, validUntil)))"
      @rename="(name) => runAction(() => renameNode(settingsNode!.id, name))"
      @connection="async (payload, done) => done(await runAction(() => replaceNodeConnection(settingsNode!.id, {
        expected_egress_revision: settingsNode!.egress_revision,
        ...payload,
      })))"
    />

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.title"
      :impact="confirm.impact"
      :consequence="confirm.consequence"
      confirm-text="确认"
      @confirm="runAction(confirm!.run); confirm = null"
      @cancel="confirm = null"
    />
  </div>
</template>

<style scoped>
.pg { width: 100%; max-width: none; margin: 0; padding: 20px 28px 40px; box-sizing: border-box; }
.hero { margin-bottom: 16px; padding-bottom: 10px; border-bottom: 1px solid var(--line-strong); }
.hero h1 { margin: 0; }
.hero-sub { margin: 6px 0 0; color: var(--text-3); font-size: 12px; }
.panel { margin-bottom: 14px; }
.panel-head .spacer { flex: 1; }
.panel-body { padding: 0 0 4px; }
.panel-body.padded { padding: 12px; }
.fail { color: var(--danger); padding: 8px 12px; display: flex; gap: 8px; align-items: center; }
.note { margin: 8px 12px; color: var(--text-3); font-size: 12px; }
.note .warn { color: var(--warn); }
.gaps .gap-list { margin: 0 0 8px; padding: 0; list-style: none; display: flex; flex-direction: column; gap: 6px; }
.gaps li { display: flex; gap: 10px; align-items: center; font-size: 12px; }
.gaps li.optional { color: var(--text-3); }
.group { border-top: 1px solid var(--line); }
.group:first-child { border-top: none; }
.group-head { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; padding: 8px 12px; }
.ops { display: flex; gap: 6px; flex-wrap: wrap; }
.bound { margin-right: 8px; white-space: nowrap; }
.link-x {
  background: none; border: none; color: var(--text-3);
  cursor: pointer; font-size: 12px; padding: 0 2px;
}
.link-x:hover { color: var(--danger); }
table.data td { white-space: normal; }
</style>
