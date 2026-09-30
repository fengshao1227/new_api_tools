import { useState } from 'react'
import { Copy, Download, ExternalLink, UserRound } from 'lucide-react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card'
import { Button } from './ui/button'
import { Badge } from './ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table'
import { useToast } from './Toast'
import {
  createInsightsCSV, formatMetric, formatUSD, formatUTCTime,
  type InsightsPayment, type UserInsightsReport,
} from '../lib/user-insights'

const riskLabels: Record<string, string> = {
  none: '无已记录风险',
  open: '待处理', confirmed: '已确认', released: '已释放', withheld: '已扣留', dismissed: '已忽略',
}

function Fact({ label, value }: { label: string; value: string }) {
  return <div className="min-w-0 space-y-1"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="break-words text-sm">{value}</dd></div>
}

function Metric({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return <div className="rounded-lg border bg-muted/20 p-4 min-w-0"><p className="text-xs text-muted-foreground">{label}</p><p className="mt-2 text-xl font-semibold tabular-nums break-words">{value}</p>{hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}</div>
}

function PaymentDetails({ label, payment }: { label: string; payment: InsightsPayment | undefined }) {
  return <div className="rounded-lg border p-4 space-y-2">
    <h4 className="text-sm font-medium">{label}</h4>
    <p className="text-sm">成功支付 {formatMetric(payment?.paid_count)} 笔 · 已知币种折合 {formatUSD(payment?.paid_usd)}</p>
    <p className="text-xs text-muted-foreground">充值到账本金 {formatUSD(payment?.credited_usd)}；支付额与入账额分别统计。</p>
    {(payment?.by_currency || []).length > 0 && <div className="flex flex-wrap gap-2">{payment!.by_currency.map(currency => <Badge variant="outline" key={currency.currency}>
      {currency.currency && currency.currency !== 'unknown' ? currency.currency : '未知币种'} {formatMetric(currency.amount)} / {formatMetric(currency.count)} 笔
    </Badge>)}</div>}
    {!!payment?.unknown_currency_count && <p className="text-xs text-amber-700 dark:text-amber-400">{payment.unknown_currency_count} 笔币种未知，未计入美元折合额。</p>}
  </div>
}

export function UserInsightsSummary({ report }: { report: UserInsightsReport }) {
  const { profile, balances, summary, activity, payments, availability } = report
  const { showToast } = useToast()
  const [dailyPage, setDailyPage] = useState(1)
  const models = report.models || []
  const days = [...(report.daily || [])].sort((a, b) => b.date.localeCompare(a.date))
  const favorite = [...models].sort((a, b) => (b.input_tokens + b.output_tokens) - (a.input_tokens + a.output_tokens))[0]
  const totalTokens = summary ? summary.input_tokens + summary.output_tokens : null
  const risk = !availability.risk ? '未采集' : profile.risk ? (riskLabels[profile.risk.status] || profile.risk.status) : '无已记录风控事件'

  async function copyEmail() {
    if (!profile.email) return
    try {
      await navigator.clipboard.writeText(profile.email)
      showToast('success', '邮箱已复制')
    } catch { showToast('error', '复制失败，请手动选择邮箱文本复制') }
  }

  function exportCSV() {
    const url = URL.createObjectURL(new Blob([createInsightsCSV(report)], { type: 'text/csv;charset=utf-8;' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `user-${profile.id}-models-${report.window.start_time}-${report.window.end_time}.csv`
    anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  return <div className="space-y-6 min-w-0">
    <Card>
      <CardHeader>
        <div className="flex flex-wrap justify-between gap-3">
          <div className="min-w-0">
            <CardTitle className="flex items-center gap-2 text-xl break-all"><UserRound className="h-5 w-5 shrink-0" />{profile.display_name || profile.username}</CardTitle>
            <CardDescription className="mt-2 break-all">@{profile.username} · ID {profile.id} · {profile.email || '未记录邮箱'}</CardDescription>
          </div>
          <div className="flex flex-wrap gap-2 self-start">
            <Button variant="outline" size="sm" disabled={!profile.email} onClick={copyEmail}><Copy className="mr-2 h-4 w-4" />复制邮箱</Button>
            <Button variant="outline" size="sm" asChild><a href="/users"><ExternalLink className="mr-2 h-4 w-4" />用户管理 / 风控</a></Button>
          </div>
        </div>
        <div className="flex flex-wrap gap-2 pt-2">
          <Badge variant={profile.status === 1 ? 'success' : 'outline'}>{profile.status === 1 ? '正常' : profile.status === 2 ? '已禁用' : `状态 ${profile.status}`}</Badge>
          <Badge variant="outline">{profile.group || '未记录分组'}</Badge>
          <Badge variant="outline">{profile.paid == null ? '付费状态未采集' : profile.paid ? '有历史付费记录' : '未发现历史付费记录'}</Badge>
          <Badge variant="outline">风控：{risk}</Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <dl className="grid grid-cols-2 lg:grid-cols-4 gap-x-5 gap-y-4">
          <Fact label="注册时间" value={formatUTCTime(profile.created_at)} />
          <Fact label="最后登录" value={formatUTCTime(profile.last_login_at)} />
          <Fact label="注册国家 / 语言" value={`${profile.signup_country || '未记录'} / ${profile.signup_language || '未记录'}`} />
          <Fact label="第三方登录绑定" value={profile.login_sources?.join('、') || '未绑定或未记录'} />
          <Fact label="获客来源" value={profile.acquisition_source || '未记录'} />
          <Fact label="来源明细" value={profile.acquisition_detail || '未记录'} />
          <Fact label="邀请人 ID" value={profile.inviter_id ? String(profile.inviter_id) : '未记录'} />
          <Fact label="账号角色" value={profile.role === 100 ? '超级管理员' : profile.role === 10 ? '管理员' : profile.role === 1 ? '普通用户' : String(profile.role)} />
          <Fact label="备注" value={profile.remark || '未记录'} />
          <Fact label="付费依据" value={profile.paid_via === 'top_up' ? '成功支付订单' : profile.paid_via === 'credited' ? '充值入账记录' : '未记录'} />
          {profile.risk && <Fact label="风控待处理 / 暂扣额" value={`${formatMetric(profile.risk.open_cases)} / ${formatUSD(profile.risk.held_usd)}`} />}
        </dl>
        <div className="border-t pt-5">
          <h3 className="mb-3 text-sm font-semibold">账户当前余额与历史累计 <span className="font-normal text-muted-foreground">· 不随时间、模型筛选改变</span></h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <Metric label="当前账户余额" value={formatUSD(balances.balance_usd)} />
            <Metric label="账户历史累计已用" value={formatUSD(balances.lifetime_used_usd)} hint="账户累计值；日志清理后可能与现存记录合计不同" />
          </div>
          <p className="text-xs text-muted-foreground mt-3">原始额度：余额 {formatMetric(balances.quota)} · 已用 {formatMetric(balances.used_quota)} · 充值累计 {formatMetric(balances.topup_quota)} · 赠送累计 {formatMetric(balances.granted_quota)}</p>
        </div>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle className="text-lg">使用概览</CardTitle><CardDescription>以下使用、模型、任务和活跃指标均按当前已应用的时间与模型统计。</CardDescription></CardHeader>
      <CardContent className="space-y-5">
        <div className="grid grid-cols-2 xl:grid-cols-4 gap-3">
          <Metric label="总 Token" value={formatMetric(totalTokens)} hint="输入（含已记录缓存）+ 输出" />
          <Metric label="计费记录数" value={formatMetric(summary?.billing_records)} hint="可能含任务补扣，不等于请求数" />
          <Metric label="消费记录额" value={formatUSD(summary?.charged_usd)} />
          <Metric label="退款流水额" value={formatUSD(summary?.refund_usd)} hint="单独展示，不从消费额重复扣减" />
          <Metric label="输入 Token（含已记录缓存）" value={formatMetric(summary?.input_tokens)} />
          <Metric label="输出 Token" value={formatMetric(summary?.output_tokens)} />
          <Metric label="失败尝试" value={formatMetric(summary?.error_records)} hint="同一请求重试可能产生多条记录" />
          <Metric label="有用量记录天数 / 有记录模型数" value={`${formatMetric(activity?.active_days)} / ${formatMetric(summary?.models_count)}`} hint="模型数包含消费、失败尝试及退款记录" />
        </div>
        <dl className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          <Fact label="首次用量记录（当前筛选）" value={formatUTCTime(activity?.first_record_at)} />
          <Fact label="最后用量记录（当前筛选）" value={formatUTCTime(activity?.last_record_at)} />
          <Fact label="最后用量模型（当前筛选）" value={activity?.last_model || '未记录'} />
          <Fact label="Token 使用最多的模型" value={favorite && favorite.input_tokens + favorite.output_tokens > 0 ? `${favorite.model_name || '未记录模型'} · ${formatMetric(favorite.input_tokens + favorite.output_tokens)} Token` : '未记录'} />
        </dl>
        <div className="rounded-lg bg-muted/30 p-4 text-xs text-muted-foreground leading-relaxed space-y-1">
          <p>缓存读取 {formatMetric(summary?.cache_read_tokens)} Token（{formatMetric(summary?.cache_read_records)} 条有记录）；缓存写入 {formatMetric(summary?.cache_write_tokens)} Token（{formatMetric(summary?.cache_write_records)} 条有记录）。</p>
          <p>已记录的缓存是输入 Token 的组成部分，不额外累加；缺失缓存信息标为“未记录”，无法补推，输入按可读取的日志字段统计。已有缓存数值仅覆盖采集到的记录。推理 Token 未单独持久化，显示“未记录”。</p>
          <p>记录可能被历史清理；无记录不代表用户从未使用。此处仅提供客观指标，复制邮箱或导出不会发送邮件。</p>
        </div>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle className="text-lg">支付记录</CardTitle><CardDescription>仅按时间统计，不受模型筛选影响；金额不含赠额。已知币种按当前配置折合美元，原币金额保留。</CardDescription></CardHeader>
      <CardContent className="space-y-3">
        <div className="grid md:grid-cols-2 gap-3"><PaymentDetails label="当前时间范围" payment={payments?.window} /><PaymentDetails label="历史累计（现存订单）" payment={payments?.lifetime} /></div>
        <p className="text-xs text-muted-foreground">首次成功支付：{formatUTCTime(payments?.first_paid_at)}；最近成功支付：{formatUTCTime(payments?.last_paid_at)}。{payments && `人民币折算配置：${formatMetric(payments.cny_per_usd)} CNY / USD。`}</p>
      </CardContent>
    </Card>

    <Card className="min-w-0 overflow-hidden">
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-3"><CardTitle className="text-lg">按模型统计</CardTitle><Button variant="outline" size="sm" disabled={!availability.logs || !models.length} onClick={exportCSV}><Download className="mr-2 h-4 w-4" />导出当前筛选 CSV</Button></div>
        <CardDescription>导出包含当前用户邮箱、已应用时间范围和下表模型；不导出请求内容或 API Key。</CardDescription>
      </CardHeader>
      <CardContent>
        {models.length ? <Table className="min-w-[1180px] text-xs">
          <TableHeader><TableRow>{['模型', '计费记录', '失败尝试', '输入（含已记录缓存）', '输出', '总 Token', '缓存读取', '缓存写入', '推理', '消费记录额', '退款流水额', '最后记录 UTC'].map(label => <TableHead className="whitespace-nowrap" key={label}>{label}</TableHead>)}</TableRow></TableHeader>
          <TableBody>{models.map(model => <TableRow key={model.model_name}>
            <TableCell className="font-medium max-w-64 break-all">{model.model_name || '未记录模型'}</TableCell>
            {[model.billing_records, model.error_records, model.input_tokens, model.output_tokens, model.input_tokens + model.output_tokens, model.cache_read_tokens, model.cache_write_tokens, model.reasoning_tokens].map((value, i) => <TableCell className="tabular-nums whitespace-nowrap" key={i}>{formatMetric(value)}</TableCell>)}
            <TableCell className="tabular-nums whitespace-nowrap">{formatUSD(model.charged_usd)}</TableCell><TableCell className="tabular-nums whitespace-nowrap">{formatUSD(model.refund_usd)}</TableCell><TableCell className="whitespace-nowrap">{formatUTCTime(model.last_record_at)}</TableCell>
          </TableRow>)}</TableBody>
        </Table> : <p className="py-8 text-center text-sm text-muted-foreground">{availability.logs ? '当前筛选没有模型使用记录' : '调用日志未采集，无法统计模型用量'}</p>}
      </CardContent>
    </Card>

    <Card className="min-w-0 overflow-hidden">
      <CardHeader><CardTitle className="text-lg">每日用量（UTC）</CardTitle><CardDescription>按 UTC 自然日汇总，最新在前；仅展示存在记录的日期。</CardDescription></CardHeader>
      <CardContent>
        {days.length ? <>
          <Table className="min-w-[720px] text-xs"><TableHeader><TableRow>{['日期 UTC', '计费记录', '输入（含已记录缓存）', '输出', '总 Token', '失败尝试', '消费记录额', '退款流水额'].map(label => <TableHead className="whitespace-nowrap" key={label}>{label}</TableHead>)}</TableRow></TableHeader>
            <TableBody>{days.slice((dailyPage - 1) * 30, dailyPage * 30).map(day => <TableRow key={day.date}><TableCell className="whitespace-nowrap">{day.date}</TableCell>{[day.billing_records, day.input_tokens, day.output_tokens, day.input_tokens + day.output_tokens, day.error_records].map((value, i) => <TableCell className="tabular-nums" key={i}>{formatMetric(value)}</TableCell>)}<TableCell>{formatUSD(day.charged_usd)}</TableCell><TableCell>{formatUSD(day.refund_usd)}</TableCell></TableRow>)}</TableBody>
          </Table>
          <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground"><span>共 {days.length} 个记录日 · 第 {dailyPage} / {Math.ceil(days.length / 30)} 页</span><div className="flex gap-2"><Button variant="outline" size="sm" disabled={dailyPage === 1} onClick={() => setDailyPage(page => page - 1)}>上一页</Button><Button variant="outline" size="sm" disabled={dailyPage * 30 >= days.length} onClick={() => setDailyPage(page => page + 1)}>下一页</Button></div></div>
        </> : <p className="py-8 text-center text-sm text-muted-foreground">{availability.logs ? '当前筛选没有每日记录' : '调用日志未采集'}</p>}
      </CardContent>
    </Card>
  </div>
}
