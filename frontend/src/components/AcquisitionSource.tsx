import { useCallback, useEffect, useMemo, useState } from 'react'
import ReactECharts from 'echarts-for-react'
import { AlertTriangle, GitBranch, RefreshCw } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { apiFetch, createAuthHeaders } from '../lib/api'
import { Button } from './ui/button'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'

interface AcquisitionBucket {
  source: string
  detail?: string
  users: number
  paid_users: number
  unpaid_users: number
  paid_rate: number
  kind?: 'captured' | 'legacy_referral' | 'unattributed' | 'schema_missing'
}

interface AcquisitionOverview {
  window_days: number
  total_users: number
  paid_users: number
  unpaid_users: number
  paid_rate: number
  source_users: number
  unattributed_users: number
  historical_referral_users: number
  attribution_status: 'ready' | 'schema_missing' | string
  by_source: AcquisitionBucket[]
  by_detail: AcquisitionBucket[]
}

type CohortMode = 'all' | 'paid' | 'unpaid'

const WINDOW_OPTIONS = [
  { value: '30', label: '近 30 天' },
  { value: '90', label: '近 90 天' },
  { value: '365', label: '近 1 年' },
  { value: '0', label: '全部时间' },
]

const SOURCE_LABELS: Record<string, string> = {
  google: 'Google 搜索', bing: 'Bing', baidu: '百度', duckduckgo: 'DuckDuckGo', youtube: 'YouTube',
  github: 'GitHub', reddit: 'Reddit', x: 'X / Twitter', linuxdo: 'Linux.do', v2ex: 'V2EX',
  direct: '直接访问', referrer: '其他外链', referral: '邀请码', '历史邀请': '历史邀请', '未采集': '未采集',
}

function sourceLabel(value: string) { return SOURCE_LABELS[value] ?? value }
function percent(value: number) { return `${(Number.isFinite(value) ? value * 100 : 0).toFixed(1)}%` }
function number(value: number) { return new Intl.NumberFormat('zh-CN').format(value || 0) }

function Metric({ label, value, hint }: { label: string; value: string; hint: string }) {
  return <div className="rounded-lg border bg-card px-4 py-3"><p className="text-xs text-muted-foreground">{label}</p><p className="mt-1 text-2xl font-semibold tracking-tight">{value}</p><p className="mt-1 text-xs text-muted-foreground">{hint}</p></div>
}

function BucketTable({ rows, detail }: { rows: AcquisitionBucket[]; detail: boolean }) {
  if (!rows.length) return <p className="py-10 text-center text-sm text-muted-foreground">当前窗口没有数据</p>
  return <div className="overflow-x-auto"><table className="w-full min-w-[620px] text-sm"><thead><tr className="border-b text-left text-xs text-muted-foreground"><th className="px-3 py-2 font-medium">{detail ? '一级来源' : '来源渠道'}</th>{detail && <th className="px-3 py-2 font-medium">二级明细</th>}<th className="px-3 py-2 text-right font-medium">用户</th><th className="px-3 py-2 text-right font-medium">已付费</th><th className="px-3 py-2 text-right font-medium">未付费</th><th className="px-3 py-2 text-right font-medium">付费率</th></tr></thead><tbody>{rows.map((row) => <tr key={`${row.source}:${row.detail ?? ''}`} className="border-b last:border-0"><td className="max-w-[220px] truncate px-3 py-3 font-medium" title={row.source}>{sourceLabel(row.source)}</td>{detail && <td className="max-w-[280px] truncate px-3 py-3 text-muted-foreground" title={row.detail}>{row.detail || '—'}</td>}<td className="px-3 py-3 text-right">{number(row.users)}</td><td className="px-3 py-3 text-right text-emerald-600 dark:text-emerald-400">{number(row.paid_users)}</td><td className="px-3 py-3 text-right text-amber-600 dark:text-amber-400">{number(row.unpaid_users)}</td><td className="px-3 py-3 text-right font-medium">{percent(row.paid_rate)}</td></tr>)}</tbody></table></div>
}

function filteredRows(rows: AcquisitionBucket[], mode: CohortMode) {
  return rows.filter((row) => mode === 'all' || (mode === 'paid' ? row.paid_users > 0 : row.unpaid_users > 0))
}

export function AcquisitionSource() {
  const { token } = useAuth()
  const [days, setDays] = useState('30')
  const [mode, setMode] = useState<CohortMode>('all')
  const [data, setData] = useState<AcquisitionOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const apiUrl = import.meta.env.VITE_API_URL || ''

  const load = useCallback(async () => {
    if (!token) return
    setLoading(true); setError('')
    try {
      const response = await apiFetch(`${apiUrl}/api/acquisition/overview?days=${days}`, { headers: createAuthHeaders(token) })
      const payload = await response.json() as { success?: boolean; data?: AcquisitionOverview; message?: string }
      if (!response.ok || !payload.success || !payload.data) throw new Error(payload.message || '来源分析加载失败')
      setData(payload.data)
    } catch (cause) { setError(cause instanceof Error ? cause.message : '来源分析加载失败') } finally { setLoading(false) }
  }, [apiUrl, days, token])

  useEffect(() => { void load() }, [load])

  const summary = data ?? { total_users: 0, paid_users: 0, unpaid_users: 0, paid_rate: 0, source_users: 0, unattributed_users: 0, historical_referral_users: 0, attribution_status: 'ready', window_days: Number(days), by_source: [], by_detail: [] }
  const rows = useMemo(() => filteredRows(summary.by_source, mode), [summary.by_source, mode])
  const sourceDonut = useMemo(() => ({ tooltip: { trigger: 'item', formatter: (p: { name: string; value: number; percent: number }) => `${p.name}<br/>${number(p.value)} 用户（${p.percent.toFixed(1)}%）` }, legend: { bottom: 0, type: 'scroll', textStyle: { color: '#64748b' } }, series: [{ type: 'pie', radius: ['48%', '72%'], center: ['50%', '44%'], itemStyle: { borderColor: '#fff', borderWidth: 3 }, label: { show: false }, data: rows.map((row) => ({ name: sourceLabel(row.source), value: mode === 'paid' ? row.paid_users : mode === 'unpaid' ? row.unpaid_users : row.users })) }] }), [mode, rows])
  const conversionBars = useMemo(() => ({ tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } }, legend: { bottom: 0, textStyle: { color: '#64748b' } }, grid: { left: 96, right: 28, top: 14, bottom: 42 }, xAxis: { type: 'value', minInterval: 1, axisLabel: { color: '#94a3b8' }, splitLine: { lineStyle: { color: '#e2e8f0' } } }, yAxis: { type: 'category', data: rows.map((row) => sourceLabel(row.source)).reverse(), axisLabel: { color: '#475569' } }, series: [{ name: '已付费', type: 'bar', stack: 'users', barMaxWidth: 18, itemStyle: { color: '#10b981', borderRadius: [0, 4, 4, 0] }, data: rows.map((row) => row.paid_users).reverse() }, { name: '未付费', type: 'bar', stack: 'users', barMaxWidth: 18, itemStyle: { color: '#f59e0b', borderRadius: [0, 4, 4, 0] }, data: rows.map((row) => row.unpaid_users).reverse() }] }), [rows])

  return <div className="space-y-6"><div className="flex flex-wrap items-center justify-between gap-3"><div className="flex items-center gap-3"><div className="rounded-lg bg-primary/10 p-2 text-primary"><GitBranch className="h-5 w-5" /></div><div><h2 className="text-xl font-semibold">来源分析</h2><p className="text-sm text-muted-foreground">自动识别来源，按注册 cohort 观察付费转化</p></div></div><div className="flex items-center gap-2"><select value={days} onChange={(event) => setDays(event.target.value)} className="h-9 rounded-md border bg-background px-3 text-sm">{WINDOW_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select><Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}><RefreshCw className={`mr-2 h-4 w-4 ${loading ? 'animate-spin' : ''}`} />刷新</Button></div></div>{summary.attribution_status !== 'ready' && <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" /><span>网关尚未提供来源字段；当前数据只能显示历史邀请和未采集用户，不能代表真实 Google/外链来源。部署网关采集后，新注册用户才会进入来源分布。</span></div>}{error && <div className="rounded-md border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive">{error}</div>}<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5"><Metric label="注册用户" value={number(summary.total_users)} hint={summary.window_days ? `近 ${summary.window_days} 天` : '全部时间'} /><Metric label="已付费用户" value={number(summary.paid_users)} hint="至少一笔成功充值" /><Metric label="未付费用户" value={number(summary.unpaid_users)} hint="尚未成功充值" /><Metric label="自动识别来源" value={number(summary.source_users)} hint={`覆盖 ${percent(summary.total_users ? summary.source_users / summary.total_users : 0)}`} /><Metric label="整体付费率" value={percent(summary.paid_rate)} hint="已付费 ÷ 注册" /></div><div className="flex flex-wrap gap-2">{(['all', 'paid', 'unpaid'] as CohortMode[]).map((value) => <Button key={value} size="sm" variant={mode === value ? 'default' : 'outline'} onClick={() => setMode(value)}>{value === 'all' ? '全部用户' : value === 'paid' ? '已付费来源' : '未付费来源'}</Button>)}</div><div className="grid gap-6 lg:grid-cols-[minmax(280px,0.8fr)_minmax(420px,1.2fr)]"><Card><CardHeader><CardTitle className="text-base">来源分布</CardTitle></CardHeader><CardContent><ReactECharts option={sourceDonut} style={{ height: 300 }} notMerge /></CardContent></Card><Card><CardHeader><CardTitle className="text-base">来源付费转化</CardTitle></CardHeader><CardContent><ReactECharts option={conversionBars} style={{ height: 300 }} notMerge /></CardContent></Card></div><Card><CardHeader><CardTitle className="text-base">一级来源</CardTitle></CardHeader><CardContent><BucketTable rows={summary.by_source} detail={false} /></CardContent></Card><Card><CardHeader><CardTitle className="text-base">二级来源明细</CardTitle></CardHeader><CardContent><BucketTable rows={summary.by_detail} detail /></CardContent></Card></div>
}
