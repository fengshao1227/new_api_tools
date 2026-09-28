// Formatting shared by the margin page and its cost-baseline panel.

export function money(value: number) {
  return `$${(Number(value) || 0).toFixed(2)}`
}

// Unit prices (per image, per call, per 1M tokens) are often below a cent:
// two decimals would print $0.015 as $0.02.
export function unitPrice(value: number) {
  const n = Number(value) || 0
  if (n !== 0 && Math.abs(n) < 1) return `$${Number(n.toFixed(6)).toString()}`
  return `$${n.toFixed(2)}`
}

export function percent(value: number) {
  return `${(Number(value) || 0).toFixed(1)}%`
}

export function number(value: number) {
  return (Number(value) || 0).toLocaleString('zh-CN')
}

export function ratio(value: number) {
  return `×${Number((Number(value) || 0).toFixed(4))}`
}

export interface APIPayload<T> {
  success?: boolean
  data?: T
  error?: { message?: string }
}

export async function readAPIResponse<T>(response: Response): Promise<APIPayload<T>> {
  const body = await response.text()
  try {
    return JSON.parse(body) as APIPayload<T>
  } catch {
    throw new Error(`接口返回了非 JSON 响应（HTTP ${response.status}），请检查插件 API 路由和代理配置`)
  }
}
