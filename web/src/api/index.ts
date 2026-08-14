import { getJSON, getText, postEmpty, postJSON } from './client'
import type {
  AccessNode,
  Account,
  CollectionPage,
  Combination,
  PageAttempt,
  Provider,
  QuoteFacets,
  QuoteFilter,
  QuotePage,
  Run,
  Side,
  SortColumn,
  SortDirection,
  Target,
  Watermark,
} from './types'

export type {
  AccessNode,
  Account,
  ActualState,
  CollectionPage,
  Combination,
  DesiredState,
  PageAttempt,
  Provider,
  Watermark,
  Quote,
  QuoteFacets,
  QuoteFilter,
  QuotePage,
  QuoteSort,
  QuoteStatus,
  Region,
  Run,
  RunState,
  SessionState,
  Side,
  SortColumn,
  SortDirection,
  Target,
} from './types'
export { APIError } from './types'

export const getAccounts = () => getJSON<Account[]>('/api/accounts')
export const createAccount = (platform: string, alias: string, session: string) =>
  postJSON<Account>('/api/accounts', { platform, alias, session })
export const renameAccount = (id: number, alias: string) =>
  postJSON<Account>(`/api/accounts/${id}/alias`, { alias })
export const replaceAccountSession = (id: number, expected_revision: number, session: string) =>
  postJSON<Account>(`/api/accounts/${id}/session`, { expected_revision, session })
export const deleteAccount = (id: number) => postEmpty<{ deleted: boolean }>(`/api/accounts/${id}/delete`)

export const getNodes = () => getJSON<AccessNode[]>('/api/nodes')
export const createNode = (input: {
  name: string
  kind: string
  region: string
  egress_mode: string
  proxy_credential?: string
  sticky_session_valid_until?: string
}) => postJSON<AccessNode>('/api/nodes', input)
export const renameNode = (id: number, name: string) => postJSON<AccessNode>(`/api/nodes/${id}/name`, { name })
export const replaceNodeConnection = (
  id: number,
  input: {
    expected_egress_revision: number
    kind: string
    region: string
    egress_mode: string
    proxy_credential?: string
    sticky_session_valid_until?: string
  },
) => postJSON<AccessNode>(`/api/nodes/${id}/connection`, input)
export const recordNodeExit = (id: number, expected_egress_revision: number, address: string, valid_until: string) =>
  postJSON<AccessNode>(`/api/nodes/${id}/exit`, { expected_egress_revision, address, valid_until })
export const assignNodeGame = (id: number, expected_assignment_revision: number, appid: number | null) =>
  postJSON<AccessNode>(`/api/nodes/${id}/assign-game`, { expected_assignment_revision, appid })
// side 传空字符串表示解除该平台的方向归属。
export const assignNodeSide = (
  id: number,
  expected_assignment_revision: number,
  platform: string,
  side: Side | '',
) => postJSON<AccessNode>(`/api/nodes/${id}/assign-side`, { expected_assignment_revision, platform, side })
export const deleteNode = (id: number) => postEmpty<{ deleted: boolean }>(`/api/nodes/${id}/delete`)

export const getWatermarks = () => getJSON<Watermark[]>('/api/watermarks')
export const setWatermark = (region: string, expected_revision: number, min_usable: number) =>
  postJSON<Watermark>('/api/watermarks', { region, expected_revision, min_usable })

export const getProviders = () => getJSON<Provider[]>('/api/providers')
export const createProvider = (input: {
  name: string
  enabled?: boolean
  priority: number
  regions: string[]
  credential: string
}) => postJSON<Provider>('/api/providers', input)
export const updateProvider = (
  id: number,
  input: { expected_revision: number; name: string; enabled: boolean; priority: number; regions: string[] },
) => postJSON<Provider>(`/api/providers/${id}/update`, input)
export const replaceProviderCredential = (id: number, expected_revision: number, credential: string) =>
  postJSON<Provider>(`/api/providers/${id}/credential`, { expected_revision, credential })
export const deleteProvider = (id: number) => postEmpty<{ deleted: boolean }>(`/api/providers/${id}/delete`)

export const getCombinations = () => getJSON<Combination[]>('/api/combinations')
export const createCombination = (account_id: number, node_id: number) =>
  postJSON<Combination>('/api/combinations', { account_id, node_id })
export const deleteCombination = (id: number) => postEmpty<{ deleted: boolean }>(`/api/combinations/${id}/delete`)

export const getTargets = () => getJSON<Target[]>('/api/targets')
export const createTarget = (platform: string, appid: number, side: Side, desired: 'enabled' | 'disabled') =>
  postJSON<Target>('/api/targets', { platform, appid, side, desired })
export const setTargetDesired = (id: number, expected_revision: number, desired: 'enabled' | 'disabled') =>
  postJSON<Target>(`/api/targets/${id}/desired`, { expected_revision, desired })
// 改采集顺序会作废当前批次：游标是列表偏移量，换顺序后续点就没有意义了
export const setTargetSort = (
  id: number,
  expected_revision: number,
  sort_column: SortColumn,
  sort_dir: SortDirection,
) => postJSON<Target>(`/api/targets/${id}/sort`, { expected_revision, sort_column, sort_dir })
// 只能删还没跑过、且已经停下来的目标；跑过的目标只能停用，采集历史不会被删。
export const deleteTarget = (id: number) => postEmpty<{ deleted: boolean }>(`/api/targets/${id}/delete`)

export const getRuns = (limit = 50) => getJSON<Run[]>(`/api/runs?limit=${limit}`)
export const getRunPages = (id: number) => getJSON<CollectionPage[]>(`/api/runs/${id}/pages`)
// 后续批次重采同一商品会把它从旧页上带走，所以历史页返回的行会越来越少
export const getPageAttempts = (id: number, sequence: number) =>
  getJSON<PageAttempt[]>(`/api/runs/${id}/pages/${sequence}/attempts`)
export const getPagePayload = (id: number, sequence: number) =>
  getText(`/api/runs/${id}/pages/${sequence}/payload`)
// 全部筛选都交给 SQL：以前在前端过滤被 LIMIT 截断的结果，筛出来的数据是不完整的
export const getQuotes = (filter: QuoteFilter = {}) => {
  const q = new URLSearchParams()
  for (const [key, value] of Object.entries(filter)) {
    if (value === undefined || value === '' || value === null) continue
    q.set(key, String(value))
  }
  return getJSON<QuotePage>(`/api/quotes?${q.toString()}`)
}
export const getQuoteFacets = (appid?: number) =>
  getJSON<QuoteFacets>(appid ? `/api/quote-facets?appid=${appid}` : '/api/quote-facets')

// Steam 只回图片路径片段，前缀在这里统一拼，库里不存整条 URL
const STEAM_IMAGE_PREFIX = 'https://community.steamstatic.com/economy/image/'
export const productImage = (iconPath: string | undefined, size = '360fx360f'): string =>
  iconPath ? `${STEAM_IMAGE_PREFIX}${iconPath}/${size}` : ''

export const getCapabilities = () => getJSON<{ detail: 'detail_unavailable' }>('/api/capabilities')
