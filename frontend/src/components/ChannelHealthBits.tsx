import { cn } from '../lib/utils'
import {
  LEVEL_TEXT, SIDE_META, categoryLabel, rateTitle, userTitle,
  type FailureTally, type Level, type Side,
} from '../lib/failureAttribution'

/** 渠道监控表格里的错误率、用户侧与类别小部件。 */

export function RateCell({ stat }: { stat: FailureTally & { level: Level; error_rate: number } }) {
  if (stat.success + stat.errors === 0) return <span className="text-xs text-muted-foreground">-</span>
  return (
    <span className={cn('text-xs font-medium cursor-help', LEVEL_TEXT[stat.level])} title={rateTitle(stat)}>
      {Number(stat.error_rate).toFixed(1)}%
    </span>
  )
}

export function UserSideCell({ stat }: { stat: FailureTally }) {
  const n = Number(stat.user_errors) + Number(stat.queue_full)
  if (n === 0) return <span className="text-xs text-muted-foreground">-</span>
  return <span className="text-xs text-muted-foreground font-mono cursor-help" title={userTitle(stat)}>{n.toLocaleString()}</span>
}

export function CategoryChips({ title, side, categories }: { title: string; side: Side; categories: Record<string, number> }) {
  const entries = Object.entries(categories || {}).sort((a, b) => b[1] - a[1])
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-xs text-muted-foreground min-w-[150px]">{title}</span>
      {entries.length === 0 ? <span className="text-xs text-muted-foreground">无</span> : entries.map(([cat, count]) => (
        <span key={cat} className={cn('inline-flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full font-medium', SIDE_META[side].color)}>
          {categoryLabel(side, cat)} × {count}
        </span>
      ))}
    </div>
  )
}
