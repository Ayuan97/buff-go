export type Side = 'bid' | 'ask'
export type DesiredState = 'enabled' | 'disabled'
export type ActualState =
  | 'starting'
  | 'waiting'
  | 'running'
  | 'blocked'
  | 'stopping'
  | 'stopped'
  | 'error'
export type QuoteStatus = 'present' | 'empty' | 'unavailable' | 'failed'
export type Region = 'domestic' | 'foreign' | 'hongkong'
export type NodeKind = 'direct' | 'proxy'
export type EgressMode = 'static' | 'sticky'
export type NodeState = 'validating' | 'available' | 'unavailable'
export type SessionState = 'unverified' | 'valid' | 'invalid'

export interface Account {
  id: number
  platform: string
  alias: string
  session_state: SessionState
  session_revision: number
  last_checked_at?: string
}

export interface NodeExit {
  address: string
  verified_at: string
  valid_until: string
}

export interface AccessNode {
  id: number
  name: string
  kind: NodeKind
  region: Region
  egress_mode: EgressMode
  state: NodeState
  egress_revision: number
  has_proxy_credential: boolean
  sticky_session_valid_until?: string
  exit?: NodeExit
}

export interface Watermark {
  region: Region
  min_usable: number
  usable: number
  revision: number
}

export interface Provider {
  id: number
  name: string
  enabled: boolean
  priority: number
  regions: Region[]
  has_credential: boolean
  revision: number
}

export interface Combination {
  id: number
  platform: string
  account_id: number
  node_id: number
}

export interface Target {
  id: number
  revision: number
  platform: string
  appid: number
  side: Side
  desired: DesiredState
  actual: ActualState
  switch_version: number
  reason?: string
  block_or_err?: string
  block_reason?: string
  recovery?: string
  sort_column: SortColumn
  sort_dir: SortDirection
  price_min_cents?: number
  price_max_cents?: number
  steam_cats: string[]
  item_classes: string[]
  recheck_at?: string
  retry_after_sec?: number
  changed_at: string
}

export interface WorkerItem {
  product_id: number
  name: string
  status: string
  present_cents?: number
}

export interface WorkerClaim {
  target_id: number
  task_id?: number
  appid: number
  side: Side
  platform: string
  kind?: 'ask_page' | 'bid_batch'
  endpoint?: string
  claimed_at?: string
  active: boolean
  items: WorkerItem[]
}

export type WorkerWaitScope = 'account_exit_endpoint' | 'node_platform' | 'combination'
export type WorkerWaitReason = 'rate_limit' | 'deferred' | 'network' | 'timeout' | 'transient'

export interface WorkerWait {
  scope: WorkerWaitScope
  reason: WorkerWaitReason
  block_or_err?: string
  block_reason?: string
  retry_at: string
  retry_after_sec?: number
  platform: string
  endpoint?: string
  side?: Side
  account_id?: number
  exit_address?: string
  node_id?: number
  combination_id?: number
}

export interface WorkerPage {
  target_id: number
  appid: number
  side: Side
  platform: string
  committed_at: string
  items: WorkerItem[]
}

export interface Worker {
  combination_id: number
  account_id: number
  account_alias: string
  platform: string
  node_id: number
  node_name: string
  exit_address?: string
  region: string
  session_state: string
  idle: boolean
  claim?: WorkerClaim
  active_waits: WorkerWait[]
  last_page?: WorkerPage
}

export interface Quote {
  product_id: number
  appid: number
  name: string
  // 平台图片路径片段，拼上 CDN 前缀才是图片地址；老商品在被重新采到之前没有
  icon_path?: string
  item_type?: string
  name_color?: string
  platform: string
  side: Side
  status: QuoteStatus
  reason_code?: string
  collected_at: string
  source_time?: string
  present_cents?: number
  present_order_count?: number
  present_collected_at?: string
  drop_cents?: number
  high_cents?: number
  drop_pct_bp?: number
  drop_count?: number
  last_drop_at?: string
}

export type DropWindow = '24h' | '7d' | '30d'
export type QuoteSort = 'price_desc' | 'price_asc' | 'listings_desc' | 'name' | 'drop_desc' | 'drop_pct_desc'

// 采集顺序决定平台先返回哪一头；改了会推进开关版本、清空该方向队列并重置补货游标
export type SortColumn = 'price' | 'quantity' | 'name'
export type SortDirection = 'asc' | 'desc'

export interface QuoteFilter {
  appid?: number
  product_id?: number
  platform?: string
  side?: Side
  keyword?: string
  item_type?: string
  item_types?: string[]
  steam_cats?: string[]
  min_cents?: number
  max_cents?: number
  dropped?: boolean
  drop_window?: DropWindow
  min_drop_cents?: number
  sort?: QuoteSort
  limit?: number
  offset?: number
}

export interface QuotePage {
  total: number
  quotes: Quote[]
}

export interface QuoteFacets {
  appids: number[]
  item_types: string[]
}

export interface PriceTick {
  tick_id: number
  product_id: number
  appid: number
  name: string
  platform: string
  side: Side
  prev_cents?: number
  price_cents: number
  collected_at: string
}

export interface PriceTickFilter {
  product_id?: number
  platform?: string
  side?: Side
  limit?: number
}

export interface PriceTickPage {
  ticks: PriceTick[]
}

export interface SteamCategory {
  slug: string
  label: string
  name: string
}

export interface SteamItemClass {
  slug: string
  label: string
  name: string
  category: string
}

export interface SteamFacetsVocab {
  categories: SteamCategory[]
  item_classes: SteamItemClass[]
}

export class APIError extends Error {
  constructor(public code: string, message?: string) {
    super(message ?? code)
    this.name = 'APIError'
  }
}
