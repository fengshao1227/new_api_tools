import { useCallback, useEffect, useState } from 'react'
import { History, RefreshCw } from 'lucide-react'
import { useAuth } from '../contexts/AuthContext'
import { Card, CardContent, CardHeader, CardTitle } from './ui/card'
import { Badge } from './ui/badge'
import { Button } from './ui/button'
import { cn } from '../lib/utils'

// 一次批量禁用/启用的审计记录（后端 /api/tokens/audit，存在 Tool 数据目录的 token_audit.jsonl）
interface TokenAuditEntry {
  at: number
  actor: string
  ip?: string
  action: 'disable' | 'enable' | string
  token_ids: number[]
  requested: number
  affected: number
  error?: string
}

const SHOWN_IDS = 12

function formatTime(ts: number) {
  return new Date(ts * 1000).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function TokenIDs({ ids }: { ids: number[] }) {
  const shown = ids.slice(0, SHOWN_IDS)
  const rest = ids.length - shown.length
  return (
    <span className="font-mono text-[11px] text-muted-foreground break-all" title={ids.join(', ')}>
      {shown.map(id => `#${id}`).join(' ')}{rest > 0 && ` … 另 ${rest} 个`}
    </span>
  )
}

/** 令牌页「最近操作」：谁、何时、对哪些令牌做了禁用 / 启用。refreshKey 变化时重新拉取。 */
export function TokenAuditLog({ refreshKey }: { refreshKey: number }) {
  const { token } = useAuth()
  const [entries, setEntries] = useState<TokenAuditEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const apiUrl = import.meta.env.VITE_API_URL || ''

  const load = useCallback(async () => {
    if (!token) return
    setLoading(true)
    setError('')
    try {
      const response = await fetch(`${apiUrl}/api/tokens/audit?limit=20`, { headers: { Authorization: `Bearer ${token}` } })
      const data = await response.json()
      if (!data.success) throw new Error(data.message || '加载操作记录失败')
      setEntries(data.data || [])
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '加载操作记录失败')
    } finally {
      setLoading(false)
    }
  }, [apiUrl, token])

  useEffect(() => { void load() }, [load, refreshKey])

  return (
    <Card>
      <CardHeader className="pb-2 flex flex-row items-center justify-between space-y-0">
        <CardTitle className="text-base font-medium flex items-center gap-2">
          <History className="w-4 h-4" />
          最近操作
          <span className="text-xs font-normal text-muted-foreground">批量禁用 / 启用记录，新的在前</span>
        </CardTitle>
        <Button variant="ghost" size="sm" onClick={() => void load()} disabled={loading} className="h-8">
          <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />
        </Button>
      </CardHeader>
      <CardContent>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {!error && entries.length === 0 && <p className="py-6 text-center text-sm text-muted-foreground">暂无操作记录</p>}
        {entries.length > 0 && (
          <ul className="divide-y divide-border/60">
            {entries.map((e, i) => (
              <li key={`${e.at}-${i}`} className="py-2 flex flex-col gap-1 sm:flex-row sm:items-start sm:gap-3">
                <span className="text-xs text-muted-foreground tabular-nums whitespace-nowrap sm:w-32">{formatTime(e.at)}</span>
                <div className="flex items-center gap-2 sm:w-44 shrink-0">
                  <Badge variant={e.action === 'disable' ? 'destructive' : 'success'}>{e.action === 'disable' ? '禁用' : '启用'}</Badge>
                  <span className="text-xs whitespace-nowrap" title={e.ip ? `来源 IP ${e.ip}` : undefined}>{e.actor}</span>
                </div>
                <div className="flex-1 min-w-0 space-y-0.5">
                  <div className="text-xs">
                    请求 {e.requested} 个，实际变更 <span className="font-medium">{e.affected}</span> 个
                    {e.error && <span className="ml-2 text-destructive">失败：{e.error}</span>}
                  </div>
                  <TokenIDs ids={e.token_ids || []} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
