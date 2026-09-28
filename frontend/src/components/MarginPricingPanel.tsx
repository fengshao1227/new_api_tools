import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ChevronRight, RefreshCw } from 'lucide-react'
import { apiFetch, createAuthHeaders } from '../lib/api'
import { cn } from '../lib/utils'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Input } from './ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'
import { number, percent, ratio, readAPIResponse, unitPrice } from './marginFormat'

interface PricingGroupPrice {
  group: string
  ratio: number
  retail_usd: number
  lowest_margin_percent: number
  highest_margin_percent: number
}

interface PricingSellGroup {
  group: string
  ratio: number
  ratio_missing?: boolean
  auto_index: number
  primary?: boolean
}

interface PricingRoute {
  channel_id: number
  channel_name: string
  channel_status: number
  priority: number
  matched_tier?: string
  selectable: boolean
  unpriced: boolean
  cost_usd: number
  margin_usd: number
  margin_percent: number
  status: string
}

interface PricingScenario {
  model_name: string
  tier: string
  condition_hint?: string
  list_usd: number
  group?: string
  group_ratio: number
  other_groups?: PricingGroupPrice[]
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
  unserved_routes: number
  status: string
  routes: PricingRoute[]
}

interface PricingModelAnalysis {
  model_name: string
  mode: string
  expression?: string
  prompt_usd_per_million?: number
  completion_usd_per_million?: number
  sell_groups: PricingSellGroup[]
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
  generated_at: number
  stale?: boolean
  stale_reason?: string
}

function pricingStatusLabel(status: string) {
  switch (status) {
    case 'healthy': return '健康毛利'
    case 'thin': return '薄利'
    case 'loss': return '亏损'
    case 'unpriced': return '未定价'
    case 'free': return '免费分组'
    default: return status || '未知'
  }
}

function pricingModeLabel(mode: string) {
  switch (mode) {
    case 'tiered_expr': return '表达式分档'
    case 'per_token': return '输入/输出 token'
    case 'per_call': return '按次计价'
    case 'unpriced': return '未定价'
    default: return mode || '未知'
  }
}

function pricingRouteStatusLabel(status: string) {
  switch (status) {
    case 'priced': return '可选'
    case 'unpriced': return '未定价'
    case 'disabled': return '已禁用'
    case 'unsupported': return '不支持此参数'
    default: return status || '未知'
  }
}

function statusVariant(status: string) {
  return status === 'loss' ? 'destructive' : status === 'thin' || status === 'free' ? 'warning' : status === 'unpriced' ? 'secondary' : 'success'
}

function groupLabel(row: PricingScenario) {
  return row.group ? `${row.group} ${ratio(row.group_ratio)}` : '未挂分组，按牌价'
}

function formatTime(unix: number) {
  return unix ? new Date(unix * 1000).toLocaleString('zh-CN', { hour12: false }) : '—'
}

export function MarginPricingPanel({ token, apiUrl, active }: { token: string | null; apiUrl: string; active: boolean }) {
  const [pricing, setPricing] = useState<PricingResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  const load = useCallback(async (refresh = false) => {
    if (!token) return
    setLoading(true)
    setError(null)
    try {
      const response = await apiFetch(`${apiUrl}/api/margin-analysis/pricing${refresh ? '?refresh=true' : ''}`, {
        headers: createAuthHeaders(token),
      })
      const payload = await readAPIResponse<PricingResult>(response)
      if (!response.ok || !payload.success || !payload.data) throw new Error(payload.error?.message || '成本基准加载失败')
      setPricing(payload.data)
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '成本基准加载失败')
    } finally {
      setLoading(false)
    }
  }, [apiUrl, token])

  useEffect(() => {
    if (active && !pricing && !error && !loading) void load()
  }, [active, error, load, loading, pricing])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (pricing?.models || [])
      .flatMap((model) => model.scenarios)
      .filter((row) => !q || `${row.model_name} ${row.tier} ${row.condition_hint || ''} ${row.group || ''}`.toLowerCase().includes(q))
  }, [pricing?.models, query])

  if (!active) return null
  if (loading && !pricing) {
    return <div className="min-h-[260px] flex items-center justify-center text-sm text-muted-foreground">正在让 new-api 解析全部定价表达式…</div>
  }
  if (error && !pricing) {
    return (
      <Card><CardContent className="p-8 text-center">
        <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-amber-500" />
        <p className="text-sm text-muted-foreground">{error}</p>
        <Button className="mt-4" variant="outline" onClick={() => void load(true)}>重试</Button>
      </CardContent></Card>
    )
  }
  if (!pricing) return null

  const total = pricing.models.reduce((count, model) => count + model.scenarios.length, 0)
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-2 rounded-lg border border-border/60 bg-muted/20 px-3 py-2 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
        <span>数据时间 {formatTime(pricing.generated_at)} · 毛利按实际零售价（牌价 × 主分组倍率）计算</span>
        <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={loading}>
          <RefreshCw className={cn('mr-2 h-4 w-4', loading && 'animate-spin')} />重新读取网关
        </Button>
      </div>
      {(pricing.stale || error) && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-800 dark:text-amber-200">
          这次没能从网关取到最新定价（{pricing.stale_reason || error}），下面是 {formatTime(pricing.generated_at)} 的结果。
        </div>
      )}
      <div className="grid gap-4 md:grid-cols-2">
        <Card><CardHeader><CardTitle className="text-lg">最高毛利场景</CardTitle><CardDescription>同一模型/档位下，选择可服务渠道的最低供应商成本。</CardDescription></CardHeader><CardContent><PricingScenarioList rows={pricing.best.slice(0, 10)} /></CardContent></Card>
        <Card><CardHeader><CardTitle className="text-lg">最低毛利场景</CardTitle><CardDescription>同一模型/档位下，选择可服务渠道的最高供应商成本。</CardDescription></CardHeader><CardContent><PricingScenarioList rows={pricing.worst.slice(0, 10)} /></CardContent></Card>
      </div>
      <Card>
        <CardHeader className="gap-3">
          <div><CardTitle>所有模型 · 参数与条件明细</CardTitle><CardDescription>每一行都是一个可计价参数场景；毛利用实际零售价，成本范围来自已服务渠道的最低/最高供应商报价。</CardDescription></div>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="筛选模型、参数、条件或分组…" className="sm:max-w-sm" />
            <span className="text-xs text-muted-foreground">显示 {number(rows.length)} / {number(total)} 个参数场景</span>
          </div>
        </CardHeader>
        <CardContent><PricingDetailTable rows={rows} /></CardContent>
      </Card>
      <Card><CardHeader><CardTitle>完整定价解析</CardTitle><CardDescription>每个模型的售卖分组与倍率、最优/最差毛利和原始条件表达式。</CardDescription></CardHeader><CardContent><PricingModelTable rows={pricing.models} /></CardContent></Card>
      <div className="space-y-2">{pricing.notes.map((note) => <div key={note} className="rounded-md border bg-muted/20 p-3 text-sm text-muted-foreground">{note}</div>)}</div>
    </div>
  )
}

function PricingScenarioList({ rows }: { rows: PricingScenario[] }) {
  if (rows.length === 0) return <div className="text-sm text-muted-foreground">没有可定价场景</div>
  return (
    <div className="space-y-2">
      {rows.map((row, index) => (
        <div key={`${row.model_name}-${row.tier}-${index}`} className="rounded-md border p-2.5 text-xs">
          <div className="flex items-center justify-between gap-2">
            <span className="font-medium">{row.model_name} · {row.tier}</span>
            <Badge variant={statusVariant(row.status)}>毛利 {percent(row.lowest_margin_percent)} ~ {percent(row.highest_margin_percent)}</Badge>
          </div>
          <div className="mt-1 text-muted-foreground">实际零售 {unitPrice(row.retail_usd)}（{groupLabel(row)}，牌价 {unitPrice(row.list_usd)}）· 成本 {unitPrice(row.lowest_cost_usd)} ~ {unitPrice(row.highest_cost_usd)}</div>
          <div className="mt-1 text-muted-foreground">{row.condition_hint || '表达式默认分支'} · 可用渠道 {row.served_routes} · 未定价 {row.unpriced_routes}</div>
        </div>
      ))}
    </div>
  )
}

function OtherGroups({ groups }: { groups?: PricingGroupPrice[] }) {
  if (!groups || groups.length === 0) return null
  return (
    <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
      {groups.map((g) => (
        <div key={g.group}>{g.group} {ratio(g.ratio)}：{unitPrice(g.retail_usd)} · {percent(g.lowest_margin_percent)} ~ {percent(g.highest_margin_percent)}</div>
      ))}
    </div>
  )
}

function PricingDetailTable({ rows }: { rows: PricingScenario[] }) {
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const toggle = (key: string) => setExpanded((current) => {
    const next = new Set(current)
    if (!next.delete(key)) next.add(key)
    return next
  })
  if (rows.length === 0) return <div className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">没有匹配的参数场景</div>

  return (
    <div className="max-h-[720px] overflow-auto rounded-md border">
      <Table className="min-w-[1180px]">
        <TableHeader className="sticky top-0 z-10 bg-background">
          <TableRow>
            <TableHead>模型</TableHead><TableHead>参数 / 条件</TableHead><TableHead>牌价</TableHead><TableHead>实际零售价</TableHead>
            <TableHead>供应商成本</TableHead><TableHead>毛利率范围</TableHead><TableHead>渠道与状态</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>{rows.flatMap((row, index) => {
          const key = `${row.model_name}-${row.tier}-${index}`
          return [
            <TableRow key={key} className="cursor-pointer" aria-expanded={expanded.has(key)} onClick={() => toggle(key)}>
              <TableCell className="align-top font-medium">{row.model_name}</TableCell>
              <TableCell className="align-top"><div className="flex items-center gap-1"><ChevronRight className={cn('h-3.5 w-3.5 text-muted-foreground transition-transform', expanded.has(key) && 'rotate-90')} />{row.tier}</div><div className="mt-1 text-xs text-muted-foreground">{row.condition_hint || '默认/直接分支'}</div></TableCell>
              <TableCell className="align-top font-mono text-muted-foreground">{unitPrice(row.list_usd)}</TableCell>
              <TableCell className="align-top"><div className="font-mono">{unitPrice(row.retail_usd)}</div><div className="mt-1 text-xs text-muted-foreground">{groupLabel(row)}</div><OtherGroups groups={row.other_groups} /></TableCell>
              <TableCell className="align-top"><div className="font-mono">{unitPrice(row.lowest_cost_usd)} ~ {unitPrice(row.highest_cost_usd)}</div><div className="mt-1 max-w-[220px] truncate text-xs text-muted-foreground" title={`最低成本：${row.lowest_cost_channel || '—'}；最高成本：${row.highest_cost_channel || '—'}`}>低：{row.lowest_cost_channel || '—'}<br />高：{row.highest_cost_channel || '—'}</div></TableCell>
              <TableCell className="align-top"><div className="font-mono">{percent(row.lowest_margin_percent)} ~ {percent(row.highest_margin_percent)}</div><div className="mt-1 text-xs text-muted-foreground">按实际零售价</div></TableCell>
              <TableCell className="align-top"><Badge variant={statusVariant(row.status)}>{pricingStatusLabel(row.status)}</Badge><div className="mt-1 text-xs text-muted-foreground">可选 {row.served_routes} · 未定价 {row.unpriced_routes} · 不支持 {row.unserved_routes}</div></TableCell>
            </TableRow>,
            ...(expanded.has(key) ? [<TableRow key={`${key}-routes`} className="hover:bg-transparent"><TableCell colSpan={7} className="bg-muted/30 p-0"><PricingRouteBreakdown routes={row.routes || []} /></TableCell></TableRow>] : []),
          ]
        })}</TableBody>
      </Table>
    </div>
  )
}

function PricingRouteBreakdown({ routes }: { routes: PricingRoute[] }) {
  if (routes.length === 0) return <div className="p-4 text-xs text-muted-foreground">没有返回渠道明细。</div>
  return (
    <div className="divide-y divide-border/60">
      <div className="flex items-center gap-3 px-4 py-2 text-[11px] text-muted-foreground"><span className="w-14 shrink-0">渠道 ID</span><span className="min-w-0 flex-1">渠道</span><span className="w-24 shrink-0">分支</span><span className="w-20 shrink-0 text-right">优先级</span><span className="w-24 shrink-0 text-right">成本</span><span className="w-24 shrink-0 text-right">毛利率</span><span className="w-24 shrink-0 text-right">状态</span></div>
      {routes.map((route) => {
        const hidden = route.unpriced || !route.selectable
        return (
          <div key={`${route.channel_id}-${route.status}`} className={cn('flex items-center gap-3 px-4 py-2 text-xs', !route.selectable && 'text-muted-foreground')}>
            <span className="w-14 shrink-0 font-mono">#{route.channel_id}</span>
            <span className="min-w-0 flex-1 truncate" title={route.channel_name}>{route.channel_name}</span>
            <span className="w-24 shrink-0 truncate text-muted-foreground">{route.matched_tier || '—'}</span>
            <span className="w-20 shrink-0 text-right font-mono text-muted-foreground">{route.priority}</span>
            <span className="w-24 shrink-0 text-right font-mono">{hidden ? '—' : unitPrice(route.cost_usd)}</span>
            <span className={cn('w-24 shrink-0 text-right font-mono', route.margin_percent < 0 && 'text-red-600')}>{hidden ? '—' : percent(route.margin_percent)}</span>
            <span className="w-24 shrink-0 text-right"><Badge variant={route.status === 'priced' ? 'success' : route.status === 'unpriced' ? 'warning' : 'secondary'}>{pricingRouteStatusLabel(route.status)}</Badge></span>
          </div>
        )
      })}
    </div>
  )
}

function PricingModelTable({ rows }: { rows: PricingModelAnalysis[] }) {
  if (rows.length === 0) return <div className="text-sm text-muted-foreground">没有解析到模型定价</div>
  return (
    <Table>
      <TableHeader><TableRow><TableHead>模型</TableHead><TableHead>模式</TableHead><TableHead>售卖分组</TableHead><TableHead>最高毛利</TableHead><TableHead>最低毛利</TableHead><TableHead>解析条件/表达式（牌价）</TableHead></TableRow></TableHeader>
      <TableBody>{rows.map((row) => (
        <TableRow key={row.model_name}>
          <TableCell className="font-medium">{row.model_name}</TableCell>
          <TableCell><Badge variant={row.unpriced ? 'destructive' : 'secondary'}>{pricingModeLabel(row.mode)}</Badge></TableCell>
          <TableCell><div className="flex max-w-[260px] flex-wrap gap-1">{(row.sell_groups || []).length === 0 ? <span className="text-xs text-muted-foreground">未挂分组</span> : row.sell_groups.map((g) => (
            <Badge key={g.group} variant={g.primary ? 'default' : 'outline'} title={`${g.auto_index >= 0 ? `AutoGroups 第 ${g.auto_index + 1} 位` : '不在 AutoGroups'}${g.ratio_missing ? '；GroupRatio 没有这一项，按 1 计' : ''}`}>{g.group} {ratio(g.ratio)}{g.primary ? ' 主' : ''}</Badge>
          ))}</div></TableCell>
          <TableCell className="text-emerald-600">{row.unpriced ? '未定价' : `${percent(row.best_margin_percent)} · ${row.best_scenario || '—'}`}</TableCell>
          <TableCell className={row.worst_margin_percent < 0 ? 'text-red-600' : 'text-amber-600'}>{row.unpriced ? '未定价' : `${percent(row.worst_margin_percent)} · ${row.worst_scenario || '—'}`}</TableCell>
          <TableCell className="max-w-[420px]"><div className="truncate text-xs text-muted-foreground" title={row.expression || ''}>{row.expression || (row.mode === 'per_token' ? `输入 ${unitPrice(row.prompt_usd_per_million || 0)}/1M · 输出 ${unitPrice(row.completion_usd_per_million || 0)}/1M` : '固定价格')}</div></TableCell>
        </TableRow>
      ))}</TableBody>
    </Table>
  )
}
