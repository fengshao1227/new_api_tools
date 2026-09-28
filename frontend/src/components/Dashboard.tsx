import { useState, useEffect, useRef } from 'react'
import { useToast } from './Toast'
import { GrowthPanel } from './GrowthPanel'
import { BusinessFinancePanel } from './BusinessFinancePanel'
import { BusinessModelRanking } from './BusinessModelRanking'
import { BusinessConversionPanel, BusinessGiftsRiskPanel } from './BusinessCustomersPanel'
import { BusinessPricingGapsPanel, BusinessSupplyPanel, BusinessTasksPanel } from './BusinessOpsPanel'
import { RefreshCw, Timer, ChevronDown, Languages, CalendarRange } from 'lucide-react'
import { Button } from './ui/button'
import { cn } from '../lib/utils'
import {
  DASHBOARD_TEXT,
  localeOf,
  readDashboardLang,
  writeDashboardLang,
  type DashboardLang,
  type RefreshSeconds,
} from '../lib/dashboardI18n'
import { WINDOW_KEYS, useBusinessDashboard, type WindowKey } from '../lib/businessDashboard'

const REFRESH_KEY = 'dashboard_refresh_interval'
const WINDOW_KEY = 'dashboard_business_window'

function readStored(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStored(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // 隐私模式下 localStorage 会抛;记不住设置不该让页面崩掉。
  }
}

function readInterval(): RefreshSeconds {
  const n = Number(readStored(REFRESH_KEY))
  return ([0, 30, 60, 120, 300] as RefreshSeconds[]).includes(n as RefreshSeconds) ? (n as RefreshSeconds) : 0
}

function readWindow(): WindowKey {
  const saved = readStored(WINDOW_KEY)
  return WINDOW_KEYS.includes(saved as WindowKey) ? (saved as WindowKey) : '7d'
}

/**
 * Beat 经营视图。最上面是增长(本月/累计注册、付费、收入 + 趋势表),不受时间窗影响;
 * 下面各块跟着时间窗走,每块一个接口、各自加载。明细(日志、渠道、任务、风控)
 * 在网关控制台看,这里只做跨表汇总。
 */
export function Dashboard() {
  const { showToast } = useToast()
  const [lang, setLang] = useState<DashboardLang>(readDashboardLang)
  const t = DASHBOARD_TEXT[lang]
  const locale = localeOf(lang)
  const toggleLang = () => {
    const next: DashboardLang = lang === 'en' ? 'zh' : 'en'
    setLang(next)
    writeDashboardLang(next)
  }

  const [range, setRange] = useState<WindowKey>(readWindow)
  const { state, reload } = useBusinessDashboard(range)
  const chooseRange = (w: WindowKey) => {
    setRange(w)
    writeStored(WINDOW_KEY, w)
  }

  const [refreshing, setRefreshing] = useState(false)
  const [lastRefreshTime, setLastRefreshTime] = useState<Date | null>(null)
  const [refreshInterval, setRefreshInterval] = useState<RefreshSeconds>(readInterval)
  const [countdown, setCountdown] = useState<number>(readInterval)
  const [showIntervalDropdown, setShowIntervalDropdown] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)

  /**
   * 手动刷新绕过缓存;自动刷新走后端缓存(毛利 5 分钟、其余 1–5 分钟)。
   * 毛利一块要扫整个窗口的日志,30 秒一次强刷会把生产库压着跑。
   */
  const handleRefresh = async (noCache: boolean) => {
    if (refreshing) return
    setRefreshing(true)
    setLastRefreshTime(new Date()) // 也让增长面板重新拉取
    try {
      const ok = await reload(noCache)
      if (noCache || !ok) showToast(ok ? 'success' : 'error', ok ? t.refreshed : t.refreshFailed)
      if (refreshInterval > 0) setCountdown(refreshInterval)
    } finally {
      setRefreshing(false)
    }
  }

  // 计时器里总是调用最新的 handleRefresh(它捕获了当前时间窗)。
  const handleRefreshRef = useRef(handleRefresh)
  useEffect(() => {
    handleRefreshRef.current = handleRefresh
  })

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setShowIntervalDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  useEffect(() => {
    if (refreshInterval === 0) {
      setCountdown(0)
      return
    }
    const timer = setInterval(() => {
      setCountdown((prev) => {
        if (prev <= 1) {
          handleRefreshRef.current(false)
          return refreshInterval
        }
        return prev - 1
      })
    }, 1000)
    return () => clearInterval(timer)
  }, [refreshInterval])

  const handleSetRefreshInterval = (interval: RefreshSeconds) => {
    setRefreshInterval(interval)
    if (interval > 0) {
      setCountdown(interval)
      writeStored(REFRESH_KEY, interval.toString())
      showToast('success', t.autoRefreshSetTo(t.interval[interval]))
    } else {
      writeStored(REFRESH_KEY, null)
      showToast('info', t.autoRefreshOff)
    }
    setShowIntervalDropdown(false)
  }

  const formatCountdown = (seconds: number) => {
    const mins = Math.floor(seconds / 60)
    const secs = seconds % 60
    return mins > 0 ? `${mins}:${secs.toString().padStart(2, '0')}` : `${secs}s`
  }

  return (
    <div className="space-y-8 animate-in fade-in duration-500">
      <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div>
          <h2 className="text-3xl font-bold tracking-tight">{t.title}</h2>
          <p className="text-muted-foreground mt-1">{t.subtitle}</p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <Button variant="ghost" size="sm" onClick={toggleLang} className="h-9 px-2 text-muted-foreground" title={t.langToggleTitle}>
            <Languages className="h-4 w-4 mr-1.5" />
            {t.langToggle}
          </Button>

          <Button variant="outline" size="sm" onClick={() => handleRefresh(true)} disabled={refreshing} className="h-9">
            <RefreshCw className={cn('h-4 w-4 mr-2', refreshing && 'animate-spin')} />
            {refreshing ? t.refreshing : t.refresh}
          </Button>

          <div className="relative" ref={dropdownRef}>
            <Button variant="outline" size="sm" onClick={() => setShowIntervalDropdown(!showIntervalDropdown)} className="h-9 min-w-[100px]">
              <Timer className="h-4 w-4 mr-2" />
              {refreshInterval > 0 ? <span className="text-primary font-medium">{formatCountdown(countdown)}</span> : t.autoRefresh}
              <ChevronDown className="h-3 w-3 ml-1" />
            </Button>
            {showIntervalDropdown && (
              <div className="absolute right-0 mt-1 w-48 bg-popover border rounded-md shadow-lg z-50">
                <div className="p-2 border-b">
                  <p className="text-xs text-muted-foreground">{t.refreshInterval}</p>
                </div>
                <div className="p-1">
                  {([0, 30, 60, 120, 300] as RefreshSeconds[]).map((interval) => (
                    <button
                      key={interval}
                      onClick={() => handleSetRefreshInterval(interval)}
                      className={cn(
                        'w-full text-left px-3 py-2 text-sm rounded hover:bg-accent transition-colors',
                        refreshInterval === interval && 'bg-accent text-accent-foreground',
                      )}
                    >
                      {t.interval[interval]}
                    </button>
                  ))}
                </div>
                <div className="p-2 border-t">
                  <p className="text-xs text-muted-foreground">
                    {t.lastRefresh(
                      lastRefreshTime
                        ? lastRefreshTime.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
                        : t.never,
                    )}
                  </p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* 增长:注册、付费、收入。魔尊要求放最上面;它只读 users / top_ups,不跟时间窗走。 */}
      <GrowthPanel refreshToken={lastRefreshTime?.getTime()} lang={lang} />

      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-t pt-6">
        <h3 className="text-sm font-medium text-muted-foreground flex items-center gap-2">
          <CalendarRange className="w-4 h-4" />
          {t.biz.windowTitle}
        </h3>
        <div className="inline-flex rounded-lg border bg-muted/50 p-1">
          {WINDOW_KEYS.map((w) => (
            <Button key={w} variant={range === w ? 'default' : 'ghost'} size="sm" onClick={() => chooseRange(w)} className="h-7 text-xs px-3">
              {t.biz.window[w]}
            </Button>
          ))}
        </div>
      </div>

      <BusinessFinancePanel state={state.finance} lang={lang} />
      <BusinessConversionPanel state={state.conversion} lang={lang} />
      <BusinessGiftsRiskPanel state={state.giftsRisk} finance={state.finance} lang={lang} />
      <BusinessTasksPanel state={state.tasks} lang={lang} />
      <BusinessSupplyPanel state={state.supply} lang={lang} />
      <BusinessPricingGapsPanel state={state.pricingGaps} lang={lang} />
      <BusinessModelRanking state={state.finance} lang={lang} />
    </div>
  )
}
