package service

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
)

// Growth answers the questions the usage dashboard cannot: how many people
// signed up, how many of them paid, and how much came in. Those live in `users`
// and `top_ups` on the main database — never in `logs` — so none of it is
// affected by a deployment that splits logs off via LOG_SQL_DSN.
//
// A note on `top_ups.money`, because it is the one column here that does not
// mean the same thing in every row. NewAPI persists a top-up's value in one of
// two places depending on the provider: Stripe writes the group-adjusted
// display amount to `money`, while the hosted-checkout providers write quota
// units to `amount` and the older epay path writes display units there. So
// `money` is the paid amount for some rows and zero or an unrelated number for
// others, and summing it over every row — including the pending ones, whose
// values were never reconciled against anything — produces a figure that means
// nothing.
//
// Revenue here is therefore SUM(money) over settled rows only. A row that was
// never paid cannot contribute to revenue no matter what its columns say, and
// restricting to `status = 'success'` is what makes the sum answer the question
// asked. Providers that settle without writing `money` will under-report rather
// than invent revenue, which is the safe direction for a number an operator
// makes decisions on.
const topUpSettledStatus = "success"

// `top_ups` has no currency column, and the rails settle in two of them: the
// Chinese ones take CNY, the card ones take USD. Summing the raw column treats
// a ¥70 order as a $70 one — on the real data that reported $250 of revenue
// where $70 came in, because every settled order is the same $10 purchase paid
// on a different rail.
//
// The rail is the only evidence in the row, and it is sufficient: epay is the
// CNY gateway, and alipay/wxpay are its methods (the orders imported from the
// portal carry the method without the provider). Everything else is a card
// processor quoting USD. An unrecognised rail is counted as USD rather than
// dropped: a new card processor is the likely case, and under-reporting a rail
// that does exist is worse than the rounding.
const cnyRailPredicate = "(payment_method IN ('alipay', 'wxpay') OR payment_provider = 'epay')"

// defaultCNYPerUSD matches what this deployment charges: NewAPI's own `Price`
// option is 7, and every settled CNY order is ¥70 against a $10 product. Set
// CNY_PER_USD if the two ever diverge — keep it equal to `Price`, or the
// dashboard and the checkout page will quote different revenue for one sale.
const defaultCNYPerUSD = 7.0

func cnyPerUSD() float64 {
	raw := strings.TrimSpace(os.Getenv("CNY_PER_USD"))
	if raw == "" {
		return defaultCNYPerUSD
	}
	rate, err := strconv.ParseFloat(raw, 64)
	if err != nil || rate <= 0 || math.IsInf(rate, 0) {
		return defaultCNYPerUSD
	}
	return rate
}

// revenueUSDExpr converts a row's `money` to USD. Built with a parsed float, so
// the rate cannot carry anything but a number into the statement.
func revenueUSDExpr() string {
	return fmt.Sprintf("(CASE WHEN %s THEN money / %g ELSE money END)", cnyRailPredicate, cnyPerUSD())
}

// paidAtExpr is when a top-up actually settled. complete_time is the settlement
// stamp, but rows completed by paths that predate it — and manually completed
// ones — can carry 0, and a zero would bucket every one of them into 1970 and
// silently drop them out of every window. create_time is the honest fallback:
// for a settled row the two are minutes apart.
const paidAtExpr = "(CASE WHEN complete_time IS NULL OR complete_time = 0 THEN create_time ELSE complete_time END)"

// dayBucket groups a unix column into local calendar days with integer
// arithmetic, so it needs no dialect-specific date function and behaves the
// same on PostgreSQL and MySQL. Same technique as GetDailyTrends.
func dayBucket(column string, tzOffset int) string {
	return fmt.Sprintf("FLOOR((%s + %d) / 86400)", column, tzOffset)
}

// startOfMonth is the first instant of the current month in the server's own
// timezone (TIMEZONE in the environment), which is the month an operator means
// when they ask what this month looks like.
func startOfMonth(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// GetGrowthMetrics returns registration, paying-customer and revenue counters
// for the current month and for all time.
func (s *DashboardService) GetGrowthMetrics(noCache bool) (map[string]interface{}, error) {
	cm := cache.Get()
	const cacheKey = "dashboard:growth:metrics"
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	monthStart := startOfMonth(time.Now()).Unix()
	result := map[string]interface{}{}

	// Registrations. created_at is 0 on accounts that predate the column and on
	// some imported ones; those are real users, so they count towards the total
	// and simply fall outside the month window rather than being excluded.
	userQuery := s.db.RebindQuery(`
		SELECT COUNT(*) AS total_users,
			COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0) AS month_users
		FROM users
		WHERE deleted_at IS NULL`)
	if row, err := s.db.QueryOneWithTimeout(15*time.Second, userQuery, monthStart); err == nil && row != nil {
		result["total_users"] = toFloat64(row["total_users"])
		result["month_users"] = toFloat64(row["month_users"])
	} else if err != nil {
		return nil, err
	}

	// Paying customers and revenue, in one pass over the settled rows.
	//
	// "Paying customers this month" is who paid during it, not who paid for the
	// first time during it: a returning customer is still a paying customer
	// this month, and counting only first payments would make the month figure
	// drift below reality as the product ages. The trend series below does
	// count first payments, because there the question is growth.
	usd := revenueUSDExpr()
	payQuery := s.db.RebindQuery(fmt.Sprintf(`
		SELECT COUNT(DISTINCT user_id) AS total_payers,
			COUNT(DISTINCT CASE WHEN %s >= ? THEN user_id END) AS month_payers,
			COALESCE(SUM(%s), 0) AS total_revenue,
			COALESCE(SUM(CASE WHEN %s >= ? THEN %s ELSE 0 END), 0) AS month_revenue,
			COALESCE(SUM(CASE WHEN %s THEN money ELSE 0 END), 0) AS total_revenue_cny,
			COUNT(*) AS settled_orders
		FROM top_ups
		WHERE (%s) = ?`, paidAtExpr, usd, paidAtExpr, usd, cnyRailPredicate, successStatusCondition()))
	if row, err := s.db.QueryOneWithTimeout(15*time.Second, payQuery, monthStart, monthStart, topUpSettledStatus); err == nil && row != nil {
		result["total_payers"] = toFloat64(row["total_payers"])
		result["month_payers"] = toFloat64(row["month_payers"])
		result["total_revenue"] = toFloat64(row["total_revenue"])
		result["month_revenue"] = toFloat64(row["month_revenue"])
		result["total_revenue_cny"] = toFloat64(row["total_revenue_cny"])
		result["settled_orders"] = toFloat64(row["settled_orders"])
	} else if err != nil {
		return nil, err
	}

	result["month_start"] = monthStart
	result["cny_per_usd"] = cnyPerUSD()

	cm.Set(cacheKey, result, 3*time.Minute)
	return result, nil
}

// GetGrowthTrend returns new registrations, first-time paying customers and
// revenue per bucket. granularity is "daily" (the last 30 days) or "monthly"
// (the last 12 months).
//
// Monthly buckets are folded from daily ones in Go rather than grouped in SQL:
// months are not a fixed number of seconds, so the integer-arithmetic bucketing
// that keeps the daily query dialect-free cannot express them, and the
// alternative is a date function that differs between PostgreSQL and MySQL.
// A year of daily rows is a few hundred at most.
func (s *DashboardService) GetGrowthTrend(granularity string, noCache bool) ([]map[string]interface{}, error) {
	monthly := granularity == "monthly"

	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:growth:trend:%s", granularity)
	if !noCache {
		var cached []map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	now := time.Now()
	tzOffset := localTZOffset()

	var start time.Time
	if monthly {
		start = startOfMonth(now).AddDate(0, -11, 0)
	} else {
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -29)
	}
	startUnix := start.Unix()

	// day bucket -> counters
	buckets := map[int64]*growthBucket{}
	at := func(day int64) *growthBucket {
		b, ok := buckets[day]
		if !ok {
			b = &growthBucket{}
			buckets[day] = b
		}
		return b
	}

	usersExpr := dayBucket("created_at", tzOffset)
	usersQuery := s.db.RebindQuery(fmt.Sprintf(`
		SELECT %s AS day_group, COUNT(*) AS new_users
		FROM users
		WHERE deleted_at IS NULL AND created_at >= ?
		GROUP BY %s`, usersExpr, usersExpr))
	rows, err := s.db.QueryWithTimeout(30*time.Second, usersQuery, startUnix)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		at(int64(toFloat64(r["day_group"]))).newUsers += int64(toFloat64(r["new_users"]))
	}

	revenueExpr := dayBucket(paidAtExpr, tzOffset)
	revenueQuery := s.db.RebindQuery(fmt.Sprintf(`
		SELECT %s AS day_group, COALESCE(SUM(%s), 0) AS revenue, COUNT(*) AS orders
		FROM top_ups
		WHERE (%s) = ? AND %s >= ?
		GROUP BY %s`, revenueExpr, revenueUSDExpr(), successStatusCondition(), paidAtExpr, revenueExpr))
	rows, err = s.db.QueryWithTimeout(30*time.Second, revenueQuery, topUpSettledStatus, startUnix)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		b := at(int64(toFloat64(r["day_group"])))
		b.revenue += toFloat64(r["revenue"])
		b.orders += int64(toFloat64(r["orders"]))
	}

	// First-time payers. The window is applied to the customer's *first*
	// settled payment, not to the payments inside it: a customer who started
	// paying last year and paid again today is not new, and counting them as
	// new would make the series add up to more customers than exist.
	firstPayExpr := dayBucket("first_paid", tzOffset)
	firstPayQuery := s.db.RebindQuery(fmt.Sprintf(`
		SELECT %s AS day_group, COUNT(*) AS new_payers
		FROM (
			SELECT user_id, MIN(%s) AS first_paid
			FROM top_ups
			WHERE (%s) = ?
			GROUP BY user_id
		) first_payments
		WHERE first_paid >= ?
		GROUP BY %s`, firstPayExpr, paidAtExpr, successStatusCondition(), firstPayExpr))
	rows, err = s.db.QueryWithTimeout(30*time.Second, firstPayQuery, topUpSettledStatus, startUnix)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		at(int64(toFloat64(r["day_group"]))).newPayers += int64(toFloat64(r["new_payers"]))
	}

	// Emit every bucket in the range, including the empty ones. A series that
	// skips quiet days draws a chart where the gaps look like activity.
	out := make([]map[string]interface{}, 0, 366)
	if monthly {
		for m := 0; m < 12; m++ {
			monthStart := start.AddDate(0, m, 0)
			monthEnd := monthStart.AddDate(0, 1, 0)
			agg := growthBucket{}
			for day := unixDayOf(monthStart, tzOffset); day < unixDayOf(monthEnd, tzOffset); day++ {
				if b, ok := buckets[day]; ok {
					agg.newUsers += b.newUsers
					agg.newPayers += b.newPayers
					agg.orders += b.orders
					agg.revenue += b.revenue
				}
			}
			out = append(out, agg.row(monthStart.Format("2006-01")))
		}
		cm.Set(cacheKey, out, 10*time.Minute)
		return out, nil
	}

	for d := 0; d < 30; d++ {
		day := start.AddDate(0, 0, d)
		agg := growthBucket{}
		if b, ok := buckets[unixDayOf(day, tzOffset)]; ok {
			agg = *b
		}
		out = append(out, agg.row(day.Format("2006-01-02")))
	}

	cm.Set(cacheKey, out, 5*time.Minute)
	return out, nil
}

type growthBucket struct {
	newUsers  int64
	newPayers int64
	orders    int64
	revenue   float64
}

func (b growthBucket) row(label string) map[string]interface{} {
	return map[string]interface{}{
		"date":       label,
		"new_users":  b.newUsers,
		"new_payers": b.newPayers,
		"orders":     b.orders,
		"revenue":    b.revenue,
	}
}

// unixDayOf is the bucket number the SQL above would produce for this instant,
// so the Go side and the query agree on where a day starts.
func unixDayOf(t time.Time, tzOffset int) int64 {
	return (t.Unix() + int64(tzOffset)) / 86400
}
