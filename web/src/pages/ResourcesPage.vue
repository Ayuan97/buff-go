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
  replaceAccountSession,
  type AccessNode,
  type Account,
  type Combination,
} from '../api'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import { fmtAgo, regionText, sideText } from '../utils/format'

const accounts = ref<Account[] | null>(null)
const nodes = ref<AccessNode[] | null>(null)
const combinations = ref<Combination[]>([])
const error = ref<string | null>(null)
const stale = ref(false)
const actionError = ref<string | null>(null)
const confirm = ref<{ title: string; impact: string; consequence: string; run: () => Promise<unknown> } | null>(null)

const showAddAccount = ref(false)
const showAddNode = ref(false)
const cookieEdit = ref<{ account: Account; session: string } | null>(null)
const exitEdit = ref<{ node: AccessNode; address: string; valid_until: string } | null>(null)

const newAccount = ref({ platform: 'steam', alias: '', session: '' })
const newNode = ref({
  name: '',
  kind: 'direct' as 'direct' | 'proxy',
  region: 'foreign' as 'domestic' | 'foreign' | 'hongkong',
  egress_mode: 'static' as 'static' | 'sticky',
  proxy_credential: '',
})
const pairDraft = ref({ account_id: 0, node_id: 0 })
const allocAppid = ref('730')

onMounted(() => { void reload() })

async function reload() {
  try {
    const [a, n, c] = await Promise.all([getAccounts(), getNodes(), getCombinations()])
    accounts.value = a
    nodes.value = n
    combinations.value = c
    error.value = null
    stale.value = false
  } catch (e) {
    if (accounts.value && nodes.value) stale.value = true
    else error.value = e instanceof Error ? e.message : '加载失败'
  }
}

async function runAction(fn: () => Promise<unknown>) {
  actionError.value = null
  try {
    await fn()
    await reload()
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : '操作失败'
  }
}

const accountName = computed(() => {
  const map = new Map<number, string>()
  for (const a of accounts.value ?? []) map.set(a.id, a.alias)
  return map
})
const nodeName = computed(() => {
  const map = new Map<number, string>()
  for (const n of nodes.value ?? []) map.set(n.id, n.name)
  return map
})

function ask(title: string, impact: string, consequence: string, run: () => Promise<unknown>) {
  confirm.value = { title, impact, consequence, run }
}
</script>

<template>
  <div class="pg">
    <header class="hero">
      <div>
        <h1>资源</h1>
        <p class="hero-sub">账号会话明文入库 · 占用中禁止改删 · Steam 节点须国外或香港</p>
      </div>
    </header>
    <div v-if="stale" class="stale-banner">数据可能过期，已保留上次成功结果</div>
    <div v-if="actionError" class="panel fail">
      {{ actionError }}
      <button class="btn sm" @click="actionError = null">知道了</button>
    </div>
    <div v-if="error" class="panel"><EmptyState kind="empty" :text="error" /></div>

    <section class="panel">
      <div class="panel-head">
        账号
        <button class="btn sm" @click="showAddAccount = true">新增</button>
      </div>
      <div class="panel-body">
        <EmptyState v-if="!accounts" kind="empty" text="加载中" />
        <table v-else class="data">
          <thead>
            <tr><th>别名</th><th>平台</th><th>会话</th><th>检查</th><th /></tr>
          </thead>
          <tbody>
            <tr v-for="a in accounts" :key="a.id">
              <td>{{ a.alias }} <span class="muted num">#{{ a.id }}</span></td>
              <td class="num">{{ a.platform }}</td>
              <td>{{ a.session_state }}</td>
              <td class="num">{{ fmtAgo(a.last_checked_at ?? null) }}</td>
              <td class="ops">
                <button class="btn sm" @click="cookieEdit = { account: a, session: '' }">换会话</button>
                <button class="btn sm danger" @click="ask('删除账号', a.alias, '占用中会失败。', () => deleteAccount(a.id))">删</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">
        节点
        <button class="btn sm" @click="showAddNode = true">新增</button>
      </div>
      <div class="panel-body">
        <EmptyState v-if="!nodes" kind="empty" text="加载中" />
        <table v-else class="data">
          <thead>
            <tr><th>名称</th><th>线路</th><th>状态</th><th>游戏</th><th>方向</th><th>出口</th><th /></tr>
          </thead>
          <tbody>
            <tr v-for="n in nodes" :key="n.id">
              <td>{{ n.name }} <span class="muted num">#{{ n.id }}</span></td>
              <td>{{ n.kind }} · {{ regionText(n.region) }} · {{ n.egress_mode }}</td>
              <td>{{ n.state }}</td>
              <td class="num">{{ n.appid ?? '未划' }}</td>
              <td>{{ n.sides.map((s) => `${s.platform}/${sideText(s.side)}`).join(' ') || '—' }}</td>
              <td class="num">{{ n.exit?.address ?? '—' }}</td>
              <td class="ops">
                <button class="btn sm" @click="exitEdit = { node: n, address: '', valid_until: '' }">出口</button>
                <button class="btn sm" @click="runAction(() => assignNodeGame(n.id, n.assignment_revision, Number(allocAppid)))">划入 {{ allocAppid }}</button>
                <button class="btn sm" @click="runAction(() => assignNodeSide(n.id, n.assignment_revision, 'steam', 'ask'))">Steam 出售</button>
                <button class="btn sm" @click="runAction(() => assignNodeSide(n.id, n.assignment_revision, 'steam', 'bid'))">Steam 求购</button>
                <button class="btn sm danger" @click="ask('删除节点', n.name, '占用中会失败。', () => deleteNode(n.id))">删</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div class="row">
          划入 appid
          <input v-model="allocAppid" class="inp" />
        </div>
      </div>
    </section>

    <section class="panel">
      <div class="panel-head">账号×节点</div>
      <div class="panel-body">
        <table class="data">
          <thead>
            <tr><th>组合</th><th>平台</th><th>账号</th><th>节点</th><th /></tr>
          </thead>
          <tbody>
            <tr v-for="c in combinations" :key="c.id">
              <td class="num">#{{ c.id }}</td>
              <td>{{ c.platform }}</td>
              <td>{{ accountName.get(c.account_id) ?? c.account_id }}</td>
              <td>{{ nodeName.get(c.node_id) ?? c.node_id }}</td>
              <td>
                <button class="btn sm danger" @click="ask('删除组合', `#${c.id}`, '占用中会失败。', () => deleteCombination(c.id))">删</button>
              </td>
            </tr>
          </tbody>
        </table>
        <form class="row" @submit.prevent="runAction(() => createCombination(pairDraft.account_id, pairDraft.node_id))">
          <select v-model.number="pairDraft.account_id">
            <option :value="0">账号</option>
            <option v-for="a in accounts ?? []" :key="a.id" :value="a.id">{{ a.alias }}</option>
          </select>
          <select v-model.number="pairDraft.node_id">
            <option :value="0">节点</option>
            <option v-for="n in nodes ?? []" :key="n.id" :value="n.id">{{ n.name }}</option>
          </select>
          <button class="btn sm" type="submit">绑定</button>
        </form>
      </div>
    </section>

    <div v-if="showAddAccount" class="modal">
      <form class="sheet" @submit.prevent="runAction(async () => { await createAccount(newAccount.platform, newAccount.alias, newAccount.session); showAddAccount = false })">
        <h2>新增账号</h2>
        <input v-model="newAccount.platform" class="inp" placeholder="platform" />
        <input v-model="newAccount.alias" class="inp" placeholder="别名" />
        <textarea v-model="newAccount.session" class="inp area" placeholder="Cookie / Chrome JSON" />
        <div class="ops">
          <button class="btn" type="submit">创建</button>
          <button class="btn" type="button" @click="showAddAccount = false">取消</button>
        </div>
      </form>
    </div>
    <div v-if="showAddNode" class="modal">
      <form class="sheet" @submit.prevent="runAction(async () => { await createNode({ name: newNode.name, kind: newNode.kind, region: newNode.region, egress_mode: newNode.egress_mode, proxy_credential: newNode.proxy_credential || undefined }); showAddNode = false })">
        <h2>新增节点</h2>
        <input v-model="newNode.name" class="inp" placeholder="名称" />
        <select v-model="newNode.kind"><option value="direct">直连</option><option value="proxy">代理</option></select>
        <select v-model="newNode.region"><option value="foreign">国外</option><option value="hongkong">香港</option><option value="domestic">国内</option></select>
        <select v-model="newNode.egress_mode"><option value="static">固定</option><option value="sticky">粘性</option></select>
        <input v-if="newNode.kind === 'proxy'" v-model="newNode.proxy_credential" class="inp" placeholder="代理 URL" />
        <div class="ops">
          <button class="btn" type="submit">创建</button>
          <button class="btn" type="button" @click="showAddNode = false">取消</button>
        </div>
      </form>
    </div>
    <div v-if="cookieEdit" class="modal">
      <form class="sheet" @submit.prevent="runAction(async () => { await replaceAccountSession(cookieEdit!.account.id, cookieEdit!.account.session_revision, cookieEdit!.session); cookieEdit = null })">
        <h2>更换会话 · {{ cookieEdit.account.alias }}</h2>
        <textarea v-model="cookieEdit.session" class="inp area" placeholder="新 Cookie" />
        <div class="ops">
          <button class="btn" type="submit">保存</button>
          <button class="btn" type="button" @click="cookieEdit = null">取消</button>
        </div>
      </form>
    </div>
    <div v-if="exitEdit" class="modal">
      <form class="sheet" @submit.prevent="runAction(async () => { await recordNodeExit(exitEdit!.node.id, exitEdit!.node.egress_revision, exitEdit!.address, new Date(exitEdit!.valid_until).toISOString()); exitEdit = null })">
        <h2>手工出口 · {{ exitEdit.node.name }}</h2>
        <input v-model="exitEdit.address" class="inp" placeholder="出口 IP" />
        <input v-model="exitEdit.valid_until" class="inp" placeholder="有效期 2030-01-01T00:00:00Z" />
        <div class="ops">
          <button class="btn" type="submit">标记可用</button>
          <button class="btn" type="button" @click="exitEdit = null">取消</button>
        </div>
      </form>
    </div>
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
.panel { border: 1px solid var(--line-strong); margin-bottom: 14px; background: var(--surface); }
.panel-head { display: flex; justify-content: space-between; align-items: center; padding: 10px 12px; border-bottom: 1px solid var(--line); font-weight: 700; }
.panel-body { padding: 10px 12px; overflow-x: auto; }
.fail { color: var(--danger); padding: 8px 12px; }
.ops { display: flex; gap: 6px; flex-wrap: wrap; }
.row { display: flex; gap: 8px; align-items: center; margin-top: 10px; }
.inp, select, textarea {
  height: 28px; background: var(--bg); color: var(--text); border: 1px solid var(--line-strong);
  font-family: var(--mono); font-size: 12px; padding: 0 8px;
}
.area { height: 90px; width: 100%; padding: 8px; }
.modal { position: fixed; inset: 0; background: #000a; display: grid; place-items: center; z-index: 20; }
.sheet { width: min(480px, 92vw); background: var(--bg-elev); border: 1px solid var(--line-strong); padding: 16px; display: flex; flex-direction: column; gap: 8px; }
.danger { color: var(--danger); }
</style>
