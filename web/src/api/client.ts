import { APIError } from './types'

const CSRF_HEADER = 'X-Buffgo-CSRF'

let csrfToken = ''

interface ErrorBody {
  code?: string
}

async function readError(res: Response): Promise<APIError> {
  try {
    const body = (await res.json()) as ErrorBody
    if (body.code) return new APIError(body.code)
  } catch {
    /* ignore non-JSON */
  }
  return new APIError(`http_${res.status}`)
}

export async function ensureContext(): Promise<void> {
  if (csrfToken) return
  const res = await fetch('/api/security/context', { credentials: 'same-origin' })
  if (!res.ok) throw await readError(res)
  const body = (await res.json()) as { csrf_token: string }
  csrfToken = body.csrf_token
}

export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { credentials: 'same-origin' })
  if (!res.ok) throw await readError(res)
  return (await res.json()) as T
}

// 平台原始响应按文本读：抓到 HTML 错误页时最需要看清内容，不能因为解析失败就丢掉
export async function getText(path: string): Promise<string> {
  const res = await fetch(path, { credentials: 'same-origin' })
  if (!res.ok) throw await readError(res)
  return await res.text()
}

export async function postJSON<T>(path: string, body: unknown = {}): Promise<T> {
  return sendPOST<T>(path, JSON.stringify(body))
}

export async function postEmpty<T>(path: string): Promise<T> {
  return sendPOST<T>(path, '')
}

async function sendPOST<T>(path: string, body: string): Promise<T> {
  await ensureContext()
  const res = await fetch(path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      [CSRF_HEADER]: csrfToken,
    },
    body: body === '' ? undefined : body,
  })
  if (res.status === 403) {
    const err = await readError(res)
    if (err.code === 'csrf_rejected') csrfToken = ''
    throw err
  }
  if (!res.ok) throw await readError(res)
  const text = await res.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}
