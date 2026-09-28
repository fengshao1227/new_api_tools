/**
 * 仪表盘一页的中英文案。
 *
 * 只覆盖仪表盘(Dashboard / GrowthPanel / Business* 经营视图各块 —— 都只被 Dashboard 用),
 * 其余页面保持中文。所以这里既不引 i18n 库,也不做全局 provider:语言由 Dashboard
 * 持有、往下传,页面之外根本看不见它。
 *
 * `zh` 显式标成 `DashboardText`:漏一个键、类型对不上,都在 `tsc` 阶段炸,
 * 而不是等到某一格静默渲染成 `undefined`。
 */

export type DashboardLang = 'en' | 'zh'

/** 经营视图的时间窗,businessDashboard 的请求参数也用它。 */
export type WindowKey = 'today' | '7d' | '30d'

/** 与 Dashboard 里的 `RefreshInterval` 同形(秒,0 = 关闭)。 */
export type RefreshSeconds = 0 | 30 | 60 | 120 | 300

const LANG_STORAGE_KEY = 'dashboard_lang'

/** 数字/时间的 locale。语言只有两种,所以不做映射表。 */
export function localeOf(lang: DashboardLang): string {
  return lang === 'zh' ? 'zh-CN' : 'en-US'
}

export const usd = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

// 坐标轴上取整,表格里保留两位:一条标着 $12.50 / $12.75 的收入轴,
// 运营看了做不出任何决定。
export const usdAxis = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 0,
})

const cnyFormatters: Record<DashboardLang, Intl.NumberFormat> = {
  zh: new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }),
  // narrowSymbol 才是 "¥210.00";en-US 的默认写法是 "CN¥210.00"。
  en: new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'CNY',
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }),
}

export function formatCny(lang: DashboardLang, value: number): string {
  return cnyFormatters[lang].format(value)
}

const EN_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/**
 * 增长趋势图的 x 轴标签。`date` 是 `YYYY-MM`(月)或 `YYYY-MM-DD`(日),后端给的。
 *
 * 用查表而不是 `new Date(date)`:纯日期串按 UTC 午夜解析,东九区看是当天、
 * 西半球看就退一天 —— 轴上的日期会比表格里的少一天。
 */
export function formatTrendLabel(
  date: string,
  granularity: 'daily' | 'monthly',
  lang: DashboardLang,
): string {
  const month = Number(date.slice(5, 7))
  const monthName = EN_MONTHS[month - 1] ?? String(month)
  if (granularity === 'monthly')
    return lang === 'zh' ? `${month} 月` : monthName
  const day = Number(date.slice(8, 10))
  return lang === 'zh' ? `${month}月${day}日` : `${monthName} ${day}`
}

const en = {
  langToggle: '中文',
  langToggleTitle: '切换为中文',

  title: 'Dashboard',
  subtitle: 'Growth, money, conversion and supply for BeatAPI at a glance',

  refresh: 'Refresh',
  refreshing: 'Refreshing…',
  autoRefresh: 'Auto refresh',
  refreshInterval: 'Refresh interval',
  never: 'Never',
  lastRefresh: (time: string) => `Last refresh: ${time}`,
  autoRefreshSetTo: (label: string) => `Auto refresh set to ${label}`,
  autoRefreshOff: 'Auto refresh turned off',
  refreshed: 'Data refreshed',
  refreshFailed: 'Some sections failed to refresh. Please try again shortly.',

  interval: { 0: 'Off', 30: '30s', 60: '1 min', 120: '2 min', 300: '5 min' } as Record<RefreshSeconds, string>,
  noData: 'No data',

  // 增长面板
  growthError: (message: string) => `Could not load growth data: ${message}`,
  growthUsers: 'Users',
  monthUsers: 'Signups this month',
  totalUsers: 'Signups all time',
  monthPayers: 'Paying users this month',
  totalPayers: 'Paying users all time',
  growthRevenue: 'Revenue',
  monthRevenue: 'Revenue this month',
  totalRevenue: 'Revenue all time',
  settledOrders: (n: number) => `${n} settled`,
  convertedFrom: (amount: string, rate: number) => ` · ${amount} of it converted at ¥${rate}/$`,
  growthTrend: 'Growth',
  last30Days: 'Last 30 days',
  last12Months: 'Last 12 months',
  colDate: 'Date',
  series: {
    newUsers: 'New signups',
    newPayers: 'First-time paying users',
    revenue: 'Revenue (USD)',
  },

  // 经营视图
  biz: {
    windowTitle: 'Window for the sections below',
    window: { today: 'Today', '7d': '7 days', '30d': '30 days' } as Record<WindowKey, string>,
    loadFailed: (message: string) => `Could not load this section: ${message}`,
    unavailable: 'Not available on this gateway',

    financeTitle: 'Revenue, billing, cost and margin',
    financeHint:
      "Billed is the gateway's own price — an internal transfer price, not revenue. Revenue is recognised only on paying customers' usage, net of signup gifts.",
    billed: 'Gateway billed',
    billedSub: (calls: string) => `${calls} calls · transfer price, not revenue`,
    providerCost: 'Supplier cost',
    providerCostSub: (paid: string, free: string) => `Paid traffic ${paid} · free/gift ${free}`,
    realizedRevenue: 'Realized usage revenue',
    realizedRevenueSub: "Paying customers' usage, gift credit excluded",
    grossProfit: 'Gross profit (all costs)',
    grossProfitSub: (pct: string) => `Margin ${pct} · includes internal/test cost`,
    externalProfit: 'External gross profit',
    externalProfitSub: (pct: string, internal: string) => `Margin ${pct} · internal/test cost ${internal} left out`,
    cashIn: 'Cash in (settled top-ups)',
    cashInSub: (orders: number, payers: number) => `${orders} orders · ${payers} payers`,
    unknownCurrency: (n: number) => ` · ${n} on an unrecognised rail, not counted`,
    unpricedWarning: (calls: string) =>
      `${calls} calls were logged without a supplier cost — cost is understated and margin overstated.`,
    daily: 'By day',
    financeSeries: { billed: 'Billed', cost: 'Supplier cost', revenue: 'Realized revenue', cash: 'Cash in' },

    modelsTitle: 'Top models',
    modelsBy: { billed: 'By billed', cost: 'By supplier cost', profit: 'By gross profit' },
    colModel: 'Model',
    colRequests: 'Calls',
    colBilled: 'Billed',
    colCost: 'Supplier cost',
    colRevenue: 'Realized revenue',
    colProfit: 'Gross profit',
    colMargin: 'Margin',
    freeExcluded: (names: string, calls: string, cost: string) =>
      `Free models left out: ${names} — ${calls} calls, supplier cost ${cost}`,

    conversionTitle: 'Signup conversion',
    conversionHint: 'Accounts registered in the window, and what they have done since',
    signups: 'Signups',
    activated: 'Used the API',
    paid: 'Paid',
    ofSignups: (pct: string) => `${pct} of signups`,
    paidOfActivated: (pct: string) => `${pct} of API users paid`,
    bySource: 'By acquisition source',
    byCountry: 'By signup country',
    colSource: 'Source',
    colCountry: 'Country',
    colPaidRate: 'Paid rate',
    unattributed: 'Unattributed',
    unknownCountry: 'Unknown',
    attributionMissing: 'This gateway does not record acquisition source or signup country.',

    giftsTitle: 'Gift credit',
    granted: 'Granted to new signups',
    grantedSub: (users: number) => `${users} accounts received a grant`,
    giftBurned: 'Burned by unpaid accounts',
    giftBurnedSub: (cost: string) => `Face value · supplier cost ${cost}`,
    liability: 'Outstanding gift balance',
    liabilitySub: (users: number) => `${users} enabled, never-paid accounts · now`,

    riskTitle: 'Risk review',
    openReview: 'Under review',
    openDeny: 'Duplicate accounts (denied)',
    holdSub: (cases: number, users: number) => `${cases} open cases · ${users} accounts`,
    decisions: 'In this window',
    flagged: 'Flagged',
    confirmed: 'Banned',
    released: 'Released',
    withheld: 'Credit withheld',
    dismissed: 'Dismissed',
    openConsole: 'Review in the gateway console',

    tasksTitle: 'Async task health',
    tasksTotal: 'Tasks',
    tasksFailed: 'Failed',
    tasksInFlight: 'Still running',
    failureRate: 'Failure rate',
    failureRateHint: 'failures ÷ finished tasks',
    refunds: 'Refunded',
    refundsSub: (n: number) => `${n} refunds`,
    colPlatform: 'Platform',
    colTotal: 'Total',
    colSuccess: 'Succeeded',
    colFailure: 'Failed',
    failReasons: 'Top failure reasons',
    noFailures: 'No failures in this window',

    supplyTitle: 'Suppliers and alerts',
    supplyHint: 'Current state, not windowed',
    upstreams: 'Supplier accounts',
    colName: 'Supplier',
    colBalance: 'Balance',
    colStatus: 'Status',
    colChecked: 'Checked',
    status: { 0: 'Unknown', 1: 'OK', 2: 'Error' } as Record<number, string>,
    lowBalance: 'Low balance',
    alerts: 'Open alerts',
    noAlerts: 'No open alerts',
    alertSince: (time: string, count: number) => `since ${time} · seen ${count}×`,

    pricingTitle: 'Pricing gaps',
    pricingHint: 'Both numbers should be zero',
    gapChannels: 'Enabled channels with unpriced models',
    unpricedCalls: 'Unpriced calls in window',
    unpricedSource: { quota_data: 'from quota_data', logs: 'from logs', '': '' } as Record<string, string>,
    noCostExpr: 'no cost_expr',
    missingModels: (n: number, total: number) => `${n} of ${total} models unpriced`,
    allPriced: 'Every enabled channel prices every model it serves',
    colChannel: 'Channel',
    colCalls: 'Calls',
  },
}

export type DashboardText = typeof en

const zh: DashboardText = {
  langToggle: 'EN',
  langToggleTitle: 'Switch to English',

  title: '仪表盘',
  subtitle: 'BeatAPI 增长、收入成本、转化与供给一览',

  refresh: '刷新',
  refreshing: '刷新中...',
  autoRefresh: '自动刷新',
  refreshInterval: '刷新间隔',
  never: '从未',
  lastRefresh: (time: string) => `上次刷新: ${time}`,
  autoRefreshSetTo: (label: string) => `自动刷新已设置为 ${label}`,
  autoRefreshOff: '自动刷新已关闭',
  refreshed: '数据已刷新',
  refreshFailed: '部分板块刷新失败，请稍后再试',

  interval: { 0: '关闭', 30: '30秒', 60: '1分钟', 120: '2分钟', 300: '5分钟' },
  noData: '暂无数据',

  growthError: (message: string) => `增长数据加载失败：${message}`,
  growthUsers: '用户',
  monthUsers: '本月注册',
  totalUsers: '累计注册',
  monthPayers: '本月付费用户',
  totalPayers: '累计付费用户',
  growthRevenue: '收入',
  monthRevenue: '本月收入',
  totalRevenue: '累计收入',
  settledOrders: (n: number) => `${n} 笔已结算`,
  convertedFrom: (amount: string, rate: number) => ` · 其中 ${amount} 按 ¥${rate}/$ 折算`,
  growthTrend: '增长趋势',
  last30Days: '近 30 天',
  last12Months: '近 12 个月',
  colDate: '日期',
  series: {
    newUsers: '新注册用户',
    newPayers: '首次付费用户',
    revenue: '收入（USD）',
  },

  biz: {
    windowTitle: '以下各块的时间窗',
    window: { today: '今天', '7d': '7 天', '30d': '30 天' },
    loadFailed: (message: string) => `本块加载失败：${message}`,
    unavailable: '当前网关没有这项数据',

    financeTitle: '收入、计费、成本与毛利',
    financeHint: '计费额是网关自己的计费价（内部转移价），不是收入；收入只认付费客户的实际消费，并剔除注册赠额。',
    billed: '网关计费额',
    billedSub: (calls: string) => `${calls} 次调用 · 内部转移价，不是收入`,
    providerCost: '供应商成本',
    providerCostSub: (paid: string, free: string) => `付费流量 ${paid} · 免费/赠额 ${free}`,
    realizedRevenue: '实际消费收入',
    realizedRevenueSub: '付费客户的消费，已排除赠额名义收入',
    grossProfit: '全成本毛利',
    grossProfitSub: (pct: string) => `毛利率 ${pct} · 含内部/测试成本`,
    externalProfit: '外部业务毛利',
    externalProfitSub: (pct: string, internal: string) => `毛利率 ${pct} · 不含内部/测试成本 ${internal}`,
    cashIn: '现金收入（到账充值）',
    cashInSub: (orders: number, payers: number) => `${orders} 笔 · ${payers} 人付款`,
    unknownCurrency: (n: number) => ` · ${n} 笔通道无法识别币种，未计入`,
    unpricedWarning: (calls: string) => `有 ${calls} 次调用未记录供应商成本 —— 成本被低估、毛利被高估。`,
    daily: '按日',
    financeSeries: { billed: '计费额', cost: '供应商成本', revenue: '实际消费收入', cash: '现金收入' },

    modelsTitle: '模型排行',
    modelsBy: { billed: '按计费额', cost: '按供应商成本', profit: '按毛利' },
    colModel: '模型',
    colRequests: '调用',
    colBilled: '计费额',
    colCost: '供应商成本',
    colRevenue: '实际消费收入',
    colProfit: '毛利',
    colMargin: '毛利率',
    freeExcluded: (names: string, calls: string, cost: string) => `已排除免费模型：${names} —— ${calls} 次调用，供应商成本 ${cost}`,

    conversionTitle: '注册转化',
    conversionHint: '时间窗内注册的账号，以及他们注册后至今的行为',
    signups: '注册',
    activated: '用过 API',
    paid: '付费',
    ofSignups: (pct: string) => `占注册 ${pct}`,
    paidOfActivated: (pct: string) => `用过 API 的人中 ${pct} 付费`,
    bySource: '按来源',
    byCountry: '按注册国家',
    colSource: '来源',
    colCountry: '国家',
    colPaidRate: '付费率',
    unattributed: '未采集',
    unknownCountry: '未知',
    attributionMissing: '当前网关没有记录来源或注册国家。',

    giftsTitle: '赠额',
    granted: '新注册发出的赠额',
    grantedSub: (users: number) => `${users} 个账号拿到赠额`,
    giftBurned: '未付费账号用掉的赠额',
    giftBurnedSub: (cost: string) => `面值 · 供应商成本 ${cost}`,
    liability: '挂在账上的赠额',
    liabilitySub: (users: number) => `${users} 个启用中、从未付费的账号 · 当前`,

    riskTitle: '风控审核',
    openReview: '审核中',
    openDeny: '多账户（不发赠额）',
    holdSub: (cases: number, users: number) => `${cases} 个待处理 · ${users} 个账号`,
    decisions: '本时间窗内',
    flagged: '新进风控',
    confirmed: '确认封禁',
    released: '放行',
    withheld: '不发赠额',
    dismissed: '驳回',
    openConsole: '去网关控制台审核',

    tasksTitle: '异步任务健康',
    tasksTotal: '任务数',
    tasksFailed: '失败',
    tasksInFlight: '进行中',
    failureRate: '失败率',
    failureRateHint: '失败 ÷ 已结束任务',
    refunds: '退款',
    refundsSub: (n: number) => `${n} 笔退款`,
    colPlatform: '平台',
    colTotal: '总数',
    colSuccess: '成功',
    colFailure: '失败',
    failReasons: '失败原因 Top',
    noFailures: '本时间窗内没有失败',

    supplyTitle: '上游与告警',
    supplyHint: '当前状态，不受时间窗影响',
    upstreams: '供应商账户',
    colName: '供应商',
    colBalance: '余额',
    colStatus: '状态',
    colChecked: '检查时间',
    status: { 0: '未知', 1: '正常', 2: '异常' },
    lowBalance: '余额不足',
    alerts: '未关闭告警',
    noAlerts: '没有未关闭的告警',
    alertSince: (time: string, count: number) => `始于 ${time} · 共 ${count} 次`,

    pricingTitle: '定价缺口',
    pricingHint: '两个数都应该是 0',
    gapChannels: '有未定价模型的启用渠道',
    unpricedCalls: '时间窗内未定价调用',
    unpricedSource: { quota_data: '来自 quota_data', logs: '来自 logs', '': '' },
    noCostExpr: '无 cost_expr',
    missingModels: (n: number, total: number) => `${total} 个模型中 ${n} 个未定价`,
    allPriced: '所有启用渠道的模型都已定价',
    colChannel: '渠道',
    colCalls: '调用',
  },
}

export const DASHBOARD_TEXT: Record<DashboardLang, DashboardText> = { en, zh }

/** 存过就用存的,没存过给英文 —— 这一页的默认语言就是英文。 */
export function readDashboardLang(): DashboardLang {
  try {
    return localStorage.getItem(LANG_STORAGE_KEY) === 'zh' ? 'zh' : 'en'
  } catch {
    return 'en'
  }
}

export function writeDashboardLang(lang: DashboardLang): void {
  try {
    localStorage.setItem(LANG_STORAGE_KEY, lang)
  } catch {
    // 隐私模式下 localStorage 会抛;记不住语言不该让整页崩掉。
  }
}
