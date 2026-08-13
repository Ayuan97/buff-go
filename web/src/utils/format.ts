// 展示格式化：金额分整数 → 元；时间 → 本地相对/绝对文本
export function fenToYuan(fen: number | null): string {
  if (fen === null) return '—'
  return '¥' + (fen / 100).toFixed(2)
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

export const sideText = (side: 'bid' | 'ask' | null): string =>
  side === 'bid' ? '求购' : side === 'ask' ? '出售' : '—'

export const taskTypeText = (t: 'summary' | 'detail'): string =>
  t === 'summary' ? '摘要' : '详情'

export const regionText = (r: 'domestic' | 'foreign' | 'hongkong'): string =>
  r === 'domestic' ? '国内' : r === 'foreign' ? '国外' : '香港'
