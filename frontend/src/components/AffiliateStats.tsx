import { useState, useEffect, useCallback, useMemo } from 'react'
import {
  Users,
  Loader2,
  RefreshCw,
  Search,
  Filter,
  ChevronDown,
  ChevronRight,
  TrendingUp,
  UserCheck,
  Receipt,
  CircleDollarSign,
  Wallet,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { Button } from './ui/button'
import { Badge } from './ui/badge'
import { Input } from './ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from './ui/table'
import { StatCard } from './StatCard'
import { useToast } from './Toast'
import { useAuth } from '../contexts/AuthContext'
import { cn } from '../lib/utils'
import { cnyRateNote, formatTopUpMoney, formatUsd } from '../lib/topup-display'

interface AffiliateRow {
  inviter_id: number
  inviter_username: string | null
  inviter_display_name: string | null
  aff_count: number
  invitee_count: number
  success_topup_count: number
  success_credited_usd: number
  success_paid_usd: number
  unknown_currency_count: number
  last_topup_at: number | null
}

interface AffiliateSummary {
  total_inviters: number
  total_invitees: number
  total_topup_count: number
  total_credited_usd: number
  total_paid_usd: number
  total_unknown_currency_count: number
  cny_per_usd: number
}

interface PaginatedResponse {
  items: AffiliateRow[]
  total: number
  page: number
  page_size: number
  total_pages: number
}

interface TopUpDetailRow {
  id: number
  user_id: number
  username: string | null
  money: number
  payment_currency?: string | null
  credited_usd?: number | null
  complete_time: number
  status: string
}

type SortBy =
  | 'success_paid_usd'
  | 'success_credited_usd'
  | 'success_topup_count'
  | 'invitee_count'
  | 'last_topup_at'
  | 'aff_count'

const SORTABLE: Record<SortBy, string> = {
  success_paid_usd: '累计实付',
  success_credited_usd: '累计入账',
  success_topup_count: '充值笔数',
  invitee_count: '充值人数',
  last_topup_at: '最近充值',
  aff_count: '邀请数',
}

const formatMoney = (n: number | null | undefined) => formatUsd(n || 0)
const formatTime = (ts: number | null | undefined) =>
  ts ? new Date(ts * 1000).toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  }) : '-'

export function AffiliateStats() {
  const { showToast } = useToast()
  const { token } = useAuth()

  const [rows, setRows] = useState<AffiliateRow[]>([])
  const [summary, setSummary] = useState<AffiliateSummary | null>(null)
  const [loading, setLoading] = useState(true)
  const [summaryLoading, setSummaryLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const [page, setPage] = useState(1)
  const pageSize = 20
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(1)

  const [search, setSearch] = useState('')
  const [searchInput, setSearchInput] = useState('')
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')
  const [sortBy, setSortBy] = useState<SortBy>('success_paid_usd')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')

  const [expandedId, setExpandedId] = useState<number | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailRows, setDetailRows] = useState<TopUpDetailRow[]>([])

  const apiUrl = import.meta.env.VITE_API_URL || ''
  const authHeaders = useMemo(() => ({
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  }), [token])

  const buildParams = useCallback(() => {
    const p = new URLSearchParams()
    if (search) p.append('search', search)
    if (startDate) p.append('start_date', startDate)
    if (endDate) p.append('end_date', endDate)
    return p
  }, [search, startDate, endDate])

  const fetchList = useCallback(async () => {
    setLoading(true)
    try {
      const params = buildParams()
      params.append('page', String(page))
      params.append('page_size', String(pageSize))
      params.append('sort_by', sortBy)
      params.append('sort_dir', sortDir)
      const res = await fetch(`${apiUrl}/api/users/affiliate-stats?${params.toString()}`, { headers: authHeaders })
      const data = await res.json()
      if (data.success) {
        const result: PaginatedResponse = data.data
        setRows(Array.isArray(result?.items) ? result.items : [])
        setTotal(typeof result?.total === 'number' ? result.total : 0)
        setTotalPages(typeof result?.total_pages === 'number' ? result.total_pages : 1)
      } else {
        showToast('error', data.error?.message || '获取邀请返利统计失败')
      }
    } catch (e) {
      console.error('Failed to fetch affiliate stats:', e)
      showToast('error', '网络错误，请重试')
    } finally {
      setLoading(false)
    }
  }, [apiUrl, authHeaders, buildParams, page, sortBy, sortDir, showToast])

  const fetchSummary = useCallback(async () => {
    setSummaryLoading(true)
    try {
      const res = await fetch(`${apiUrl}/api/users/affiliate-stats/summary?${buildParams().toString()}`, { headers: authHeaders })
      const data = await res.json()
      if (data.success) setSummary(data.data)
    } catch (e) {
      console.error('Failed to fetch affiliate summary:', e)
    } finally {
      setSummaryLoading(false)
    }
  }, [apiUrl, authHeaders, buildParams])

  useEffect(() => { fetchList() }, [fetchList])
  useEffect(() => { fetchSummary() }, [fetchSummary])
  // 任何筛选条件变化都回到第一页
  useEffect(() => { setPage(1); setExpandedId(null) }, [search, startDate, endDate, sortBy, sortDir])

  const handleRefresh = async () => {
    setRefreshing(true)
    await Promise.all([fetchList(), fetchSummary()])
    setRefreshing(false)
    showToast('success', '数据已刷新')
  }

  const applySearch = () => setSearch(searchInput.trim())

  const toggleSort = (col: SortBy) => {
    if (sortBy === col) {
      setSortDir(d => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortBy(col)
      setSortDir('desc')
    }
  }

  // 展开某一行：请求该邀请人名下被邀请人的最近成功充值
  const toggleExpand = async (inviterId: number) => {
    if (expandedId === inviterId) {
      setExpandedId(null)
      setDetailRows([])
      return
    }
    setExpandedId(inviterId)
    setDetailRows([])
    setDetailLoading(true)
    try {
      const p = new URLSearchParams()
      p.append('inviter_id', String(inviterId))
      p.append('status', 'success')
      p.append('page', '1')
      p.append('page_size', '10')
      if (startDate) p.append('start_date', startDate)
      if (endDate) p.append('end_date', endDate)
      const res = await fetch(`${apiUrl}/api/top-ups?${p.toString()}`, { headers: authHeaders })
      const data = await res.json()
      if (data.success && Array.isArray(data.data?.items)) {
        setDetailRows(data.data.items as TopUpDetailRow[])
      } else {
        setDetailRows([])
      }
    } catch (e) {
      console.error('Failed to fetch top-up details:', e)
      showToast('error', '获取明细失败')
    } finally {
      setDetailLoading(false)
    }
  }

  const sortIndicator = (col: SortBy) => {
    if (sortBy !== col) return null
    return <span className="ml-1 text-xs text-muted-foreground">{sortDir === 'asc' ? '↑' : '↓'}</span>
  }

  return (
    <div className="space-y-6 animate-in fade-in duration-300">
      {/* Header */}
      <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div>
          <h3 className="text-xl font-bold tracking-tight">邀请返利统计</h3>
          <p className="text-muted-foreground text-sm mt-1">
            按邀请人聚合被邀请用户的成功充值（美元，按支付完成时间归属），用于核算返利与排查异常
          </p>
        </div>
        <div className="flex items-center gap-3 flex-wrap">
          <Button variant="outline" size="sm" onClick={handleRefresh} disabled={refreshing || loading} className="h-9">
            <RefreshCw className={cn('h-4 w-4 mr-2', refreshing && 'animate-spin')} />
            刷新
          </Button>
        </div>
      </div>

      {/* Statistics Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-4">
        <StatCard
          title="有效邀请人"
          value={summaryLoading ? '-' : `${summary?.total_inviters || 0} 人`}
          icon={Users}
          color="indigo"
          className="border-l-4 border-l-indigo-500"
        />
        <StatCard
          title="被邀充值人数"
          value={summaryLoading ? '-' : `${summary?.total_invitees || 0} 人`}
          icon={UserCheck}
          color="cyan"
          className="border-l-4 border-l-cyan-500"
        />
        <StatCard
          title="成功充值笔数"
          value={summaryLoading ? '-' : `${summary?.total_topup_count || 0} 笔`}
          icon={Receipt}
          color="emerald"
          className="border-l-4 border-l-emerald-500"
        />
        <StatCard
          title="累计入账额度"
          value={summaryLoading ? '-' : formatMoney(summary?.total_credited_usd)}
          subValue="入账额度（美元）"
          icon={CircleDollarSign}
          color="amber"
          className="border-l-4 border-l-amber-500"
        />
        <StatCard
          title="累计实付金额"
          value={summaryLoading ? '-' : formatMoney(summary?.total_paid_usd)}
          subValue={summaryLoading ? undefined : `折美元，${cnyRateNote(summary?.cny_per_usd)}${(summary?.total_unknown_currency_count || 0) > 0 ? `；${summary?.total_unknown_currency_count} 笔币种未知未计入` : ''}`}
          icon={Wallet}
          color="rose"
          className="border-l-4 border-l-rose-500"
        />
      </div>

      {/* Filters */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-base font-medium flex items-center gap-2">
            <Filter className="w-4 h-4" />
            筛选条件
          </CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
            <div className="relative">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <Input
                placeholder="搜索邀请人 username / display_name"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') applySearch() }}
                className="pl-9 h-9"
              />
            </div>
            <Input
              type="date"
              value={startDate}
              onChange={(e) => setStartDate(e.target.value)}
              className="h-9"
              placeholder="起始日期"
            />
            <Input
              type="date"
              value={endDate}
              onChange={(e) => setEndDate(e.target.value)}
              className="h-9"
              placeholder="结束日期"
            />
            <Button onClick={applySearch} variant="default" size="sm" className="h-9">
              <Search className="h-4 w-4 mr-2" />
              应用搜索
            </Button>
          </div>
          <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
            <span>排序：</span>
            {(Object.keys(SORTABLE) as SortBy[]).map(col => (
              <button
                key={col}
                onClick={() => toggleSort(col)}
                className={cn(
                  'px-2 py-0.5 rounded border transition-colors',
                  sortBy === col ? 'bg-primary text-primary-foreground border-primary' : 'hover:bg-muted',
                )}
              >
                {SORTABLE[col]}{sortIndicator(col)}
              </button>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Table */}
      <Card>
        <CardHeader className="pb-3 flex flex-row items-center justify-between">
          <CardTitle className="text-base font-medium flex items-center gap-2">
            <TrendingUp className="w-4 h-4" />
            邀请返利明细
            <Badge variant="outline" className="ml-2">{total} 个邀请人</Badge>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-12 text-muted-foreground">
              <Loader2 className="h-5 w-5 mr-2 animate-spin" />
              加载中...
            </div>
          ) : rows.length === 0 ? (
            <div className="py-12 text-center text-muted-foreground text-sm">暂无邀请返利数据</div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8"></TableHead>
                    <TableHead>邀请人</TableHead>
                    <TableHead className="text-right cursor-pointer select-none" onClick={() => toggleSort('aff_count')}>
                      邀请数{sortIndicator('aff_count')}
                    </TableHead>
                    <TableHead className="text-right cursor-pointer select-none" onClick={() => toggleSort('invitee_count')}>
                      充值人数{sortIndicator('invitee_count')}
                    </TableHead>
                    <TableHead className="text-right cursor-pointer select-none" onClick={() => toggleSort('success_topup_count')}>
                      充值笔数{sortIndicator('success_topup_count')}
                    </TableHead>
                    <TableHead className="text-right cursor-pointer select-none" onClick={() => toggleSort('success_credited_usd')}>
                      累计入账 (美元){sortIndicator('success_credited_usd')}
                    </TableHead>
                    <TableHead className="text-right cursor-pointer select-none" onClick={() => toggleSort('success_paid_usd')}>
                      累计实付 (美元){sortIndicator('success_paid_usd')}
                    </TableHead>
                    <TableHead className="cursor-pointer select-none" onClick={() => toggleSort('last_topup_at')}>
                      最近充值{sortIndicator('last_topup_at')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.flatMap(row => {
                    const main = (
                      <TableRow
                        key={row.inviter_id}
                        className="cursor-pointer hover:bg-muted/40"
                        onClick={() => toggleExpand(row.inviter_id)}
                      >
                        <TableCell>
                          {expandedId === row.inviter_id
                            ? <ChevronDown className="h-4 w-4 text-muted-foreground" />
                            : <ChevronRight className="h-4 w-4 text-muted-foreground" />}
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col">
                            <span className="font-medium">{row.inviter_username || `#${row.inviter_id}`}</span>
                            {row.inviter_display_name && row.inviter_display_name !== row.inviter_username && (
                              <span className="text-xs text-muted-foreground">{row.inviter_display_name}</span>
                            )}
                            <span className="text-[10px] text-muted-foreground">ID: {row.inviter_id}</span>
                          </div>
                        </TableCell>
                        <TableCell className="text-right" title="users.aff_count（NewAPI 自维护，仅作旁证）">
                          <span className="text-muted-foreground">{row.aff_count}</span>
                        </TableCell>
                        <TableCell className="text-right">{row.invitee_count}</TableCell>
                        <TableCell className="text-right">{row.success_topup_count}</TableCell>
                        <TableCell className="text-right">{formatMoney(row.success_credited_usd)}</TableCell>
                        <TableCell className="text-right font-semibold text-primary" title={row.unknown_currency_count > 0 ? `${row.unknown_currency_count} 笔币种未知，未计入` : undefined}>
                          {formatMoney(row.success_paid_usd)}
                          {row.unknown_currency_count > 0 && <span className="ml-1 text-xs font-normal text-amber-600">+{row.unknown_currency_count} 笔未知</span>}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">{formatTime(row.last_topup_at)}</TableCell>
                      </TableRow>
                    )
                    if (expandedId !== row.inviter_id) return [main]
                    const detail = (
                      <TableRow key={`${row.inviter_id}-detail`} className="bg-muted/20 hover:bg-muted/20">
                        <TableCell></TableCell>
                        <TableCell colSpan={7} className="py-3">
                          {detailLoading ? (
                            <div className="flex items-center text-sm text-muted-foreground">
                              <Loader2 className="h-4 w-4 mr-2 animate-spin" />加载明细...
                            </div>
                          ) : detailRows.length === 0 ? (
                            <div className="text-sm text-muted-foreground">该邀请人名下暂无成功充值记录</div>
                          ) : (
                            <div>
                              <div className="text-xs text-muted-foreground mb-2">
                                最近 {detailRows.length} 条被邀请人成功充值（按完成时间倒序）
                              </div>
                              <Table>
                                <TableHeader>
                                  <TableRow>
                                    <TableHead>充值 ID</TableHead>
                                    <TableHead>被邀请人</TableHead>
                                    <TableHead className="text-right">入账 (美元)</TableHead>
                                    <TableHead className="text-right">金额（原币种）</TableHead>
                                    <TableHead>完成时间</TableHead>
                                  </TableRow>
                                </TableHeader>
                                <TableBody>
                                  {detailRows.map(d => (
                                    <TableRow key={d.id}>
                                      <TableCell className="text-xs">{d.id}</TableCell>
                                      <TableCell>
                                        {d.username || `#${d.user_id}`}
                                        <span className="text-[10px] text-muted-foreground ml-2">ID:{d.user_id}</span>
                                      </TableCell>
                                      <TableCell className="text-right">{formatMoney(d.credited_usd)}</TableCell>
                                      <TableCell className="text-right font-medium whitespace-nowrap">{formatTopUpMoney(d.money, d.payment_currency)}</TableCell>
                                      <TableCell className="text-xs text-muted-foreground">{formatTime(d.complete_time)}</TableCell>
                                    </TableRow>
                                  ))}
                                </TableBody>
                              </Table>
                            </div>
                          )}
                        </TableCell>
                      </TableRow>
                    )
                    return [main, detail]
                  })}
                </TableBody>
              </Table>
            </div>
          )}

          {/* Pagination */}
          {!loading && rows.length > 0 && (
            <div className="flex items-center justify-between mt-4 text-sm text-muted-foreground">
              <div>
                第 {page} / {totalPages} 页，共 {total} 个邀请人
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPage(p => Math.max(1, p - 1))}
                  disabled={page <= 1}
                >上一页</Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                  disabled={page >= totalPages}
                >下一页</Button>
              </div>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
