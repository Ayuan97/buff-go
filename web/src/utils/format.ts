// 展示格式化：金额分整数 → 元；时间 → 本地相对/绝对文本
export function fenToYuan(fen: number | null | undefined): string {
  if (fen == null) return '—'
  return '¥' + (fen / 100).toFixed(2)
}

export function dropPctText(bp: number | null | undefined): string {
  if (bp == null || bp <= 0) return ''
  const pct = bp / 100
  return `-${pct >= 10 ? pct.toFixed(0) : pct.toFixed(1)}%`
}

export function dropWindowLabel(window: string): string {
  if (window === '7d') return '7 天'
  if (window === '30d') return '30 天'
  return '24 小时'
}

export function tickDelta(prevCents: number | undefined, priceCents: number): number | null {
  if (prevCents == null) return null
  return priceCents - prevCents
}

export function tickDeltaText(prevCents: number | undefined, priceCents: number): string {
  const delta = tickDelta(prevCents, priceCents)
  if (delta == null) return '首次入价'
  const sign = delta > 0 ? '+' : ''
  return `${sign}${fenToYuan(delta)}`
}

export function fmtTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export function fmtAgo(iso: string | null): string {
  if (!iso) return '—'
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 0) return fmtTime(iso)
  const m = Math.floor(diff / 60_000)
  if (m < 1) return '刚刚'
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

// 本地节流每隔几秒就把目标短暂标成 cooldown，那是正常的采集节拍，不是被平台挡住。
// 后端两种情况共用一个原因码，只能按「还要等多久」区分：秒级的是节拍，
// 平台真限流是分钟到小时级。阈值取得比节流间隔宽、比临时失败重试间隔窄。
const pacingBeatWindowMs = 15_000

export function isPacingBeat(reason: string | null | undefined, recheckAt: string | null | undefined): boolean {
  if (reason !== 'cooldown' || !recheckAt) return false
  const wait = new Date(recheckAt).getTime() - Date.now()
  return wait > 0 && wait <= pacingBeatWindowMs
}

// 目标的下次巡检时刻：读的人只关心还要等多久，绝对时间留给 title
export function fmtUntil(iso: string | null): string {
  if (!iso) return ''
  const diff = new Date(iso).getTime() - Date.now()
  if (diff <= 0) return '即将重试'
  const seconds = Math.round(diff / 1000)
  if (seconds < 60) return `约 ${seconds} 秒后恢复`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `约 ${minutes} 分钟后恢复`
  return `约 ${Math.round(minutes / 60)} 小时后恢复`
}

export const sideText = (side: 'bid' | 'ask' | null): string =>
  side === 'bid' ? '求购' : side === 'ask' ? '出售' : '—'

export const taskTypeText = (t: 'summary' | 'detail'): string =>
  t === 'summary' ? '摘要' : '详情'

export const regionText = (r: 'domestic' | 'foreign' | 'hongkong'): string =>
  r === 'domestic' ? '国内' : r === 'foreign' ? '国外' : '香港'

export const sessionStateText = (s: 'unverified' | 'valid' | 'invalid'): string =>
  s === 'valid' ? '正常' : s === 'invalid' ? '已失效' : '未验证'

// 节点必须记录已验证的公网出口 IP 才能参与采集，直连节点同样如此，
// 所以 validating 对使用者的意义就是「还缺出口 IP」。
export const nodeStateText = (s: 'validating' | 'available' | 'unavailable'): string =>
  s === 'available' ? '可用' : s === 'unavailable' ? '不可用' : '待填出口 IP'

export const nodeKindText = (k: 'direct' | 'proxy'): string =>
  k === 'direct' ? '本机直连' : '代理'

export const egressModeText = (m: 'static' | 'sticky'): string =>
  m === 'static' ? '固定出口' : '会话保持'

export const RUST_APPID = 252490

export const KNOWN_GAMES = [
  { appid: 730, short: 'CS2', full: 'Counter-Strike 2' },
  { appid: 570, short: 'Dota2', full: 'Dota 2' },
  { appid: 440, short: 'TF2', full: 'Team Fortress 2' },
  { appid: RUST_APPID, short: 'Rust', full: 'Rust' },
] as const

const GAMES: Record<number, { short: string; full: string }> = Object.fromEntries(
  KNOWN_GAMES.map((game) => [game.appid, { short: game.short, full: game.full }]),
)

export const gameName = (appid: number): string => GAMES[appid]?.short ?? `游戏 ${appid}`

export const gameFullName = (appid: number): string => GAMES[appid]?.full ?? `游戏 ${appid}`

const PLATFORM_LABELS: Record<string, string> = { steam: 'Steam', buff: 'BUFF', igxe: 'IGXE' }

export const platformLabel = (platform: string): string =>
  PLATFORM_LABELS[platform.toLowerCase()] ?? platform

// Steam 商品页用 appid + market_hash_name 就能定位。BUFF/IGXE 的商品页要平台自己的 goods_id，
// 目录里没有，不能拿 Steam 名字去猜。
export function platformItemURL(platform: string, appid: number, name: string): string | null {
  const hash = name.trim()
  if (!hash || appid < 1) return null
  if (platform.toLowerCase() === 'steam') {
    return `https://steamcommunity.com/market/listings/${appid}/${encodeURIComponent(hash)}`
  }
  return null
}

// 行情单格的原因码和目标状态原因码是两套词表：目标那套有库约束枚举，
// 这套是抓取器按商品写的自由 slug，只有下面三个会真实出现。
export const quoteStatusText = (status: 'present' | 'empty' | 'unavailable' | 'failed'): string =>
  ({ present: '有价', empty: '空行情', unavailable: '不可用', failed: '采集失败' })[status] ?? status

export const quoteReasonText = (reason: string | undefined): string => {
  if (!reason) return ''
  const map: Record<string, string> = {
    price_unverified: '价格没解析出来',
    item_missing: '平台上查不到这个商品',
    currency_rejected: '币种不是人民币，或挂单数据自相矛盾',
  }
  return map[reason] ?? reason
}

export const apiErrorText = (code: string): string => {
  const map: Record<string, string> = {
    session_required: '请粘贴 Cookie',
    session_too_large: 'Cookie 内容过大',
    account_not_found: '账号不存在，刷新后再试',
    account_occupied: '账号采集中，先关掉对应方向再改',
    account_in_use: '账号还绑着组合，先解绑',
    account_revision_conflict: '账号已被改过，刷新后再试',
    account_conflict: '别名已存在',
    invalid_account: '账号内容不合法',
    node_not_found: '节点不存在，刷新后再试',
    node_occupied: '节点采集中，先关掉对应方向再改',
    node_in_use: '节点还绑着组合，先解绑',
    node_revision_conflict: '节点已被改过，刷新后再试',
    node_name_conflict: '节点名已存在',
    invalid_node: '节点内容不合法',
    invalid_exit_address: '出口必须是公网 IP，不能填 127.0.0.1 或内网地址',
    platform_region_mismatch: '节点地域和平台不匹配：Steam 要国外或香港节点，BUFF、IGXE 要国内或香港节点',
    combination_occupied: '组合采集中，先关掉对应方向再改',
    combination_incompatible: '这个账号和节点不能绑：账号或节点不存在',
    combination_conflict: '这对账号和节点已经绑过了',
    combination_not_found: '组合不存在，刷新后再试',
    resource_in_use: '还有组合在用，先解绑',
    invalid_combination: '组合内容不合法',
    collection_not_found: '采集目标不存在，刷新后再试',
    target_not_found: '采集目标不存在，刷新后再试',
    collection_conflict: '该游戏方向已经加过了',
    target_not_stopped: '先关掉这个方向，等它停下来再移除',
    collection_in_use: '这个方向跑过采集，只能关掉，不能移除（移除会丢采集历史）',
    invalid_collection: '采集目标内容不合法',
    collection_storage_unavailable: '数据库不可用，稍后再试',
    market_storage_unavailable: '数据库不可用，稍后再试',
    collection_error: '采集数据异常，检查服务日志',
    credential_required: '请填写代理商凭证',
    credential_too_large: '凭证内容过大',
    provider_not_found: '代理商不存在，刷新后再试',
    provider_conflict: '代理商名称已存在',
    provider_revision_conflict: '配置已被改过，刷新后再试',
    invalid_provider: '代理商内容不合法',
    resource_storage_unavailable: '数据库不可用，稍后再试',
    resource_integrity: '资源数据异常，检查服务日志',
    resource_error: '资源操作失败，检查服务日志',
    invalid_product_id: '商品不存在或不合法',
    invalid_side: '方向只能是求购或出售',
    invalid_price_ticks: '变价记录查询不合法',
    invalid_json: '请求内容不合法',
    body_not_allowed: '请求内容不合法',
    csrf_rejected: '页面过期，刷新后再试',
    origin_rejected: '请求被安全策略拒绝',
    invalid_host: '请求被安全策略拒绝',
    method_not_allowed: '该操作不被支持',
    not_found: '接口不存在',
    internal_error: '服务内部错误，检查服务日志',
  }
  return map[code] ?? code
}

export const targetReasonText = (reason: string | null): string => {
  if (!reason) return ''
  const map: Record<string, string> = {
    no_combination: '没有可用组合',
    egress_unavailable: '出口不可用或地域不符',
    session_invalid: '会话失效，请更换 Cookie',
    cooldown: '限流冷却中',
    missing_rate_policy: '缺少限频策略',
    next_cycle: '等待下一轮',
    scheduler_opportunity: '等待调度空档',
    transient_failure: '暂时失败，将重试',
    invalid_config: '配置无效',
    interface_unverified: '接口未验证',
    scheduler_failure: '调度失败',
    state_integrity: '状态不一致',
  }
  return map[reason] ?? reason
}
