import { useState, useEffect, useCallback, useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import { useAuth } from '../contexts/AuthContext'
import { UserPlus, Users, CreditCard, UserCheck, Wallet, DollarSign, TrendingUp, Loader2 } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { cn } from '../lib/utils'
import {
  DASHBOARD_TEXT,
  formatCny,
  formatTrendLabel,
  localeOf,
  usd,
  usdAxis,
  type DashboardLang,
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

type Granularity = 'daily' | 'monthly'

export function GrowthPanel({ refreshToken, lang }: { refreshToken?: number; lang: DashboardLang }) {
  const { token } = useAuth()
  const apiUrl = import.meta.env.VITE_API_URL || ''
  const t = DASHBOARD_TEXT[lang]
  const locale = localeOf(lang)

  const [metrics, setMetrics] = useState<GrowthMetrics | null>(null)
  const [trend, setTrend] = useState<GrowthPoint[]>([])
  const [granularity, setGranularity] = useState<Granularity>('daily')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const headers = useMemo(
    () => ({ 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }),
    [token],
  )

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setError(null)
        const [m, t] = await Promise.all([
          fetch(`${apiUrl}/api/dashboard/growth`, { headers, signal }),
          fetch(`${apiUrl}/api/dashboard/growth/trend?granularity=${granularity}`, { headers, signal }),
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
    [apiUrl, headers, granularity],
  )

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load, refreshToken])

  const option = useMemo(() => {
    const labels = trend.map((p) => formatTrendLabel(p.date, granularity, lang))
    return {
      grid: { left: 48, right: 56, top: 24, bottom: 32, containLabel: true },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'line' },
        formatter: (params: Array<{ axisValue: string; seriesName: string; value: number; color: string }>) => {
          const head = `<div style="font-weight:600;margin-bottom:4px">${params[0]?.axisValue ?? ''}</div>`
          return (
            head +
            params
              .map((p) => {
                // 比的是同一个常量,不是字面量 —— 换语言后 series 名字变了,
                // 写死中文会让金额那条悄悄退回成裸数字。
                const value = p.seriesName === t.series.revenue ? usd.format(p.value ?? 0) : `${p.value ?? 0}`
                return `<div style="display:flex;align-items:center;gap:6px">
                  <span style="width:8px;height:8px;border-radius:2px;background:${p.color}"></span>
                  <span style="flex:1">${p.seriesName}</span>
                  <span style="font-weight:600">${value}</span>
                </div>`
              })
              .join('')
          )
        },
      },
      legend: { bottom: 0, itemWidth: 10, itemHeight: 10, icon: 'roundRect' },
      xAxis: {
        type: 'category',
        data: labels,
        boundaryGap: false,
        axisLine: { lineStyle: { color: 'hsl(var(--border))' } },
        axisTick: { show: false },
        axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 },
      },
      yAxis: [
        {
          type: 'value',
          minInterval: 1,
          splitLine: { lineStyle: { type: 'dashed', color: 'hsl(var(--border))' } },
          axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 },
        },
        {
          // Revenue gets its own axis. On one axis a $10 order is invisible
          // next to 261 signups, which is exactly the day worth seeing.
          type: 'value',
          splitLine: { show: false },
          axisLabel: {
            color: 'hsl(var(--muted-foreground))',
            fontSize: 11,
            formatter: (v: number) => usdAxis.format(v),
          },
        },
      ],
      series: [
        {
          name: t.series.newUsers,
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: trend.map((p) => p.new_users),
          itemStyle: { color: '#f97316' },
          areaStyle: { opacity: 0.12 },
        },
        {
          name: t.series.newPayers,
          type: 'line',
          smooth: true,
          symbolSize: 7,
          // Only draw a marker where someone actually started paying — a line
          // of dots at zero reads as activity that did not happen.
          showSymbol: true,
          data: trend.map((p) => (p.new_payers > 0 ? p.new_payers : null)),
          connectNulls: false,
          itemStyle: { color: '#14b8a6' },
        },
        {
          name: t.series.revenue,
          type: 'line',
          smooth: true,
          showSymbol: false,
          yAxisIndex: 1,
          data: trend.map((p) => p.revenue),
          itemStyle: { color: '#1e4e5f' },
        },
      ],
    }
  }, [trend, granularity, lang, t])

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

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0">
          <CardTitle className="text-lg flex items-center gap-2">
            <TrendingUp className="w-5 h-5 text-primary" />
            {t.growthTrend}
          </CardTitle>
          <div className="flex items-center rounded-lg bg-muted p-0.5 text-sm">
            {(['daily', 'monthly'] as Granularity[]).map((g) => (
              <button
                key={g}
                onClick={() => setGranularity(g)}
                className={cn(
                  'px-3 py-1.5 rounded-md transition-colors',
                  granularity === g
                    ? 'bg-background shadow-sm font-medium'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {g === 'daily' ? t.last30Days : t.last12Months}
              </button>
            ))}
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="h-[320px] flex items-center justify-center text-muted-foreground">
              <Loader2 className="w-5 h-5 animate-spin" />
            </div>
          ) : (
            <ReactECharts option={option} style={{ height: 320 }} notMerge lazyUpdate />
          )}

          <div className="mt-4 -mx-6 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-y text-muted-foreground">
                  <th className="text-left font-normal py-2 px-6">{t.colDate}</th>
                  <th className="text-right font-normal py-2 px-3">{t.series.newUsers}</th>
                  <th className="text-right font-normal py-2 px-3">{t.series.newPayers}</th>
                  <th className="text-right font-normal py-2 px-6">{t.series.revenue}</th>
                </tr>
              </thead>
              <tbody>
                {[...trend].reverse().map((p) => (
                  <tr key={p.date} className="border-b last:border-0 hover:bg-muted/40">
                    <td className="py-2 px-6 tabular-nums">{p.date}</td>
                    <td
                      className={cn('py-2 px-3 text-right tabular-nums', !p.new_users && 'text-muted-foreground')}
                    >
                      {p.new_users}
                    </td>
                    <td
                      className={cn('py-2 px-3 text-right tabular-nums', !p.new_payers && 'text-muted-foreground')}
                    >
                      {p.new_payers}
                    </td>
                    <td
                      className={cn('py-2 px-6 text-right tabular-nums', !p.revenue && 'text-muted-foreground')}
                    >
                      {usd.format(p.revenue || 0)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
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
