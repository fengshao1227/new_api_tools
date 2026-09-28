// 渠道健康的归属口径（与网关一致，见 backend service/failure_attribution.go）：
// 只有渠道/上游侧失败计入错误率，用户侧失败与排队单列。

export type Level = 'green' | 'yellow' | 'red' | 'idle'
export type Side = 'channel' | 'user' | 'queue'

/** 与网关同一口径：只有渠道/上游侧失败计入错误率，用户侧失败与排队单列。 */
export interface FailureTally {
  success: number
  errors: number
  user_errors: number
  queue_full: number
  channel_categories: Record<string, number>
  user_categories: Record<string, number>
}

export interface ChannelLogStat extends FailureTally {
  channel_id: number
  attempts: number
  error_rate: number
  level: Level
  avg_use_time: number | null
  avg_task_seconds: number | null
  task_finished: number
}

export interface ModelHealth extends FailureTally {
  model_name: string
  attempts: number
  error_rate: number
  level: Level
  sync_success: number
  empty_count: number
  avg_use_time: number | null
  max_use_time: number | null
  bucket_fast: number
  bucket_mid: number
  bucket_slow: number
  bucket_very_slow: number
  avg_task_seconds: number | null
  task_finished: number
}

export interface ErrorAnalysis {
  total: number
  channel_errors: number
  user_errors: number
  queue_full: number
  channel_categories: Record<string, number>
  user_categories: Record<string, number>
  samples: {
    created_at: number
    model_name: string
    channel_id: number
    username: string
    content: string
    side: Side
    category: string
  }[]
  sampled: number
}

export interface SinglePointModel {
  model: string
  channel_id: number
  channel_name: string
  group: string
}

export const CHANNEL_CATEGORY_LABELS: Record<string, string> = {
  upstream_error: '上游 5xx / 临时错误',
  timeout: '超时',
  rate_limited: '上游限流',
  empty_result: '空回复',
  upstream_account: '上游账号 / 余额',
  model_unavailable: '上游无可用渠道',
  param_rejected: '参数被本渠道拒（别家接了）',
  other: '其他',
}

export const USER_CATEGORY_LABELS: Record<string, string> = {
  content_policy: '内容违规',
  invalid_request: '参数错误',
  input_unreachable: '素材下载/处理失败',
  insufficient_balance: '用户余额不足',
  client_closed: '客户先断开 499',
}

export const SIDE_META: Record<Side, { label: string; color: string }> = {
  channel: { label: '渠道侧', color: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400' },
  user: { label: '用户侧', color: 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300' },
  queue: { label: '排队', color: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-400' },
}

export const LEVEL_TEXT: Record<Level, string> = {
  red: 'text-red-600',
  yellow: 'text-yellow-600',
  green: 'text-green-600',
  idle: 'text-muted-foreground',
}

export const LEVEL_DOT: Record<Level, string> = {
  red: 'bg-red-500',
  yellow: 'bg-yellow-500',
  green: 'bg-green-500',
  idle: 'bg-gray-300 dark:bg-gray-600',
}

/** 网关渠道告警的线：至少 5 次计入的尝试才判色，≥80% 红，≥20% 黄。 */
export const MIN_SAMPLES = 5
export function levelOf(success: number, errors: number): Level {
  const counted = success + errors
  if (counted < MIN_SAMPLES) return 'idle'
  if (errors * 100 >= 80 * counted) return 'red'
  if (errors * 100 >= 20 * counted) return 'yellow'
  return 'green'
}

export function categoryLabel(side: Side, category: string) {
  if (side === 'queue') return '有并发上限的渠道报满'
  const labels = side === 'user' ? USER_CATEGORY_LABELS : CHANNEL_CATEGORY_LABELS
  return labels[category] || category
}

export function breakdown(categories: Record<string, number>, side: Side) {
  return Object.entries(categories || {})
    .sort((a, b) => b[1] - a[1])
    .map(([cat, n]) => `${categoryLabel(side, cat)} ${n}`)
    .join(' · ')
}

export function rateTitle(s: FailureTally & { level: Level }) {
  const parts = [`计入 ${s.success + s.errors} 次（成功 ${s.success} / 渠道侧失败 ${s.errors}）`]
  if (s.level === 'idle') parts.push(`不足 ${MIN_SAMPLES} 次，不判色`)
  const channel = breakdown(s.channel_categories, 'channel')
  if (channel) parts.push(`渠道侧：${channel}`)
  return parts.join('\n')
}

export function userTitle(s: FailureTally) {
  const parts = []
  const user = breakdown(s.user_categories, 'user')
  if (user) parts.push(`用户侧（不计入）：${user}`)
  if (s.queue_full > 0) parts.push(`排队（不计入）：有并发上限的渠道报满 ${s.queue_full}`)
  return parts.join('\n')
}

export function durationText(sync: number | null, task: number | null) {
  if (sync != null) return `${Number(sync).toFixed(1)}s`
  if (task != null) return `${Number(task).toFixed(0)}s（任务）`
  return '-'
}
