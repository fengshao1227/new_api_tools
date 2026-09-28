import { Filter, Gift, ShieldAlert, ExternalLink } from 'lucide-react'
import { DASHBOARD_TEXT, localeOf, type DashboardLang } from '../lib/dashboardI18n'
import {
  RISK_CONSOLE_URL,
  type BusinessConversion,
  type BusinessFinance,
  type BusinessGiftsRisk,
  type ConversionBucket,
  type SectionState,
} from '../lib/businessDashboard'
import { MiniTable, SectionShell, Stat, money, num, pct, type Column } from './BusinessBits'

/** 注册转化:时间窗内注册的人 → 用过 API → 付费,及按来源 / 国家拆分。 */
export function BusinessConversionPanel({ state, lang }: { state: SectionState<BusinessConversion>; lang: DashboardLang }) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const c = state.data

  const bucketColumns = (header: string, label: (key: string) => string): Column<ConversionBucket>[] => [
    { header, cell: (b) => <span className="font-medium">{label(b.key)}</span> },
    { header: t.signups, align: 'right', cell: (b) => num(b.signups, locale) },
    { header: t.activated, align: 'right', cell: (b) => `${num(b.activated, locale)} · ${pct(b.activation_rate)}` },
    { header: t.paid, align: 'right', cell: (b) => num(b.paid, locale) },
    { header: t.colPaidRate, align: 'right', cell: (b) => pct(b.paid_rate) },
  ]
  const sourceLabel = (key: string) => (key === 'unattributed' ? t.unattributed : key)
  const countryLabel = (key: string) => (key === 'unknown' ? t.unknownCountry : key)

  return (
    <SectionShell
      title={t.conversionTitle}
      icon={Filter}
      hint={t.conversionHint}
      loading={state.loading}
      error={state.error}
      hasData={!!c}
      errorText={t.loadFailed}
    >
      {c && (
        <>
          <div className="grid grid-cols-1 xs:grid-cols-3 gap-3">
            <Stat label={t.signups} value={num(c.signups, locale)} />
            <Stat label={t.activated} value={num(c.activated, locale)} sub={t.ofSignups(pct(c.activation_rate))} />
            <Stat
              label={t.paid}
              value={num(c.paid, locale)}
              sub={`${t.ofSignups(pct(c.paid_rate))} · ${t.paidOfActivated(pct(c.paid_of_activated_rate))}`}
              tone={c.paid > 0 ? 'positive' : 'default'}
            />
          </div>
          {c.attribution_available ? (
            <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
              <div>
                <div className="text-sm font-medium text-muted-foreground mb-2">{t.bySource}</div>
                <MiniTable rows={c.by_source} columns={bucketColumns(t.colSource, sourceLabel)} rowKey={(b) => b.key} empty={DASHBOARD_TEXT[lang].noData} />
              </div>
              <div>
                <div className="text-sm font-medium text-muted-foreground mb-2">{t.byCountry}</div>
                <MiniTable rows={c.by_country} columns={bucketColumns(t.colCountry, countryLabel)} rowKey={(b) => b.key} empty={DASHBOARD_TEXT[lang].noData} />
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">{t.attributionMissing}</p>
          )}
        </>
      )}
    </SectionShell>
  )
}

/**
 * 赠额与风控。「未付费账号用掉的赠额」来自毛利块(同一套分桶口径),
 * 所以这里也接 finance 的状态,不另算一遍。
 */
export function BusinessGiftsRiskPanel({
  state,
  finance,
  lang,
}: {
  state: SectionState<BusinessGiftsRisk>
  finance: SectionState<BusinessFinance>
  lang: DashboardLang
}) {
  const t = DASHBOARD_TEXT[lang].biz
  const locale = localeOf(lang)
  const g = state.data?.gifts
  const r = state.data?.risk
  const margin = finance.data?.margin

  const consoleLink = (
    <a
      href={RISK_CONSOLE_URL}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1 text-sm text-primary hover:underline shrink-0"
    >
      {t.openConsole}
      <ExternalLink className="w-3.5 h-3.5" />
    </a>
  )

  return (
    <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
      <SectionShell title={t.giftsTitle} icon={Gift} loading={state.loading} error={state.error} hasData={!!g} errorText={t.loadFailed}>
        {g && (g.available ? (
          <div className="grid grid-cols-1 xs:grid-cols-3 xl:grid-cols-1 2xl:grid-cols-3 gap-3">
            <Stat label={t.granted} value={money(g.granted_usd)} sub={t.grantedSub(g.granted_users)} />
            <Stat
              label={t.giftBurned}
              value={margin ? money(margin.free_user_billed_usd) : '—'}
              sub={margin ? t.giftBurnedSub(money(margin.gift_and_free_cost_usd)) : undefined}
              tone="warning"
            />
            <Stat label={t.liability} value={money(g.liability_usd)} sub={t.liabilitySub(g.liability_users)} tone="warning" />
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">{t.unavailable}</p>
        ))}
      </SectionShell>

      <SectionShell
        title={t.riskTitle}
        icon={ShieldAlert}
        action={consoleLink}
        loading={state.loading}
        error={state.error}
        hasData={!!r}
        errorText={t.loadFailed}
      >
        {r && (r.available ? (
          <>
            <div className="grid grid-cols-1 xs:grid-cols-2 gap-3">
              <Stat
                label={t.openReview}
                value={money(r.review.held_usd)}
                sub={t.holdSub(r.review.cases, r.review.users)}
                tone={r.review.cases > 0 ? 'warning' : 'default'}
              />
              <Stat
                label={t.openDeny}
                value={money(r.deny.held_usd)}
                sub={t.holdSub(r.deny.cases, r.deny.users)}
                tone={r.deny.cases > 0 ? 'warning' : 'default'}
              />
            </div>
            <div>
              <div className="text-sm font-medium text-muted-foreground mb-2">{t.decisions}</div>
              <div className="grid grid-cols-2 sm:grid-cols-5 gap-2 text-center">
                {([
                  [t.flagged, r.flagged],
                  [t.confirmed, r.confirmed],
                  [t.released, r.released],
                  [t.withheld, r.withheld],
                  [t.dismissed, r.dismissed],
                ] as const).map(([label, value]) => (
                  <div key={label} className="rounded-md bg-muted/40 p-2">
                    <div className="text-lg font-semibold tabular-nums">{num(value, locale)}</div>
                    <div className="text-xs text-muted-foreground">{label}</div>
                  </div>
                ))}
              </div>
            </div>
          </>
        ) : (
          <p className="text-sm text-muted-foreground">{t.unavailable}</p>
        ))}
      </SectionShell>
    </div>
  )
}
