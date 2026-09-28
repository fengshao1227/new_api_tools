import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { useAuth } from '../contexts/AuthContext'
import { useToast } from './Toast'
import ReactECharts from 'echarts-for-react/esm/core'
import * as echarts from 'echarts/core'
import { MapChart } from 'echarts/charts'
import { TooltipComponent, VisualMapComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([MapChart, TooltipComponent, VisualMapComponent, CanvasRenderer])
import {
  Globe, MapPin, RefreshCw, Loader2, Flag,
  AlertTriangle, Activity, ChevronDown, Timer,
  Database, CheckCircle2
} from 'lucide-react'
import { IPLookup } from './IPLookup'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from './ui/card'
import { Button } from './ui/button'
import { cn } from '../lib/utils'

interface CountryStats {
  country: string
  country_code: string
  ip_count: number
  request_count: number
  user_count: number
  percentage: number
}

interface IPDistributionData {
  total_ips: number
  total_requests: number
  sampled_ip_limit: number
  sampled_ips: number
  sampled_requests: number
  coverage_percentage: number
  geo_available: boolean
  by_country: CountryStats[]
  snapshot_time: number
}

type TimeWindow = '1h' | '6h' | '24h' | '7d'
// GeoIP names countries in Chinese; the ECharts world map (public/world.json)
// names its regions in English, some abbreviated. Intl gives the English name
// for an ISO code; these are the codes whose world.json name differs from it.
const WORLD_MAP_NAME_OVERRIDES: Record<string, string> = {
  AG: 'Antigua and Barb.', AX: 'Aland', BA: 'Bosnia and Herz.', CD: 'Dem. Rep. Congo',
  CF: 'Central African Rep.', CG: 'Congo', CI: "Côte d'Ivoire", CZ: 'Czech Rep.',
  DO: 'Dominican Rep.', EH: 'W. Sahara', FK: 'Falkland Is.', FO: 'Faeroe Is.',
  GQ: 'Eq. Guinea', GS: 'S. Geo. and S. Sandw. Is.', HM: 'Heard I. and McDonald Is.',
  IO: 'Br. Indian Ocean Ter.', KP: 'Dem. Rep. Korea', KR: 'Korea', KY: 'Cayman Is.',
  LA: 'Lao PDR', LC: 'Saint Lucia', MK: 'Macedonia', MM: 'Myanmar', MP: 'N. Mariana Is.',
  PF: 'Fr. Polynesia', PM: 'St. Pierre and Miquelon', PS: 'Palestine', SB: 'Solomon Is.',
  SH: 'Saint Helena', SS: 'S. Sudan', ST: 'São Tomé and Principe', SZ: 'Swaziland',
  TC: 'Turks and Caicos Is.', TF: 'Fr. S. Antarctic Lands', TR: 'Turkey',
  TT: 'Trinidad and Tobago', VC: 'St. Vin. and Gren.', VI: 'U.S. Virgin Is.',
}

const englishRegionNames = (() => {
  try {
    return new Intl.DisplayNames(['en'], { type: 'region' })
  } catch {
    return null
  }
})()

function worldMapName(item: CountryStats) {
  const code = (item.country_code || '').toUpperCase()
  if (WORLD_MAP_NAME_OVERRIDES[code]) return WORLD_MAP_NAME_OVERRIDES[code]
  if (/^[A-Z]{2}$/.test(code) && code !== 'XX' && code !== 'LO') {
    try {
      const name = englishRegionNames?.of(code)
      if (name && name !== code) return name
    } catch {
      // Unknown code: fall through to the GeoIP name.
    }
  }
  return item.country
}

const COUNTRY_RANK_LIMIT = 20

function formatCountdown(seconds: number) {
  const mins = Math.floor(seconds / 60)
  const secs = seconds % 60
  return mins > 0 ? `${mins}:${secs.toString().padStart(2, '0')}` : `${secs}s`
}

const getIntervalLabel = (interval: number) => {
  switch (interval) {
    case 0: return '关闭'
    case 30: return '30秒'
    case 60: return '1分钟'
    case 120: return '2分钟'
    case 300: return '5分钟'
    case 600: return '10分钟'
    default: return '关闭'
  }
}

export function IPAnalysis() {
  const { token } = useAuth()
  const { showToast } = useToast()
  const [data, setData] = useState<IPDistributionData | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [timeWindow, setTimeWindow] = useState<TimeWindow>('24h')
  const [mapLoaded, setMapLoaded] = useState(false)
  const mapLoadedRef = useRef(false)
  
  const [showIntervalDropdown, setShowIntervalDropdown] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)

  // 点击外部关闭下拉菜单
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setShowIntervalDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  // 自动刷新相关状态
  const IP_REFRESH_KEY = 'ip_analysis_refresh_interval'
  const [refreshInterval, setRefreshInterval] = useState<number>(() => {
    const saved = localStorage.getItem(IP_REFRESH_KEY)
    return saved ? parseInt(saved, 10) : 0  // 默认0，等待从后端获取
  })
  const [countdown, setCountdown] = useState(refreshInterval)
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const refreshIntervalRef = useRef(refreshInterval)
  const [systemScale, setSystemScale] = useState<string>('')  // 系统规模描述

  // 主题检测
  const [isDarkMode, setIsDarkMode] = useState(() => {
    if (typeof window !== 'undefined') {
      return document.documentElement.classList.contains('dark')
    }
    return false
  })

  // 监听主题变化
  useEffect(() => {
    const observer = new MutationObserver((mutations) => {
      mutations.forEach((mutation) => {
        if (mutation.attributeName === 'class') {
          setIsDarkMode(document.documentElement.classList.contains('dark'))
        }
      })
    })
    observer.observe(document.documentElement, { attributes: true })
    return () => observer.disconnect()
  }, [])

  const apiUrl = import.meta.env.VITE_API_URL || ''
  const [mapError, setMapError] = useState(false)
  
  // 加载世界地图 - 多源备用 + 超时处理
  useEffect(() => {
    if (mapLoadedRef.current) return
    mapLoadedRef.current = true
    
    const MAP_SOURCES = [
      '/world.json', // 本地文件优先
      'https://cdn.jsdelivr.net/gh/mouday/echarts-map@master/echarts-4.2.1-rc1-map/json/world.json',
      'https://fastly.jsdelivr.net/gh/mouday/echarts-map@master/echarts-4.2.1-rc1-map/json/world.json',
      'https://gcore.jsdelivr.net/gh/mouday/echarts-map@master/echarts-4.2.1-rc1-map/json/world.json',
    ]
    
    const fetchWithTimeout = (url: string, timeout = 8000): Promise<Response> => {
      return Promise.race([
        fetch(url),
        new Promise<never>((_, reject) => 
          setTimeout(() => reject(new Error('Timeout')), timeout)
        )
      ])
    }
    
    const tryLoadMap = async () => {
      for (const url of MAP_SOURCES) {
        try {
          console.log(`[Map] Trying: ${url}`)
          const res = await fetchWithTimeout(url)
          if (!res.ok) continue
          const worldJson = await res.json()
          echarts.registerMap('world', worldJson)
          setMapLoaded(true)
          console.log(`[Map] Loaded from: ${url}`)
          return
        } catch (err) {
          console.warn(`[Map] Failed: ${url}`, err)
        }
      }
      // 所有源都失败
      console.error('[Map] All sources failed')
      setMapError(true)
    }
    
    tryLoadMap()
  }, [])

  const getAuthHeaders = useCallback(() => ({
    'Content-Type': 'application/json',
    'Authorization': `Bearer ${token}`,
  }), [token])

  // 获取系统规模设置，自动调整刷新间隔（仅当用户没有手动设置时）
  const fetchSystemScale = useCallback(async () => {
    try {
      const response = await fetch(`${apiUrl}/api/system/scale`, { headers: getAuthHeaders() })
      const res = await response.json()
      if (res.success && res.data?.settings) {
        const settings = res.data.settings
        const interval = settings.frontend_refresh_interval || 60
        setSystemScale(settings.description || '')

        // 只有在用户没有手动设置过时，才使用系统推荐值
        const saved = localStorage.getItem(IP_REFRESH_KEY)
        if (!saved) {
          setRefreshInterval(interval)
          setCountdown(interval)
          refreshIntervalRef.current = interval
        }
      }
    } catch (error) {
      console.error('Failed to fetch system scale:', error)
    }
  }, [apiUrl, getAuthHeaders])

  // 组件加载时获取系统规模设置
  useEffect(() => {
    fetchSystemScale()
  }, [fetchSystemScale])

  const fetchData = useCallback(async (noCache = false) => {
    try {
      const cacheParam = noCache ? '&no_cache=true' : ''
      const response = await fetch(
        `${apiUrl}/api/dashboard/ip-distribution?window=${timeWindow}${cacheParam}`,
        { headers: getAuthHeaders() }
      )
      const result = await response.json()
      if (result.success) {
        setData(result.data)
      }
    } catch (error) {
      console.error('Failed to fetch IP distribution:', error)
      showToast('error', '获取 IP 分布数据失败')
    }
  }, [apiUrl, getAuthHeaders, timeWindow, showToast])

  useEffect(() => {
    const loadData = async () => {
      setLoading(true)
      await fetchData()
      setLoading(false)
    }
    loadData()
  }, [fetchData])

  const handleRefresh = async () => {
    setRefreshing(true)
    await fetchData(true)
    setRefreshing(false)
    showToast('success', '数据已刷新')
  }

  // 更新 refreshIntervalRef
  useEffect(() => {
    refreshIntervalRef.current = refreshInterval
  }, [refreshInterval])

  // 保存刷新间隔到 localStorage
  const handleRefreshIntervalChange = useCallback((val: number) => {
    setRefreshInterval(val)
    setCountdown(val)
    refreshIntervalRef.current = val
    if (val > 0) {
      localStorage.setItem(IP_REFRESH_KEY, val.toString())
      const label = val >= 60 ? `${val / 60}分钟` : `${val}秒`
      showToast('success', `IP 分析自动刷新已设置为 ${label}`)
    } else {
      localStorage.removeItem(IP_REFRESH_KEY)
      showToast('info', 'IP 分析自动刷新已关闭')
    }
  }, [showToast])

  // 自动刷新逻辑
  useEffect(() => {
    if (refreshInterval <= 0) {
      if (countdownRef.current) {
        clearInterval(countdownRef.current)
        countdownRef.current = null
      }
      return
    }

    setCountdown(refreshIntervalRef.current)

    const doAutoRefresh = async () => {
      await fetchData(true)
    }

    countdownRef.current = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          doAutoRefresh()
          return refreshIntervalRef.current
        }
        return prev - 1
      })
    }, 1000)

    return () => {
      if (countdownRef.current) {
        clearInterval(countdownRef.current)
      }
    }
  }, [refreshInterval, fetchData])

  const formatNumber = (num: number) => {
    if (num >= 1000000) return `${(num / 1000000).toFixed(1)}M`
    if (num >= 1000) return `${(num / 1000).toFixed(1)}K`
    return num.toString()
  }

  const getTimeWindowLabel = (window: TimeWindow) => {
    const labels: Record<TimeWindow, string> = {
      '1h': '1小时',
      '6h': '6小时',
      '24h': '24小时',
      '7d': '7天',
    }
    return labels[window]
  }

  // 世界地图配置
  const worldMapOption = useMemo(() => {
    if (!data || !mapLoaded) return {}

    const maxValue = data.by_country[0]?.request_count || 100

    // 构建数据映射用于 tooltip
    const dataMap = new Map(
      data.by_country.map(item => [worldMapName(item), item])
    )

    // 转换数据为 ECharts 格式
    const mapData = data.by_country.map(item => ({
      name: worldMapName(item),
      value: item.request_count,
    }))

    // 主题相关配色
    const themeColors = isDarkMode ? {
      bgColor: '#0c1929',
      areaColor: '#1e3a5f',
      borderColor: '#2d4a6f',
      emphasisColor: '#fbbf24',
      textColor: '#94a3b8',
      tooltipBg: 'rgba(15, 23, 42, 0.95)',
      tooltipBorder: '#334155',
      gradientColors: ['#1e3a5f', '#1d4ed8', '#3b82f6', '#60a5fa', '#93c5fd']
    } : {
      bgColor: '#f0f9ff',
      areaColor: '#f8fafc',
      borderColor: '#cbd5e1',
      emphasisColor: '#f59e0b',
      textColor: '#64748b',
      tooltipBg: 'rgba(255, 255, 255, 0.98)',
      tooltipBorder: '#e2e8f0',
      gradientColors: ['#eff6ff', '#bfdbfe', '#60a5fa', '#3b82f6', '#1d4ed8']
    }

    return {
      backgroundColor: themeColors.bgColor,
      tooltip: {
        trigger: 'item',
        backgroundColor: themeColors.tooltipBg,
        borderColor: themeColors.tooltipBorder,
        borderWidth: 1,
        padding: [12, 16],
        textStyle: {
          color: isDarkMode ? '#e2e8f0' : '#1e293b',
          fontSize: 13,
        },
        formatter: (params: { name: string; seriesType?: string }) => {
          if (params.seriesType === 'effectScatter') {
            return ''
          }
          const itemData = dataMap.get(params.name)
          if (itemData) {
            // Share of the sampled requests, the same figure as the ranking table.
            const percentage = itemData.percentage.toFixed(2)
            return `
              <div style="font-weight: 600; font-size: 14px; margin-bottom: 8px; padding-bottom: 8px; border-bottom: 1px solid ${themeColors.tooltipBorder}">
                ${params.name}
              </div>
              <div style="display: grid; grid-template-columns: auto auto; gap: 4px 16px; font-size: 13px;">
                <span style="color: ${themeColors.textColor}">流量</span>
                <span style="font-weight: 500; text-align: right;">${itemData.request_count.toLocaleString('zh-CN')}</span>
                <span style="color: ${themeColors.textColor}">占比</span>
                <span style="font-weight: 500; text-align: right;">${percentage}%</span>
                <span style="color: ${themeColors.textColor}">IP 数</span>
                <span style="font-weight: 500; text-align: right;">${itemData.ip_count.toLocaleString('zh-CN')}</span>
                <span style="color: ${themeColors.textColor}">用户数</span>
                <span style="font-weight: 500; text-align: right;">${itemData.user_count.toLocaleString('zh-CN')}</span>
              </div>
            `
          }
          return `<div style="font-weight: 500">${params.name}</div><div style="color: ${themeColors.textColor}; font-size: 12px; margin-top: 4px;">暂无数据</div>`
        }
      },
      visualMap: {
        min: 0,
        max: maxValue,
        text: ['高', '低'],
        realtime: false,
        calculable: true,
        inRange: {
          color: themeColors.gradientColors
        },
        textStyle: {
          color: themeColors.textColor,
          fontSize: 12
        },
        left: 20,
        bottom: 20,
        itemWidth: 12,
        itemHeight: 120,
      },
      series: [
        {
          name: '流量分布',
          type: 'map',
          map: 'world',
          roam: true,
          scaleLimit: { min: 1, max: 10 },
          zoom: 1.2,
          emphasis: {
            label: {
              show: true,
              color: isDarkMode ? '#f8fafc' : '#1e293b',
              fontSize: 12,
              fontWeight: 500,
            },
            itemStyle: {
              areaColor: themeColors.emphasisColor,
              shadowColor: 'rgba(0, 0, 0, 0.3)',
              shadowBlur: 10,
            }
          },
          select: { disabled: true },
          itemStyle: {
            areaColor: themeColors.areaColor,
            borderColor: themeColors.borderColor,
            borderWidth: 0.5
          },
          label: { show: false },
          data: mapData
        }
      ]
    }
  }, [data, mapLoaded, isDarkMode])

  // Countries GeoIP could place: not the "未知" row, not private networks.
  const placedCountries = (data?.by_country || []).filter(
    (item) => item.country_code && item.country_code !== 'XX' && item.country_code !== 'LO'
  )

  if (loading) {
    return (
      <div className="flex justify-center items-center py-40">
        <Loader2 className="h-12 w-12 animate-spin text-primary" />
      </div>
    )
  }

  return (
    <div className="space-y-6 animate-in fade-in duration-500">
      {/* Header */}
      <div className="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div>
          <h2 className="text-3xl font-bold tracking-tight flex items-center gap-2">
            <Globe className="w-8 h-8 text-primary" />
            IP 地区分析
          </h2>
          <p className="text-muted-foreground mt-1">
            访问来源地区分布与流量统计
          </p>
        </div>
        <div className="flex items-center gap-3">
          <Button
            variant="outline"
            size="sm"
            onClick={handleRefresh}
            disabled={refreshing}
            className="h-9"
          >
            <RefreshCw className={cn("h-4 w-4 mr-2", refreshing && "animate-spin")} />
            {refreshing ? '正在获取最新数据...' : (
              refreshInterval > 0 ? `刷新 (${countdown}s)` : '刷新'
            )}
          </Button>
          <div className="relative" ref={dropdownRef}>
            <Button 
              variant="outline" 
              size="sm" 
              onClick={() => setShowIntervalDropdown(!showIntervalDropdown)}
              className="h-9 min-w-[100px]"
              title={systemScale ? `当前系统规模: ${systemScale}` : ''}
            >
              <Timer className="h-4 w-4 mr-2" />
              {refreshInterval > 0 ? (
                <span className="flex items-center gap-1">
                  <span className="text-primary font-medium">{formatCountdown(countdown)}</span>
                </span>
              ) : (
                '自动刷新'
              )}
              <ChevronDown className="h-3 w-3 ml-1" />
            </Button>
            
            {showIntervalDropdown && (
              <div className="absolute right-0 mt-1 w-48 bg-popover border rounded-md shadow-lg z-50">
                <div className="p-2 border-b">
                  <p className="text-xs text-muted-foreground">刷新间隔</p>
                </div>
                <div className="p-1">
                  {([0, 30, 60, 300, 600]).map((interval) => (
                    <button
                      key={interval}
                      onClick={() => {
                        handleRefreshIntervalChange(interval)
                        setShowIntervalDropdown(false)
                      }}
                      className={cn(
                        "w-full text-left px-3 py-2 text-sm rounded hover:bg-accent transition-colors",
                        refreshInterval === interval && "bg-accent text-accent-foreground"
                      )}
                    >
                      {getIntervalLabel(interval)}
                    </button>
                  ))}
                </div>
              </div>
            )}
          </div>
          {systemScale && (
            <span className="text-xs text-muted-foreground hidden sm:inline" title="根据系统规模自动检测">
              {systemScale}
            </span>
          )}
          <div className="inline-flex rounded-lg border bg-muted/50 p-1">
            {(['1h', '6h', '24h', '7d'] as TimeWindow[]).map((w) => (
              <Button
                key={w}
                variant={timeWindow === w ? 'default' : 'ghost'}
                size="sm"
                onClick={() => setTimeWindow(w)}
                className="h-7 text-xs px-3"
              >
                {getTimeWindowLabel(w)}
              </Button>
            ))}
          </div>
        </div>
      </div>

      {/* Overview Stats */}
      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 gap-4">
        <StatCard
          title="独立 IP 数"
          value={formatNumber(data?.total_ips || 0)}
          rawValue={data?.total_ips || 0}
          icon={MapPin}
          color="blue"
        />
        <StatCard
          title="总流量"
          value={formatNumber(data?.total_requests || 0)}
          rawValue={data?.total_requests || 0}
          icon={Activity}
          color="emerald"
        />
        <StatCard
          title="国家/地区数(样本)"
          value={formatNumber(placedCountries.length)}
          rawValue={placedCountries.length}
          icon={Flag}
          color="purple"
        />
        <StatCard
          title="样本覆盖"
          value={`${(data?.coverage_percentage || 0).toFixed(1)}%`}
          rawValue={data?.sampled_requests || 0}
          icon={Database}
          color="cyan"
        />
        <StatCard
          title="GeoIP"
          value={data?.geo_available ? '已就绪' : '未就绪'}
          icon={CheckCircle2}
          color={data?.geo_available ? 'emerald' : 'slate'}
        />
      </div>

      {/* IP Lookup */}
      <IPLookup />

      {/* World Map */}
      <Card className="shadow-sm">
        <CardHeader className="pb-2">
          <CardTitle className="text-lg flex items-center gap-2">
            <Globe className="w-5 h-5 text-muted-foreground" />
            Web 流量请求（按国家/地区）
          </CardTitle>
          <CardDescription>
            过去 {getTimeWindowLabel(timeWindow)} · Top {formatNumber(data?.sampled_ip_limit || 3000)} IP 地理样本
            {data && ` · 覆盖 ${data.coverage_percentage.toFixed(1)}% 流量`}
          </CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {mapError ? (
            <div className="h-[450px] flex flex-col items-center justify-center text-muted-foreground bg-muted/20 rounded-b-lg gap-3">
              <AlertTriangle className="h-10 w-10 text-yellow-500" />
              <span>地图加载失败，请刷新页面重试</span>
              <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                刷新页面
              </Button>
            </div>
          ) : !mapLoaded ? (
            <div className="h-[450px] flex items-center justify-center text-muted-foreground">
              <Loader2 className="h-8 w-8 animate-spin mr-2" />
              加载地图中...
            </div>
          ) : data && data.by_country.length > 0 ? (
            <div className="relative overflow-hidden rounded-b-lg">
              <ReactECharts
                key={isDarkMode ? 'dark' : 'light'}
                echarts={echarts}
                option={worldMapOption}
                style={{ height: '450px', width: '100%' }}
                opts={{ renderer: 'canvas' }}
              />
            </div>
          ) : (
            <div className="h-[450px] flex items-center justify-center text-muted-foreground bg-muted/20 rounded-b-lg">
              暂无数据
            </div>
          )}
        </CardContent>
      </Card>

      {/* Country Ranking */}
      <Card className="shadow-sm">
        <CardHeader className="pb-2">
          <CardTitle className="text-lg">国家/地区排名</CardTitle>
          <CardDescription>
            过去 {getTimeWindowLabel(timeWindow)}
            {data && ` · ${formatNumber(data.sampled_ips)} 个样本 IP / ${formatNumber(data.sampled_requests)} 次请求 · 占比按样本请求计；用户数按 IP 累加，同一用户多个 IP 会重复计`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {data && data.by_country.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b">
                    <th className="text-left py-3 px-4 font-medium text-muted-foreground">国家/地区</th>
                    <th className="text-right py-3 px-4 font-medium text-muted-foreground">IP 数</th>
                    <th className="text-right py-3 px-4 font-medium text-muted-foreground">用户数</th>
                    <th className="text-right py-3 px-4 font-medium text-muted-foreground">流量</th>
                    <th className="text-right py-3 px-4 font-medium text-muted-foreground">占比</th>
                  </tr>
                </thead>
                <tbody>
                  {data.by_country.slice(0, COUNTRY_RANK_LIMIT).map((item) => (
                    <tr key={`${item.country_code}-${item.country}`} className="border-b last:border-0 hover:bg-muted/50 transition-colors">
                      <td className="py-3 px-4">
                        <span className="text-sm">{item.country}</span>
                        {item.country_code && item.country_code !== 'XX' && (
                          <span className="ml-2 text-xs text-muted-foreground">{item.country_code}</span>
                        )}
                      </td>
                      <td className="py-3 px-4 text-right tabular-nums text-muted-foreground">
                        {item.ip_count.toLocaleString('zh-CN')}
                      </td>
                      <td className="py-3 px-4 text-right tabular-nums text-muted-foreground">
                        {item.user_count.toLocaleString('zh-CN')}
                      </td>
                      <td className="py-3 px-4 text-right tabular-nums font-medium">
                        {item.request_count.toLocaleString('zh-CN')}
                      </td>
                      <td className="py-3 px-4 text-right tabular-nums text-muted-foreground">
                        {item.percentage.toFixed(1)}%
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {data.by_country.length > COUNTRY_RANK_LIMIT && (
                <p className="px-4 pt-3 text-xs text-muted-foreground">
                  另有 {data.by_country.length - COUNTRY_RANK_LIMIT} 个国家/地区未列出
                </p>
              )}
            </div>
          ) : (
            <div className="h-[200px] flex items-center justify-center text-muted-foreground bg-muted/20 rounded-lg">
              暂无数据
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// Stat Card Component
interface StatCardProps {
  title: string
  value: string
  rawValue?: number  // 原始数值，用于 tooltip 显示完整数字
  icon: React.ElementType
  color: string
}

function StatCard({ title, value, rawValue, icon: Icon, color }: StatCardProps) {
  const colorMap: Record<string, { bg: string }> = {
    blue: { bg: 'bg-blue-50 text-blue-700 dark:bg-blue-950 dark:text-blue-300' },
    emerald: { bg: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' },
    purple: { bg: 'bg-purple-50 text-purple-700 dark:bg-purple-950 dark:text-purple-300' },
    orange: { bg: 'bg-orange-50 text-orange-700 dark:bg-orange-950 dark:text-orange-300' },
    cyan: { bg: 'bg-cyan-50 text-cyan-700 dark:bg-cyan-950 dark:text-cyan-300' },
    slate: { bg: 'bg-slate-50 text-slate-700 dark:bg-slate-900 dark:text-slate-300' },
  }
  const theme = colorMap[color] || colorMap.blue

  return (
    <Card className="overflow-hidden hover:shadow-md transition-all duration-200">
      <CardContent className="p-5">
        <div className="flex justify-between items-start">
          <div className="space-y-2">
            <p className="text-sm font-medium text-muted-foreground">{title}</p>
            <div 
              className="text-2xl font-bold tracking-tight cursor-default"
              title={rawValue !== undefined ? rawValue.toLocaleString('zh-CN') : undefined}
            >
              {value}
            </div>
          </div>
          <div className={cn("p-2.5 rounded-xl", theme.bg)}>
            <Icon className="w-5 h-5" />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
