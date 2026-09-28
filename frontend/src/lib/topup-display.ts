/**
 * Money on the top-up pages follows one rule: every total is USD, and a single
 * order keeps its own currency. `top_ups` has no currency column, so the
 * backend derives it from the payment rail and returns `payment_currency`
 * ("CNY" / "USD", empty when no rule matches). Unknown-currency orders are
 * counted apart and never enter a USD total.
 */

/** ¥7 = $1 unless the backend reports its configured CNY_PER_USD. */
export const DEFAULT_CNY_PER_USD = 7

/** Missing or invalid values must not look like a confirmed zero payment. */
export function formatTopUpAmount(amount?: number | null): string {
  if (typeof amount !== 'number' || !Number.isFinite(amount)) return '—'
  const rounded = amount.toFixed(2)
  return rounded === '-0.00' ? '0.00' : rounded
}

/** One order's own amount: ¥70.00 CNY / $10.00 USD. Use the backend's confirmed currency; never infer it from an amount. */
export function formatTopUpMoney(money?: number | null, currency?: string | null): string {
  const amount = formatTopUpAmount(money)
  if (amount === '—') return amount
  if (currency === 'USD') return `$${amount} USD`
  if (currency === 'CNY') return `¥${amount} CNY`
  return `${amount}（币种未知）`
}

/** A USD total: $1,234.56. */
export function formatUsd(value?: number | null): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  const abs = Math.abs(value).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return value < 0 && abs !== '0.00' ? `-$${abs}` : `$${abs}`
}

/** The caption shown next to every converted total. */
export function cnyRateNote(rate?: number | null): string {
  const r = typeof rate === 'number' && Number.isFinite(rate) && rate > 0 ? rate : DEFAULT_CNY_PER_USD
  return `人民币按 ¥${r}=$1 折算`
}
