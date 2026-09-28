import { useState } from 'react'
import { Box } from 'lucide-react'
import { DASHBOARD_TEXT, localeOf, type DashboardLang } from '../lib/dashboardI18n'
import type { BusinessFinance, BusinessModelRow, SectionState } from '../lib/businessDashboard'
import { MiniTable, SectionShell, money, num, pctValue, type Column } from './BusinessBits'
import { cn } from '../lib/utils'

type RankKey = 'billed' | 'cost' | 'profit'

/**
 * 模型 Top 10,按计费额 / 供应商成本 / 毛利三种排法。计费为 0 的免费模型
 * (如 jev-1.13-free)由后端排除,脚注里列出它们的调用量与成本,免得成本被藏起来。
 */
export function BusinessModelRanking({ state, lang }: { state: SectionState<BusinessFinance>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const [rank, setRank] = useState<RankKey>('billed')
  const models = state.data?.models
  const rows = models ? { billed: models.by_billed, cost: models.by_cost, profit: models.by_profit }[rank] ?? [] : []

  const columns: Column<BusinessModelRow>[] = [
    { header: t.colModel, cell: (r) => <span className="font-medium break-all">{r.model}</span> },
    { header: t.colRequests, align: 'right', cell: (r) => num(r.requests, locale) },
    { header: t.colBilled, align: 'right', cell: (r) => money(r.billed_usd) },
    { header: t.colCost, align: 'right', cell: (r) => money(r.provider_cost_usd) },
    { header: t.colRevenue, align: 'right', cell: (r) => money(r.realized_revenue_usd) },
    {
      header: t.colProfit,
      align: 'right',
      cell: (r) => <span className={r.gross_profit_usd < 0 ? 'text-red-600' : 'text-emerald-600'}>{money(r.gross_profit_usd)}</span>,
    },
    { header: t.colMargin, align: 'right', cell: (r) => pctValue(r.margin_percent) },
  ]

  const tabs = (
    <div className="flex items-center rounded-lg bg-muted p-0.5 text-sm shrink-0">
      {(['billed', 'cost', 'profit'] as RankKey[]).map((key) => (
        <button
          key={key}
          onClick={() => setRank(key)}
          className={cn(
            'px-3 py-1.5 rounded-md transition-colors whitespace-nowrap',
            rank === key ? 'bg-background shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {t.modelsBy[key]}
        </button>
      ))}
    </div>
  )

  return (
    <SectionShell
      title={t.modelsTitle}
      icon={Box}
      action={tabs}
      loading={state.loading}
      error={state.error}
      hasData={!!models}
      errorText={t.loadFailed}
    >
      <MiniTable rows={rows} columns={columns} rowKey={(r) => r.model} empty={DASHBOARD_TEXT[lang].noData} />
      {models && models.excluded_free_models.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {t.freeExcluded(
            models.excluded_free_models.join(', '),
            num(models.excluded_free_requests, locale),
            money(models.excluded_free_cost_usd),
          )}
        </p>
      )}
    </SectionShell>
  )
}
