import { useState, useEffect, useCallback, useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import { useAuth } from '../contexts/AuthContext'
import { UserPlus, Users, CreditCard, UserCheck, Wallet, DollarSign, Loader2 } from 'lucide-react'
import { Card, CardContent } from './ui/card'
import { cn } from '../lib/utils'
import {
  DASHBOARD_TEXT,
  formatCny,
  formatTrendLabel,
  localeOf,
  usd,
  usdAxis,
  type DashboardLang,
  type GrowthRange,
} from '../lib/dashboardI18n'

interface GrowthMetrics {
  total_users: number
  month_users: number
  total_payers: number
  month_payers: number
  total_revenue: number
  month_revenue: number
  total_revenue_cny: number
  cny_per_usd: number
  settled_orders: number
}

interface GrowthPoint {
  date: string
  new_users: number
  new_payers: number
  orders: number
  revenue: number
}

type MetricKey = 'users' | 'payers' | 'revenue'

const RANGES: GrowthRange[] = ['7d', '30d', 'all']
const METRICS: MetricKey[] = ['users', 'payers', 'revenue']

// 7D is the tail of the 30-day series; only "all" needs its own query.
const RANGE_QUERY: Record<GrowthRange, string> = { '7d': 'daily', '30d': 'daily', all: 'all' }

const METRIC_STYLE: Record<MetricKey, { color: string; pick: (p: GrowthPoint) => number }> = {
  users: { color: '#6366f1', pick: (p) => p.new_users },
  payers: { color: '#f97316', pick: (p) => p.new_payers },
  revenue: { color: '#10b981', pick: (p) => p.revenue },
}

// ECharts draws on a canvas, which cannot resolve CSS variables: an
// `hsl(var(--border))` there silently paints black. These read on light and
// dark backgrounds alike.
const AXIS_GREY = '#94a3b8'
const GRID_GREY = 'rgba(148, 163, 184, 0.18)'

const compact = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 })

export function GrowthPanel({ refreshToken, lang }: { refreshToken?: number; lang: DashboardLang }) {
  const { token } = useAuth()
  const apiUrl = import.meta.env.VITE_API_URL || ''
  const t = DASHBOARD_TEXT[lang]
  const locale = localeOf(lang)

  const [metrics, setMetrics] = useState<GrowthMetrics | null>(null)
  const [trend, setTrend] = useState<GrowthPoint[]>([])
  const [range, setRange] = useState<GrowthRange>('30d')
  const [metric, setMetric] = useState<MetricKey>('users')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const headers = useMemo(
    () => ({ 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }),
    [token],
  )
  const query = RANGE_QUERY[range]

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setError(null)
        const [m, t] = await Promise.all([
          fetch(`${apiUrl}/api/dashboard/growth`, { headers, signal }),
          fetch(`${apiUrl}/api/dashboard/growth/trend?granularity=${query}`, { headers, signal }),
        ])
        if (!m.ok || !t.ok) throw new Error(`HTTP ${m.ok ? t.status : m.status}`)
        const mj = await m.json()
        const tj = await t.json()
        if (mj.success) setMetrics(mj.data)
        if (tj.success) setTrend(tj.data ?? [])
      } catch (e) {
        if ((e as Error).name === 'AbortError') return
        setError((e as Error).message)
      } finally {
        setLoading(false)
      }
    },
    [apiUrl, headers, query],
  )

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load, refreshToken])

  const points = useMemo(() => (range === '7d' ? trend.slice(-7) : trend), [trend, range])

  // The trend endpoint returns what each day added. A running total needs the
  // count from before the window too: that is the all-time total in the
  // metrics above minus what the window added, so the last point of every
  // series lands exactly on the number in its metric card.
  const series = useMemo(() => {
    const totals: Record<MetricKey, number | undefined> = {
      users: metrics?.total_users,
      payers: metrics?.total_payers,
      revenue: metrics?.total_revenue,
    }
    const out = {} as Record<MetricKey, { added: number; total: number; daily: number[]; points: number[] }>
    for (const key of METRICS) {
      const daily = points.map((p) => METRIC_STYLE[key].pick(p) || 0)
      const added = daily.reduce((a, b) => a + b, 0)
      let running = Math.max(0, (totals[key] ?? added) - added)
      const cumulative = daily.map((v) => (running += v))
      out[key] = { added, total: cumulative[cumulative.length - 1] ?? totals[key] ?? 0, daily, points: cumulative }
    }
    return out
  }, [points, metrics])

  const formatValue = useCallback(
    (key: MetricKey, v: number) => (key === 'revenue' ? usdAxis.format(v) : Math.round(v).toLocaleString(locale)),
    [locale],
  )

  const option = useMemo(() => {
    const { color } = METRIC_STYLE[metric]
    const s = series[metric]
    const labels = points.map((p) => formatTrendLabel(p.date, 'daily', lang))
    const last = s.points.length - 1
    // About five dates along the axis, always including both ends.
    const step = Math.max(1, Math.round(last / 4))
    return {
      animationDuration: 700,
      animationEasing: 'cubicOut',
      grid: { left: 8, right: 8, top: 16, bottom: 8, containLabel: true },
      tooltip: {
        trigger: 'axis',
        backgroundColor: 'rgba(255, 255, 255, 0.96)',
        borderColor: 'rgba(148, 163, 184, 0.3)',
        borderWidth: 1,
        padding: [10, 12],
        textStyle: { color: '#0f172a', fontSize: 12 },
        extraCssText: 'border-radius:10px;box-shadow:0 8px 24px rgba(15,23,42,0.08);',
        axisPointer: { type: 'line', lineStyle: { color: AXIS_GREY, type: 'dashed', opacity: 0.6 } },
        formatter: (params: Array<{ axisValue: string; dataIndex: number }>) => {
          const p = params[0]
          if (!p) return ''
          const day = s.daily[p.dataIndex] ?? 0
          return `<div style="color:#64748b;font-size:11px;margin-bottom:4px">${p.axisValue}</div>
            <div style="font-size:15px;font-weight:600;font-variant-numeric:tabular-nums">${formatValue(metric, s.points[p.dataIndex] ?? 0)}</div>
            ${day > 0 ? `<div style="color:${color};font-size:11px;margin-top:2px">${t.dayAdded(formatValue(metric, day))}</div>` : ''}`
        },
      },
      xAxis: {
        type: 'category',
        data: labels,
        boundaryGap: false,
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: {
          color: AXIS_GREY,
          fontSize: 11,
          margin: 14,
          interval: (i: number) => i === 0 || i === last || (i % step === 0 && last - i > step / 2),
          alignMinLabel: 'left',
          alignMaxLabel: 'right',
        },
      },
      yAxis: {
        type: 'value',
        min: 0,
        splitNumber: 3,
        position: 'right',
        axisLabel: {
          color: AXIS_GREY,
          fontSize: 11,
          formatter: (v: number) => (metric === 'revenue' ? `$${compact.format(v)}` : compact.format(v)),
        },
        splitLine: { lineStyle: { color: GRID_GREY } },
      },
      series: [
        {
          type: 'line',
          // A running total never dips; monotone smoothing keeps the curve
          // from drawing a dip between two equal days.
          smooth: 0.35,
          smoothMonotone: 'x',
          symbol: 'none',
          // Mark only today's point: where the line ends is the headline.
          data: s.points.map((v, i) =>
            i === last
              ? {
                  value: v,
                  symbol: 'circle',
                  symbolSize: 9,
                  itemStyle: { color, borderColor: '#fff', borderWidth: 2.5, shadowBlur: 10, shadowColor: `${color}80` },
                }
              : v,
          ),
          lineStyle: { width: 2.75, color, shadowBlur: 12, shadowColor: `${color}33`, shadowOffsetY: 6 },
          itemStyle: { color },
          areaStyle: {
            color: {
              type: 'linear',
              x: 0,
              y: 0,
              x2: 0,
              y2: 1,
              colorStops: [
                { offset: 0, color: `${color}33` },
                { offset: 1, color: `${color}00` },
              ],
            },
          },
        },
      ],
    }
  }, [metric, series, points, lang, formatValue, t])

  if (error) {
    return (
      <Card>
        <CardContent className="p-6 text-sm text-destructive">{t.growthError(error)}</CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <h3 className="text-sm font-medium text-muted-foreground">{t.growthUsers}</h3>
        <div className="grid grid-cols-1 xs:grid-cols-2 lg:grid-cols-4 gap-3 sm:gap-4">
          <MetricCard title={t.monthUsers} value={metrics?.month_users} icon={UserPlus} loading={loading} locale={locale} />
          <MetricCard title={t.totalUsers} value={metrics?.total_users} icon={Users} loading={loading} locale={locale} />
          <MetricCard title={t.monthPayers} value={metrics?.month_payers} icon={CreditCard} loading={loading} locale={locale} />
          <MetricCard title={t.totalPayers} value={metrics?.total_payers} icon={UserCheck} loading={loading} locale={locale} />
        </div>
      </section>

      <section className="space-y-3">
        <h3 className="text-sm font-medium text-muted-foreground">{t.growthRevenue}</h3>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-3 sm:gap-4">
          <MetricCard title={t.monthRevenue} value={metrics?.month_revenue} icon={Wallet} loading={loading} locale={locale} money />
          <MetricCard
            title={t.totalRevenue}
            value={metrics?.total_revenue}
            icon={DollarSign}
            loading={loading}
            locale={locale}
            money
            // Say that a conversion happened and at what rate. The alternative
            // is a USD figure that quietly contains yuan, which is the bug this
            // card was changed to fix.
            hint={
              metrics
                ? t.settledOrders(metrics.settled_orders) +
                  (metrics.total_revenue_cny > 0
                    ? t.convertedFrom(formatCny(lang, metrics.total_revenue_cny), metrics.cny_per_usd)
                    : '')
                : undefined
            }
          />
        </div>
      </section>

      <Card className="overflow-hidden rounded-2xl border-border/60 shadow-[0_1px_2px_rgba(15,23,42,0.04),0_12px_32px_-12px_rgba(15,23,42,0.10)]">
        <div className="flex flex-wrap items-center justify-between gap-3 px-6 pt-6">
          <div>
            <div className="text-base font-semibold tracking-tight">{t.growthTrend}</div>
            <div className="text-xs text-muted-foreground mt-0.5">{t.cumulativeNote}</div>
          </div>
          <div className="flex items-center rounded-full bg-muted/70 p-1 text-xs font-medium">
            {RANGES.map((r) => (
              <button
                key={r}
                onClick={() => setRange(r)}
                className={cn(
                  'min-w-[48px] px-3 py-1.5 rounded-full transition-all',
                  range === r
                    ? 'bg-background text-foreground shadow-sm'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {t.range[r]}
              </button>
            ))}
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-3 gap-2 px-4 pt-5">
          {METRICS.map((key) => {
            const active = metric === key
            const { color } = METRIC_STYLE[key]
            const s = series[key]
            return (
              <button
                key={key}
                onClick={() => setMetric(key)}
                className={cn(
                  'relative text-left rounded-xl px-4 py-3.5 transition-colors',
                  active ? 'bg-muted/50' : 'hover:bg-muted/30',
                )}
              >
                <div className="flex items-center gap-2 text-[13px] text-muted-foreground">
                  <span
                    className="h-2 w-2 rounded-full transition-opacity"
                    style={{ background: color, opacity: active ? 1 : 0.35 }}
                  />
                  {t.cumulative[key]}
                </div>
                <div className="mt-1.5 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
                  {loading ? (
                    <span className="inline-block h-8 w-20 rounded bg-muted animate-pulse" />
                  ) : (
                    <span
                      className={cn(
                        'text-[28px] leading-none font-semibold tracking-tight tabular-nums transition-colors',
                        !active && 'text-foreground/70',
                      )}
                    >
                      {formatValue(key, s.total)}
                    </span>
                  )}
                  {!loading && s.added > 0 && (
                    <span className="text-xs font-medium tabular-nums" style={{ color }}>
                      {t.deltaIn(formatValue(key, s.added), range)}
                    </span>
                  )}
                </div>
                <span
                  className="absolute left-4 right-4 bottom-0 h-[2px] rounded-full transition-opacity"
                  style={{ background: color, opacity: active ? 1 : 0 }}
                />
              </button>
            )
          })}
        </div>

        <div className="px-4 pb-4 pt-2">
          {loading ? (
            <div className="h-[340px] flex items-center justify-center text-muted-foreground">
              <Loader2 className="w-5 h-5 animate-spin" />
            </div>
          ) : (
            <ReactECharts option={option} style={{ height: 340 }} notMerge lazyUpdate />
          )}
        </div>
      </Card>

      <Card className="rounded-2xl border-border/60">
        <div className="px-6 pt-5 pb-3 text-sm font-medium">{t.dailyBreakdown}</div>
        <div className="overflow-x-auto max-h-[420px] overflow-y-auto">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-card">
              <tr className="border-y text-muted-foreground">
                <th className="text-left font-normal py-2 px-6">{t.colDate}</th>
                <th className="text-right font-normal py-2 px-3">{t.series.newUsers}</th>
                <th className="text-right font-normal py-2 px-3">{t.series.newPayers}</th>
                <th className="text-right font-normal py-2 px-6">{t.series.revenue}</th>
              </tr>
            </thead>
            <tbody>
              {[...points].reverse().map((p) => (
                <tr key={p.date} className="border-b last:border-0 hover:bg-muted/40">
                  <td className="py-2 px-6 tabular-nums">{p.date}</td>
                  <td className={cn('py-2 px-3 text-right tabular-nums', !p.new_users && 'text-muted-foreground')}>
                    {p.new_users}
                  </td>
                  <td className={cn('py-2 px-3 text-right tabular-nums', !p.new_payers && 'text-muted-foreground')}>
                    {p.new_payers}
                  </td>
                  <td className={cn('py-2 px-6 text-right tabular-nums', !p.revenue && 'text-muted-foreground')}>
                    {usd.format(p.revenue || 0)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  )
}

function MetricCard({
  title,
  value,
  icon: Icon,
  loading,
  money,
  hint,
  locale,
}: {
  title: string
  value?: number
  icon: React.ComponentType<{ className?: string }>
  loading?: boolean
  money?: boolean
  hint?: string
  locale: string
}) {
  return (
    <Card className="glass-card">
      <CardContent className="p-5">
        <div className="flex items-start justify-between">
          <span className="text-sm text-muted-foreground">{title}</span>
          <Icon className="w-4 h-4 text-muted-foreground/60" />
        </div>
        <div className="mt-3 text-3xl font-semibold tabular-nums">
          {loading ? (
            <span className="inline-block h-8 w-20 rounded bg-muted animate-pulse align-middle" />
          ) : money ? (
            usd.format(value ?? 0)
          ) : (
            (value ?? 0).toLocaleString(locale)
          )}
        </div>
        {hint && !loading && <div className="mt-1 text-xs text-muted-foreground">{hint}</div>}
      </CardContent>
    </Card>
  )
}
