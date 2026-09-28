import { ListChecks, Server, Tags, AlertTriangle } from 'lucide-react'
import { Badge } from './ui/badge'
import { DASHBOARD_TEXT, localeOf, type DashboardLang } from '../lib/dashboardI18n'
import type {
  AlertRow,
  BusinessPricingGaps,
  BusinessSupply,
  BusinessTasks,
  PricingGapChannel,
  SectionState,
  TaskRow,
  UpstreamRow,
} from '../lib/businessDashboard'
import { MiniTable, SectionShell, Stat, money, num, pct, unixTime, type Column } from './BusinessBits'
import { cn } from '../lib/utils'

/** 异步任务健康:按平台 / 模型的成败、失败原因 Top、退款。 */
export function BusinessTasksPanel({ state, lang }: { state: SectionState<BusinessTasks>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const d = state.data
  const columns: Column<TaskRow>[] = [
    { header: t.colPlatform, cell: (r) => <span className="font-medium">{r.platform || '—'}</span> },
    { header: t.colModel, cell: (r) => <span className="break-all">{r.model || '—'}</span> },
    { header: t.colTotal, align: 'right', cell: (r) => num(r.total, locale) },
    { header: t.colSuccess, align: 'right', cell: (r) => num(r.success, locale) },
    { header: t.colFailure, align: 'right', cell: (r) => <span className={r.failure > 0 ? 'text-red-600' : ''}>{num(r.failure, locale)}</span> },
    { header: t.colUserFailure, align: 'right', cell: (r) => <span className="text-muted-foreground">{num(r.user_failure, locale)}</span> },
    { header: t.failureRate, align: 'right', cell: (r) => pct(r.failure_rate) },
  ]

  return (
    <SectionShell title={t.tasksTitle} icon={ListChecks} loading={state.loading} error={state.error} hasData={!!d} errorText={t.loadFailed}>
      {d && (
        <>
          {d.available ? (
            <>
              <div className="grid grid-cols-2 lg:grid-cols-6 gap-3">
                <Stat label={t.tasksTotal} value={num(d.total, locale)} />
                <Stat label={t.tasksFailed} value={num(d.failure, locale)} tone={d.failure > 0 ? 'danger' : 'default'} />
                <Stat label={t.tasksUserFailed} value={num(d.user_failure, locale)} sub={t.tasksUserFailedHint} />
                <Stat label={t.failureRate} value={pct(d.failure_rate)} sub={t.failureRateHint} tone={d.failure_rate > 0.1 ? 'danger' : 'default'} />
                <Stat label={t.tasksInFlight} value={num(d.in_flight, locale)} />
                <Stat label={t.refunds} value={money(d.refund_usd)} sub={t.refundsSub(d.refund_count)} />
              </div>
              <MiniTable rows={d.rows} columns={columns} rowKey={(r) => `${r.platform}|${r.model}`} empty={DASHBOARD_TEXT[lang].noData} />
              <div className="grid gap-4 md:grid-cols-2">
                <ReasonList title={t.failReasons} reasons={d.reasons} empty={t.noFailures} locale={locale} />
                <ReasonList title={t.userFailReasons} reasons={d.user_reasons ?? []} empty={t.noFailures} locale={locale} muted />
              </div>
            </>
          ) : (
            <div className="space-y-3">
              <p className="text-sm text-muted-foreground">{t.unavailable}</p>
              <Stat label={t.refunds} value={money(d.refund_usd)} sub={t.refundsSub(d.refund_count)} />
            </div>
          )}
        </>
      )}
    </SectionShell>
  )
}

function ReasonList({ title, reasons, empty, locale, muted = false }: {
  title: string
  reasons: { reason: string; count: number }[]
  empty: string
  locale: string
  muted?: boolean
}) {
  return (
    <div>
      <div className="text-sm font-medium text-muted-foreground mb-2">{title}</div>
      {reasons.length === 0 ? (
        <p className="text-sm text-muted-foreground">{empty}</p>
      ) : (
        <ul className="space-y-1.5">
          {reasons.map((r) => (
            <li key={r.reason} className="flex items-start justify-between gap-3 text-sm">
              <span className="break-all text-muted-foreground">{r.reason}</span>
              <span className={cn('tabular-nums shrink-0', muted ? 'text-muted-foreground' : 'font-medium')}>{num(r.count, locale)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** 上游账户与未关闭告警。都是当前状态,不随时间窗变。 */
export function BusinessSupplyPanel({ state, lang }: { state: SectionState<BusinessSupply>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const d = state.data
  const columns: Column<UpstreamRow>[] = [
    { header: t.colName, cell: (u) => <span className="font-medium">{u.name}</span> },
    {
      header: t.colBalance,
      align: 'right',
      cell: (u) => (
        <span className={cn(u.low && 'text-red-600 font-medium')}>
          {u.balance.toLocaleString(locale, { maximumFractionDigits: 2 })} {u.currency}
        </span>
      ),
    },
    {
      header: t.colStatus,
      cell: (u) => (
        <div className="flex flex-wrap items-center gap-1">
          <Badge variant={u.status === 1 ? 'success' : u.status === 2 ? 'destructive' : 'secondary'}>{t.status[u.status] ?? u.status}</Badge>
          {u.low && <Badge variant="destructive">{t.lowBalance}</Badge>}
          {u.last_error && (
            <span className="text-xs text-muted-foreground truncate max-w-[220px]" title={u.last_error}>
              {u.last_error}
            </span>
          )}
        </div>
      ),
    },
    { header: t.colChecked, align: 'right', cell: (u) => unixTime(u.last_checked_at || u.updated_at, locale) },
  ]

  return (
    <SectionShell title={t.supplyTitle} icon={Server} hint={t.supplyHint} loading={state.loading} error={state.error} hasData={!!d} errorText={t.loadFailed}>
      {d && (
        <div className="grid grid-cols-1 xl:grid-cols-5 gap-6">
          <div className="xl:col-span-3">
            <div className="text-sm font-medium text-muted-foreground mb-2">{t.upstreams}</div>
            {d.upstream_available ? (
              <MiniTable rows={d.upstreams} columns={columns} rowKey={(u) => String(u.id)} empty={DASHBOARD_TEXT[lang].noData} />
            ) : (
              <p className="text-sm text-muted-foreground">{t.unavailable}</p>
            )}
          </div>
          <div className="xl:col-span-2">
            <div className="text-sm font-medium text-muted-foreground mb-2">{t.alerts}</div>
            {!d.alerts_available ? (
              <p className="text-sm text-muted-foreground">{t.unavailable}</p>
            ) : d.alerts.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t.noAlerts}</p>
            ) : (
              <ul className="space-y-2">
                {d.alerts.map((a: AlertRow) => (
                  <li key={a.key} className="rounded-md border border-amber-500/30 bg-amber-500/5 p-2.5 text-sm">
                    <div className="flex items-start gap-2">
                      <AlertTriangle className="w-4 h-4 text-amber-600 mt-0.5 shrink-0" />
                      <div className="min-w-0">
                        <div className="font-medium break-words">{a.title || a.key}</div>
                        <div className="text-xs text-muted-foreground">
                          {a.kind} · {t.alertSince(unixTime(a.first_at, locale), a.count)}
                        </div>
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </SectionShell>
  )
}

/** 定价缺口:启用渠道里没有成本表达式的模型,以及时间窗内的未定价调用。正常都是 0。 */
export function BusinessPricingGapsPanel({ state, lang }: { state: SectionState<BusinessPricingGaps>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const d = state.data
  const gapCount = d?.channels.length ?? 0
  const columns: Column<PricingGapChannel>[] = [
    { header: t.colChannel, cell: (c) => <span className="font-medium">#{c.id} {c.name}</span> },
    {
      header: t.colModel,
      cell: (c) => (
        <div className="space-y-1">
          <div className="flex flex-wrap gap-1">
            {c.no_cost_expr && <Badge variant="destructive">{t.noCostExpr}</Badge>}
            <span className="text-xs text-muted-foreground">{t.missingModels(c.missing_models.length, c.model_count)}</span>
          </div>
          <div className="text-xs break-all text-red-600">{c.missing_models.join(', ')}</div>
        </div>
      ),
    },
  ]

  return (
    <SectionShell title={t.pricingTitle} icon={Tags} hint={t.pricingHint} loading={state.loading} error={state.error} hasData={!!d} errorText={t.loadFailed}>
      {d && (
        <>
          <div className="grid grid-cols-1 xs:grid-cols-2 gap-3">
            <Stat label={t.gapChannels} value={d.channels_available ? num(gapCount, locale) : '—'} tone={gapCount > 0 ? 'danger' : 'positive'} />
            <Stat
              label={t.unpricedCalls}
              value={num(d.unpriced_calls, locale)}
              sub={t.unpricedSource[d.unpriced_source] || undefined}
              tone={d.unpriced_calls > 0 ? 'danger' : 'positive'}
            />
          </div>
          {!d.channels_available ? (
            <p className="text-sm text-muted-foreground">{t.unavailable}</p>
          ) : gapCount === 0 ? (
            <p className="text-sm text-emerald-600">{t.allPriced}</p>
          ) : (
            <MiniTable rows={d.channels} columns={columns} rowKey={(c) => String(c.id)} empty={DASHBOARD_TEXT[lang].noData} />
          )}
          {d.unpriced.length > 0 && (
            <ul className="space-y-1 text-sm">
              {d.unpriced.map((u) => (
                <li key={`${u.channel_id}|${u.model}`} className="flex justify-between gap-3">
                  <span className="break-all">
                    #{u.channel_id} {u.channel_name} · <span className="text-red-600">{u.model}</span>
                  </span>
                  <span className="tabular-nums text-red-600 shrink-0">
                    {num(u.calls, locale)} {t.colCalls}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </SectionShell>
  )
}
