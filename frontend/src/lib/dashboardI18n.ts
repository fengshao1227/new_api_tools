/**
 * 仪表盘一页的中英文案。
 *
 * 只覆盖仪表盘(Dashboard / GrowthPanel / TrendChart —— 后两个只被 Dashboard 用),
 * 其余页面保持中文。所以这里既不引 i18n 库,也不做全局 provider:语言由 Dashboard
 * 持有、往下传两层,页面之外根本看不见它。
 *
 * `zh` 显式标成 `DashboardText`:漏一个键、类型对不上,都在 `tsc` 阶段炸,
 * 而不是等到某一格静默渲染成 `undefined`。
 */

export type DashboardLang = 'en' | 'zh'

/** 与 Dashboard 里的 `PeriodType` 同形。 */
export type PeriodKey = '24h' | '3d' | '7d' | '14d'

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
  subtitle: 'System health and live activity at a glance',

  refresh: 'Refresh',
  refreshing: 'Refreshing…',
  retry: 'Retry',
  retrying: 'Retrying…',
  autoRefresh: 'Auto refresh',
  refreshInterval: 'Refresh interval',
  never: 'Never',
  lastRefresh: (time: string) => `Last refresh: ${time}`,
  autoRefreshSetTo: (label: string) => `Auto refresh set to ${label}`,
  autoRefreshOff: 'Auto refresh turned off',
  refreshed: 'Data refreshed',
  refreshFailed: 'Refresh failed. Please try again shortly.',
  refreshFailedDetail: 'Refresh failed. Please try again shortly — the database may be under heavy load.',
  retryFailed: 'Retry failed. Please try again shortly.',
  retryFailedDetail: 'Retry failed. Please try again shortly — the database may be under heavy load.',
  loadTimeout: 'The dashboard timed out. Please try again — the database may be under heavy load.',

  period: { '24h': '24h', '3d': '3d', '7d': '7d', '14d': '14d' } as Record<PeriodKey, string>,
  interval: { 0: 'Off', 30: '30s', 60: '1 min', 120: '2 min', 300: '5 min' } as Record<RefreshSeconds, string>,

  // 大型系统刷新确认
  confirmRefreshTitle: 'Confirm a full refresh',
  scaleLarge: 'Large system',
  scaleHuge: 'Very large system',
  logsToScan: 'Logs to scan',
  estimatedTime: 'Estimated time',
  rows: (formatted: string) => `${formatted} rows`,
  cancel: 'Cancel',
  confirmRefresh: 'Refresh anyway',
  refreshingData: 'Refreshing data…',
  scanningRows: (formatted: string) => `Scanning ${formatted} log rows`,
  manyRows: 'a lot of',
  estimatedWait: (formatted: string) => `This should take about ${formatted}. Please wait.`,
  aWhile: 'a while',

  // 平台资源
  resources: 'Platform resources',
  users: 'Users',
  tokens: 'Tokens',
  channels: 'Channels',
  models: 'Models',
  redemptions: 'Redemption codes',
  activeSuffix: (n: number, period: string) => `${n} active (${period})`,
  onlineSuffix: (n: number) => `${n} online`,
  availableModels: 'Available',
  unusedSuffix: (n: number) => `${n} unused`,

  // 流量分析
  traffic: (period: string) => `Traffic (${period})`,
  totalRequests: 'Requests',
  quotaSpent: 'Spend',
  totalTokens: 'Total tokens',
  promptTokens: 'Input tokens',
  completionTokens: 'Output tokens',
  avgResponse: 'Avg. response',

  // 模型分布
  modelUsage: 'Model usage',
  topModels: (period: string) => `Top 8 models by requests (${period})`,
  noData: 'No data',

  // 榜首
  requestKing: 'Busiest user',
  requestKingSub: (period: string) => `Most requests (${period})`,
  requestKingValue: 'Total requests',
  quotaKing: 'Top spender',
  quotaKingSub: (period: string) => `Highest spend (${period})`,
  quotaKingValue: 'Total spend',

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
    newPayers: 'New paying users',
    revenue: 'Revenue (USD)',
  },

  // 请求趋势图
  hourlyTrend: 'Requests by hour',
  dailyTrend: 'Requests by day',
  trendDesc: {
    '24h': 'Last 24 hours',
    '3d': 'Last 3 days',
    '7d': 'Last 7 days',
    '14d': 'Last 14 days',
  } as Record<PeriodKey, string>,
  trendTotal: 'Total requests',
  noTrendData: 'No trend data yet',
  tipRequests: 'Requests',
  tipUsers: 'Users',
  tipSpend: 'Spend',
}

export type DashboardText = typeof en

const zh: DashboardText = {
  langToggle: 'EN',
  langToggleTitle: 'Switch to English',

  title: '仪表盘',
  subtitle: '系统运行状态与实时数据概览',

  refresh: '刷新',
  refreshing: '刷新中...',
  retry: '重试',
  retrying: '重试中...',
  autoRefresh: '自动刷新',
  refreshInterval: '刷新间隔',
  never: '从未',
  lastRefresh: (time: string) => `上次刷新: ${time}`,
  autoRefreshSetTo: (label: string) => `自动刷新已设置为 ${label}`,
  autoRefreshOff: '自动刷新已关闭',
  refreshed: '数据已刷新',
  refreshFailed: '刷新失败，请稍后再试',
  refreshFailedDetail: '刷新失败，请稍后再试（可能是数据库负载过高）',
  retryFailed: '重试失败，请稍后再试',
  retryFailedDetail: '重试失败，请稍后再试（可能是数据库负载过高）',
  loadTimeout: '仪表盘加载超时，请稍后重试（可能是数据库负载过高）',

  period: { '24h': '24小时', '3d': '3天', '7d': '7天', '14d': '14天' },
  interval: { 0: '关闭', 30: '30秒', 60: '1分钟', 120: '2分钟', 300: '5分钟' },

  confirmRefreshTitle: '确认刷新数据',
  scaleLarge: '大型系统',
  scaleHuge: '超大型系统',
  logsToScan: '预计扫描日志',
  estimatedTime: '预计耗时',
  rows: (formatted: string) => `${formatted} 条`,
  cancel: '取消',
  confirmRefresh: '确认刷新',
  refreshingData: '正在刷新数据...',
  scanningRows: (formatted: string) => `正在查询 ${formatted} 条日志数据`,
  manyRows: '大量',
  estimatedWait: (formatted: string) => `预计需要 ${formatted}，请耐心等待`,
  aWhile: '较长时间',

  resources: '平台资源',
  users: '用户总数',
  tokens: '令牌总数',
  channels: '渠道总数',
  models: '模型数量',
  redemptions: '兑换码',
  activeSuffix: (n: number, period: string) => `${n} 活跃(${period})`,
  onlineSuffix: (n: number) => `${n} 在线`,
  availableModels: '可用模型',
  unusedSuffix: (n: number) => `${n} 未用`,

  traffic: (period: string) => `流量分析 (${period})`,
  totalRequests: '请求总数',
  quotaSpent: '消耗额度',
  totalTokens: '总 Token',
  promptTokens: '输入 Token',
  completionTokens: '输出 Token',
  avgResponse: '平均响应',

  modelUsage: '模型使用分布',
  topModels: (period: string) => `${period}内 Top 8 活跃模型`,
  noData: '暂无数据',

  requestKing: '请求之王',
  requestKingSub: (period: string) => `${period}内请求数最多`,
  requestKingValue: '总请求数',
  quotaKing: '土豪榜首',
  quotaKingSub: (period: string) => `${period}内消耗额度最多`,
  quotaKingValue: '总消耗额度',

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
    newPayers: '新增付费用户',
    revenue: '收入（USD）',
  },

  hourlyTrend: '每小时请求趋势',
  dailyTrend: '每日请求趋势',
  trendDesc: {
    '24h': '24小时数据概览',
    '3d': '近3天数据概览',
    '7d': '近7天数据概览',
    '14d': '近14天数据概览',
  },
  trendTotal: '请求总数',
  noTrendData: '暂无趋势数据',
  tipRequests: '请求数',
  tipUsers: '用户数',
  tipSpend: '消耗',
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
