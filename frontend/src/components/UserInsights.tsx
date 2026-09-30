import { useEffect, useState, type FormEvent } from 'react'
import { AlertCircle, Loader2, Search, Users } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { UserInsightsSummary } from './UserInsightsSummary'
import { UserInsightsDetails } from './UserInsightsDetails'
import {
  fetchInsightsData, formatMetric, formatUTCTime, insightsQuery, insightsWarnings, openUserInsights,
  resolveInsightsWindow, userIdFromSearch, utcDateInput,
  type DatePreset, type InsightsDraft, type InsightsUserOption, type UserInsightsReport,
} from '../lib/user-insights'

const selectClass = 'h-10 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'
type SearchResult = { items: InsightsUserOption[]; total: number }

function initialDraft(): InsightsDraft {
  const now = Math.floor(Date.now() / 1000)
  return { preset: '30', start: utcDateInput(now - 30 * 86400), end: utcDateInput(now), model: '' }
}

export function UserInsights() {
  const { token } = useAuth()
  const [userId, setUserId] = useState(() => userIdFromSearch(window.location.search))
  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState<{ value: string; run: number } | null>(null)
  const [searchResult, setSearchResult] = useState<{ key: string; data: SearchResult } | null>(null)
  const [searchFailure, setSearchFailure] = useState<{ key: string; message: string } | null>(null)
  const [draft, setDraft] = useState<InsightsDraft>(initialDraft)
  const [applied, setApplied] = useState(() => resolveInsightsWindow(initialDraft()))
  const [appliedDraft, setAppliedDraft] = useState<InsightsDraft>(draft)
  const [validationError, setValidationError] = useState('')
  const [run, setRun] = useState(0)
  const [reportResult, setReportResult] = useState<{ key: string; data: UserInsightsReport } | null>(null)
  const [reportFailure, setReportFailure] = useState<{ key: string; message: string } | null>(null)
  const [modelOptions, setModelOptions] = useState<{ userId: number; values: string[] } | null>(null)
  const reportPath = userId ? `/api/users/${userId}/insights?${insightsQuery(applied)}` : ''
  const reportKey = `${reportPath}:${run}`
  const report = reportResult?.key === reportKey ? reportResult.data : null
  const reportWarnings = insightsWarnings(report?.availability.warnings)
  const reportError = reportFailure?.key === reportKey ? reportFailure.message : null
  const loading = !!userId && !!token && !report && !reportError
  const searchKey = search ? `${search.value}:${search.run}` : ''
  const searchData = searchResult?.key === searchKey ? searchResult.data : null
  const searchError = searchFailure?.key === searchKey ? searchFailure.message : null
  const searching = !!search && !searchData && !searchError
  const pendingChanges = JSON.stringify(draft) !== JSON.stringify(appliedDraft)
  const currentModels = modelOptions?.userId === userId ? modelOptions.values : []
  const modelChoices = Array.from(new Set([...currentModels, ...(draft.model ? [draft.model] : [])])).sort()

  useEffect(() => {
    function onPopState() {
      const next = userIdFromSearch(window.location.search)
      if (userId === next) return
      setUserId(next)
      setDraft(current => ({ ...current, model: '' }))
      setAppliedDraft(current => ({ ...current, model: '' }))
      setApplied(current => ({ ...current, model: '' }))
      setValidationError('')
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [userId])

  useEffect(() => {
    if (!search || !token) return
    const controller = new AbortController()
    const query = new URLSearchParams({ search: search.value, page: '1', page_size: '20' })
    fetchInsightsData<SearchResult>(`/api/users?${query}`, token, controller.signal)
      .then(data => { if (!controller.signal.aborted) setSearchResult({ key: searchKey, data }) })
      .catch(error => { if (!controller.signal.aborted) setSearchFailure({ key: searchKey, message: error instanceof Error ? error.message : '搜索失败' }) })
    return () => controller.abort()
  }, [search, searchKey, token])

  useEffect(() => {
    if (!userId || !token) return
    const controller = new AbortController()
    fetchInsightsData<UserInsightsReport>(reportPath, token, controller.signal)
      .then(data => {
        if (controller.signal.aborted) return
        if (data.profile.id !== userId) throw new Error('返回用户与当前选择不一致，请重新查询')
        setReportResult({ key: reportKey, data })
        setModelOptions(previous => ({
          userId, values: Array.from(new Set([
            ...(previous?.userId === userId ? previous.values : []),
            ...(data.models || []).map(model => model.model_name).filter(Boolean),
          ])),
        }))
      })
      .catch(error => { if (!controller.signal.aborted) setReportFailure({ key: reportKey, message: error instanceof Error ? error.message : '查询失败' }) })
    return () => controller.abort()
  }, [userId, token, reportPath, reportKey])

  function submitSearch(event: FormEvent) {
    event.preventDefault()
    const value = searchInput.trim()
    if (value) setSearch(previous => ({ value, run: (previous?.run || 0) + 1 }))
  }

  function applyFilters(event: FormEvent) {
    event.preventDefault()
    try {
      setApplied(resolveInsightsWindow(draft))
      setAppliedDraft({ ...draft })
      setValidationError('')
      setRun(value => value + 1)
    } catch (error) { setValidationError(error instanceof Error ? error.message : '筛选条件无效') }
  }

  return <div className="space-y-6 min-w-0">
    <div className="space-y-2"><h1 className="text-2xl font-bold tracking-tight">用户画像</h1><p className="text-sm text-muted-foreground">查询用户资料、支付和模型使用明细，为人工运营提供客观指标。</p></div>

    <Card>
      <CardHeader><CardTitle className="text-lg">查找用户</CardTitle><CardDescription>输入邮箱、用户名或显示名称，再从搜索结果选择用户。</CardDescription></CardHeader>
      <CardContent className="space-y-4">
        <form className="flex flex-col sm:flex-row gap-2" onSubmit={submitSearch}>
          <label htmlFor="insights-user-search" className="sr-only">邮箱、用户名或显示名称</label>
          <Input id="insights-user-search" className="h-10 flex-1 min-w-0" value={searchInput} onChange={event => setSearchInput(event.target.value)} placeholder="邮箱 / 用户名 / 显示名称" autoComplete="off" maxLength={200} />
          <Button type="submit" className="h-10" disabled={!searchInput.trim()}><Search className="mr-2 h-4 w-4" />搜索用户</Button>
        </form>
        {search && <div className="space-y-2" aria-busy={searching}>
          <p className="text-xs text-muted-foreground break-all">搜索条件：{search.value}{searchData && ` · 共 ${formatMetric(searchData.total)} 位用户`}</p>
          {searching ? <p role="status" className="flex gap-2 items-center py-4 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />正在查找用户…</p>
            : searchError ? <p role="alert" className="text-sm text-destructive">{searchError}，可点击搜索重试。</p>
              : searchData?.items?.length ? <div className="grid md:grid-cols-2 gap-2">{searchData.items.map(user => <button type="button" key={user.id} onClick={() => openUserInsights(user.id)} aria-pressed={userId === user.id} className={`rounded-lg border p-3 text-left min-w-0 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${userId === user.id ? 'border-primary bg-primary/5' : 'hover:bg-muted/50'}`}>
                <p className="font-medium text-sm break-all">{user.username}{user.display_name ? ` · ${user.display_name}` : ''}<span className="text-xs text-muted-foreground ml-2">#{user.id}</span></p><p className="text-xs text-muted-foreground break-all mt-1">{user.email || '未记录邮箱'} · {user.group || '未记录分组'}{user.status === 2 ? ' · 已禁用' : ''}</p>
              </button>)}</div> : <p className="py-4 text-sm text-muted-foreground">没有匹配用户，请换一个邮箱或用户名。</p>}
          {searchData && searchData.total > (searchData.items || []).length && <p className="text-xs text-muted-foreground">仅展示前 20 位，请补充完整邮箱或用户名缩小范围。</p>}
        </div>}
      </CardContent>
    </Card>

    {!userId ? <div className="rounded-lg border border-dashed bg-muted/10 px-4 py-16 text-center text-muted-foreground"><Users className="mx-auto h-9 w-9 mb-3 opacity-50" /><p className="text-sm">选择用户后查看画像，也可从用户管理点击“查看画像”。</p></div>
      : <>
        <Card>
          <CardHeader><CardTitle className="text-lg">筛选使用数据</CardTitle><CardDescription>当前用户：{report ? `${report.profile.username}（#${report.profile.id}）` : `#${userId}`}。日期按 UTC 输入，精确模型匹配。</CardDescription></CardHeader>
          <CardContent>
            <form onSubmit={applyFilters} className="space-y-3">
              <div className="grid sm:grid-cols-2 lg:grid-cols-[1fr_1.5fr_auto] items-end gap-3">
                <label className="space-y-1.5 text-sm min-w-0"><span>时间范围</span><select className={selectClass} value={draft.preset} onChange={event => setDraft(current => ({ ...current, preset: event.target.value as DatePreset }))}><option value="7">最近 7 天</option><option value="30">最近 30 天</option><option value="90">最近 90 天</option><option value="all">全部现存记录</option><option value="custom">自定义 UTC 时间</option></select></label>
                <label className="space-y-1.5 text-sm min-w-0"><span>模型</span><select className={selectClass} value={draft.model} onChange={event => setDraft(current => ({ ...current, model: event.target.value }))}><option value="">全部模型</option>{modelChoices.map(model => <option key={model} value={model}>{model}</option>)}</select></label>
                <Button type="submit" className="h-10" disabled={loading}><Search className="mr-2 h-4 w-4" />应用筛选 / 刷新</Button>
              </div>
              {draft.preset === 'custom' && <div className="grid sm:grid-cols-2 gap-3"><label className="space-y-1.5 text-sm min-w-0"><span>开始时间 UTC（包含）</span><Input className="h-10 min-w-0" type="datetime-local" step="1" value={draft.start} onChange={event => setDraft(current => ({ ...current, start: event.target.value }))} /></label><label className="space-y-1.5 text-sm min-w-0"><span>结束时间 UTC（不包含）</span><Input className="h-10 min-w-0" type="datetime-local" step="1" value={draft.end} onChange={event => setDraft(current => ({ ...current, end: event.target.value }))} /></label></div>}
              {pendingChanges && <p className="text-xs text-amber-700 dark:text-amber-400">筛选有未应用的修改，下方仍显示已应用条件的数据。</p>}
              {validationError && <p role="alert" className="text-sm text-destructive">{validationError}</p>}
            </form>
            <div className="mt-4 rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground leading-relaxed break-words">
              <p>已应用：用户 #{userId} · {applied.model || '全部模型'}</p><p>{applied.all_time ? '全部现存记录' : formatUTCTime(applied.start_time)} → {formatUTCTime(applied.end_time)}（包含起点、不包含终点）</p>
              <p>账户资料、余额和历史累计支付不受这些筛选影响；支付时间范围不受模型筛选影响。</p>
            </div>
          </CardContent>
        </Card>
        {loading ? <div className="flex justify-center items-center gap-2 py-16 text-muted-foreground" role="status"><Loader2 className="h-5 w-5 animate-spin" /><p className="text-sm">正在查询用户 #{userId} 的画像…</p></div>
          : reportError ? <Card><CardContent className="py-10 flex flex-col gap-3 items-center text-center" role="alert"><AlertCircle className="h-6 w-6 text-destructive" /><p className="text-sm text-destructive">{reportError}</p><Button variant="outline" onClick={() => setRun(value => value + 1)}>重新查询</Button></CardContent></Card>
            : report && <>
              {reportWarnings.length > 0 && <div role="status" className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 text-sm text-amber-800 dark:text-amber-300"><p className="font-medium mb-2">部分数据未采集或不可用</p><ul className="space-y-1 list-disc pl-4">{reportWarnings.map(warning => <li key={warning}>{warning}</li>)}</ul></div>}
              <UserInsightsSummary key={`summary:${reportKey}`} report={report} />
              <UserInsightsDetails key={`details:${reportKey}`} report={report} />
            </>}
      </>}
  </div>
}
