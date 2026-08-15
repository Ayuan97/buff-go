import type { Region } from '../api/types'

/** 平台接口目标线路：国内站 / 国外站（香港节点两类都能打） */
export type PlatformTargetRegion = 'domestic' | 'foreign'

/**
 * 平台 → 目标线路。
 * Steam 只打国外接口；BUFF/IGXE 只打国内接口。
 * 节点能否用：node.region 允许该目标（见 nodeAllowsPlatform）。
 */
export function platformTargetRegion(platform: string): PlatformTargetRegion {
  const p = platform.toLowerCase()
  if (p === 'steam') return 'foreign'
  // buff / igxe 及默认国内站
  return 'domestic'
}

/** 节点地域是否允许访问该目标线路（与后端 NodeRegion.Allows 对齐） */
export function regionAllowsTarget(region: Region, target: PlatformTargetRegion): boolean {
  if (region === 'hongkong') return true
  return region === target
}

/** 节点能否服务该平台 */
export function nodeAllowsPlatform(region: Region, platform: string): boolean {
  return regionAllowsTarget(region, platformTargetRegion(platform))
}

export function platformRegionHint(platform: string): string {
  const t = platformTargetRegion(platform)
  if (t === 'foreign') return '仅国外/香港节点'
  return '仅国内/香港节点'
}

export function regionMismatchReason(region: Region, platform: string): string | null {
  if (nodeAllowsPlatform(region, platform)) return null
  if (platform.toLowerCase() === 'steam' && region === 'domestic') {
    return '国内节点不可采 Steam'
  }
  if (region === 'foreign' && platformTargetRegion(platform) === 'domestic') {
    return '国外节点不可采国内站'
  }
  return '地域与平台不匹配'
}
