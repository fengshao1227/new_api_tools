import { useState, useEffect, useCallback, useMemo } from 'react'
import { useToast } from './Toast'
import { useAuth } from '../contexts/AuthContext'
import { Server, Loader2, RefreshCw, AlertTriangle, CheckCircle2, XCircle, Wallet, Activity } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { Button } from './ui/button'
import { Badge } from './ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'
import { Select } from './ui/select'
import { StatCard } from './StatCard'
import { cn } from '../lib/utils'
import {
  LEVEL_DOT, MIN_SAMPLES, SIDE_META, categoryLabel, durationText, levelOf,
  type ChannelLogStat, type ErrorAnalysis, type Level, type ModelHealth, type Side, type SinglePointModel,
} from '../lib/failureAttribution'
import { CategoryChips, RateCell, UserSideCell } from './ChannelHealthBits'

interface ChannelRecord {
  id: number
  name: string
  type: number
  status: number
  priority: number
  weight: number
  balance: number
  balance_updated_time: number
  response_time: number
  test_time: number
  used_quota: number
  group: string
  tag: string
  model_count: number
  created_time: number
}

export function ChannelMonitor() {
  const { showToast } = useToast()
  const { token } = useAuth()

  const [channels, setChannels] = useState<ChannelRecord[]>([])
  const [logStats, setLogStats] = useState<Map<number, ChannelLogStat>>(new Map())
  const [modelHealth, setModelHealth] = useState<ModelHealth[]>([])
  const [errorAnalysis, setErrorAnalysis] = useState<ErrorAnalysis | null>(null)
  const [singlePoint, setSinglePoint] = useState<SinglePointModel[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [hours, setHours] = useState(24)
  const [sampleSide, setSampleSide] = useState<Side | 'all'>('channel')

  const apiUrl = import.meta.env.VITE_API_URL || ''
  const getAuthHeaders = useCallback(() => ({
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  }), [token])

  const fetchAll = useCallback(async () => {
    try {
      const [chRes, lsRes, mhRes, eaRes, amRes] = await Promise.all([
        fetch(`${apiUrl}/api/channels/overview`, { headers: getAuthHeaders() }),
        fetch(`${apiUrl}/api/channels/log-stats?hours=${hours}`, { headers: getAuthHeaders() }),
        fetch(`${apiUrl}/api/channels/model-health?hours=${hours}`, { headers: getAuthHeaders() }),
        fetch(`${apiUrl}/api/channels/error-analysis?hours=${hours}`, { headers: getAuthHeaders() }),
        fetch(`${apiUrl}/api/channels/ability-matrix`, { headers: getAuthHeaders() }),
      ])
      const [ch, ls, mh, ea, am] = await Promise.all([chRes.json(), lsRes.json(), mhRes.json(), eaRes.json(), amRes.json()])
      if (ch.success) setChannels(ch.data || [])
      if (ls.success) {
        const m = new Map<number, ChannelLogStat>()
        for (const s of ls.data || []) m.set(s.channel_id, s)
        setLogStats(m)
      }
      if (mh.success) setModelHealth(mh.data || [])
      if (ea.success) setErrorAnalysis(ea.data)
      if (am.success) setSinglePoint(am.data?.single_point_models || [])
    } catch (error) {
      showToast('error', '获取渠道数据失败')
      console.error('Failed to fetch channel data:', error)
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [apiUrl, getAuthHeaders, hours, showToast])

  useEffect(() => { setLoading(true); fetchAll() }, [fetchAll])

  const handleRefresh = () => { setRefreshing(true); fetchAll() }

  const formatQuota = (q: number) => `$${(q / 500000).toFixed(2)}`
  const formatTs = (ts: number) => {
    if (!ts || ts <= 0) return '-'
    return new Date(ts * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  }

  const statusBadge = (s: number) => {
    if (s === 1) return <Badge variant="success">启用</Badge>
    if (s === 2) return <Badge variant="secondary">手动禁用</Badge>
    if (s === 3) return <Badge variant="destructive">自动禁用</Badge>
    return <Badge variant="secondary">未知({s})</Badge>
  }

  const totals = useMemo(() => {
    const active = channels.filter(c => c.status === 1).length
    // 余额不合计：各上游自报、币种不一（二手上游常标 $ 实为 ¥），相加没有意义
    const balanceReported = channels.filter(c => Number(c.balance_updated_time) > 0).length
    let success = 0, errors = 0, user = 0, queue = 0
    logStats.forEach(s => {
      success += Number(s.success) || 0
      errors += Number(s.errors) || 0
      user += Number(s.user_errors) || 0
      queue += Number(s.queue_full) || 0
    })
    const counted = success + errors
    return { active, balanceReported, errors, user, queue, errRate: counted > 0 ? (errors / counted) * 100 : 0, level: levelOf(success, errors) }
  }, [channels, logStats])

  // 单点模型按唯一渠道的健康度排序：渠道正在出错的排前面
  const singlePointRows = useMemo(() => {
    const rank: Record<Level, number> = { red: 0, yellow: 1, green: 2, idle: 3 }
    return singlePoint
      .map(m => ({ ...m, level: (logStats.get(m.channel_id)?.level || 'idle') as Level }))
      .sort((a, b) => rank[a.level] - rank[b.level] || a.model.localeCompare(b.model))
  }, [singlePoint, logStats])

  const samples = useMemo(() => {
    const all = errorAnalysis?.samples || []
    return (sampleSide === 'all' ? all : all.filter(s => s.side === sampleSide)).slice(0, 50)
  }, [errorAnalysis, sampleSide])

  if (loading) {
    return (
      <div className="flex justify-center items-center py-32">
        <Loader2 className="h-10 w-10 animate-spin text-primary" />
      </div>
    )
  }

  const cardColor = totals.level === 'red' ? 'red' : totals.level === 'yellow' ? 'yellow' : 'green'

  return (
    <div className="space-y-6 animate-in fade-in duration-500">
      {/* Header */}
      <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div>
          <h2 className="text-3xl font-bold tracking-tight">渠道监控</h2>
          <p className="text-muted-foreground mt-1">
            渠道余额（上游自报）、性能与错误率一览（只读，不含渠道密钥）。错误率只算渠道/上游侧失败，口径同网关：
            内容违规、参数错误、素材打不开、用户余额不足、客户先断开算用户侧，不计入；少于 {MIN_SAMPLES} 次不判色。
          </p>
        </div>
        <div className="flex items-center gap-3">
          <Select value={String(hours)} onChange={(e) => setHours(Number(e.target.value))} className="h-9 w-28">
            <option value="24">近 24 小时</option>
            <option value="72">近 3 天</option>
            <option value="168">近 7 天</option>
          </Select>
          <Button variant="outline" size="sm" onClick={handleRefresh} disabled={refreshing} className="h-9">
            <RefreshCw className={cn('h-4 w-4 mr-2', refreshing && 'animate-spin')} />
            刷新
          </Button>
        </div>
      </div>

      {/* Stat Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard title="渠道总数" value={`${channels.length}`} icon={Server} color="blue" className="border-l-4 border-l-blue-500" />
        <StatCard title="启用渠道" value={`${totals.active}`} icon={CheckCircle2} color="green" className="border-l-4 border-l-green-500" />
        <StatCard title="已上报余额" value={`${totals.balanceReported} / ${channels.length}`} subValue="逐渠道见下表，币种以上游为准，不合计" icon={Wallet} color="yellow" className="border-l-4 border-l-yellow-500" />
        <StatCard
          title="渠道侧错误率"
          value={`${totals.errRate.toFixed(2)}%`}
          subValue={`渠道侧 ${totals.errors} · 用户侧 ${totals.user} · 排队 ${totals.queue}（后两项不计入）`}
          icon={Activity}
          color={cardColor}
          className={cn('border-l-4', cardColor === 'red' ? 'border-l-red-500' : cardColor === 'yellow' ? 'border-l-yellow-500' : 'border-l-green-500')}
        />
      </div>

      {/* 单点模型预警 */}
      {singlePointRows.length > 0 && (
        <Card className="border-yellow-300/60 dark:border-yellow-800/60">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-medium flex items-center gap-2 text-yellow-700 dark:text-yellow-400">
              <AlertTriangle className="w-4 h-4" />
              单点风险模型（仅一个启用渠道支撑，圆点为该渠道的渠道侧健康度）
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex flex-wrap gap-2">
              {singlePointRows.map((m) => (
                <span
                  key={m.model}
                  className={cn(
                    'inline-flex items-center gap-1.5 text-xs px-2 py-1 rounded-md border',
                    m.level === 'red' ? 'bg-red-50 border-red-200 dark:bg-red-900/30 dark:border-red-800'
                      : m.level === 'yellow' ? 'bg-yellow-50 border-yellow-200 dark:bg-yellow-900/30 dark:border-yellow-800'
                        : 'bg-muted/40 border-border'
                  )}
                  title={`分组 ${m.group} · 渠道 ${m.channel_name || m.channel_id}`}
                >
                  <span className={cn('w-1.5 h-1.5 rounded-full', LEVEL_DOT[m.level])} />
                  <code className="font-mono">{m.model}</code>
                  <span className="text-muted-foreground">→ {m.channel_name || `#${m.channel_id}`}</span>
                </span>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* 渠道表 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-medium">渠道列表</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {channels.length === 0 ? (
            <div className="py-16 text-center text-muted-foreground">
              <Server className="h-8 w-8 mx-auto mb-3 opacity-40" />
              <p>暂无渠道：请先在 NewAPI 管理台添加渠道</p>
            </div>
          ) : (
            <div className="overflow-x-auto border-t">
              <Table>
                <TableHeader className="bg-muted/50">
                  <TableRow>
                    <TableHead className="w-[50px]">ID</TableHead>
                    <TableHead>名称</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>分组</TableHead>
                    <TableHead>优先级/权重</TableHead>
                    <TableHead title="渠道余额由上游自报，币种以上游为准（二手上游可能标 $ 实为 ¥），不同渠道不可相加">余额（上游自报）</TableHead>
                    <TableHead>测速</TableHead>
                    <TableHead>已用额度</TableHead>
                    <TableHead>模型数</TableHead>
                    <TableHead title="每次尝试记在各自渠道上：同步成功、错误日志、结束的任务（任务回执不算）">窗口尝试</TableHead>
                    <TableHead title="渠道侧失败 ÷（成功 + 渠道侧失败）；≥80% 红、≥20% 黄，少于 5 次不判色">渠道侧错误率</TableHead>
                    <TableHead title="用户侧失败与排队，不计入错误率">用户侧</TableHead>
                    <TableHead>平均耗时</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {channels.map((c) => {
                    const stat = logStats.get(c.id)
                    return (
                      <TableRow key={c.id} className="hover:bg-muted/50">
                        <TableCell className="font-mono text-xs text-muted-foreground">{c.id}</TableCell>
                        <TableCell className="font-medium text-sm max-w-[160px] truncate" title={c.name}>
                          {c.name}
                          {c.tag && <span className="ml-1.5 text-[10px] px-1.5 py-0.5 rounded-full bg-primary/10 text-primary">{c.tag}</span>}
                        </TableCell>
                        <TableCell>{statusBadge(c.status)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground max-w-[100px] truncate" title={c.group}>{c.group || 'default'}</TableCell>
                        <TableCell className="text-xs text-muted-foreground font-mono">{c.priority} / {c.weight}</TableCell>
                        <TableCell>
                          {Number(c.balance_updated_time) > 0 ? (
                            <div className="flex flex-col text-xs" title="上游自报余额，币种以上游为准">
                              <span className={cn('font-medium font-mono', Number(c.balance) <= 0 ? 'text-muted-foreground' : 'text-foreground')}>
                                {Number(c.balance).toFixed(2)}
                              </span>
                              <span className="text-muted-foreground">{formatTs(c.balance_updated_time)}</span>
                            </div>
                          ) : (
                            <span className="text-xs text-muted-foreground" title="网关尚未拉取过该渠道余额">未上报</span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                          {c.response_time > 0 ? `${(c.response_time / 1000).toFixed(1)}s` : '-'}
                          <span className="block">{formatTs(c.test_time)}</span>
                        </TableCell>
                        <TableCell className="text-xs">{formatQuota(c.used_quota)}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{c.model_count}</TableCell>
                        <TableCell className="text-xs font-mono">{stat ? Number(stat.attempts).toLocaleString() : '-'}</TableCell>
                        <TableCell>{stat ? <RateCell stat={stat} /> : <span className="text-xs text-muted-foreground">-</span>}</TableCell>
                        <TableCell>{stat ? <UserSideCell stat={stat} /> : <span className="text-xs text-muted-foreground">-</span>}</TableCell>
                        <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                          {stat ? durationText(stat.avg_use_time, stat.avg_task_seconds) : '-'}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 模型健康 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-medium">模型健康（窗口期内，按尝试）</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {modelHealth.length === 0 ? (
            <div className="py-12 text-center text-muted-foreground text-sm">窗口期内暂无调用日志</div>
          ) : (
            <div className="overflow-x-auto border-t">
              <Table>
                <TableHeader className="bg-muted/50">
                  <TableRow>
                    <TableHead>模型</TableHead>
                    <TableHead>尝试数</TableHead>
                    <TableHead>渠道侧错误率</TableHead>
                    <TableHead>用户侧</TableHead>
                    <TableHead title="同步成功里 completion_tokens = 0 的比例；任务不看 token">空回复率</TableHead>
                    <TableHead>平均/最大耗时</TableHead>
                    <TableHead className="min-w-[180px]">耗时分布 (&lt;3s / 3-10s / 10-30s / &gt;30s)</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {modelHealth.map((m) => {
                    const syncSuccess = Number(m.sync_success) || 0
                    const emptyRate = syncSuccess > 0 ? (Number(m.empty_count) / syncSuccess) * 100 : null
                    const buckets = [Number(m.bucket_fast), Number(m.bucket_mid), Number(m.bucket_slow), Number(m.bucket_very_slow)]
                    const bucketSum = buckets.reduce((a, b) => a + b, 0) || 1
                    const colors = ['bg-green-500', 'bg-yellow-500', 'bg-orange-500', 'bg-red-500']
                    return (
                      <TableRow key={m.model_name} className="hover:bg-muted/50">
                        <TableCell className="font-mono text-xs max-w-[200px] truncate" title={m.model_name}>{m.model_name}</TableCell>
                        <TableCell className="text-xs font-mono">{Number(m.attempts).toLocaleString()}</TableCell>
                        <TableCell><RateCell stat={m} /></TableCell>
                        <TableCell><UserSideCell stat={m} /></TableCell>
                        <TableCell>
                          {emptyRate === null ? <span className="text-xs text-muted-foreground">-</span> : (
                            <span className={cn('text-xs', emptyRate > 10 ? 'text-red-600 font-medium' : 'text-muted-foreground')}>{emptyRate.toFixed(1)}%</span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground whitespace-nowrap">
                          {m.avg_use_time != null
                            ? `${Number(m.avg_use_time).toFixed(1)}s / ${m.max_use_time != null ? `${Number(m.max_use_time)}s` : '-'}`
                            : durationText(null, m.avg_task_seconds)}
                        </TableCell>
                        <TableCell>
                          {syncSuccess > 0 ? (
                            <div className="flex h-3 w-full max-w-[220px] rounded-full overflow-hidden bg-muted" title={`<3s: ${buckets[0]} · 3-10s: ${buckets[1]} · 10-30s: ${buckets[2]} · >30s: ${buckets[3]}`}>
                              {buckets.map((b, i) => (
                                b > 0 ? <div key={i} className={colors[i]} style={{ width: `${(b / bucketSum) * 100}%` }} /> : null
                              ))}
                            </div>
                          ) : <span className="text-xs text-muted-foreground">-</span>}
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 错误分析 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base font-medium flex items-center gap-2">
            <XCircle className="w-4 h-4 text-red-500" />
            错误日志分析（窗口内 {errorAnalysis?.total || 0} 条）
          </CardTitle>
        </CardHeader>
        <CardContent>
          {!errorAnalysis || errorAnalysis.total === 0 ? (
            <div className="py-8 text-center text-muted-foreground text-sm">窗口期内没有错误日志 🎉</div>
          ) : (
            <div className="space-y-4">
              <div className="space-y-2">
                <CategoryChips title={`渠道/上游侧 ${errorAnalysis.channel_errors}（计入错误率）`} side="channel" categories={errorAnalysis.channel_categories} />
                <CategoryChips title={`用户侧 ${errorAnalysis.user_errors}（不计入）`} side="user" categories={errorAnalysis.user_categories} />
                {errorAnalysis.queue_full > 0 && (
                  <CategoryChips title={`排队 ${errorAnalysis.queue_full}（不计入）`} side="queue" categories={{ queue_full: errorAnalysis.queue_full }} />
                )}
              </div>
              <div className="flex items-center gap-2 text-xs">
                <span className="text-muted-foreground">最近样本：</span>
                {(['channel', 'user', 'queue', 'all'] as const).map(side => (
                  <Button key={side} size="sm" variant={sampleSide === side ? 'default' : 'outline'} className="h-7 px-2 text-xs" onClick={() => setSampleSide(side)}>
                    {side === 'all' ? '全部' : SIDE_META[side].label}
                  </Button>
                ))}
              </div>
              {samples.length === 0 ? (
                <div className="py-6 text-center text-muted-foreground text-sm">最近 {errorAnalysis.sampled} 条样本里没有这一类</div>
              ) : (
                <div className="overflow-x-auto rounded-md border">
                  <Table>
                    <TableHeader className="bg-muted/50">
                      <TableRow>
                        <TableHead className="w-[110px]">时间</TableHead>
                        <TableHead>归属</TableHead>
                        <TableHead>类别</TableHead>
                        <TableHead>模型</TableHead>
                        <TableHead>渠道</TableHead>
                        <TableHead>用户</TableHead>
                        <TableHead>内容</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {samples.map((s, i) => (
                        <TableRow key={i} className="hover:bg-muted/50">
                          <TableCell className="text-xs text-muted-foreground whitespace-nowrap">{formatTs(s.created_at)}</TableCell>
                          <TableCell><span className={cn('text-[11px] px-1.5 py-0.5 rounded-full whitespace-nowrap', SIDE_META[s.side]?.color)}>{SIDE_META[s.side]?.label || s.side}</span></TableCell>
                          <TableCell className="text-xs whitespace-nowrap">{categoryLabel(s.side, s.category)}</TableCell>
                          <TableCell className="font-mono text-xs max-w-[140px] truncate" title={s.model_name}>{s.model_name || '-'}</TableCell>
                          <TableCell className="text-xs text-muted-foreground">#{s.channel_id}</TableCell>
                          <TableCell className="text-xs">{s.username || '-'}</TableCell>
                          <TableCell className="text-xs text-muted-foreground max-w-[320px] truncate" title={s.content}>{s.content}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              )}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
