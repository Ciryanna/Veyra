// 管理端 API 封装。令牌存 localStorage（自用网关，浏览器即信任边界；
// 令牌本身仍只经 Header 传输，不进 URL——URL 会进代理日志）。
const TOKEN_KEY = 'veyra_admin_token'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setToken(t: string) {
  localStorage.setItem(TOKEN_KEY, t)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: {
      Authorization: `Bearer ${getToken()}`,
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const parsed = JSON.parse(text)
      msg = parsed.error ?? parsed.error?.message ?? msg
    } catch {
      /* 非 JSON 错误体，保留状态码信息 */
    }
    throw new ApiError(res.status, msg)
  }
  return JSON.parse(text) as T
}

// ---- 类型（与 internal/httpapi/admin.go 一一对应）----

export interface ProviderRow {
  name: string
  kind: string
  base_url: string
  api_key_masked: string
  region: string
  enabled: boolean
  created_at: number
  updated_at: number
}

export interface ProviderInput {
  name: string
  kind: string
  base_url: string
  api_key?: string
  region: string
}

export interface ModelRow {
  provider: string
  name: string
  upstream_model: string
  enabled: boolean
  price_in_per_mtok: number // USD/Mtok；0 = 未定价
  price_out_per_mtok: number
}

// 价格输入：0/空 = 保留原价（后端"非零即更新"约定）。
export interface ModelInput {
  provider: string
  name: string
  upstream_model?: string
  price_in_per_mtok?: number
  price_out_per_mtok?: number
}

export interface VKeyRow {
  app_name: string
  key_prefix: string
  enabled: boolean
  created_at: number
  last_used_at: number
}

export interface VKeyCreated {
  app_name: string
  key: string
  key_prefix: string
}

export interface Stats {
  requests: number
  input_tokens: number
  output_tokens: number
  errors: number
  active_apps: number
  cost_usd: number
}

export interface UsageRow {
  label: string
  requests: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  errors: number
  avg_duration_ms: number
  cost_usd: number
}
