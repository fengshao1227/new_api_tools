import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertTriangle, BarChart3, CalendarDays, RefreshCw, ShieldAlert, TrendingUp } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { apiFetch, createAuthHeaders } from '../lib/api'
import { cn } from '../lib/utils'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Input } from './ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'

type MarginSection = 'overview' | 'models' | 'channels' | 'users' | 'pricing'

interface MarginSummary {
  requests: number
  realized_revenue_usd: number
  provider_cost_usd: number
  gross_profit_usd: number
  gross_margin_percent: number
  external_profit_usd: number
  external_margin_percent: number
  paid_traffic_cost_usd: number
  gift_and_free_cost_usd: number
  internal_cost_usd: number
  gift_nominal_usd: number
  gift_provider_cost_usd: number
  paid_customer_count: number
  free_customer_count: number
  manual_credit_user_count: number
  internal_user_count: number
  unpriced_calls: number
  estimated_calls: number
  unpriced_cost_usd: number
  zero_quota_cost_calls: number
}

interface MarginDailyPoint {
  date: string
  requests: number
  revenue_usd: number
  provider_cost_usd: number
  gross_profit_usd: number
  gift_and_free_cost_usd: number
  internal_cost_usd: number
}

interface MarginBreakdown {
  key: string
  name: string
  user_id?: number
  channel_id?: number
  bucket?: string
  requests: number
  revenue_usd: number
  provider_cost_usd: number
  gross_profit_usd: number
  margin_percent: number
  gift_and_free_cost_usd: number
  internal_cost_usd: number
  unpriced_calls: number
  estimated_calls: number
}

interface MarginResult {
  range: { start_date: string; end_date: string; timezone: string; day_count: number }
  quota_per_unit: number
  currency: string
  summary: MarginSummary
  daily: MarginDailyPoint[]
  models: MarginBreakdown[]
  channels: MarginBreakdown[]
  users: MarginBreakdown[]
  notes: string[]
}

interface PricingScenario {
  model_name: string
  tier: string
  condition_hint?: string
  retail_usd: number
  lowest_cost_usd: number
  highest_cost_usd: number
  highest_margin_percent: number
  lowest_margin_percent: number
  lowest_cost_channel?: string
  highest_cost_channel?: string
  cost_routes: number
  unpriced_routes: number
  served_routes: number
  status: string
}

interface PricingModelAnalysis {
  model_name: string
  mode: string
  expression?: string
  prompt_usd_per_million?: number
  completion_usd_per_million?: number
  scenarios: PricingScenario[]
  best_margin_percent: number
  worst_margin_percent: number
  best_scenario?: string
  worst_scenario?: string
  unpriced: boolean
}

interface PricingResult {
  quota_per_unit: number
  currency: string
  models: PricingModelAnalysis[]
  best: PricingScenario[]
  worst: PricingScenario[]
  notes: string[]
}

function localDate(offsetMonths = 0, day = 1) {
  const now = new Date()
  const value = new Date(now.getFullYear(), now.getMonth() + offsetMonths, day)
  const month = String(value.getMonth() + 1).padStart(2, '0')
  const date = String(value.getDate()).padStart(2, '0')
  return `${value.getFullYear()}-${month}-${date}`
}

function money(value: number) {
  return `$${(Number(value) || 0).toFixed(2)}`
}

function percent(value: number) {
  return `${(Number(value) || 0).toFixed(1)}%`
}

function number(value: number) {
  return (Number(value) || 0).toLocaleString('zh-CN')
}

function bucketLabel(bucket?: string) {
  switch (bucket) {
    case 'customer_paid': return '已付费客户'
    case 'deleted_paid': return '已删除但有付款'
    case 'customer_free': return '免费/赠额客户'
    case 'deleted_no_payment': return '已删除未付款'
    case 'manual_or_test_credit': return '手工授信/测试'
    case 'staff_or_root': return '管理员/内部'
    default: return bucket || '未知'
  }
}

function MetricCard({ title, value, detail, tone = 'default' }: { title: string; value: string; detail: string; tone?: 'default' | 'positive' | 'warning' | 'danger' }) {
  return (
    <Card className={cn(
      'overflow-hidden',
      tone === 'positive' && 'border-emerald-500/30 bg-emerald-500/[0.04]',
      tone === 'warning' && 'border-amber-500/30 bg-amber-500/[0.04]',
      tone === 'danger' && 'border-red-500/30 bg-red-500/[0.04]',
    )}>
      <CardHeader className="pb-2">
        <CardDescription>{title}</CardDescription>
        <CardTitle className="text-2xl tracking-tight">{value}</CardTitle>
      </CardHeader>
      <CardContent className="text-xs text-muted-foreground">{detail}</CardContent>
    </Card>
  )
}

function BreakdownTable({ rows, emptyText }: { rows: MarginBreakdown[]; emptyText: string }) {
  if (rows.length === 0) {
    return <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">{emptyText}</div>
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>对象</TableHead>
          <TableHead>请求</TableHead>
          <TableHead>已消费收入</TableHead>
          <TableHead>供应商成本</TableHead>
          <TableHead>毛利</TableHead>
          <TableHead>毛利率</TableHead>
          <TableHead>异常</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={`${row.key}-${row.bucket || ''}`}>
            <TableCell>
              <div className="font-medium truncate max-w-[260px]" title={row.name}>{row.name || row.key}</div>
              {row.bucket && <div className="text-xs text-muted-foreground">{bucketLabel(row.bucket)}</div>}
            </TableCell>
            <TableCell>{number(row.requests)}</TableCell>
            <TableCell>{money(row.revenue_usd)}</TableCell>
            <TableCell>{money(row.provider_cost_usd)}</TableCell>
            <TableCell className={row.gross_profit_usd >= 0 ? 'text-emerald-600' : 'text-red-600'}>{money(row.gross_profit_usd)}</TableCell>
            <TableCell>{row.revenue_usd > 0 ? percent(row.margin_percent) : '—'}</TableCell>
            <TableCell>
              {row.unpriced_calls || row.estimated_calls ? (
                <div className="flex gap-1">
                  {row.unpriced_calls > 0 && <Badge variant="destructive">未定价 {row.unpriced_calls}</Badge>}
                  {row.estimated_calls > 0 && <Badge variant="warning">估算 {row.estimated_calls}</Badge>}
                </div>
              ) : <span className="text-xs text-muted-foreground">—</span>}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function MarginAnalysis() {
  const { token } = useAuth()
  const [startDate, setStartDate] = useState(localDate(0, 1))
  const [endDate, setEndDate] = useState(localDate())
  const [data, setData] = useState<MarginResult | null>(null)
  const [pricing, setPricing] = useState<PricingResult | null>(null)
  const [pricingLoading, setPricingLoading] = useState(false)
  const [pricingError, setPricingError] = useState<string | null>(null)
  const [section, setSection] = useState<MarginSection>('overview')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const apiUrl = import.meta.env.VITE_API_URL || ''

  const load = useCallback(async (refresh = false) => {
    if (!token) return
    setError(null)
    refresh ? setRefreshing(true) : setLoading(true)
    try {
      const params = new URLSearchParams({ start_date: startDate, end_date: endDate })
      if (refresh) params.set('no_cache', 'true')
      const response = await apiFetch(`${apiUrl}/api/margin-analysis?${params.toString()}`, {
        headers: createAuthHeaders(token),
      })
      const payload = await response.json()
      if (!response.ok || !payload.success) throw new Error(payload.error?.message || '毛利分析加载失败')
      setData(payload.data)
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '毛利分析加载失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [apiUrl, endDate, startDate, token])

  useEffect(() => { void load() }, [load])

  const loadPricing = useCallback(async () => {
    if (!token || pricingLoading) return
    setPricingLoading(true)
    setPricingError(null)
    try {
      const response = await apiFetch(`${apiUrl}/api/margin-analysis/pricing`, {
        headers: createAuthHeaders(token),
      })
      const payload = await response.json()
      if (!response.ok || !payload.success) throw new Error(payload.error?.message || '成本基准加载失败')
      setPricing(payload.data)
    } catch (pricingLoadError) {
      setPricingError(pricingLoadError instanceof Error ? pricingLoadError.message : '成本基准加载失败')
    } finally {
      setPricingLoading(false)
    }
  }, [apiUrl, pricingLoading, token])

  useEffect(() => {
    if (section === 'pricing' && !pricing && !pricingError) void loadPricing()
  }, [loadPricing, pricing, pricingError, section])

  const maxDaily = useMemo(() => Math.max(1, ...(data?.daily || []).flatMap((row) => [row.revenue_usd, row.provider_cost_usd])), [data?.daily])

  if (loading) {
    return <div className="min-h-[420px] flex items-center justify-center text-sm text-muted-foreground">正在读取消费日志并核算赠额来源…</div>
  }

  if (error || !data) {
    return (
      <Card>
        <CardContent className="p-8 text-center">
          <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-amber-500" />
          <p className="text-sm text-muted-foreground">{error || '没有可显示的数据'}</p>
          <Button className="mt-4" variant="outline" onClick={() => void load(true)}>重试</Button>
        </CardContent>
      </Card>
    )
  }

  const summary = data.summary
  const tabs: { id: MarginSection; label: string }[] = [
    { id: 'overview', label: '总览' },
    { id: 'models', label: '模型拆分' },
    { id: 'channels', label: '渠道拆分' },
    { id: 'users', label: '用户拆分' },
    { id: 'pricing', label: '成本基准' },
  ]

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <TrendingUp className="h-6 w-6 text-primary" />
            <h2 className="text-2xl font-bold tracking-tight">毛利全面分析</h2>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">按实际已消费额度核算，不把未使用充值余额当收入。</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center gap-2 rounded-md border bg-background px-2 py-1.5">
            <CalendarDays className="h-4 w-4 text-muted-foreground" />
            <Input type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} className="h-7 w-[132px] border-0 p-0 text-xs shadow-none" />
            <span className="text-muted-foreground">至</span>
            <Input type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} className="h-7 w-[132px] border-0 p-0 text-xs shadow-none" />
          </div>
          <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={refreshing}>
            <RefreshCw className={cn('mr-2 h-4 w-4', refreshing && 'animate-spin')} />刷新
          </Button>
        </div>
      </div>

      <div className="flex items-center justify-between rounded-lg border border-border/60 bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
        <span>{data.range.start_date} 至 {data.range.end_date} · {data.range.timezone}</span>
        <span>额度单位：{number(data.quota_per_unit)} = $1</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard title="实际消费收入" value={money(summary.realized_revenue_usd)} detail={`${number(summary.requests)} 次消费，已排除赠额名义收入`} tone="positive" />
        <MetricCard title="供应商成本" value={money(summary.provider_cost_usd)} detail={`付费流量 ${money(summary.paid_traffic_cost_usd)} · 免费/赠额 ${money(summary.gift_and_free_cost_usd)}`} tone="warning" />
        <MetricCard title="全成本毛利" value={money(summary.gross_profit_usd)} detail={`毛利率 ${percent(summary.gross_margin_percent)} · 含内部/测试成本`} tone={summary.gross_profit_usd >= 0 ? 'positive' : 'danger'} />
        <MetricCard title="外部业务毛利" value={money(summary.external_profit_usd)} detail={`剔除内部成本后，毛利率 ${percent(summary.external_margin_percent)}`} tone="positive" />
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Card><CardContent className="p-4"><div className="text-xs text-muted-foreground">赠额名义消耗</div><div className="mt-1 text-xl font-semibold">{money(summary.gift_nominal_usd)}</div><div className="text-xs text-muted-foreground">供应商实际成本 {money(summary.gift_provider_cost_usd)}</div></CardContent></Card>
        <Card><CardContent className="p-4"><div className="text-xs text-muted-foreground">付费客户</div><div className="mt-1 text-xl font-semibold">{number(summary.paid_customer_count)}</div><div className="text-xs text-muted-foreground">免费客户 {number(summary.free_customer_count)}</div></CardContent></Card>
        <Card><CardContent className="p-4"><div className="text-xs text-muted-foreground">内部/测试成本</div><div className="mt-1 text-xl font-semibold">{money(summary.internal_cost_usd)}</div><div className="text-xs text-muted-foreground">手工授信 {number(summary.manual_credit_user_count)} 个账号</div></CardContent></Card>
        <Card><CardContent className="p-4"><div className="text-xs text-muted-foreground">成本数据风险</div><div className="mt-1 text-xl font-semibold">{number(summary.unpriced_calls + summary.estimated_calls)} 条</div><div className="text-xs text-muted-foreground">未定价 {number(summary.unpriced_calls)} · 估算 {number(summary.estimated_calls)}</div></CardContent></Card>
      </div>

      <div className="flex gap-1 overflow-x-auto border-b border-border/60">
        {tabs.map((tab) => (
          <button key={tab.id} type="button" onClick={() => setSection(tab.id)} className={cn('whitespace-nowrap border-b-2 px-4 py-2 text-sm font-medium transition-colors', section === tab.id ? 'border-primary text-primary' : 'border-transparent text-muted-foreground hover:text-foreground')}>
            {tab.label}
          </button>
        ))}
      </div>

      {section === 'overview' && (
        <div className="grid gap-6 xl:grid-cols-[1.3fr_1fr]">
          <Card>
            <CardHeader><CardTitle className="flex items-center gap-2 text-lg"><BarChart3 className="h-5 w-5" />每日收入与成本</CardTitle><CardDescription>按本地时区展开，空白日不隐藏。</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              {data.daily.map((row) => (
                <div key={row.date} className="grid grid-cols-[82px_1fr_82px] items-center gap-3 text-xs">
                  <span className="text-muted-foreground">{row.date.slice(5)}</span>
                  <div className="space-y-1">
                    <div className="h-2 overflow-hidden rounded-full bg-emerald-500/15"><div className="h-full rounded-full bg-emerald-500" style={{ width: `${Math.min(100, row.revenue_usd / maxDaily * 100)}%` }} /></div>
                    <div className="h-2 overflow-hidden rounded-full bg-amber-500/15"><div className="h-full rounded-full bg-amber-500" style={{ width: `${Math.min(100, row.provider_cost_usd / maxDaily * 100)}%` }} /></div>
                  </div>
                  <div className="text-right font-mono"><div className="text-emerald-600">{money(row.revenue_usd)}</div><div className="text-amber-600">{money(row.provider_cost_usd)}</div></div>
                </div>
              ))}
              <div className="flex gap-4 pt-2 text-xs text-muted-foreground"><span><i className="mr-1 inline-block h-2 w-2 rounded-full bg-emerald-500" />收入</span><span><i className="mr-1 inline-block h-2 w-2 rounded-full bg-amber-500" />成本</span></div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle className="flex items-center gap-2 text-lg"><ShieldAlert className="h-5 w-5" />核算边界</CardTitle><CardDescription>这几项决定数字能否直接用于财务判断。</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              {data.notes.map((note) => <div key={note} className="rounded-md border bg-muted/20 p-3 text-sm text-muted-foreground">{note}</div>)}
              {(summary.unpriced_calls > 0 || summary.zero_quota_cost_calls > 0) && <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-800 dark:text-amber-200">存在 {number(summary.unpriced_calls)} 条未定价日志，以及 {number(summary.zero_quota_cost_calls)} 条零计费但有供应商成本的请求。</div>}
            </CardContent>
          </Card>
        </div>
      )}

      {section === 'models' && <Card><CardHeader><CardTitle>模型毛利排行</CardTitle><CardDescription>按毛利排序，成本为网关日志中的供应商实际/估算成本。</CardDescription></CardHeader><CardContent><BreakdownTable rows={data.models} emptyText="当前范围没有模型消费记录" /></CardContent></Card>}
      {section === 'channels' && <Card><CardHeader><CardTitle>渠道毛利排行</CardTitle><CardDescription>展示实际命中的渠道，便于发现贵渠道和未定价渠道。</CardDescription></CardHeader><CardContent><BreakdownTable rows={data.channels} emptyText="当前范围没有渠道消费记录" /></CardContent></Card>}
      {section === 'users' && <Card><CardHeader><CardTitle>用户毛利排行</CardTitle><CardDescription>已付款、免费赠额、手工授信和内部账号分开统计。</CardDescription></CardHeader><CardContent><BreakdownTable rows={data.users} emptyText="当前范围没有用户消费记录" /></CardContent></Card>}
      {section === 'pricing' && (
        pricingLoading ? <div className="min-h-[260px] flex items-center justify-center text-sm text-muted-foreground">正在让 new-api 解析全部定价表达式…</div> : pricingError ? (
          <Card><CardContent className="p-8 text-center"><AlertTriangle className="mx-auto mb-3 h-8 w-8 text-amber-500" /><p className="text-sm text-muted-foreground">{pricingError}</p><Button className="mt-4" variant="outline" onClick={() => { setPricingError(null); void loadPricing() }}>重试</Button></CardContent></Card>
        ) : pricing ? (
          <div className="space-y-6">
            <div className="grid gap-4 md:grid-cols-2">
              <Card><CardHeader><CardTitle className="text-lg">最高毛利场景</CardTitle><CardDescription>同一模型/档位下，选择可服务渠道的最低供应商成本。</CardDescription></CardHeader><CardContent><PricingScenarioTable rows={pricing.best.slice(0, 10)} /></CardContent></Card>
              <Card><CardHeader><CardTitle className="text-lg">最低毛利场景</CardTitle><CardDescription>同一模型/档位下，选择可服务渠道的最高供应商成本。</CardDescription></CardHeader><CardContent><PricingScenarioTable rows={pricing.worst.slice(0, 10)} /></CardContent></Card>
            </div>
            <Card><CardHeader><CardTitle>完整定价解析</CardTitle><CardDescription>每个模型的模式、最优/最差毛利和原始条件表达式。</CardDescription></CardHeader><CardContent><PricingModelTable rows={pricing.models} /></CardContent></Card>
            <div className="space-y-2">{pricing.notes.map((note) => <div key={note} className="rounded-md border bg-muted/20 p-3 text-sm text-muted-foreground">{note}</div>)}</div>
          </div>
        ) : null
      )}
    </div>
  )
}

function PricingScenarioTable({ rows }: { rows: PricingScenario[] }) {
  if (rows.length === 0) return <div className="text-sm text-muted-foreground">没有可定价场景</div>
  return <div className="space-y-2">{rows.map((row, index) => <div key={`${row.model_name}-${row.tier}-${index}`} className="rounded-md border p-2.5 text-xs"><div className="flex items-center justify-between gap-2"><span className="font-medium">{row.model_name} · {row.tier}</span><Badge variant={row.status === 'loss' ? 'destructive' : row.status === 'thin' ? 'warning' : 'success'}>{percent(row.lowest_margin_percent)} ~ {percent(row.highest_margin_percent)}</Badge></div><div className="mt-1 text-muted-foreground">售价 {money(row.retail_usd)} · 成本 {money(row.lowest_cost_usd)} ~ {money(row.highest_cost_usd)}</div><div className="mt-1 text-muted-foreground">{row.condition_hint || '表达式默认分支'} · 路由 {row.cost_routes}</div></div>)}</div>
}

function PricingModelTable({ rows }: { rows: PricingModelAnalysis[] }) {
  if (rows.length === 0) return <div className="text-sm text-muted-foreground">没有解析到模型定价</div>
  return <Table><TableHeader><TableRow><TableHead>模型</TableHead><TableHead>模式</TableHead><TableHead>最高毛利</TableHead><TableHead>最低毛利</TableHead><TableHead>解析条件/表达式</TableHead></TableRow></TableHeader><TableBody>{rows.map((row) => <TableRow key={row.model_name}><TableCell className="font-medium">{row.model_name}</TableCell><TableCell><Badge variant={row.unpriced ? 'destructive' : 'secondary'}>{row.mode}</Badge></TableCell><TableCell className="text-emerald-600">{row.unpriced ? '未定价' : `${percent(row.best_margin_percent)} · ${row.best_scenario || '—'}`}</TableCell><TableCell className={row.worst_margin_percent < 0 ? 'text-red-600' : 'text-amber-600'}>{row.unpriced ? '未定价' : `${percent(row.worst_margin_percent)} · ${row.worst_scenario || '—'}`}</TableCell><TableCell className="max-w-[420px]"><div className="truncate text-xs text-muted-foreground" title={row.expression || ''}>{row.expression || (row.mode === 'per_token' ? `输入 ${money(row.prompt_usd_per_million || 0)}/1M · 输出 ${money(row.completion_usd_per_million || 0)}/1M` : '固定价格')}</div></TableCell></TableRow>)}</TableBody></Table>
}
