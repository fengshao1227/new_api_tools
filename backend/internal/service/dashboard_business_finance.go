package service

import (
	"fmt"
	"sort"
	"time"
)

// Money for one window, in the margin report's own vocabulary (ADR 0002):
//
//   - billed: SUM(logs.quota) — what the gateway charged at its own prices.
//     An internal transfer price, never revenue.
//   - supplier cost: SUM(logs.cost), same unit as quota.
//   - realized usage revenue / gross profit / external profit: straight from
//     MarginAnalysisService, which recognises revenue only on paying
//     customers' consumption net of their signup gift, and books free,
//     internal and charged-back traffic as cost.
//   - cash in: settled top-ups by the time they settled, converted with the
//     shared top-up currency rules — the same figure the growth panel sums.

// BusinessCash is money that actually arrived in the window.
type BusinessCash struct {
	RevenueUSD float64 `json:"revenue_usd"`
	Orders     int64   `json:"orders"`
	Payers     int64   `json:"payers"`
	// UnknownCurrencyOrders settled on a rail the currency rules do not
	// recognise; they are left out of RevenueUSD rather than guessed.
	UnknownCurrencyOrders int64 `json:"unknown_currency_orders"`
}

// BusinessFinanceDay is one local calendar day of the window.
type BusinessFinanceDay struct {
	Date               string  `json:"date"`
	Requests           int64   `json:"requests"`
	BilledUSD          float64 `json:"billed_usd"`
	ProviderCostUSD    float64 `json:"provider_cost_usd"`
	RealizedRevenueUSD float64 `json:"realized_revenue_usd"`
	GrossProfitUSD     float64 `json:"gross_profit_usd"`
	CashRevenueUSD     float64 `json:"cash_revenue_usd"`
}

// BusinessModelRow is one model's money in the window.
type BusinessModelRow struct {
	Model              string  `json:"model"`
	Requests           int64   `json:"requests"`
	BilledUSD          float64 `json:"billed_usd"`
	ProviderCostUSD    float64 `json:"provider_cost_usd"`
	RealizedRevenueUSD float64 `json:"realized_revenue_usd"`
	GrossProfitUSD     float64 `json:"gross_profit_usd"`
	MarginPercent      float64 `json:"margin_percent"`
	UnpricedCalls      int64   `json:"unpriced_calls"`
}

// BusinessModelRanking is the top models three ways. Models that billed
// nothing in the window (free models such as jev-1.13-free) are left out of
// all three lists — they would otherwise top any ranking by volume — and are
// summarised in the Excluded* fields so their supplier cost is not hidden.
type BusinessModelRanking struct {
	ByBilled             []BusinessModelRow `json:"by_billed"`
	ByCost               []BusinessModelRow `json:"by_cost"`
	ByProfit             []BusinessModelRow `json:"by_profit"`
	ExcludedFreeModels   []string           `json:"excluded_free_models"`
	ExcludedFreeRequests int64              `json:"excluded_free_requests"`
	ExcludedFreeCostUSD  float64            `json:"excluded_free_cost_usd"`
}

// BusinessFinance is the money section of the business view.
type BusinessFinance struct {
	Window DashboardWindow      `json:"window"`
	Margin MarginSummary        `json:"margin"`
	Cash   BusinessCash         `json:"cash"`
	Daily  []BusinessFinanceDay `json:"daily"`
	Models BusinessModelRanking `json:"models"`
}

const businessModelRankLimit = 10

// GetFinance returns billed, cost, margin and cash-in figures for a window.
func (s *BusinessDashboardService) GetFinance(window string, noCache bool) (BusinessFinance, error) {
	w := ResolveDashboardWindow(window, time.Now())
	return businessCached("dashboard:beat:finance:"+w.Key, 5*time.Minute, noCache, func() (BusinessFinance, error) {
		return s.loadFinance(w)
	})
}

func (s *BusinessDashboardService) loadFinance(w DashboardWindow) (BusinessFinance, error) {
	margin, err := (&MarginAnalysisService{db: s.db, logDB: s.logDB}).GetMarginAnalysis(MarginAnalysisParams{
		StartTime: w.Start, EndTime: w.End, Limit: 500, NoCache: true,
	})
	if err != nil {
		return BusinessFinance{}, err
	}
	cash, cashByDay, err := s.loadCash(w)
	if err != nil {
		return BusinessFinance{}, err
	}
	daily := make([]BusinessFinanceDay, 0, len(margin.Daily))
	for _, d := range margin.Daily {
		daily = append(daily, BusinessFinanceDay{
			Date: d.Date, Requests: d.Requests, BilledUSD: d.BilledUSD,
			ProviderCostUSD: d.ProviderCostUSD, RealizedRevenueUSD: d.RevenueUSD,
			GrossProfitUSD: d.GrossProfitUSD, CashRevenueUSD: cashByDay[d.Date],
		})
	}
	return BusinessFinance{
		Window: w,
		Margin: margin.Summary,
		Cash:   cash,
		Daily:  daily,
		Models: buildBusinessModelRanking(margin.Models, businessModelRankLimit),
	}, nil
}

// loadCash sums settled top-ups by the moment they settled, per local day.
func (s *BusinessDashboardService) loadCash(w DashboardWindow) (BusinessCash, map[string]float64, error) {
	paidUSD := topUpPaidUSDSQL("")
	paidAt := topUpPaidAtSQL("")
	day := dayBucket(paidAt, localTZOffset())
	where := fmt.Sprintf("%s AND %s >= ? AND %s <= ?", successStatusCondition(), paidAt, paidAt)

	byDay := map[string]float64{}
	var cash BusinessCash
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(fmt.Sprintf(`
		SELECT %s AS day_group,
			COALESCE(SUM(%s), 0) AS revenue,
			COUNT(*) AS orders,
			COALESCE(SUM(CASE WHEN %s IS NULL THEN 1 ELSE 0 END), 0) AS unknown_orders
		FROM top_ups WHERE %s
		GROUP BY %s`, day, paidUSD, paidUSD, where, day)), w.Start, w.End)
	if err != nil {
		return cash, nil, fmt.Errorf("cash revenue query failed: %w", err)
	}
	for _, r := range rows {
		revenue := toFloat64(r["revenue"])
		byDay[marginDayLabel(int64(toFloat64(r["day_group"])))] += revenue
		cash.RevenueUSD += revenue
		cash.Orders += toInt64(r["orders"])
		cash.UnknownCurrencyOrders += toInt64(r["unknown_orders"])
	}

	payers, err := s.db.QueryOneWithTimeout(businessQueryTimeout, s.db.RebindQuery(
		"SELECT COUNT(DISTINCT user_id) AS payers FROM top_ups WHERE "+where), w.Start, w.End)
	if err != nil {
		return cash, nil, fmt.Errorf("cash payer query failed: %w", err)
	}
	cash.Payers = toInt64(payers["payers"])
	return cash, byDay, nil
}

// buildBusinessModelRanking ranks models by billed amount, supplier cost and
// gross profit, excluding models that billed nothing in the window.
func buildBusinessModelRanking(models []MarginBreakdown, limit int) BusinessModelRanking {
	ranking := BusinessModelRanking{ExcludedFreeModels: []string{}}
	rows := make([]BusinessModelRow, 0, len(models))
	type freeModel struct {
		name     string
		requests int64
	}
	free := make([]freeModel, 0)
	for _, m := range models {
		if m.BilledUSD <= 0 {
			free = append(free, freeModel{name: m.Name, requests: m.Requests})
			ranking.ExcludedFreeRequests += m.Requests
			ranking.ExcludedFreeCostUSD += m.ProviderCostUSD
			continue
		}
		rows = append(rows, BusinessModelRow{
			Model: m.Name, Requests: m.Requests, BilledUSD: m.BilledUSD,
			ProviderCostUSD: m.ProviderCostUSD, RealizedRevenueUSD: m.RevenueUSD,
			GrossProfitUSD: m.GrossProfitUSD, MarginPercent: m.MarginPercent,
			UnpricedCalls: m.UnpricedCalls,
		})
	}
	sort.Slice(free, func(i, j int) bool { return free[i].requests > free[j].requests })
	for i, f := range free {
		if i == limit {
			break
		}
		ranking.ExcludedFreeModels = append(ranking.ExcludedFreeModels, f.name)
	}
	ranking.ByBilled = topModelRows(rows, limit, func(r BusinessModelRow) float64 { return r.BilledUSD })
	ranking.ByCost = topModelRows(rows, limit, func(r BusinessModelRow) float64 { return r.ProviderCostUSD })
	ranking.ByProfit = topModelRows(rows, limit, func(r BusinessModelRow) float64 { return r.GrossProfitUSD })
	return ranking
}

func topModelRows(rows []BusinessModelRow, limit int, key func(BusinessModelRow) float64) []BusinessModelRow {
	sorted := make([]BusinessModelRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		if key(sorted[i]) != key(sorted[j]) {
			return key(sorted[i]) > key(sorted[j])
		}
		return sorted[i].Model < sorted[j].Model
	})
	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
}
