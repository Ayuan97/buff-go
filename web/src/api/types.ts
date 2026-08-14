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
export type RunState = 'pending' | 'running' | 'succeeded' | 'failed' | 'stopped'
export type Completeness = '' | 'complete' | 'partial'
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

export interface NodeSide {
  platform: string
  side: Side
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
  assignment_revision: number
  appid?: number
  sides: NodeSide[]
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
  recovery?: string
  sort_column: SortColumn
  sort_dir: SortDirection
  recheck_at?: string
  changed_at: string
}

export interface Run {
  id: number
  target_id?: number
  task_type: 'summary' | 'detail'
  platform: string
  appid: number
  side?: Side
  state: RunState
  completeness?: Completeness
  reason?: string
  cursor?: string
  last_page_sequence: number
  created_at: string
  started_at?: string
  finished_at?: string
}

export interface CollectionPage {
  page_sequence: number
  cursor_before?: string
  cursor_after?: string
  collected_at: string
  committed_at: string
  // 保留下来的原始响应字节数，0 表示副本已被淘汰或这个方向不产出单页响应
  payload_bytes: number
  // 归属能力上线之前提交的页没有这三项
  account_id?: number
  account_alias?: string
  exit_address?: string
}

export interface PageAttempt {
  product_id: number
  appid: number
  name: string
  platform: string
  side: Side
  status: QuoteStatus
  reason_code?: string
  collected_at: string
  source_time?: string
  present_cents?: number
  present_order_count?: number
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
}

export type QuoteSort = 'price_desc' | 'price_asc' | 'listings_desc' | 'name'

// 采集顺序决定平台先返回哪一头，改了会作废当前批次
export type SortColumn = 'price' | 'quantity' | 'name'
export type SortDirection = 'asc' | 'desc'

export interface QuoteFilter {
  appid?: number
  platform?: string
  side?: Side
  keyword?: string
  item_type?: string
  min_cents?: number
  max_cents?: number
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

export class APIError extends Error {
  constructor(public code: string, message?: string) {
    super(message ?? code)
    this.name = 'APIError'
  }
}
