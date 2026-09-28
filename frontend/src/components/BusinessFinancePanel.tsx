import { useMemo } from 'react'
import ReactECharts from 'echarts-for-react'
import { DollarSign, AlertTriangle } from 'lucide-react'
import { DASHBOARD_TEXT, formatTrendLabel, localeOf, usdAxis, type DashboardLang } from '../lib/dashboardI18n'
import type { BusinessFinance, SectionState } from '../lib/businessDashboard'
import { SectionShell, Stat, money, num, pctValue } from './BusinessBits'

/**
 * 收入 / 计费 / 成本 / 毛利。口径与毛利页相同(MarginAnalysisService),
 * 计费额单列并标明是内部转移价 —— 它不是收入。
 */
export function BusinessFinancePanel({ state, lang }: { state: SectionState<BusinessFinance>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const data = state.data
  const m = data?.margin
  const cash = data?.cash

  return (
    <SectionShell
      title={t.financeTitle}
      icon={DollarSign}
      hint={t.financeHint}
      loading={state.loading}
      error={state.error}
      hasData={!!data}
      errorText={t.loadFailed}
    >
      {m && cash && (
        <>
          <div className="grid grid-cols-1 xs:grid-cols-2 lg:grid-cols-3 gap-3">
            <Stat label={t.billed} value={money(m.billed_usd)} sub={t.billedSub(num(m.requests, locale))} tone="muted" />
            <Stat
              label={t.providerCost}
              value={money(m.provider_cost_usd)}
              sub={t.providerCostSub(money(m.paid_traffic_cost_usd), money(m.gift_and_free_cost_usd))}
              tone="warning"
            />
            <Stat
              label={t.cashIn}
              value={money(cash.revenue_usd)}
              sub={t.cashInSub(cash.orders, cash.payers) + (cash.unknown_currency_orders > 0 ? t.unknownCurrency(cash.unknown_currency_orders) : '')}
            />
            <Stat label={t.realizedRevenue} value={money(m.realized_revenue_usd)} sub={t.realizedRevenueSub} tone="positive" />
            <Stat
              label={t.grossProfit}
              value={money(m.gross_profit_usd)}
              sub={t.grossProfitSub(pctValue(m.gross_margin_percent))}
              tone={m.gross_profit_usd >= 0 ? 'positive' : 'danger'}
            />
            <Stat
              label={t.externalProfit}
              value={money(m.external_profit_usd)}
              sub={t.externalProfitSub(pctValue(m.external_margin_percent), money(m.internal_cost_usd))}
              tone={m.external_profit_usd >= 0 ? 'positive' : 'danger'}
            />
          </div>

          {m.unpriced_calls > 0 && (
            <div className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300">
              <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
              {t.unpricedWarning(num(m.unpriced_calls, locale))}
            </div>
          )}

          {data.daily.length > 1 && <FinanceTrend data={data} lang={lang} />}
        </>
      )}
    </SectionShell>
  )
}

function FinanceTrend({ data, lang }: { data: BusinessFinance; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const option = useMemo(() => {
    const s = t.financeSeries
    const line = (name: string, values: number[], color: string) => ({
      name,
      type: 'line',
      smooth: true,
      showSymbol: false,
      data: values,
      itemStyle: { color },
    })
    return {
      grid: { left: 16, right: 16, top: 16, bottom: 32, containLabel: true },
      tooltip: {
        trigger: 'axis',
        valueFormatter: (v: number) => `$${(v ?? 0).toFixed(2)}`,
      },
      legend: { bottom: 0, itemWidth: 10, itemHeight: 10, icon: 'roundRect' },
      xAxis: {
        type: 'category',
        data: data.daily.map((d) => formatTrendLabel(d.date, 'daily', lang)),
        boundaryGap: true,
        axisLine: { lineStyle: { color: 'hsl(var(--border))' } },
        axisTick: { show: false },
        axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        splitLine: { lineStyle: { type: 'dashed', color: 'hsl(var(--border))' } },
        axisLabel: { color: 'hsl(var(--muted-foreground))', fontSize: 11, formatter: (v: number) => usdAxis.format(v) },
      },
      series: [
        { name: s.cash, type: 'bar', barMaxWidth: 18, data: data.daily.map((d) => d.cash_revenue_usd), itemStyle: { color: '#93c5fd' } },
        line(s.billed, data.daily.map((d) => d.billed_usd), '#94a3b8'),
        line(s.cost, data.daily.map((d) => d.provider_cost_usd), '#f59e0b'),
        line(s.revenue, data.daily.map((d) => d.realized_revenue_usd), '#10b981'),
      ],
    }
  }, [data, lang, t])

  return (
    <div>
      <div className="text-sm font-medium text-muted-foreground mb-2">{t.daily}</div>
      <ReactECharts option={option} style={{ height: 260 }} notMerge lazyUpdate />
    </div>
  )
}
