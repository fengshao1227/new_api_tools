import { useState, useEffect, useCallback, useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import { useAuth } from '../contexts/AuthContext'
import { UserPlus, Users, CreditCard, UserCheck, Wallet, DollarSign, TrendingUp, Loader2 } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { cn } from '../lib/utils'

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

const cny = new Intl.NumberFormat('zh-CN', {
  style: 'currency',
  currency: 'CNY',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

interface GrowthPoint {
  date: string
  new_users: number
  new_payers: number
  orders: number
  revenue: number
}

type Granularity = 'daily' | 'monthly'

const currency = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

// Whole numbers on the axis and compact ones in the table: a revenue axis
// labelled $12.50 and $12.75 says nothing an operator can act on.
const axisCurrency = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 0,
})

export function GrowthPanel({ refreshToken }: { refreshToken?: number }) {
  const { token } = useAuth()
  const apiUrl = import.meta.env.VITE_API_URL || ''

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
    const labels = trend.map((p) =>
      granularity === 'monthly'
        ? `${Number(p.date.slice(5, 7))} 月`
        : `${Number(p.date.slice(5, 7))}月${Number(p.date.slice(8, 10))}日`,
    )
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
                const value = p.seriesName === '收入（USD）' ? currency.format(p.value ?? 0) : `${p.value ?? 0}`
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
            formatter: (v: number) => axisCurrency.format(v),
          },
        },
      ],
      series: [
        {
          name: '新注册用户',
          type: 'line',
          smooth: true,
          showSymbol: false,
          data: trend.map((p) => p.new_users),
          itemStyle: { color: '#f97316' },
          areaStyle: { opacity: 0.12 },
        },
        {
          name: '新增付费用户',
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
          name: '收入（USD）',
          type: 'line',
          smooth: true,
          showSymbol: false,
          yAxisIndex: 1,
          data: trend.map((p) => p.revenue),
          itemStyle: { color: '#1e4e5f' },
        },
      ],
    }
  }, [trend, granularity])

  if (error) {
    return (
      <Card>
        <CardContent className="p-6 text-sm text-destructive">增长数据加载失败：{error}</CardContent>
      </Card>
    )
  }

  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <h3 className="text-sm font-medium text-muted-foreground">用户</h3>
        <div className="grid grid-cols-1 xs:grid-cols-2 lg:grid-cols-4 gap-3 sm:gap-4">
          <MetricCard title="本月注册" value={metrics?.month_users} icon={UserPlus} loading={loading} />
          <MetricCard title="累计注册" value={metrics?.total_users} icon={Users} loading={loading} />
          <MetricCard title="本月付费用户" value={metrics?.month_payers} icon={CreditCard} loading={loading} />
          <MetricCard title="累计付费用户" value={metrics?.total_payers} icon={UserCheck} loading={loading} />
        </div>
      </section>

      <section className="space-y-3">
        <h3 className="text-sm font-medium text-muted-foreground">收入</h3>
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-3 sm:gap-4">
          <MetricCard title="本月收入" value={metrics?.month_revenue} icon={Wallet} loading={loading} money />
          <MetricCard
            title="累计收入"
            value={metrics?.total_revenue}
            icon={DollarSign}
            loading={loading}
            money
            // Say that a conversion happened and at what rate. The alternative
            // is a USD figure that quietly contains yuan, which is the bug this
            // card was changed to fix.
            hint={
              metrics
                ? `${metrics.settled_orders} 笔已结算` +
                  (metrics.total_revenue_cny > 0
                    ? ` · 其中 ${cny.format(metrics.total_revenue_cny)} 按 ¥${metrics.cny_per_usd}/$ 折算`
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
            增长趋势
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
                {g === 'daily' ? '近 30 天' : '近 12 个月'}
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
                  <th className="text-left font-normal py-2 px-6">日期</th>
                  <th className="text-right font-normal py-2 px-3">新注册用户</th>
                  <th className="text-right font-normal py-2 px-3">新增付费用户</th>
                  <th className="text-right font-normal py-2 px-6">收入（USD）</th>
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
                      {currency.format(p.revenue || 0)}
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
}: {
  title: string
  value?: number
  icon: React.ComponentType<{ className?: string }>
  loading?: boolean
  money?: boolean
  hint?: string
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
            currency.format(value ?? 0)
          ) : (
            (value ?? 0).toLocaleString()
          )}
        </div>
        {hint && !loading && <div className="mt-1 text-xs text-muted-foreground">{hint}</div>}
      </CardContent>
    </Card>
  )
}
