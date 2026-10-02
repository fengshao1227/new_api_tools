import { useCallback, useEffect, useMemo, useState } from 'react'
import ReactECharts from 'echarts-for-react'
import {
  Activity,
  Database,
  KeyRound,
  Layers3,
  Loader2,
  Network,
  Users,
} from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { apiFetch } from '../lib/api'
import type { DashboardLang } from '../lib/dashboardI18n'
import { cn } from '../lib/utils'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'

interface NativeOverview {
  total_users: number
  active_users_24h: number
  total_tokens: number
  active_tokens_24h: number
}

interface NativeUsage {
  requests_24h: number
  tokens_24h: number
  quota_used_24h: number
  input_tokens_24h?: number
  output_tokens_24h?: number
}

interface NativeSeriesPoint {
  label: string
  requests: number
  tokens: number
}

interface NativeModelUsage {
  model: string
  requests: number
  tokens: number
}

interface NativeDashboardData {
  overview: NativeOverview
  usage: NativeUsage
  models: NativeModelUsage[]
  daily: NativeSeriesPoint[]
  hourly: NativeSeriesPoint[]
}

interface NativeDashboardPanelProps {
  lang: DashboardLang
  refreshToken?: number
}

type UnknownRecord = Record<string, unknown>

const COPY = {
  zh: {
    title: '原生资源概览',
    hint: '来自网关原生统计接口，显示资源总量与最近 24 小时活跃情况。',
    users: '用户总数',
    activeUsers: '24h 活跃用户',
    tokens: '令牌总数',
    activeTokens: '24h 活跃令牌',
    traffic: '24h 流量统计',
    requests: '请求数',
    tokenUsage: 'Token 用量',
    quotaUsage: '额度消耗',
    inputTokens: '输入 Token',
    outputTokens: '输出 Token',
    daily: '每日请求趋势',
    hourly: '24h 请求趋势',
    models: '模型使用趋势',
    requestSeries: '请求数',
    tokenSeries: 'Token 数',
    noData: '暂无数据',
    loading: '加载原生资源中…',
    loadFailed: (message: string) => `原生资源加载失败：${message}`,
  },
  en: {
    title: 'Native resources',
    hint: 'Native gateway statistics for totals and activity over the last 24 hours.',
    users: 'Total users',
    activeUsers: 'Active users (24h)',
    tokens: 'Total tokens',
    activeTokens: 'Active tokens (24h)',
    traffic: 'Traffic (24h)',
    requests: 'Requests',
    tokenUsage: 'Token usage',
    quotaUsage: 'Quota used',
    inputTokens: 'Input tokens',
    outputTokens: 'Output tokens',
    daily: 'Daily request trend',
    hourly: '24h request trend',
    models: 'Model usage trend',
    requestSeries: 'Requests',
    tokenSeries: 'Tokens',
    noData: 'No data',
    loading: 'Loading native resources…',
    loadFailed: (message: string) => `Could not load native resources: ${message}`,
  },
} as const

function numberValue(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

function recordValue(record: UnknownRecord, ...keys: string[]): unknown {
  for (const key of keys) {
    if (record[key] !== undefined && record[key] !== null) return record[key]
  }
  return undefined
}

function asRecord(value: unknown): UnknownRecord {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as UnknownRecord : {}
}

function asRows(value: unknown): UnknownRecord[] {
  if (Array.isArray(value)) return value.map(asRecord)
  const record = asRecord(value)
  return Object.entries(record).map(([label, row]) => ({
    label,
    ...(typeof row === 'object' && row !== null ? row as UnknownRecord : { requests: row }),
  }))
}

function readLabel(row: UnknownRecord, fallback: string): string {
  const label = recordValue(row, 'label', 'date', 'day', 'hour', 'timestamp', 'name', 'period')
  if (typeof label === 'string' || typeof label === 'number') return String(label)
  return fallback
}

function normalizeSeries(value: unknown): NativeSeriesPoint[] {
  return asRows(value).map((row, index) => ({
    label: readLabel(row, String(index + 1)),
    requests: numberValue(recordValue(row, 'requests', 'request_count', 'count', 'total')),
    tokens: numberValue(recordValue(row, 'tokens', 'token_count', 'usage', 'total_tokens')),
  }))
}

function normalizeModels(value: unknown): NativeModelUsage[] {
  return asRows(value)
    .map((row, index) => {
      const inputTokens = numberValue(recordValue(row, 'input_tokens', 'prompt_tokens', 'total_prompt_tokens'))
      const outputTokens = numberValue(recordValue(row, 'output_tokens', 'completion_tokens', 'total_completion_tokens'))
      return {
        model: String(recordValue(row, 'model', 'model_name', 'name', 'label') ?? `Model ${index + 1}`),
        requests: numberValue(recordValue(row, 'requests', 'request_count', 'count', 'total', 'total_requests')),
        tokens: numberValue(recordValue(row, 'tokens', 'token_count', 'usage', 'total_tokens')) || inputTokens + outputTokens,
      }
    })
    .filter((row) => row.model.length > 0)
    .sort((a, b) => b.requests - a.requests || b.tokens - a.tokens)
    .slice(0, 10)
}

function normalizeNativeDashboard(value: unknown): NativeDashboardData {
  const envelope = asRecord(value)
  const payload = asRecord(envelope.data ?? value)
  const overview = asRecord(payload.overview)
  const usage = asRecord(payload.usage)
  const inputTokens = numberValue(recordValue(usage, 'input_tokens_24h', 'input_tokens', 'prompt_tokens', 'total_prompt_tokens'))
  const outputTokens = numberValue(recordValue(usage, 'output_tokens_24h', 'output_tokens', 'completion_tokens', 'total_completion_tokens'))
  const totalTokens = numberValue(recordValue(usage, 'tokens_24h', 'tokens', 'token_count', 'total_tokens')) || inputTokens + outputTokens

  return {
    overview: {
      total_users: numberValue(recordValue(overview, 'total_users', 'users', 'user_count')),
      active_users_24h: numberValue(recordValue(overview, 'active_users_24h', 'active_users', 'users_24h')),
      total_tokens: numberValue(recordValue(overview, 'total_tokens', 'tokens', 'token_count')),
      active_tokens_24h: numberValue(recordValue(overview, 'active_tokens_24h', 'active_tokens', 'tokens_24h')),
    },
    usage: {
      requests_24h: numberValue(recordValue(usage, 'requests_24h', 'requests', 'request_count', 'total_requests')),
      tokens_24h: totalTokens,
      quota_used_24h: numberValue(recordValue(usage, 'quota_used_24h', 'quota_used', 'total_quota_used')),
      input_tokens_24h: inputTokens,
      output_tokens_24h: outputTokens,
    },
    models: normalizeModels(payload.models),
    daily: normalizeSeries(payload.daily),
    hourly: normalizeSeries(payload.hourly),
  }
}

function formatNumber(value: number, locale: string): string {
  return value.toLocaleString(locale, { maximumFractionDigits: 0 })
}

function metricIconColor(color: string) {
  return cn('rounded-xl p-2.5', color)
}

function Metric({
  icon: Icon,
  label,
  value,
  hint,
  color,
  locale,
}: {
  icon: typeof Users
  label: string
  value: number
  hint?: string
  color: string
  locale: string
}) {
  return (
    <div className="rounded-lg border bg-background/70 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-xs font-medium text-muted-foreground">{label}</p>
          <p className="mt-2 text-2xl font-semibold tabular-nums tracking-tight">{formatNumber(value, locale)}</p>
          {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
        </div>
        <div className={metricIconColor(color)}>
          <Icon className="h-4 w-4" />
        </div>
      </div>
    </div>
  )
}

function EmptyChart({ message }: { message: string }) {
  return <div className="flex h-[270px] items-center justify-center text-sm text-muted-foreground">{message}</div>
}

export function NativeDashboardPanel({ lang, refreshToken }: NativeDashboardPanelProps) {
  const { token } = useAuth()
  const apiUrl = import.meta.env.VITE_API_URL || ''
  const copy = COPY[lang]
  const locale = lang === 'zh' ? 'zh-CN' : 'en-US'
  const [data, setData] = useState<NativeDashboardData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (signal: AbortSignal, noCache: boolean) => {
    try {
      setError(null)
      const query = new URLSearchParams({ period: '24h', no_cache: noCache ? 'true' : 'false' })
      const response = await apiFetch(`${apiUrl}/api/dashboard/native?${query.toString()}`, {
        headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        signal,
      })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const body = await response.json() as UnknownRecord
      if (body.success === false) {
        const bodyError = asRecord(body.error)
        throw new Error(String(bodyError.message ?? body.message ?? 'request failed'))
      }
      setData(normalizeNativeDashboard(body))
    } catch (cause) {
      if (cause instanceof Error && cause.name === 'AbortError') return
      setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      if (!signal.aborted) setLoading(false)
    }
  }, [apiUrl, token])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal, refreshToken !== undefined)
    return () => controller.abort()
  }, [load, refreshToken])

  const dailyOption = useMemo(() => ({
    grid: { left: 44, right: 20, top: 20, bottom: 34, containLabel: true },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: data?.daily.map((row) => row.label) ?? [],
      axisTick: { show: false },
      axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 },
      axisLine: { lineStyle: { color: 'hsl(var(--border))' } },
    },
    yAxis: { type: 'value', minInterval: 1, axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 }, splitLine: { lineStyle: { type: 'dashed', color: 'hsl(var(--border))' } } },
    series: [{ name: copy.requestSeries, type: 'line', smooth: true, showSymbol: false, data: data?.daily.map((row) => row.requests) ?? [], itemStyle: { color: '#2563eb' }, areaStyle: { opacity: 0.12 } }],
  }), [copy.requestSeries, data?.daily])

  const hourlyOption = useMemo(() => ({
    grid: { left: 44, right: 20, top: 20, bottom: 34, containLabel: true },
    tooltip: { trigger: 'axis' },
    xAxis: {
      type: 'category',
      data: data?.hourly.map((row) => row.label) ?? [],
      axisTick: { show: false },
      axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 },
      axisLine: { lineStyle: { color: 'hsl(var(--border))' } },
    },
    yAxis: { type: 'value', minInterval: 1, axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 }, splitLine: { lineStyle: { type: 'dashed', color: 'hsl(var(--border))' } } },
    series: [{ name: copy.requestSeries, type: 'bar', barMaxWidth: 18, data: data?.hourly.map((row) => row.requests) ?? [], itemStyle: { color: '#0d9488', borderRadius: [4, 4, 0, 0] } }],
  }), [copy.requestSeries, data?.hourly])

  const modelOption = useMemo(() => ({
    grid: { left: 100, right: 28, top: 12, bottom: 24, containLabel: true },
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    xAxis: { type: 'value', minInterval: 1, axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 }, splitLine: { lineStyle: { type: 'dashed', color: 'hsl(var(--border))' } } },
    yAxis: { type: 'category', inverse: true, data: data?.models.map((row) => row.model) ?? [], axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11, width: 92, overflow: 'truncate' } },
    series: [{ name: copy.requestSeries, type: 'bar', data: data?.models.map((row) => row.requests) ?? [], barMaxWidth: 18, itemStyle: { color: '#7c3aed', borderRadius: [0, 4, 4, 0] } }],
  }), [copy.requestSeries, data?.models])

  if (loading && !data) {
    return (
      <Card>
        <CardContent className="flex min-h-40 items-center justify-center gap-2 p-6 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" />
          {copy.loading}
        </CardContent>
      </Card>
    )
  }

  if (error && !data) {
    return <Card><CardContent className="p-6 text-sm text-destructive">{copy.loadFailed(error)}</CardContent></Card>
  }

  if (!data) return null

  return (
    <section className="space-y-4">
      <div>
        <h3 className="flex items-center gap-2 text-lg font-semibold">
          <Database className="h-5 w-5 text-primary" />
          {copy.title}
        </h3>
        <p className="mt-1 text-sm text-muted-foreground">{copy.hint}</p>
      </div>

      {error && <div className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">{copy.loadFailed(error)}</div>}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric icon={Users} label={copy.users} value={data.overview.total_users} hint={copy.activeUsers + ': ' + formatNumber(data.overview.active_users_24h, locale)} color="bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300" locale={locale} />
        <Metric icon={Activity} label={copy.activeUsers} value={data.overview.active_users_24h} hint={copy.users + ': ' + formatNumber(data.overview.total_users, locale)} color="bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300" locale={locale} />
        <Metric icon={KeyRound} label={copy.tokens} value={data.overview.total_tokens} hint={copy.activeTokens + ': ' + formatNumber(data.overview.active_tokens_24h, locale)} color="bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300" locale={locale} />
        <Metric icon={Network} label={copy.activeTokens} value={data.overview.active_tokens_24h} hint={copy.tokens + ': ' + formatNumber(data.overview.total_tokens, locale)} color="bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300" locale={locale} />
      </div>

      <Card>
        <CardHeader className="pb-3"><CardTitle className="flex items-center gap-2 text-base"><Activity className="h-4 w-4 text-primary" />{copy.traffic}</CardTitle></CardHeader>
        <CardContent>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
            <Metric icon={Network} label={copy.requests} value={data.usage.requests_24h} color="bg-sky-100 text-sky-700 dark:bg-sky-950 dark:text-sky-300" locale={locale} />
            <Metric icon={KeyRound} label={copy.tokenUsage} value={data.usage.tokens_24h} color="bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-950 dark:text-fuchsia-300" locale={locale} />
            <Metric icon={Database} label={copy.quotaUsage} value={data.usage.quota_used_24h} color="bg-cyan-100 text-cyan-700 dark:bg-cyan-950 dark:text-cyan-300" locale={locale} />
            <Metric icon={Layers3} label={copy.inputTokens} value={data.usage.input_tokens_24h ?? 0} color="bg-slate-100 text-slate-700 dark:bg-slate-900 dark:text-slate-300" locale={locale} />
            <Metric icon={Layers3} label={copy.outputTokens} value={data.usage.output_tokens_24h ?? 0} color="bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300" locale={locale} />
          </div>
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-base">{copy.daily}</CardTitle></CardHeader>
          <CardContent>{data.daily.length ? <ReactECharts option={dailyOption} style={{ height: 270 }} notMerge lazyUpdate /> : <EmptyChart message={copy.noData} />}</CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-base">{copy.hourly}</CardTitle></CardHeader>
          <CardContent>{data.hourly.length ? <ReactECharts option={hourlyOption} style={{ height: 270 }} notMerge lazyUpdate /> : <EmptyChart message={copy.noData} />}</CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="flex items-center gap-2 text-base"><Layers3 className="h-4 w-4 text-primary" />{copy.models}</CardTitle></CardHeader>
        <CardContent>{data.models.length ? <ReactECharts option={modelOption} style={{ height: Math.max(260, data.models.length * 30) }} notMerge lazyUpdate /> : <EmptyChart message={copy.noData} />}</CardContent>
      </Card>
    </section>
  )
}
