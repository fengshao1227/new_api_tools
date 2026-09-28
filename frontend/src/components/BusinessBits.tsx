import type { ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from './ui/card'
import { cn } from '../lib/utils'
import { usd } from '../lib/dashboardI18n'

/** 经营视图各块共用的小部件:外框、数字卡、表格、格式化。 */

export const money = (v: number | undefined | null) => usd.format(v ?? 0)

/** 0..1 的比例 → 百分比。 */
export const pct = (rate: number | undefined | null) => `${((rate ?? 0) * 100).toFixed(1)}%`

/** 已经是百分数(后端毛利率 = 毛利 / 收入 × 100)。 */
export const pctValue = (value: number | undefined | null) => `${(value ?? 0).toFixed(1)}%`

export const num = (v: number | undefined | null, locale: string) => (v ?? 0).toLocaleString(locale)

export const unixTime = (ts: number, locale: string) =>
  ts > 0 ? new Date(ts * 1000).toLocaleString(locale, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—'

type Tone = 'default' | 'positive' | 'warning' | 'danger' | 'muted'

const toneClass: Record<Tone, string> = {
  default: 'text-foreground',
  positive: 'text-emerald-600 dark:text-emerald-400',
  warning: 'text-amber-600 dark:text-amber-400',
  danger: 'text-red-600 dark:text-red-400',
  muted: 'text-muted-foreground',
}

export function Stat({ label, value, sub, tone = 'default' }: { label: string; value: ReactNode; sub?: ReactNode; tone?: Tone }) {
  return (
    <div className="rounded-lg border bg-card/60 p-4 min-w-0">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn('mt-1 text-2xl font-semibold tabular-nums truncate', toneClass[tone])}>{value}</div>
      {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
    </div>
  )
}

export function SectionShell({
  title,
  icon: Icon,
  hint,
  action,
  loading,
  error,
  hasData,
  errorText,
  children,
}: {
  title: string
  icon: React.ComponentType<{ className?: string }>
  hint?: string
  action?: ReactNode
  loading: boolean
  error: string | null
  hasData: boolean
  errorText: (message: string) => string
  children: ReactNode
}) {
  return (
    <Card className="shadow-sm">
      <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0">
        <div className="min-w-0">
          <CardTitle className="text-lg flex items-center gap-2">
            <Icon className="w-5 h-5 text-primary" />
            {title}
            {loading && hasData && <Loader2 className="w-4 h-4 animate-spin text-muted-foreground" />}
          </CardTitle>
          {hint && <CardDescription className="mt-1">{hint}</CardDescription>}
        </div>
        {action}
      </CardHeader>
      <CardContent className="space-y-4">
        {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{errorText(error)}</div>}
        {!hasData && loading ? (
          <div className="h-32 flex items-center justify-center text-muted-foreground">
            <Loader2 className="w-5 h-5 animate-spin" />
          </div>
        ) : (
          hasData && children
        )}
      </CardContent>
    </Card>
  )
}

export interface Column<T> {
  header: string
  cell: (row: T) => ReactNode
  align?: 'left' | 'right'
  className?: string
}

export function MiniTable<T>({ rows, columns, rowKey, empty }: { rows: T[]; columns: Column<T>[]; rowKey: (row: T, i: number) => string; empty: string }) {
  if (rows.length === 0) {
    return <div className="rounded-md bg-muted/30 p-4 text-center text-sm text-muted-foreground">{empty}</div>
  }
  return (
    <div className="-mx-6 overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-y text-muted-foreground">
            {columns.map((c, i) => (
              <th
                key={c.header}
                className={cn(
                  'font-normal py-2 px-3 whitespace-nowrap',
                  c.align === 'right' ? 'text-right' : 'text-left',
                  i === 0 && 'pl-6',
                  i === columns.length - 1 && 'pr-6',
                )}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, r) => (
            <tr key={rowKey(row, r)} className="border-b last:border-0 hover:bg-muted/40">
              {columns.map((c, i) => (
                <td
                  key={c.header}
                  className={cn(
                    'py-2 px-3 tabular-nums',
                    c.align === 'right' ? 'text-right' : 'text-left',
                    i === 0 && 'pl-6',
                    i === columns.length - 1 && 'pr-6',
                    c.className,
                  )}
                >
                  {c.cell(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
