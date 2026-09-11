/** Missing or invalid values must not look like a confirmed zero payment. */
export function formatTopUpAmount(amount?: number | null): string {
  if (typeof amount !== 'number' || !Number.isFinite(amount)) return '—'
  const rounded = amount.toFixed(2)
  return rounded === '-0.00' ? '0.00' : rounded
}

/** Use the backend's confirmed currency; never infer it from an amount. */
export function formatTopUpMoney(money?: number | null, currency?: string | null): string {
  const amount = formatTopUpAmount(money)
  if (amount === '—') return amount
  if (currency === 'USD') return `$${amount} USD`
  if (currency === 'CNY') return `¥${amount} CNY`
  return `${amount}（币种未确认）`
}
