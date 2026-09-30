import { useEffect, useState } from 'react'
import { Loader2, RefreshCw } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Button } from './ui/button'
import { Badge } from './ui/badge'
import { Tabs, TabsList, TabsTrigger } from './ui/tabs'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'
import { CHANNEL_CATEGORY_LABELS, USER_CATEGORY_LABELS } from '../lib/failureAttribution'
import {
  fetchInsightsData, formatMetric, formatUSD, formatUTCTime, insightsQuery, insightsWarnings,
  type InsightsLog, type InsightsPage, type InsightsTask, type UserInsightsReport,
} from '../lib/user-insights'

const selectClass = 'h-9 rounded-md border border-input bg-background px-3 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'
const taskLabels: Record<string, string> = {
  SUCCESS: '成功', FAILURE: '失败', IN_PROGRESS: '进行中', NOT_START: '未开始', SUBMITTED: '已提交', QUEUED: '排队中', UNKNOWN: '未知',
}

const failureLabels: Record<string, string> = {
  ...CHANNEL_CATEGORY_LABELS, ...USER_CATEGORY_LABELS,
  param_rejected: '参数被拒绝', queue_full: '渠道排队已满',
}

function LogsTable({ logs }: { logs: InsightsLog[] }) {
  return <Table className="min-w-[1080px] text-xs">
    <TableHeader><TableRow>{['时间 UTC / 类型', '模型', '输入（含已记录缓存）', '输出', '缓存读 / 写', '额度折合 USD', '耗时', '记录详情'].map(label => <TableHead key={label} className="whitespace-nowrap">{label}</TableHead>)}</TableRow></TableHeader>
    <TableBody>{logs.map(log => <TableRow key={log.id}>
      <TableCell className="whitespace-nowrap space-y-1"><div>{formatUTCTime(log.created_at)}</div><Badge variant="outline">{log.type === 2 ? '消费记录' : log.type === 5 ? '失败尝试' : log.type === 6 ? '退款流水' : `类型 ${log.type}`}</Badge></TableCell>
      <TableCell className="max-w-60 break-all">{log.model_name || '未记录模型'}</TableCell>
      <TableCell className="tabular-nums">{formatMetric(log.input_tokens)}</TableCell><TableCell className="tabular-nums">{formatMetric(log.completion_tokens)}</TableCell>
      <TableCell className="tabular-nums whitespace-nowrap">{formatMetric(log.cache_read_tokens)} / {formatMetric(log.cache_write_tokens)}</TableCell>
      <TableCell className="tabular-nums">{formatUSD(log.quota_usd)}</TableCell>
      <TableCell className="whitespace-nowrap">{formatMetric(log.use_time)} 秒</TableCell>
      <TableCell className="min-w-60"><details><summary className="cursor-pointer text-primary">记录 #{log.id}</summary><dl className="mt-2 space-y-1 break-all text-muted-foreground">
        {log.type === 5 && <div>失败类别：{failureLabels[log.content] || '其他失败'}</div>}
        <div>请求 ID：{log.request_id || '未记录'}</div><div>令牌名称 / ID：{log.token_name || '未记录'} / {log.token_id || '未记录'}</div>
        <div>渠道 ID：{log.channel_id || '未记录'}</div><div>IP：{log.ip || '未记录'}</div><div>流式：{log.is_stream ? '是' : '否'}</div>
        <div>上游成本：{formatUSD(log.cost_usd)}</div><div>原始输入计数：{formatMetric(log.prompt_tokens)}</div><div>推理 Token：{formatMetric(log.reasoning_tokens)}</div>
        {log.is_task && <div>任务 ID：{log.task_id || '未记录'}</div>}
        {log.voided_quota != null && <div>已作废原始额度：{formatMetric(log.voided_quota)}</div>}
      </dl></details></TableCell>
    </TableRow>)}</TableBody>
  </Table>
}

function TasksTable({ tasks }: { tasks: InsightsTask[] }) {
  return <Table className="min-w-[1100px] text-xs">
    <TableHeader><TableRow>{['提交时间 UTC', '任务 ID / 平台', '模型 / 动作', '状态 / 进度', '当前任务额度 USD', '开始 / 完成时间 UTC', '失败类别'].map(label => <TableHead key={label} className="whitespace-nowrap">{label}</TableHead>)}</TableRow></TableHeader>
    <TableBody>{tasks.map(task => <TableRow key={task.id}>
      <TableCell className="whitespace-nowrap">{formatUTCTime(task.submit_time)}</TableCell>
      <TableCell className="max-w-64 break-all"><div>{task.task_id}</div><div className="mt-1 text-muted-foreground">{task.platform || '未记录平台'}</div></TableCell>
      <TableCell className="max-w-60 break-all"><div>{task.model_name || '未记录模型'}</div><div className="mt-1 text-muted-foreground">{task.action || '未记录动作'}</div></TableCell>
      <TableCell className="whitespace-nowrap"><Badge variant="outline">{taskLabels[task.status] || task.status || '未知'}</Badge><div className="mt-1">{task.progress || '未记录'}</div></TableCell>
      <TableCell className="tabular-nums">{formatUSD(task.quota_usd)}</TableCell>
      <TableCell className="whitespace-nowrap"><div>{formatUTCTime(task.start_time)}</div><div>{formatUTCTime(task.finish_time)}</div></TableCell>
      <TableCell className="max-w-60 break-words">{task.fail_reason ? (failureLabels[task.fail_reason] || '其他失败') : '—'}</TableCell>
    </TableRow>)}</TableBody>
  </Table>
}

export function UserInsightsDetails({ report }: { report: UserInsightsReport }) {
  const { token } = useAuth()
  const [tab, setTab] = useState<'logs' | 'tasks'>('logs')
  const [type, setType] = useState('all')
  const [status, setStatus] = useState('all')
  const [page, setPage] = useState(1)
  const [retry, setRetry] = useState(0)
  const [result, setResult] = useState<{ key: string; data: InsightsPage<InsightsLog | InsightsTask> } | null>(null)
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null)
  const available = tab === 'logs' ? report.availability.logs : report.availability.tasks
  const query = insightsQuery(report.window)
  query.set('page', String(page))
  query.set('page_size', '20')
  if (tab === 'logs') query.set('type', type)
  else if (status !== 'all') query.set('status', status)
  const path = `/api/users/${report.profile.id}/insights/${tab}?${query}`
  const requestKey = `${path}:${retry}`
  const data = result?.key === requestKey ? result.data : null
  const warnings = insightsWarnings(data?.warnings)
  const error = failure?.key === requestKey ? failure.message : null
  const loading = available && !!token && !data && !error

  useEffect(() => {
    if (!available || !token) return
    const controller = new AbortController()
    fetchInsightsData<InsightsPage<InsightsLog | InsightsTask>>(path, token, controller.signal)
      .then(data => { if (!controller.signal.aborted) setResult({ key: requestKey, data }) })
      .catch(error => { if (!controller.signal.aborted) setFailure({ key: requestKey, message: error instanceof Error ? error.message : '查询失败' }) })
    return () => controller.abort()
  }, [available, token, path, requestKey])

  const totalPages = data ? Math.max(1, Math.ceil(data.total / data.page_size)) : 1
  const items = data?.items || []
  return <Card className="min-w-0 overflow-hidden">
    <CardHeader>
      <div className="flex flex-wrap items-start justify-between gap-3"><div><CardTitle className="text-lg">调用与任务明细</CardTitle><CardDescription className="mt-2">遵循页面上方已应用的用户、时间和模型；时间范围包含起点、不包含终点。</CardDescription></div><Button variant="outline" size="sm" disabled={loading || !available} onClick={() => setRetry(value => value + 1)}><RefreshCw className="mr-2 h-4 w-4" />刷新明细</Button></div>
      <div className="flex flex-wrap gap-3 items-center pt-2">
        <Tabs value={tab} onValueChange={value => { setTab(value as 'logs' | 'tasks'); setPage(1) }}><TabsList><TabsTrigger value="logs">调用日志</TabsTrigger><TabsTrigger value="tasks">异步任务</TabsTrigger></TabsList></Tabs>
        {tab === 'logs' ? <label className="flex items-center gap-2 text-sm">记录类型<select aria-label="记录类型" className={selectClass} value={type} onChange={event => { setType(event.target.value); setPage(1) }}><option value="all">全部记录</option><option value="consume">消费记录</option><option value="error">失败尝试</option><option value="refund">退款流水</option></select></label>
          : <label className="flex items-center gap-2 text-sm">任务状态<select aria-label="任务状态" className={selectClass} value={status} onChange={event => { setStatus(event.target.value); setPage(1) }}><option value="all">全部状态</option>{Object.entries(taskLabels).filter(([key]) => key !== 'UNKNOWN').map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label>}
      </div>
      {tab === 'tasks' && <p className="text-xs text-muted-foreground">当前筛选任务：共 {formatMetric(report.tasks?.total)} · 成功 {formatMetric(report.tasks?.success)} · 失败 {formatMetric(report.tasks?.failed)} · 进行中 {formatMetric(report.tasks?.in_progress)}。任务按提交时间筛选，当前额度不等于该时间段结算总额。</p>}
    </CardHeader>
    <CardContent aria-busy={loading}>
      {!available || data?.available === false ? <p className="py-8 text-center text-sm text-muted-foreground">{tab === 'logs' ? '调用日志' : '任务信息'}未采集，无法查询。</p>
        : loading ? <div className="flex justify-center items-center gap-2 py-12 text-sm text-muted-foreground" role="status"><Loader2 className="h-5 w-5 animate-spin" />正在查询{tab === 'logs' ? '调用日志' : '任务'}…</div>
          : error ? <div role="alert" className="py-8 text-center space-y-3"><p className="text-sm text-destructive">{error}</p><Button variant="outline" onClick={() => setRetry(value => value + 1)}>重试</Button></div>
            : items.length ? tab === 'logs' ? <LogsTable logs={items as InsightsLog[]} /> : <TasksTable tasks={items as InsightsTask[]} />
              : <p className="py-8 text-center text-sm text-muted-foreground">当前筛选没有{tab === 'logs' ? '调用' : '任务'}记录。</p>}
      {warnings.length ? <ul className="mt-3 space-y-1 text-xs text-amber-700 dark:text-amber-400">{warnings.map(warning => <li key={warning}>{warning}</li>)}</ul> : null}
      {data && data.available && <div className="mt-4 flex flex-wrap justify-between items-center gap-3"><p className="text-sm text-muted-foreground">共 {formatMetric(data.total)} 条 · 第 {data.page} / {totalPages} 页 · 每页 {data.page_size} 条</p><div className="flex gap-2"><Button variant="outline" size="sm" disabled={page === 1 || loading} onClick={() => setPage(value => value - 1)}>上一页</Button><Button variant="outline" size="sm" disabled={page >= totalPages || loading} onClick={() => setPage(value => value + 1)}>下一页</Button></div></div>}
    </CardContent>
  </Card>
}
