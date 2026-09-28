package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

// Every money figure on the analytics tab is paid USD from top_up_currency.go
// (CNY converted at CNY_PER_USD, unknown currencies left out), and revenue is
// dated by when it was paid — the same rules the growth panel uses, so the two
// pages report the same number for the same window.

// TopUpTrendPoint is one bucket of the revenue trend. Orders sit in the bucket
// of their event time: paid orders when they were paid, the rest when created.
type TopUpTrendPoint struct {
	Date         string  `json:"date"`
	Timestamp    int64   `json:"timestamp"`
	Count        int64   `json:"count"`
	SuccessCount int64   `json:"success_count"`
	SuccessMoney float64 `json:"success_money"` // 成功单实付折美元
}

// TopUpFinancialSummary represents a monthly financial summary (USD).
type TopUpFinancialSummary struct {
	Period      string  `json:"period"`
	Revenue     float64 `json:"revenue"`
	Count       int64   `json:"count"`     // 成功单数，含币种未知
	AvgOrder    float64 `json:"avg_order"` // 已确认币种成功单的客单价
	GrowthRate  float64 `json:"growth_rate"`
	CreditedUSD float64 `json:"credited_usd"`
	SuccessRate float64 `json:"success_rate"`
	// UnknownCurrencyCount 成功但币种未知、未计入 Revenue 的订单数。
	UnknownCurrencyCount int64 `json:"unknown_currency_count"`
}

// TopUpTopUser represents a top user by paid USD.
type TopUpTopUser struct {
	UserID               int64   `json:"user_id"`
	Username             string  `json:"username"`
	Count                int64   `json:"count"`
	PaidUSD              float64 `json:"paid_usd"`
	CreditedUSD          float64 `json:"credited_usd"`
	UnknownCurrencyCount int64   `json:"unknown_currency_count"`
}

// PaymentMethodDistribution represents payment method breakdown (USD).
type PaymentMethodDistribution struct {
	Method               string  `json:"method"`
	Count                int64   `json:"count"`
	PaidUSD              float64 `json:"paid_usd"`
	Percentage           float64 `json:"percentage"`
	UnknownCurrencyCount int64   `json:"unknown_currency_count"`
}

// TopUpRealtimeStats represents real-time comparison stats (USD, by paid time).
type TopUpRealtimeStats struct {
	TodayMoney     float64 `json:"today_money"`
	TodayCount     int64   `json:"today_count"`
	YesterdayMoney float64 `json:"yesterday_money"`
	YesterdayCount int64   `json:"yesterday_count"`
	DayGrowth      float64 `json:"day_growth"`
	WeekMoney      float64 `json:"week_money"`
	WeekCount      int64   `json:"week_count"`
	LastWeekMoney  float64 `json:"last_week_money"`
	LastWeekCount  int64   `json:"last_week_count"`
	WeekGrowth     float64 `json:"week_growth"`
	MonthMoney     float64 `json:"month_money"`
	MonthCount     int64   `json:"month_count"`
	LastMonthMoney float64 `json:"last_month_money"`
	LastMonthCount int64   `json:"last_month_count"`
	MonthGrowth    float64 `json:"month_growth"`
	CNYPerUSD      float64 `json:"cny_per_usd"`
}

// HourlyHeatmapPoint represents a single cell in the heatmap
type HourlyHeatmapPoint struct {
	DayOfWeek int     `json:"day_of_week"` // 0=Sunday, 6=Saturday
	Hour      int     `json:"hour"`        // 0-23
	Count     int64   `json:"count"`
	Money     float64 `json:"money"` // 实付折美元
}

// successStatusCondition returns the SQL condition for successful top-ups.
// Pass a qualified column (for example "t.status") when the query joins another
// table that also has a status column.
func successStatusCondition(columnRefs ...string) string {
	column := "status"
	if len(columnRefs) > 0 && strings.TrimSpace(columnRefs[0]) != "" {
		column = columnRefs[0]
	}
	trimmed := fmt.Sprintf("TRIM(%s)", column)
	return fmt.Sprintf("(LOWER(%s) IN ('success', 'completed') OR %s = '1')", trimmed, trimmed)
}

// TopUpTrendsParams holds query parameters for revenue trends
type TopUpTrendsParams struct {
	Granularity string // "daily" | "weekly" | "monthly"; default daily
	StartDate   string // "YYYY-MM-DD"; optional
	EndDate     string // "YYYY-MM-DD"; optional
	Days        int    // fallback when StartDate/EndDate are empty; default 30
}

// resolveTrendsRange normalizes params and returns the effective [startTs, endTs] range
// (inclusive) plus the resolved granularity.
func resolveTrendsRange(p TopUpTrendsParams) (granularity string, startTs, endTs int64) {
	granularity = strings.ToLower(strings.TrimSpace(p.Granularity))
	switch granularity {
	case "weekly", "monthly":
	default:
		granularity = "daily"
	}

	days := p.Days
	if days < 1 || days > 365 {
		days = 30
	}

	loc := time.Now().Location()
	now := time.Now().In(loc)
	endOfToday := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)

	var sTs, eTs int64
	customRange := false
	if p.StartDate != "" && p.EndDate != "" {
		s, errS := util.ParseDateToTimestampPublic(p.StartDate, false)
		e, errE := util.ParseDateToTimestampPublic(p.EndDate, true)
		if errS == nil && errE == nil && s <= e {
			sTs, eTs = s, e
			customRange = true
		}
	}
	if !customRange {
		startDay := now.AddDate(0, 0, -(days - 1))
		startTs = time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, loc).Unix()
		endTs = endOfToday.Unix()
		return
	}
	startTs, endTs = sTs, eTs
	return
}

// GetTopUpTrends returns revenue trends with configurable granularity and date range
func GetTopUpTrends(p TopUpTrendsParams) ([]TopUpTrendPoint, error) {
	granularity, startTs, endTs := resolveTrendsRange(p)

	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:trends:%s:%d:%d", granularity, startTs, endTs)
	var cached []TopUpTrendPoint
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return cached, nil
	}

	var (
		result []TopUpTrendPoint
		err    error
	)
	switch granularity {
	case "weekly":
		result, err = topUpTrendsWeekly(startTs, endTs)
	case "monthly":
		result, err = topUpTrendsMonthly(startTs, endTs)
	default:
		result, err = topUpTrendsDaily(startTs, endTs)
	}
	if err != nil {
		return nil, err
	}

	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

// topUpTrendAggregates is what every trend bucket reports, over topUpFactsSQL.
const topUpTrendAggregates = `COUNT(*) as total_count,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN 1 ELSE 0 END), 0) as success_count,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN paid_usd ELSE 0 END), 0) as success_money`

func fillTopUpTrendPoint(point *TopUpTrendPoint, row map[string]interface{}) {
	point.Count = toInt64(row["total_count"])
	point.SuccessCount = toInt64(row["success_count"])
	point.SuccessMoney = toFloat64(row["success_money"])
}

// queryTopUpTrendBuckets groups the window's orders by bucketExpr, an integer
// expression over event_time, and returns the rows keyed by bucket.
func queryTopUpTrendBuckets(bucketExpr string, startTs, endTs int64) (map[int64]map[string]interface{}, error) {
	db := database.Get()
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT %s as bucket, %s
		FROM %s
		WHERE event_time >= ? AND event_time <= ?
		GROUP BY %s
		ORDER BY bucket ASC`,
		bucketExpr, topUpTrendAggregates, topUpFactsSQL("1=1", "f"), bucketExpr))

	rows, err := db.QueryWithTimeout(15*time.Second, query, startTs, endTs)
	if err != nil {
		return nil, err
	}
	lookup := make(map[int64]map[string]interface{}, len(rows))
	for _, row := range rows {
		lookup[toInt64(row["bucket"])] = row
	}
	return lookup, nil
}

// topUpTrendsDaily groups by day in the local timezone.
func topUpTrendsDaily(startTs, endTs int64) ([]TopUpTrendPoint, error) {
	tzOffset := localTZOffset()
	lookup, err := queryTopUpTrendBuckets(fmt.Sprintf("FLOOR((event_time + %d) / 86400)", tzOffset), startTs, endTs)
	if err != nil {
		return nil, fmt.Errorf("top-up trends daily query failed: %w", err)
	}

	loc := time.Now().Location()
	startTime := time.Unix(startTs, 0).In(loc)
	endTime := time.Unix(endTs, 0).In(loc)
	cursor := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 0, 0, 0, 0, loc)
	last := time.Date(endTime.Year(), endTime.Month(), endTime.Day(), 0, 0, 0, 0, loc)

	result := make([]TopUpTrendPoint, 0)
	for !cursor.After(last) {
		expectedGroup := (cursor.Unix() + int64(tzOffset)) / 86400
		point := TopUpTrendPoint{
			Date:      cursor.Format("2006-01-02"),
			Timestamp: cursor.Unix(),
		}
		if existing, ok := lookup[expectedGroup]; ok {
			fillTopUpTrendPoint(&point, existing)
		}
		result = append(result, point)
		cursor = cursor.AddDate(0, 0, 1)
	}
	return result, nil
}

// topUpTrendsWeekly groups by ISO week (Monday-aligned) in the local timezone.
// Bucket math: FLOOR((event_time + tz - 345600) / 604800).
// 345600 = 4 * 86400 shifts Unix epoch (1970-01-01 Thu) so the *following* Monday
// (1970-01-05) becomes bucket 0 — every other Monday-aligned week aligns from there.
func topUpTrendsWeekly(startTs, endTs int64) ([]TopUpTrendPoint, error) {
	tzOffset := localTZOffset()
	lookup, err := queryTopUpTrendBuckets(fmt.Sprintf("FLOOR((event_time + %d - 345600) / 604800)", tzOffset), startTs, endTs)
	if err != nil {
		return nil, fmt.Errorf("top-up trends weekly query failed: %w", err)
	}

	loc := time.Now().Location()
	cursor := mondayOf(time.Unix(startTs, 0).In(loc))
	last := mondayOf(time.Unix(endTs, 0).In(loc))

	result := make([]TopUpTrendPoint, 0)
	for !cursor.After(last) {
		expectedGroup := (cursor.Unix() + int64(tzOffset) - 345600) / 604800
		year, week := cursor.ISOWeek()
		point := TopUpTrendPoint{
			Date:      fmt.Sprintf("%04d-W%02d", year, week),
			Timestamp: cursor.Unix(),
		}
		if existing, ok := lookup[expectedGroup]; ok {
			fillTopUpTrendPoint(&point, existing)
		}
		result = append(result, point)
		cursor = cursor.AddDate(0, 0, 7)
	}
	return result, nil
}

// mondayOf returns the Monday of the week containing t, at midnight local time.
func mondayOf(t time.Time) time.Time {
	loc := t.Location()
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(weekday - 1))
	return monday
}

// topUpTrendsMonthly walks each calendar month in the range and runs an aggregate query
// per month. SQL-side grouping for months is messy across MySQL/PG; the per-month loop
// keeps the bucket boundaries correct and stays under the cache layer anyway.
func topUpTrendsMonthly(startTs, endTs int64) ([]TopUpTrendPoint, error) {
	db := database.Get()
	loc := time.Now().Location()

	startTime := time.Unix(startTs, 0).In(loc)
	endTime := time.Unix(endTs, 0).In(loc)
	cursor := time.Date(startTime.Year(), startTime.Month(), 1, 0, 0, 0, 0, loc)

	query := db.RebindQuery(fmt.Sprintf(`SELECT %s FROM %s WHERE event_time >= ? AND event_time <= ?`,
		topUpTrendAggregates, topUpFactsSQL("1=1", "f")))

	result := make([]TopUpTrendPoint, 0)
	for !cursor.After(endTime) {
		nextMonth := cursor.AddDate(0, 1, 0)
		queryStart := max(cursor.Unix(), startTs)
		queryEnd := min(nextMonth.Unix()-1, endTs)

		point := TopUpTrendPoint{
			Date:      cursor.Format("2006-01"),
			Timestamp: cursor.Unix(),
		}
		if queryStart <= queryEnd {
			row, err := db.QueryOneWithTimeout(10*time.Second, query, queryStart, queryEnd)
			if err == nil && row != nil {
				fillTopUpTrendPoint(&point, row)
			}
		}

		result = append(result, point)
		cursor = nextMonth
	}
	return result, nil
}

// GetTopUpFinancialSummary returns monthly financial summaries. Each month
// holds the orders whose event time falls in it, so a ¥70 order created on the
// 31st and paid on the 1st is next month's revenue, as on the growth panel.
func GetTopUpFinancialSummary(months int) ([]TopUpFinancialSummary, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:financial:%d", months)
	var cached []TopUpFinancialSummary
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return cached, nil
	}

	db := database.Get()
	now := time.Now()
	loc := now.Location()
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT COUNT(*) as total_count,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN 1 ELSE 0 END), 0) as success_count,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN paid_usd ELSE 0 END), 0) as success_money,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN credited_usd ELSE 0 END), 0) as success_credited,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' AND pay_currency IS NULL THEN 1 ELSE 0 END), 0) as unknown_currency_count
		FROM %s
		WHERE event_time >= ? AND event_time <= ?`, topUpFactsSQL("1=1", "f")))

	result := make([]TopUpFinancialSummary, 0, months)
	for i := 0; i < months; i++ {
		monthStart := time.Date(now.Year(), now.Month()-time.Month(i), 1, 0, 0, 0, 0, loc)
		monthEnd := monthStart.AddDate(0, 1, 0).Add(-time.Second)
		row, err := db.QueryOneWithTimeout(10*time.Second, query, monthStart.Unix(), monthEnd.Unix())
		if err != nil {
			continue
		}
		result = append(result, buildTopUpFinancialSummary(monthStart.Format("2006-01"), row))
	}

	// Calculate growth rates (compare with previous month)
	for i := 0; i < len(result)-1; i++ {
		if result[i+1].Revenue > 0 {
			result[i].GrowthRate = math.Round((result[i].Revenue-result[i+1].Revenue)/result[i+1].Revenue*10000) / 100
		}
	}

	cm.Set(cacheKey, result, 10*time.Minute)
	return result, nil
}

func buildTopUpFinancialSummary(period string, row map[string]interface{}) TopUpFinancialSummary {
	totalCount := toInt64(row["total_count"])
	successCount := toInt64(row["success_count"])
	successMoney := toFloat64(row["success_money"])
	unknown := toInt64(row["unknown_currency_count"])

	summary := TopUpFinancialSummary{
		Period:               period,
		Revenue:              round2(successMoney),
		Count:                successCount,
		CreditedUSD:          round2(toFloat64(row["success_credited"])),
		UnknownCurrencyCount: unknown,
	}
	// The average is over the orders the revenue actually contains.
	if priced := successCount - unknown; priced > 0 {
		summary.AvgOrder = round2(successMoney / float64(priced))
	}
	if totalCount > 0 {
		summary.SuccessRate = math.Round(float64(successCount)/float64(totalCount)*10000) / 100
	}
	return summary
}

// GetTopUpTopUsers returns top users by paid USD over orders paid in the window.
func GetTopUpTopUsers(limit int, days int) ([]TopUpTopUser, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:topusers:%d:%d", limit, days)
	var cached []TopUpTopUser
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return cached, nil
	}

	db := database.Get()
	startTime := time.Now().AddDate(0, 0, -days).Unix()

	castExpr := "CAST(f.user_id AS CHAR)"
	if db.IsPG {
		castExpr = "CAST(f.user_id AS TEXT)"
	}

	query := db.RebindQuery(fmt.Sprintf(`
		SELECT f.user_id,
			COALESCE(u.username, %s) as username,
			COUNT(*) as count,
			COALESCE(SUM(f.paid_usd), 0) as paid_total,
			COALESCE(SUM(f.credited_usd), 0) as credited_total,
			COALESCE(SUM(CASE WHEN f.pay_currency IS NULL THEN 1 ELSE 0 END), 0) as unknown_currency_count
		FROM %s
		LEFT JOIN users u ON f.user_id = u.id
		WHERE f.status_bucket = 'success' AND f.paid_at >= ?
		GROUP BY f.user_id, u.username
		ORDER BY paid_total DESC, f.user_id ASC
		LIMIT ?`, castExpr, topUpFactsSQL("1=1", "f")))

	rows, err := db.QueryWithTimeout(15*time.Second, query, startTime, limit)
	if err != nil {
		return nil, fmt.Errorf("top users query failed: %w", err)
	}

	result := make([]TopUpTopUser, 0, len(rows))
	for _, row := range rows {
		result = append(result, TopUpTopUser{
			UserID:               toInt64(row["user_id"]),
			Username:             fmt.Sprintf("%v", row["username"]),
			Count:                toInt64(row["count"]),
			PaidUSD:              round2(toFloat64(row["paid_total"])),
			CreditedUSD:          round2(toFloat64(row["credited_total"])),
			UnknownCurrencyCount: toInt64(row["unknown_currency_count"]),
		})
	}

	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

// GetPaymentMethodDistribution returns the payment method breakdown by paid USD.
func GetPaymentMethodDistribution(days int) ([]PaymentMethodDistribution, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:payment_dist:%d", days)
	var cached []PaymentMethodDistribution
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return cached, nil
	}

	db := database.Get()
	startTime := time.Now().AddDate(0, 0, -days).Unix()

	query := db.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(payment_method, '未知') as method,
			COUNT(*) as count,
			COALESCE(SUM(paid_usd), 0) as paid_total,
			COALESCE(SUM(CASE WHEN pay_currency IS NULL THEN 1 ELSE 0 END), 0) as unknown_currency_count
		FROM %s
		WHERE status_bucket = 'success' AND paid_at >= ?
		GROUP BY payment_method
		ORDER BY paid_total DESC`, topUpFactsSQL("1=1", "f")))

	rows, err := db.QueryWithTimeout(10*time.Second, query, startTime)
	if err != nil {
		return nil, fmt.Errorf("payment distribution query failed: %w", err)
	}

	var totalMoney float64
	for _, row := range rows {
		totalMoney += toFloat64(row["paid_total"])
	}

	result := make([]PaymentMethodDistribution, 0, len(rows))
	for _, row := range rows {
		money := toFloat64(row["paid_total"])
		pct := float64(0)
		if totalMoney > 0 {
			pct = math.Round(money/totalMoney*10000) / 100
		}
		method := fmt.Sprintf("%v", row["method"])
		if method == "" || method == "<nil>" {
			method = "未知"
		}
		result = append(result, PaymentMethodDistribution{
			Method:               method,
			Count:                toInt64(row["count"]),
			PaidUSD:              round2(money),
			Percentage:           pct,
			UnknownCurrencyCount: toInt64(row["unknown_currency_count"]),
		})
	}

	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

// GetTopUpRealtimeStats returns real-time comparison statistics: paid USD and
// order counts of the orders paid today / this week / this month and the
// period before each.
func GetTopUpRealtimeStats() (*TopUpRealtimeStats, error) {
	db := database.Get()
	now := time.Now()
	loc := now.Location()

	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
	yesterdayStart := todayStart - 86400
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := time.Date(now.Year(), now.Month(), now.Day()-(weekday-1), 0, 0, 0, 0, loc).Unix()
	lastWeekStart := weekStart - 7*86400
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Unix()
	lastMonthStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, loc).Unix()

	// 缓存键编入日历边界，跨日 / 跨周 / 跨月时自动失效，避免临界点展示昨天数据。
	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:realtime:%d:%d:%d", todayStart, weekStart, monthStart)
	var cached TopUpRealtimeStats
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return &cached, nil
	}

	// The facts filter carries no placeholder, so the args below bind in the
	// order they appear in the SELECT list, then the outer WHERE.
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN paid_usd ELSE 0 END), 0) as today_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN 1 ELSE 0 END), 0) as today_count,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN paid_usd ELSE 0 END), 0) as yesterday_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN 1 ELSE 0 END), 0) as yesterday_count,
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN paid_usd ELSE 0 END), 0) as week_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN 1 ELSE 0 END), 0) as week_count,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN paid_usd ELSE 0 END), 0) as last_week_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN 1 ELSE 0 END), 0) as last_week_count,
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN paid_usd ELSE 0 END), 0) as month_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? THEN 1 ELSE 0 END), 0) as month_count,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN paid_usd ELSE 0 END), 0) as last_month_money,
			COALESCE(SUM(CASE WHEN paid_at >= ? AND paid_at < ? THEN 1 ELSE 0 END), 0) as last_month_count
		FROM %s
		WHERE paid_at >= ?`, topUpFactsSQL(successStatusCondition(), "f")))

	row, err := db.QueryOneWithTimeout(15*time.Second, query,
		todayStart, todayStart,
		yesterdayStart, todayStart, yesterdayStart, todayStart,
		weekStart, weekStart,
		lastWeekStart, weekStart, lastWeekStart, weekStart,
		monthStart, monthStart,
		lastMonthStart, monthStart, lastMonthStart, monthStart,
		lastMonthStart, // WHERE condition
	)
	if err != nil {
		return nil, fmt.Errorf("realtime stats query failed: %w", err)
	}

	stats := buildTopUpRealtimeStats(row)
	cm.Set(cacheKey, stats, 2*time.Minute)
	return stats, nil
}

func buildTopUpRealtimeStats(row map[string]interface{}) *TopUpRealtimeStats {
	stats := &TopUpRealtimeStats{CNYPerUSD: cnyPerUSD()}
	if row == nil {
		return stats
	}
	stats.TodayMoney = round2(toFloat64(row["today_money"]))
	stats.TodayCount = toInt64(row["today_count"])
	stats.YesterdayMoney = round2(toFloat64(row["yesterday_money"]))
	stats.YesterdayCount = toInt64(row["yesterday_count"])
	stats.WeekMoney = round2(toFloat64(row["week_money"]))
	stats.WeekCount = toInt64(row["week_count"])
	stats.LastWeekMoney = round2(toFloat64(row["last_week_money"]))
	stats.LastWeekCount = toInt64(row["last_week_count"])
	stats.MonthMoney = round2(toFloat64(row["month_money"]))
	stats.MonthCount = toInt64(row["month_count"])
	stats.LastMonthMoney = round2(toFloat64(row["last_month_money"]))
	stats.LastMonthCount = toInt64(row["last_month_count"])
	stats.DayGrowth = topUpGrowthRate(stats.TodayMoney, stats.YesterdayMoney)
	stats.WeekGrowth = topUpGrowthRate(stats.WeekMoney, stats.LastWeekMoney)
	stats.MonthGrowth = topUpGrowthRate(stats.MonthMoney, stats.LastMonthMoney)
	return stats
}

func topUpGrowthRate(current, previous float64) float64 {
	if previous <= 0 {
		return 0
	}
	return math.Round((current-previous)/previous*10000) / 100
}

// GetTopUpHourlyHeatmap returns the paid-order heatmap for the past N days,
// placed at the hour each order was paid.
func GetTopUpHourlyHeatmap(days int) ([]HourlyHeatmapPoint, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:heatmap:%d", days)
	var cached []HourlyHeatmapPoint
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return cached, nil
	}

	db := database.Get()
	startTime := time.Now().AddDate(0, 0, -days).Unix()
	tzOffset := localTZOffset()

	hourExpr, dowExpr := topUpHeatmapTimeExpressions(tzOffset, db.IsPG, "paid_at")

	query := db.RebindQuery(fmt.Sprintf(`
		SELECT %s as day_of_week,
			%s as hour,
			COUNT(*) as count,
			COALESCE(SUM(paid_usd), 0) as money
		FROM %s
		WHERE status_bucket = 'success' AND paid_at >= ?
		GROUP BY %s, %s
		ORDER BY day_of_week, hour`,
		dowExpr, hourExpr, topUpFactsSQL("1=1", "f"), dowExpr, hourExpr))

	rows, err := db.QueryWithTimeout(15*time.Second, query, startTime)
	if err != nil {
		return nil, fmt.Errorf("heatmap query failed: %w", err)
	}

	result := topUpHeatmapGrid(rows)

	cm.Set(cacheKey, result, 10*time.Minute)
	return result, nil
}

func topUpHeatmapGrid(rows []map[string]interface{}) []HourlyHeatmapPoint {
	result := make([]HourlyHeatmapPoint, 0, 7*24)
	heatmap := make(map[string]*HourlyHeatmapPoint)

	for dow := 0; dow < 7; dow++ {
		for h := 0; h < 24; h++ {
			key := fmt.Sprintf("%d-%d", dow, h)
			point := &HourlyHeatmapPoint{DayOfWeek: dow, Hour: h}
			heatmap[key] = point
		}
	}

	// Fill with data
	for _, row := range rows {
		dow := int(toInt64(row["day_of_week"]))
		hour := int(toInt64(row["hour"]))
		key := fmt.Sprintf("%d-%d", dow, hour)
		if point, ok := heatmap[key]; ok {
			point.Count = toInt64(row["count"])
			point.Money = toFloat64(row["money"])
		}
	}

	for dow := 0; dow < 7; dow++ {
		for h := 0; h < 24; h++ {
			key := fmt.Sprintf("%d-%d", dow, h)
			result = append(result, *heatmap[key])
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].DayOfWeek != result[j].DayOfWeek {
			return result[i].DayOfWeek < result[j].DayOfWeek
		}
		return result[i].Hour < result[j].Hour
	})

	return result
}

func topUpHeatmapTimeExpressions(tzOffset int, isPG bool, column string) (hourExpr, dowExpr string) {
	// Extract day of week and hour from a unix timestamp column with timezone offset.
	// Day of week: (day_bucket + 4) % 7 gives 0=Sunday because Unix epoch was Thursday=4.
	hourExpr = fmt.Sprintf("FLOOR(((%s + %d) %% 86400) / 3600)", column, tzOffset)
	dayBucketExpr := fmt.Sprintf("FLOOR((%s + %d) / 86400)", column, tzOffset)
	if isPG {
		// PostgreSQL FLOOR(bigint division) returns double precision, and modulo
		// is not defined for double precision. Cast before applying %.
		dayBucketExpr = fmt.Sprintf("CAST(%s AS BIGINT)", dayBucketExpr)
	}
	dowExpr = fmt.Sprintf("(%s + 4) %% 7", dayBucketExpr)
	return hourExpr, dowExpr
}

// FunnelStatusBucket counts top-ups grouped into the normalised status buckets.
type FunnelStatusBucket struct {
	Status string  `json:"status"`
	Count  int64   `json:"count"`
	Money  float64 `json:"money"` // 实付折美元（币种未知的不计）
}

// FunnelPaymentBucket reports per-payment-method success rate.
type FunnelPaymentBucket struct {
	Method       string  `json:"method"`
	TotalCount   int64   `json:"total_count"`
	SuccessCount int64   `json:"success_count"`
	SuccessRate  float64 `json:"success_rate"` // 0-100
}

// TopUpFunnelData is the conversion-funnel response.
type TopUpFunnelData struct {
	StatusBreakdown   []FunnelStatusBucket  `json:"status_breakdown"`
	ByPaymentMethod   []FunnelPaymentBucket `json:"by_payment_method"`
	AvgCompletionSecs float64               `json:"avg_completion_secs"`
	TotalCount        int64                 `json:"total_count"`
}

// topUpFunnelStatusOrder is the stable order the funnel emits its buckets in.
var topUpFunnelStatusOrder = []string{"success", "pending", "reviewing", "failed", "expired", "unknown"}

// GetTopUpFunnel returns conversion funnel statistics for the past N days,
// windowed by event time like the rest of the analytics tab.
func GetTopUpFunnel(days int) (*TopUpFunnelData, error) {
	if days < 1 || days > 365 {
		days = 30
	}

	cm := cache.Get()
	cacheKey := fmt.Sprintf("topup:funnel:%d", days)
	var cached TopUpFunnelData
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return &cached, nil
	}

	startTime := time.Now().AddDate(0, 0, -days).Unix()
	facts := topUpFactsSQL("1=1", "f")

	statuses, totalCount, err := topUpFunnelStatuses(facts, startTime)
	if err != nil {
		return nil, err
	}
	payments, err := topUpFunnelPayments(facts, startTime)
	if err != nil {
		return nil, err
	}

	result := &TopUpFunnelData{
		StatusBreakdown:   statuses,
		ByPaymentMethod:   payments,
		AvgCompletionSecs: topUpFunnelAvgCompletion(facts, startTime),
		TotalCount:        totalCount,
	}

	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

func topUpFunnelStatuses(facts string, startTime int64) ([]FunnelStatusBucket, int64, error) {
	db := database.Get()
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT status_bucket as bucket,
			COUNT(*) as count,
			COALESCE(SUM(paid_usd), 0) as money
		FROM %s
		WHERE event_time >= ?
		GROUP BY status_bucket`, facts))

	rows, err := db.QueryWithTimeout(15*time.Second, query, startTime)
	if err != nil {
		return nil, 0, fmt.Errorf("funnel status query failed: %w", err)
	}

	tally := map[string]FunnelStatusBucket{}
	var totalCount int64
	for _, row := range rows {
		status := fmt.Sprintf("%v", row["bucket"])
		count := toInt64(row["count"])
		tally[status] = FunnelStatusBucket{Status: status, Count: count, Money: round2(toFloat64(row["money"]))}
		totalCount += count
	}
	out := make([]FunnelStatusBucket, 0, len(topUpFunnelStatusOrder))
	for _, s := range topUpFunnelStatusOrder {
		if b, ok := tally[s]; ok {
			out = append(out, b)
		} else {
			out = append(out, FunnelStatusBucket{Status: s})
		}
	}
	return out, totalCount, nil
}

func topUpFunnelPayments(facts string, startTime int64) ([]FunnelPaymentBucket, error) {
	db := database.Get()
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(payment_method, '') as method,
			COUNT(*) as total_count,
			COALESCE(SUM(CASE WHEN status_bucket = 'success' THEN 1 ELSE 0 END), 0) as success_count
		FROM %s
		WHERE event_time >= ?
		GROUP BY payment_method
		ORDER BY total_count DESC`, facts))

	rows, err := db.QueryWithTimeout(15*time.Second, query, startTime)
	if err != nil {
		return nil, fmt.Errorf("funnel payment query failed: %w", err)
	}

	out := make([]FunnelPaymentBucket, 0, len(rows))
	for _, row := range rows {
		total := toInt64(row["total_count"])
		success := toInt64(row["success_count"])
		rate := float64(0)
		if total > 0 {
			rate = math.Round(float64(success)/float64(total)*10000) / 100
		}
		method := strings.TrimSpace(fmt.Sprintf("%v", row["method"]))
		if method == "" || method == "<nil>" {
			method = "未知"
		}
		out = append(out, FunnelPaymentBucket{Method: method, TotalCount: total, SuccessCount: success, SuccessRate: rate})
	}
	return out, nil
}

// topUpFunnelAvgCompletion is the average create→complete latency of the
// window's successful, properly stamped orders.
func topUpFunnelAvgCompletion(facts string, startTime int64) float64 {
	db := database.Get()
	// 显式 CAST 成 double，避免 PG 上 AVG(bigint) 返回 numeric 时驱动 scan 出意外类型。
	avgCastExpr := "CAST(AVG(complete_time - create_time) AS DOUBLE PRECISION)"
	if !db.IsPG {
		// MySQL：DOUBLE 关键字（无 PRECISION）；SQLite 也接受 DOUBLE。
		avgCastExpr = "CAST(AVG(complete_time - create_time) AS DOUBLE)"
	}
	query := db.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(%s, 0) as avg_secs
		FROM %s
		WHERE event_time >= ?
			AND status_bucket = 'success'
			AND complete_time > 0
			AND complete_time >= create_time`,
		avgCastExpr, facts))

	row, _ := db.QueryOneWithTimeout(10*time.Second, query, startTime)
	if row == nil {
		return 0
	}
	return round2(toFloat64(row["avg_secs"]))
}
