package service

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

// Every figure the panel shows about money paid comes from `top_ups`, and that
// table has no currency column. The gateway's rails write it differently:
//
//   - epay (methods alipay / wxpay): money is the CNY price (amount × 7),
//     amount is display USD.
//   - stripe: money is the credited units × group ratio (every ratio is 1, so it
//     is the USD charged), amount is the number of units bought.
//   - dodo / paypal / creem: money is the USD charged, amount is raw quota
//     (÷ 500000 for USD).
//   - waffo / waffo_pancake: money is USD, amount is display units.
//   - beatapi (orders migrated from the old portal): money is what the order
//     paid, amount is round(credits_usd); the currency follows the method —
//     alipay / wxpay paid CNY, stripe / paypal / dodo paid USD.
//   - rows written before payment_provider existed carry only the method.
//
// The functions below turn those rules into one SQL expression per question —
// which currency, how many USD were paid, how many USD were credited, when the
// order settled — plus the Go mirror used on single rows, so a card, a chart,
// a CSV line and a record row can never disagree about the same order.
//
// A row that matches no rule has an unknown currency. It is never guessed:
// its paid USD is NULL, so SUM() leaves it out of every revenue total, and the
// callers report those rows separately instead of letting a yuan amount pass
// for dollars (or the reverse).

const (
	topUpCurrencyCNY = "CNY"
	topUpCurrencyUSD = "USD"

	topUpRailBeatAPI = "beatapi"
	// topUpRailMoneyCredit is the rail whose credit is recorded in money, not
	// amount: Stripe stores units bought in amount and the USD value in money.
	topUpRailMoneyCredit = "stripe"
)

var (
	topUpCNYRails          = []string{"epay"}
	topUpCNYMethods        = []string{"alipay", "wxpay"}
	topUpUSDRails          = []string{"stripe", "dodo", "paypal", "creem", "waffo", "waffo_pancake"}
	topUpBeatAPIUSDMethods = []string{"stripe", "paypal", "dodo"}
	// Rails whose amount column holds raw quota rather than display USD.
	topUpQuotaAmountRails = []string{"dodo", "paypal", "creem"}
	// Subscription purchases are mirrored into top_ups with amount = 0: they
	// buy a plan, not quota. Their trade numbers are the only marker the row
	// carries (epay SUBUSR…, Stripe/Creem sub_ref_…, Waffo Pancake …_SUB-).
	topUpSubscriptionTradeNoPrefixes = []string{"SUBUSR", "sub_ref_", "WAFFO_PANCAKE_SUB-"}
)

// ---------- Go side: one row at a time ----------

func normalizeTopUpToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// topUpRail is the processor that settled a row: its provider, or its method
// on rows written before the provider column existed.
func topUpRail(provider, method string) string {
	if p := normalizeTopUpToken(provider); p != "" {
		return p
	}
	return normalizeTopUpToken(method)
}

// topUpCurrencyOf returns "CNY", "USD", or "" when the row matches no rule.
func topUpCurrencyOf(provider, method string) string {
	rail := topUpRail(provider, method)
	m := normalizeTopUpToken(method)
	switch {
	case slices.Contains(topUpCNYRails, rail) || slices.Contains(topUpCNYMethods, m):
		return topUpCurrencyCNY
	case slices.Contains(topUpUSDRails, rail),
		rail == topUpRailBeatAPI && slices.Contains(topUpBeatAPIUSDMethods, m):
		return topUpCurrencyUSD
	default:
		return ""
	}
}

// topUpPaidUSDOf converts the paid amount to USD. ok is false when the
// currency is unknown, in which case the row has no USD value at all.
func topUpPaidUSDOf(currency string, money float64) (usd float64, ok bool) {
	switch currency {
	case topUpCurrencyCNY:
		return money / cnyPerUSD(), true
	case topUpCurrencyUSD:
		return money, true
	default:
		return 0, false
	}
}

// topUpCreditedUSDOf is the quota the order bought, in USD.
func topUpCreditedUSDOf(provider, method string, amount int64, money float64) float64 {
	rail := topUpRail(provider, method)
	switch {
	case slices.Contains(topUpQuotaAmountRails, rail):
		return float64(amount) / float64(util.TokensPerUSD)
	case rail == topUpRailMoneyCredit:
		return money
	default:
		return float64(amount)
	}
}

func isTopUpSubscriptionTradeNo(tradeNo string) bool {
	tradeNo = strings.TrimSpace(tradeNo)
	for _, prefix := range topUpSubscriptionTradeNoPrefixes {
		if strings.HasPrefix(tradeNo, prefix) {
			return true
		}
	}
	return false
}

// ---------- SQL side: the same rules as expressions ----------
//
// Every builder takes the table alias ("" for an unaliased top_ups) and only
// emits standard SQL — CASE, COALESCE, LOWER, TRIM, IN, LIKE — so the result
// runs unchanged on PostgreSQL, MySQL and the SQLite the tests use. The only
// value spliced in is the exchange rate, which is a parsed float.

// topUpSQLCols names the columns one expression reads. provider is "”" on a
// deployment whose top_ups predates payment_provider, which makes every rule
// fall back to the method.
type topUpSQLCols struct {
	provider, method, money, amount, status, createTime, completeTime, tradeNo string
}

func topUpColumn(alias, column string) string {
	if alias == "" {
		return column
	}
	return alias + "." + column
}

// topUpSQLColsFor resolves the columns of top_ups (under alias) once, so one
// builder call costs one payment_provider probe however often it uses it.
func topUpSQLColsFor(alias string) topUpSQLCols {
	return topUpSQLColsWithProvider(alias, topUpPaymentProviderExpr(alias))
}

func topUpSQLColsWithProvider(alias, provider string) topUpSQLCols {
	return topUpSQLCols{
		provider:     provider,
		method:       topUpColumn(alias, "payment_method"),
		money:        topUpColumn(alias, "money"),
		amount:       topUpColumn(alias, "amount"),
		status:       topUpColumn(alias, "status"),
		createTime:   topUpColumn(alias, "create_time"),
		completeTime: topUpColumn(alias, "complete_time"),
		tradeNo:      topUpColumn(alias, "trade_no"),
	}
}

func sqlQuotedList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return strings.Join(quoted, ", ")
}

// sqlFloatLiteral always carries a decimal point, so no dialect can read the
// division it appears in as integer division.
func sqlFloatLiteral(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func topUpNormalizedSQL(expr string) string {
	return fmt.Sprintf("LOWER(TRIM(COALESCE(%s, '')))", expr)
}

func (c topUpSQLCols) methodNorm() string { return topUpNormalizedSQL(c.method) }

// rail mirrors topUpRail: the provider, or the method when there is none.
func (c topUpSQLCols) rail() string {
	provider := topUpNormalizedSQL(c.provider)
	return fmt.Sprintf("(CASE WHEN %s <> '' THEN %s ELSE %s END)", provider, provider, c.methodNorm())
}

func (c topUpSQLCols) cnyPredicate() string {
	return fmt.Sprintf("(%s IN (%s) OR %s IN (%s))",
		c.rail(), sqlQuotedList(topUpCNYRails), c.methodNorm(), sqlQuotedList(topUpCNYMethods))
}

func (c topUpSQLCols) usdPredicate() string {
	rail := c.rail()
	return fmt.Sprintf("(%s IN (%s) OR (%s = '%s' AND %s IN (%s)))",
		rail, sqlQuotedList(topUpUSDRails),
		rail, topUpRailBeatAPI, c.methodNorm(), sqlQuotedList(topUpBeatAPIUSDMethods))
}

func (c topUpSQLCols) currency() string {
	return fmt.Sprintf("(CASE WHEN %s THEN '%s' WHEN %s THEN '%s' END)",
		c.cnyPredicate(), topUpCurrencyCNY, c.usdPredicate(), topUpCurrencyUSD)
}

func (c topUpSQLCols) paidUSD() string {
	return fmt.Sprintf("(CASE WHEN %s THEN %s / %s WHEN %s THEN %s END)",
		c.cnyPredicate(), c.money, sqlFloatLiteral(cnyPerUSD()), c.usdPredicate(), c.money)
}

func (c topUpSQLCols) creditedUSD() string {
	rail := c.rail()
	return fmt.Sprintf("(CASE WHEN %s IN (%s) THEN %s / %d.0 WHEN %s = '%s' THEN %s ELSE %s END)",
		rail, sqlQuotedList(topUpQuotaAmountRails), c.amount, util.TokensPerUSD,
		rail, topUpRailMoneyCredit, c.money, c.amount)
}

func (c topUpSQLCols) paidAt() string {
	return fmt.Sprintf("(CASE WHEN %s IS NULL OR %s = 0 THEN %s ELSE %s END)",
		c.completeTime, c.completeTime, c.createTime, c.completeTime)
}

func (c topUpSQLCols) eventTime() string {
	return fmt.Sprintf("(CASE WHEN %s THEN %s ELSE %s END)", successStatusCondition(c.status), c.paidAt(), c.createTime)
}

func (c topUpSQLCols) subscription() string {
	parts := make([]string, len(topUpSubscriptionTradeNoPrefixes))
	for i, prefix := range topUpSubscriptionTradeNoPrefixes {
		parts[i] = fmt.Sprintf("COALESCE(%s, '') LIKE '%s%%'", c.tradeNo, strings.ReplaceAll(prefix, "'", "''"))
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// The wrappers below are the API the panels (and the dashboard) build on.

// topUpCNYPredicateSQL is true on rows paid in CNY.
func topUpCNYPredicateSQL(alias string) string { return topUpSQLColsFor(alias).cnyPredicate() }

// topUpCurrencySQL evaluates to 'CNY', 'USD' or NULL (unknown).
func topUpCurrencySQL(alias string) string { return topUpSQLColsFor(alias).currency() }

// topUpPaidUSDSQL is the paid amount in USD, NULL when the currency is
// unknown — SUM() then skips the row rather than counting yuan as dollars.
func topUpPaidUSDSQL(alias string) string { return topUpSQLColsFor(alias).paidUSD() }

// topUpCreditedUSDSQL is the quota the order bought, in USD.
func topUpCreditedUSDSQL(alias string) string { return topUpSQLColsFor(alias).creditedUSD() }

// topUpPaidAtSQL is when a settled order was paid: complete_time, or
// create_time on rows completed by paths that never stamped it (a zero would
// put them in 1970 and drop them out of every window). Only meaningful on
// successful rows; topUpEventTimeSQL handles mixed statuses.
func topUpPaidAtSQL(alias string) string { return topUpSQLColsWithProvider(alias, "''").paidAt() }

// topUpEventTimeSQL places every row on the timeline once: successful orders
// when they were paid (revenue belongs to the day the money came in, as the
// growth panel counts it), everything else when it was created.
func topUpEventTimeSQL(alias string) string {
	return topUpSQLColsWithProvider(alias, "''").eventTime()
}

// topUpSubscriptionSQL matches the amount = 0 rows that mirror plan purchases.
func topUpSubscriptionSQL(alias string) string {
	return topUpSQLColsWithProvider(alias, "''").subscription()
}

// topUpFactsSQL is top_ups as a derived table named alias, carrying the
// normalised columns every revenue query reads. innerWhere filters raw rows
// (unaliased columns). Its placeholders bind after any that appear before the
// FROM clause, so callers with placeholders in the SELECT list keep it
// placeholder-free and filter in the outer WHERE instead.
func topUpFactsSQL(innerWhere, alias string) string {
	c := topUpSQLColsFor("")
	return fmt.Sprintf(`(SELECT id, user_id, money, amount, payment_method, create_time, complete_time,
			%s AS status_bucket, %s AS pay_currency, %s AS paid_usd, %s AS credited_usd,
			%s AS paid_at, %s AS event_time
		FROM top_ups WHERE %s) %s`,
		topUpStatusBucketSQL("status"), c.currency(), c.paidUSD(), c.creditedUSD(),
		c.paidAt(), c.eventTime(), innerWhere, alias)
}

// ---------- shared aggregation ----------

// topUpBucketTotals is one normalised status bucket's money figures.
type topUpBucketTotals struct {
	Bucket               string  `db:"status_bucket"`
	Count                int64   `db:"cnt"`
	PaidUSD              float64 `db:"paid_usd"`
	CreditedUSD          float64 `db:"credited_usd"`
	CNYMoney             float64 `db:"cny_money"`
	USDMoney             float64 `db:"usd_money"`
	UnknownCurrencyCount int64   `db:"unknown_currency_count"`
	UnknownCurrencyMoney float64 `db:"unknown_currency_money"`
}

// queryTopUpBucketTotals groups the top_ups rows matching whereSQL (unaliased
// columns, dialect placeholders) by status bucket.
func queryTopUpBucketTotals(whereSQL string, args []interface{}) ([]topUpBucketTotals, error) {
	query := fmt.Sprintf(`SELECT status_bucket,
		COUNT(*) AS cnt,
		COALESCE(SUM(paid_usd), 0) AS paid_usd,
		COALESCE(SUM(credited_usd), 0) AS credited_usd,
		COALESCE(SUM(CASE WHEN pay_currency = '%[1]s' THEN money ELSE 0 END), 0) AS cny_money,
		COALESCE(SUM(CASE WHEN pay_currency = '%[2]s' THEN money ELSE 0 END), 0) AS usd_money,
		COALESCE(SUM(CASE WHEN pay_currency IS NULL THEN 1 ELSE 0 END), 0) AS unknown_currency_count,
		COALESCE(SUM(CASE WHEN pay_currency IS NULL THEN money ELSE 0 END), 0) AS unknown_currency_money
		FROM %[3]s
		GROUP BY status_bucket`,
		topUpCurrencyCNY, topUpCurrencyUSD, topUpFactsSQL(whereSQL, "f"))
	var rows []topUpBucketTotals
	if err := database.Get().DB.Select(&rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}
