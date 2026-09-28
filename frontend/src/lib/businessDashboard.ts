/**
 * 仪表盘「经营视图」各块的数据形状与加载。
 *
 * 后端每块一个端点(/api/dashboard/business/*),这里并行拉取、各自记录
 * loading / error —— 一块慢或挂了只影响那一张卡,不拖整页。
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useAuth } from '../contexts/AuthContext'
import type { WindowKey } from './dashboardI18n'

export type { WindowKey }
export const WINDOW_KEYS: WindowKey[] = ['today', '7d', '30d']

/** 网关控制台的风控页。明细在那里看,这里只给入口。 */
export const RISK_CONSOLE_URL = 'https://beatapi.fengshao1227.com/risk'

export interface DashboardWindow {
  key: WindowKey
  start: number
  end: number
  days: number
}

export interface MarginSummary {
  requests: number
  billed_usd: number
  free_user_billed_usd: number
  realized_revenue_usd: number
  provider_cost_usd: number
  gross_profit_usd: number
  gross_margin_percent: number
  external_profit_usd: number
  external_margin_percent: number
  paid_traffic_cost_usd: number
  gift_and_free_cost_usd: number
  internal_cost_usd: number
  non_revenue_cost_usd: number
  unpriced_calls: number
  estimated_calls: number
  zero_quota_cost_calls: number
}

export interface BusinessCash {
  revenue_usd: number
  orders: number
  payers: number
  unknown_currency_orders: number
}

export interface BusinessFinanceDay {
  date: string
  requests: number
  billed_usd: number
  provider_cost_usd: number
  realized_revenue_usd: number
  gross_profit_usd: number
  cash_revenue_usd: number
}

export interface BusinessModelRow {
  model: string
  requests: number
  billed_usd: number
  provider_cost_usd: number
  realized_revenue_usd: number
  gross_profit_usd: number
  margin_percent: number
  unpriced_calls: number
}

export interface BusinessFinance {
  window: DashboardWindow
  margin: MarginSummary
  cash: BusinessCash
  daily: BusinessFinanceDay[]
  models: {
    by_billed: BusinessModelRow[]
    by_cost: BusinessModelRow[]
    by_profit: BusinessModelRow[]
    excluded_free_models: string[]
    excluded_free_requests: number
    excluded_free_cost_usd: number
  }
}

export interface ConversionBucket {
  key: string
  signups: number
  activated: number
  paid: number
  activation_rate: number
  paid_rate: number
}

export interface BusinessConversion {
  window: DashboardWindow
  signups: number
  activated: number
  paid: number
  activation_rate: number
  paid_rate: number
  paid_of_activated_rate: number
  by_source: ConversionBucket[]
  by_country: ConversionBucket[]
  attribution_available: boolean
}

export interface RiskHold {
  cases: number
  users: number
  held_usd: number
}

export interface BusinessGiftsRisk {
  window: DashboardWindow
  gifts: {
    available: boolean
    signups: number
    granted_users: number
    granted_usd: number
    liability_users: number
    liability_usd: number
  }
  risk: {
    available: boolean
    review: RiskHold
    deny: RiskHold
    flagged: number
    confirmed: number
    released: number
    withheld: number
    dismissed: number
  }
}

/** failure / reasons 只含渠道/上游侧；用户侧（内容违规、参数、素材）单列，不计入失败率。 */
export interface TaskRow {
  platform: string
  model: string
  total: number
  success: number
  failure: number
  user_failure: number
  in_flight: number
  failure_rate: number
}

export interface BusinessTasks {
  window: DashboardWindow
  available: boolean
  total: number
  success: number
  failure: number
  user_failure: number
  in_flight: number
  failure_rate: number
  rows: TaskRow[]
  reasons: { reason: string; count: number }[]
  user_reasons: { reason: string; count: number }[]
  refund_count: number
  refund_usd: number
}

export interface UpstreamRow {
  id: number
  name: string
  balance: number
  low_balance: number
  low: boolean
  currency: string
  status: number
  last_error: string
  last_checked_at: number
  updated_at: number
}

export interface AlertRow {
  key: string
  kind: string
  title: string
  first_at: number
  last_seen_at: number
  count: number
}

export interface BusinessSupply {
  upstream_available: boolean
  upstreams: UpstreamRow[]
  alerts_available: boolean
  alerts: AlertRow[]
}

export interface PricingGapChannel {
  id: number
  name: string
  priority: number
  no_cost_expr: boolean
  model_count: number
  missing_models: string[]
}

export interface BusinessPricingGaps {
  window: DashboardWindow
  channels_available: boolean
  channels: PricingGapChannel[]
  unpriced_source: '' | 'quota_data' | 'logs'
  unpriced_calls: number
  unpriced: { channel_id: number; channel_name: string; model: string; calls: number }[]
}

export interface SectionState<T> {
  data: T | null
  loading: boolean
  error: string | null
}

interface SectionMap {
  finance: BusinessFinance
  conversion: BusinessConversion
  giftsRisk: BusinessGiftsRisk
  tasks: BusinessTasks
  supply: BusinessSupply
  pricingGaps: BusinessPricingGaps
}

export type SectionName = keyof SectionMap
export type BusinessState = { [K in SectionName]: SectionState<SectionMap[K]> }

const SECTION_PATHS: Record<SectionName, string> = {
  finance: 'finance',
  conversion: 'conversion',
  giftsRisk: 'gifts-risk',
  tasks: 'tasks',
  supply: 'supply',
  pricingGaps: 'pricing-gaps',
}

const SECTION_NAMES = Object.keys(SECTION_PATHS) as SectionName[]

/** 毛利一块要扫整个窗口的日志,30 天冷缓存可能要几十秒。 */
const SECTION_TIMEOUT_MS = 120_000

const emptyState = (): BusinessState =>
  Object.fromEntries(SECTION_NAMES.map((name) => [name, { data: null, loading: true, error: null }])) as BusinessState

export function useBusinessDashboard(windowKey: WindowKey) {
  const { token } = useAuth()
  const apiUrl = import.meta.env.VITE_API_URL || ''
  const [state, setState] = useState<BusinessState>(emptyState)
  const windowRef = useRef(windowKey)
  windowRef.current = windowKey

  const loadSection = useCallback(
    async (name: SectionName, noCache: boolean, outer?: AbortSignal): Promise<boolean> => {
      const requested = windowRef.current
      const controller = new AbortController()
      const abort = () => controller.abort()
      outer?.addEventListener('abort', abort)
      const timer = globalThis.setTimeout(abort, SECTION_TIMEOUT_MS)
      setState((s) => ({ ...s, [name]: { ...s[name], loading: true, error: null } }))
      try {
        const params = new URLSearchParams({ window: requested })
        if (noCache) params.set('no_cache', 'true')
        const response = await fetch(`${apiUrl}/api/dashboard/business/${SECTION_PATHS[name]}?${params}`, {
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
          signal: controller.signal,
        })
        const body = await response.json().catch(() => null)
        if (!response.ok || !body?.success) {
          throw new Error(body?.error?.message || `HTTP ${response.status}`)
        }
        // 切窗口后,旧窗口的慢请求回来了也不能覆盖新数据。
        if (windowRef.current !== requested) return false
        setState((s) => ({ ...s, [name]: { data: body.data, loading: false, error: null } }))
        return true
      } catch (e) {
        if (windowRef.current !== requested || outer?.aborted) return false
        const message = controller.signal.aborted ? 'timeout' : (e as Error).message
        setState((s) => ({ ...s, [name]: { ...s[name], loading: false, error: message } }))
        return false
      } finally {
        globalThis.clearTimeout(timer)
        outer?.removeEventListener('abort', abort)
      }
    },
    [apiUrl, token],
  )

  const reload = useCallback(
    async (noCache: boolean, signal?: AbortSignal): Promise<boolean> => {
      const results = await Promise.all(SECTION_NAMES.map((name) => loadSection(name, noCache, signal)))
      return results.every(Boolean)
    },
    [loadSection],
  )

  useEffect(() => {
    const controller = new AbortController()
    setState(emptyState())
    reload(false, controller.signal)
    return () => controller.abort()
  }, [reload, windowKey])

  return { state, reload }
}
