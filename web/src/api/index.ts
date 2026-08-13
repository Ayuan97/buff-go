import { getJSON, postEmpty, postJSON } from './client'
import type {
  AccessNode,
  Account,
  Combination,
  Quote,
  Run,
  Side,
  Target,
} from './types'

export type {
  AccessNode,
  Account,
  ActualState,
  Combination,
  DesiredState,
  Quote,
  QuoteStatus,
  Region,
  Run,
  RunState,
  SessionState,
  Side,
  Target,
} from './types'
export { APIError } from './types'

export const getAccounts = () => getJSON<Account[]>('/api/accounts')
export const createAccount = (platform: string, alias: string, session: string) =>
  postJSON<Account>('/api/accounts', { platform, alias, session })
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
export const assignNodeSide = (id: number, expected_assignment_revision: number, platform: string, side: Side) =>
  postJSON<AccessNode>(`/api/nodes/${id}/assign-side`, { expected_assignment_revision, platform, side })
export const deleteNode = (id: number) => postEmpty<{ deleted: boolean }>(`/api/nodes/${id}/delete`)

export const getCombinations = () => getJSON<Combination[]>('/api/combinations')
export const createCombination = (account_id: number, node_id: number) =>
  postJSON<Combination>('/api/combinations', { account_id, node_id })
export const deleteCombination = (id: number) => postEmpty<{ deleted: boolean }>(`/api/combinations/${id}/delete`)

export const getTargets = () => getJSON<Target[]>('/api/targets')
export const createTarget = (platform: string, appid: number, side: Side, desired: 'enabled' | 'disabled') =>
  postJSON<Target>('/api/targets', { platform, appid, side, desired })
export const setTargetDesired = (id: number, expected_revision: number, desired: 'enabled' | 'disabled') =>
  postJSON<Target>(`/api/targets/${id}/desired`, { expected_revision, desired })

export const getRuns = (limit = 50) => getJSON<Run[]>(`/api/runs?limit=${limit}`)
export const getQuotes = (appid?: number, platform?: string) => {
  const q = new URLSearchParams()
  if (appid) q.set('appid', String(appid))
  if (platform) q.set('platform', platform)
  q.set('limit', '200')
  return getJSON<Quote[]>(`/api/quotes?${q.toString()}`)
}

export const getCapabilities = () => getJSON<{ detail: 'detail_unavailable' }>('/api/capabilities')
