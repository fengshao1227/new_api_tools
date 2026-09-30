import { apiFetch, createAuthHeaders } from './api.ts'

export interface InsightsWindow {
  start_time: number
  end_time: number
  model: string
  all_time?: boolean
}

export type DatePreset = '7' | '30' | '90' | 'all' | 'custom'
export interface InsightsDraft {
  preset: DatePreset
  start: string
  end: string
  model: string
}

export interface InsightsMetrics {
  billing_records: number
  error_records: number
  refund_records: number
  charged_quota: number
  charged_usd: number
  refund_quota: number
  refund_usd: number
  input_tokens: number
  raw_prompt_tokens: number
  output_tokens: number
  cache_read_tokens: number | null
  cache_write_tokens: number | null
  reasoning_tokens: number | null
  models_count: number
  token_details_recorded: number
  cache_read_records: number
  cache_write_records: number
}

export interface InsightsModel extends InsightsMetrics {
  model_name: string
  last_record_at: number | null
}

export interface InsightsPayment {
  paid_count: number
  paid_usd: number | null
  credited_usd: number | null
  unknown_currency_count: number
  by_currency: { currency: string; count: number; amount: number }[]
}

export interface InsightsProfile {
  id: number
  username: string
  display_name: string | null
  email: string | null
  status: number
  role: number
  group: string | null
  remark: string | null
  created_at: number | null
  last_login_at: number | null
  signup_country: string | null
  signup_language: string | null
  acquisition_source: string | null
  acquisition_detail: string | null
  login_sources: string[]
  inviter_id: number | null
  paid: boolean | null
  paid_via: string | null
  risk?: { status: string; open_cases?: number; held_usd?: number; resolved_at?: number } | null
}

export interface UserInsightsReport {
  window: InsightsWindow
  profile: InsightsProfile
  balances: {
    quota: number; used_quota: number; topup_quota: number | null
    granted_quota: number | null; balance_usd: number; lifetime_used_usd: number
  }
  payments: {
    window: InsightsPayment; lifetime: InsightsPayment
    first_paid_at: number | null; last_paid_at: number | null
    cny_per_usd: number
  } | null
  activity: {
    first_record_at: number | null; last_record_at: number | null
    active_days: number; last_model: string | null
  } | null
  summary: InsightsMetrics | null
  models: InsightsModel[]
  daily: (InsightsMetrics & { date: string })[]
  tasks: { total: number; success: number; failed: number; in_progress: number } | null
  availability: {
    profile: boolean; logs: boolean; tasks: boolean; acquisition: boolean
    payments: boolean; risk: boolean; token_details: boolean; reasoning_tokens: boolean; warnings: string[]
  }
}

export interface InsightsUserOption {
  id: number; username: string; display_name: string | null; email: string | null
  status: number; group: string | null
}

export interface InsightsLog {
  id: number; created_at: number; type: number; model_name: string
  request_id: string | null; token_id: number; token_name: string; channel_id: number
  prompt_tokens: number; input_tokens: number; completion_tokens: number
  cache_read_tokens: number | null; cache_write_tokens: number | null
  reasoning_tokens: number | null; quota: number; quota_usd: number
  cost: number | null; cost_usd: number | null; use_time: number
  is_stream: boolean; ip: string; task_id: string | null; is_task: boolean; voided_quota: number | null
  content: string
}

export interface InsightsTask {
  id: number; task_id: string; platform: string; model_name: string | null
  action: string; status: string; progress: string
  submit_time: number; start_time: number; finish_time: number
  quota: number; quota_usd: number; fail_reason: string
}

export interface InsightsPage<T> {
  items: T[]; page: number; page_size: number; total: number; available: boolean; warnings: string[]
}

const warningLabels: Record<string, string> = {
  acquisition_schema_missing: '注册来源字段不完整，部分获客信息未采集。',
  account_times_incomplete: '注册或最后登录时间未完整采集。',
  oauth_bindings_unavailable: '第三方登录绑定信息不可用，已展示能够读取的其他登录来源。',
  risk_unavailable: '风控信息未采集。',
  payments_unavailable: '支付记录不可用，暂时无法统计历史付费。',
  credited_amount_incomplete: '部分订单的充值入账金额无法确认，累计入账额显示为“未记录”。',
  logs_unavailable: '调用日志不可用，暂时无法统计模型用量。',
  tasks_unavailable: '未采集可用于当前筛选的任务信息。',
  token_details_unavailable: '缓存等 Token 明细未采集，输入计数按现存基础字段统计。',
  log_token_details_unavailable: '部分记录的 Token 明细不可用，输入按原始日志展示。',
  log_database_fallback_may_be_stale: '独立日志库不可用，当前读取主库日志，数据可能不完整或滞后。',
}

export function insightsWarnings(codes: readonly string[] | null | undefined): string[] {
  // Standalone reasoning usage is intentionally absent and explained by the token panel.
  return Array.from(new Set((codes || [])
    .filter(code => code !== 'reasoning_tokens_not_recorded')
    .map(code => warningLabels[code] || '部分数据暂不可用，请检查服务配置。')))
}

export function formatMetric(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? '未记录' : value.toLocaleString('zh-CN', { maximumFractionDigits: 4 })
}

export function formatUSD(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? '未记录' : `$${value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 6 })}`
}

export function utcDateInput(epoch: number): string {
  return new Date(epoch * 1000).toISOString().slice(0, 19)
}

export function formatUTCTime(epoch: number | null | undefined): string {
  if (!epoch || !Number.isFinite(epoch)) return '未记录'
  return `${utcDateInput(epoch).replace('T', ' ')} UTC`
}

function parseUTCInput(input: string): number {
  const normalized = input.length === 16 ? `${input}:00` : input
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(normalized)) throw new Error('请填写完整的 UTC 起止时间')
  const milliseconds = Date.parse(`${normalized}Z`)
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString().slice(0, 19) !== normalized) {
    throw new Error('日期或时间无效')
  }
  return milliseconds / 1000
}

export function resolveInsightsWindow(draft: InsightsDraft, now = Math.floor(Date.now() / 1000)): InsightsWindow {
  const end = draft.preset === 'custom' ? parseUTCInput(draft.end) : now
  const start = draft.preset === 'custom' ? parseUTCInput(draft.start)
    : draft.preset === 'all' ? 0 : end - Number(draft.preset) * 86400
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start < 0 || start >= end) {
    throw new Error('开始时间必须早于结束时间，且不能早于 1970 年')
  }
  return { start_time: start, end_time: end, model: draft.model, all_time: draft.preset === 'all' }
}

export function insightsQuery(window: InsightsWindow): URLSearchParams {
  const query = new URLSearchParams({ start_time: String(window.start_time), end_time: String(window.end_time) })
  if (window.model) query.set('model', window.model)
  return query
}

export function userIdFromSearch(search: string): number | null {
  const raw = new URLSearchParams(search).get('user_id')
  if (!raw || !/^[1-9]\d*$/.test(raw)) return null
  const id = Number(raw)
  return Number.isSafeInteger(id) ? id : null
}

export function openUserInsights(id: number): void {
  if (!Number.isSafeInteger(id) || id < 1) return
  window.history.pushState(null, '', `/user-insights?user_id=${id}`)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export async function fetchInsightsData<T>(path: string, token: string, signal: AbortSignal): Promise<T> {
  const response = await apiFetch(`${import.meta.env.VITE_API_URL || ''}${path}`, {
    headers: createAuthHeaders(token), signal,
  })
  if (!response.ok) {
    if (response.status === 404) throw new Error('用户不存在，或当前服务尚未提供用户画像接口')
    if (response.status === 403 || response.status === 401) throw new Error('无访问权限，请重新登录')
    if (response.status === 400) throw new Error('查询条件无效，请检查时间和模型筛选')
    throw new Error(`查询失败（HTTP ${response.status}），请稍后重试`)
  }
  const result = await response.json() as { success: boolean; data: T }
  if (!result.success || result.data == null) throw new Error('查询失败，服务未返回有效数据')
  return result.data
}

export function csvCell(value: string | number | null | undefined): string {
  const text = value == null ? '未记录' : String(value)
  // Spreadsheet import may strip leading whitespace before evaluating a formula.
  const safe = /^[\s\uFEFF]*[=+\-@]/.test(text) ? `'${text}` : text
  return `"${safe.replace(/"/g, '""')}"`
}

export function createInsightsCSV(report: UserInsightsReport): string {
  const headings = ['用户 ID', '用户名', '邮箱', '开始时间 UTC（含）', '结束时间 UTC（不含）', '模型', '计费记录数', '失败尝试', '退款记录数', '输入 Token（含已记录缓存）', '输出 Token', '总 Token', '缓存读取 Token', '缓存写入 Token', '推理 Token', '消费记录额 USD', '退款流水额 USD', '最后记录 UTC']
  const rows = (report.models || []).map(model => [
    report.profile.id, report.profile.username, report.profile.email,
    report.window.all_time ? '全部现存记录' : formatUTCTime(report.window.start_time),
    formatUTCTime(report.window.end_time), model.model_name || '未记录模型',
    model.billing_records, model.error_records, model.refund_records,
    model.input_tokens, model.output_tokens, model.input_tokens + model.output_tokens,
    model.cache_read_tokens, model.cache_write_tokens, model.reasoning_tokens,
    model.charged_usd, model.refund_usd, formatUTCTime(model.last_record_at),
  ])
  return '\uFEFF' + [headings, ...rows].map(row => row.map(csvCell).join(',')).join('\r\n')
}
