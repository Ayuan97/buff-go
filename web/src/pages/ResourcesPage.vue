<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  assignNodeGame,
  assignNodeSide,
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
  type Side,
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
  gameName,
  nodeKindText,
  nodeStateText,
  platformLabel,
  regionText,
  sessionStateText,
  sideText,
} from '../utils/format'
import { nodeAllowsPlatform } from '../utils/platformRegion'

// 只有 Steam 接入了采集，所以只为 Steam 主动展示两个方向的配置槽位；
// 库里已存在的其它平台归属仍会显示出来。
const LIVE_PLATFORMS = ['steam']
const SIDES: Side[] = ['ask', 'bid']

const accounts = ref<Account[] | null>(null)
const nodes = ref<AccessNode[] | null>(null)
const combinations = ref<Combination[]>([])
const loadError = ref<string | null>(null)
const stale = ref(false)
const actionError = ref<string | null>(null)
const busy = ref(false)
const showAllGaps = ref(false)

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

onMounted(() => { void reload() })

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
  } catch (e) {
    if (accounts.value && nodes.value) stale.value = true
    else loadError.value = e instanceof Error ? apiErrorText(e.message) : '加载失败'
  }
}

async function runAction(fn: () => Promise<unknown>) {
  if (busy.value) return
  actionError.value = null
  busy.value = true
  try {
    await fn()
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? apiErrorText(e.message) : '操作失败'
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

interface SideGroup {
  key: string
  platform: string
  side: Side
  nodes: AccessNode[]
}

interface GameBlock {
  appid: number
  nodes: AccessNode[]
  groups: SideGroup[]
  undirected: AccessNode[]
}

const gameBlocks = computed<GameBlock[]>(() => {
  const byGame = new Map<number, AccessNode[]>()
  for (const node of nodes.value ?? []) {
    if (!node.appid) continue
    const list = byGame.get(node.appid) ?? []
    list.push(node)
    byGame.set(node.appid, list)
  }
  return [...byGame.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([appid, list]) => {
      const slots = new Map<string, SideGroup>()
      for (const platform of LIVE_PLATFORMS) {
        for (const side of SIDES) {
          slots.set(`${platform}/${side}`, { key: `${platform}/${side}`, platform, side, nodes: [] })
        }
      }
      for (const node of list) {
        for (const assignment of node.sides) {
          const key = `${assignment.platform}/${assignment.side}`
          const group = slots.get(key) ?? {
            key,
            platform: assignment.platform,
            side: assignment.side,
            nodes: [],
          }
          group.nodes.push(node)
          slots.set(key, group)
        }
      }
      return {
        appid,
        nodes: list,
        groups: [...slots.values()],
        undirected: list.filter((node) => node.sides.length === 0),
      }
    })
})

const unassignedNodes = computed(() => (nodes.value ?? []).filter((node) => !node.appid))

function accountsOfNode(node: AccessNode): Account[] {
  return (combosByNode.value.get(node.id) ?? [])
    .map((combo) => accountById.value.get(combo.account_id))
    .filter((account): account is Account => account !== undefined)
}

function comboOf(node: AccessNode, accountId: number): Combination | undefined {
  return (combosByNode.value.get(node.id) ?? []).find((combo) => combo.account_id === accountId)
}

// 组合要求账号平台与节点已划方向的平台一致，所以候选只列这些平台的未绑账号。
function bindCandidates(node: AccessNode): Account[] {
  const platforms = new Set(node.sides.map((s) => s.platform))
  const bound = new Set((combosByNode.value.get(node.id) ?? []).map((c) => c.account_id))
  return (accounts.value ?? []).filter((a) => platforms.has(a.platform) && !bound.has(a.id))
}

function lineOf(node: AccessNode): string {
  const parts = [nodeKindText(node.kind), regionText(node.region)]
  if (node.kind === 'proxy') parts.push(egressModeText(node.egress_mode))
  return parts.join(' · ')
}

function stateClass(node: AccessNode): string {
  if (node.state === 'available') return 'ok'
  return node.state === 'unavailable' ? 'danger' : 'warn'
}

function sessionClass(account: Account): string {
  if (account.session_state === 'valid') return 'ok'
  return account.session_state === 'invalid' ? 'danger' : 'info'
}

function groupSummary(group: SideGroup): string {
  if (!group.nodes.length) return '还没有节点'
  const withAccount = group.nodes.filter((node) => accountsOfNode(node).length > 0).length
  const usable = group.nodes.filter((node) => node.state === 'available').length
  return `${group.nodes.length} 个节点 · ${usable} 个出口已填 · ${withAccount} 个已绑账号`
}

interface Gap {
  text: string
  nodeId?: number
  optional?: boolean
}

const gaps = computed<Gap[]>(() => {
  const list: Gap[] = []
  const accountList = accounts.value ?? []
  const nodeList = nodes.value ?? []
  if (!accountList.length) list.push({ text: '还没有账号，先加一个 Steam 账号并粘贴 Cookie' })
  for (const account of accountList) {
    if (account.session_state === 'invalid') {
      list.push({ text: `账号 ${account.alias} 的登录已失效，换一份 Cookie` })
    }
  }
  if (!nodeList.length) list.push({ text: '还没有节点，加一个本机直连或代理节点' })
  for (const node of nodeList) {
    if (node.state !== 'available') {
      list.push({ text: `节点 ${node.name} 还没填出口 IP，不会被采集使用`, nodeId: node.id })
    }
  }
  for (const node of nodeList) {
    if (!node.appid) list.push({ text: `节点 ${node.name} 还没划给游戏`, nodeId: node.id })
  }
  for (const node of nodeList) {
    if (node.appid && !node.sides.length) {
      list.push({ text: `节点 ${node.name} 在 ${gameName(node.appid)} 下还没指定采出售还是求购`, nodeId: node.id })
    }
  }
  for (const node of nodeList) {
    if (!node.appid || !node.sides.length || accountsOfNode(node).length) continue
    const first = node.sides[0]
    list.push({
      text: `${gameName(node.appid)} · ${platformLabel(first.platform)} ${sideText(first.side)} 的节点 ${node.name} 还没绑账号`,
      nodeId: node.id,
    })
  }
  for (const block of gameBlocks.value) {
    for (const group of block.groups) {
      if (group.nodes.length || !LIVE_PLATFORMS.includes(group.platform)) continue
      list.push({
        text: `${gameName(block.appid)} · ${platformLabel(group.platform)} ${sideText(group.side)} 还没有节点，出售和求购必须用两个不同节点`,
        optional: true,
      })
    }
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
  if (!draft) return
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
  await runAction(async () => {
    if (alias !== draft.account.alias) await renameAccount(draft.account.id, alias)
    if (session) await replaceAccountSession(draft.account.id, draft.account.session_revision, session)
    accountEdit.value = null
  })
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
      <p class="hero-sub">采集要跑起来，需要一个账号、一个填了出口 IP 的节点、把节点划给游戏和方向、再把账号绑到节点上。</p>
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

    <section v-for="block in gameBlocks" :key="block.appid" class="panel">
      <div class="panel-head">
        {{ gameName(block.appid) }} · {{ block.appid }}
        <span class="spacer" />
        <span class="muted">{{ block.nodes.length }} 个节点</span>
      </div>
      <div class="panel-body">
        <div v-for="group in block.groups" :key="group.key" class="group">
          <div class="group-head">
            <span class="strong">{{ platformLabel(group.platform) }} · {{ sideText(group.side) }}</span>
            <span class="muted">{{ groupSummary(group) }}</span>
          </div>
          <table v-if="group.nodes.length" class="data">
            <thead>
              <tr><th>节点</th><th>线路</th><th>状态</th><th>出口 IP</th><th>账号</th><th>操作</th></tr>
            </thead>
            <tbody>
              <tr v-for="node in group.nodes" :key="node.id">
                <td>{{ node.name }}</td>
                <td>{{ lineOf(node) }}</td>
                <td><span class="badge" :class="stateClass(node)">{{ nodeStateText(node.state) }}</span></td>
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
                      @click="ask('解绑账号', `${account.alias} × ${node.name}`, '采集中会解绑失败。解绑后这个方向就没有可用组合。', () => deleteCombination(comboOf(node, account.id)!.id))"
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
                  <span v-else-if="!accountsOfNode(node).length" class="muted">没有同平台账号</span>
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
          <p v-else class="note">
            这个方向还没有节点。
            <template v-if="LIVE_PLATFORMS.includes(group.platform)">
              同一个节点在同一平台只能采一个方向，所以出售和求购要各配一个节点。
            </template>
          </p>
        </div>

        <div v-if="block.undirected.length" class="group">
          <div class="group-head">
            <span class="strong">还没指定方向</span>
            <span class="muted">{{ block.undirected.length }} 个节点划进了这个游戏但没说采什么</span>
          </div>
          <table class="data">
            <thead>
              <tr><th>节点</th><th>线路</th><th>状态</th><th>出口 IP</th><th>操作</th></tr>
            </thead>
            <tbody>
              <tr v-for="node in block.undirected" :key="node.id">
                <td>{{ node.name }}</td>
                <td>{{ lineOf(node) }}</td>
                <td><span class="badge" :class="stateClass(node)">{{ nodeStateText(node.state) }}</span></td>
                <td class="num">{{ node.exit?.address ?? '未填' }}</td>
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
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">
        未划游戏的节点
        <span class="spacer" />
        <button class="btn sm" @click="showAddNode = true">新增节点</button>
      </div>
      <div class="panel-body">
        <EmptyState v-if="!nodes" kind="empty" text="读取中" />
        <p v-else-if="!unassignedNodes.length" class="note">所有节点都划了游戏。新加的节点会先落在这里。</p>
        <table v-else class="data">
          <thead>
            <tr><th>节点</th><th>线路</th><th>状态</th><th>出口 IP</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="node in unassignedNodes" :key="node.id">
              <td>{{ node.name }}</td>
              <td>{{ lineOf(node) }}</td>
              <td><span class="badge" :class="stateClass(node)">{{ nodeStateText(node.state) }}</span></td>
              <td class="num">{{ node.exit?.address ?? '未填' }}</td>
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
      :platforms="[...new Set([...LIVE_PLATFORMS, ...settingsNode.sides.map((s) => s.platform)])]"
      :has-bound-accounts="accountsOfNode(settingsNode).length > 0"
      :busy="busy"
      @close="settingsNodeId = null"
      @exit="(address, validUntil) => runAction(() => recordNodeExit(settingsNode!.id, settingsNode!.egress_revision, address, validUntil))"
      @game="(appid) => runAction(() => assignNodeGame(settingsNode!.id, settingsNode!.assignment_revision, appid || null))"
      @side="(platform, side) => runAction(() => assignNodeSide(settingsNode!.id, settingsNode!.assignment_revision, platform, side))"
      @rename="(name) => runAction(() => renameNode(settingsNode!.id, name))"
      @connection="(payload) => runAction(() => replaceNodeConnection(settingsNode!.id, {
        expected_egress_revision: settingsNode!.egress_revision,
        ...payload,
      }))"
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
.pg { max-width: 1100px; margin: 0 auto; padding: 20px 24px 48px; }
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
