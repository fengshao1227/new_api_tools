package service

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

const marginAnalysisTimeout = 60 * time.Second

var signupGiftAmountPattern = regexp.MustCompile(`[\$＄]([0-9]+(?:\.[0-9]+)?)`)

// MarginAnalysisParams is the read-only time window for the margin report.
// EndTime is inclusive, matching the other analytics endpoints in this tool.
type MarginAnalysisParams struct {
	StartTime int64
	EndTime   int64
	Limit     int
	NoCache   bool
}

type MarginAnalysisRange struct {
	Start     int64  `json:"start"`
	End       int64  `json:"end"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Timezone  string `json:"timezone"`
	DayCount  int    `json:"day_count"`
}

type MarginSummary struct {
	Requests              int64   `json:"requests"`
	RealizedRevenueUSD    float64 `json:"realized_revenue_usd"`
	ProviderCostUSD       float64 `json:"provider_cost_usd"`
	GrossProfitUSD        float64 `json:"gross_profit_usd"`
	GrossMarginPercent    float64 `json:"gross_margin_percent"`
	ExternalProfitUSD     float64 `json:"external_profit_usd"`
	ExternalMarginPercent float64 `json:"external_margin_percent"`
	PaidTrafficCostUSD    float64 `json:"paid_traffic_cost_usd"`
	GiftAndFreeCostUSD    float64 `json:"gift_and_free_cost_usd"`
	InternalCostUSD       float64 `json:"internal_cost_usd"`
	GiftNominalUSD        float64 `json:"gift_nominal_usd"`
	GiftProviderCostUSD   float64 `json:"gift_provider_cost_usd"`
	PaidCustomerCount     int64   `json:"paid_customer_count"`
	FreeCustomerCount     int64   `json:"free_customer_count"`
	ManualCreditUserCount int64   `json:"manual_credit_user_count"`
	InternalUserCount     int64   `json:"internal_user_count"`
	UnpricedCalls         int64   `json:"unpriced_calls"`
	EstimatedCalls        int64   `json:"estimated_calls"`
	UnpricedCostUSD       float64 `json:"unpriced_cost_usd"`
	ZeroQuotaCostCalls    int64   `json:"zero_quota_cost_calls"`
}

type MarginDailyPoint struct {
	Date               string  `json:"date"`
	Requests           int64   `json:"requests"`
	RevenueUSD         float64 `json:"revenue_usd"`
	ProviderCostUSD    float64 `json:"provider_cost_usd"`
	GrossProfitUSD     float64 `json:"gross_profit_usd"`
	GiftAndFreeCostUSD float64 `json:"gift_and_free_cost_usd"`
	InternalCostUSD    float64 `json:"internal_cost_usd"`
}

type MarginBreakdown struct {
	Key                string  `json:"key"`
	Name               string  `json:"name"`
	UserID             int64   `json:"user_id,omitempty"`
	ChannelID          int64   `json:"channel_id,omitempty"`
	Bucket             string  `json:"bucket,omitempty"`
	Requests           int64   `json:"requests"`
	RevenueUSD         float64 `json:"revenue_usd"`
	ProviderCostUSD    float64 `json:"provider_cost_usd"`
	GrossProfitUSD     float64 `json:"gross_profit_usd"`
	MarginPercent      float64 `json:"margin_percent"`
	GiftAndFreeCostUSD float64 `json:"gift_and_free_cost_usd"`
	InternalCostUSD    float64 `json:"internal_cost_usd"`
	UnpricedCalls      int64   `json:"unpriced_calls"`
	EstimatedCalls     int64   `json:"estimated_calls"`
}

type MarginAnalysisResult struct {
	Range        MarginAnalysisRange `json:"range"`
	QuotaPerUnit int64               `json:"quota_per_unit"`
	Currency     string              `json:"currency"`
	Summary      MarginSummary       `json:"summary"`
	Daily        []MarginDailyPoint  `json:"daily"`
	Models       []MarginBreakdown   `json:"models"`
	Channels     []MarginBreakdown   `json:"channels"`
	Users        []MarginBreakdown   `json:"users"`
	Notes        []string            `json:"notes"`
}

type marginUser struct {
	ID           int64
	Username     string
	Role         int64
	TopUpQuota   int64
	GrantedQuota int64
}

type marginGroupRow struct {
	UserID      int64
	Username    string
	DayGroup    int64
	ModelName   string
	ChannelID   int64
	ChannelName string
	Requests    int64
	Quota       float64
	Cost        float64
	Unpriced    int64
	Estimated   int64
}

type marginUserState struct {
	UserID      int64
	Username    string
	Bucket      string
	PeriodQuota float64
	PeriodCost  float64
	BeforeQuota float64
	GiftQuota   float64
	GrantQuota  float64
}

type marginAccumulator struct {
	Requests         int64
	RevenueQuota     float64
	ProviderCost     float64
	PaidTrafficCost  float64
	GiftFreeCost     float64
	InternalCost     float64
	GiftNominalQuota float64
	GiftProviderCost float64
	UnpricedCalls    int64
	EstimatedCalls   int64
	UnpricedCost     float64
	ZeroQuotaCost    int64
}

func isPaidMarginBucket(bucket string) bool {
	return bucket == "customer_paid" || bucket == "deleted_paid"
}

func isInternalMarginBucket(bucket string) bool {
	return bucket == "manual_or_test_credit" || bucket == "staff_or_root"
}

func addMarginGroup(acc *marginAccumulator, row marginGroupRow, state marginUserState, giftQuota, giftCost float64) {
	acc.Requests += row.Requests
	acc.ProviderCost += row.Cost
	acc.UnpricedCalls += row.Unpriced
	acc.EstimatedCalls += row.Estimated
	if row.Quota == 0 && row.Cost > 0 {
		acc.ZeroQuotaCost++
	}
	if row.Unpriced > 0 {
		acc.UnpricedCost += row.Cost
	}

	if isPaidMarginBucket(state.Bucket) {
		acc.RevenueQuota += row.Quota - giftQuota
		acc.PaidTrafficCost += row.Cost - giftCost
		acc.GiftProviderCost += giftCost
		acc.GiftNominalQuota += giftQuota
		return
	}
	if isInternalMarginBucket(state.Bucket) {
		acc.InternalCost += row.Cost
		return
	}
	// Unpaid customers do not create revenue, but every supplier bill still
	// belongs in the result. Gift quota is tracked separately from the whole
	// free/unpaid cost because old accounts may have exhausted their grant.
	acc.GiftFreeCost += row.Cost
	acc.GiftProviderCost += giftCost
	acc.GiftNominalQuota += giftQuota
}

func (a marginAccumulator) merge(other marginAccumulator) marginAccumulator {
	a.Requests += other.Requests
	a.RevenueQuota += other.RevenueQuota
	a.ProviderCost += other.ProviderCost
	a.PaidTrafficCost += other.PaidTrafficCost
	a.GiftFreeCost += other.GiftFreeCost
	a.InternalCost += other.InternalCost
	a.GiftNominalQuota += other.GiftNominalQuota
	a.GiftProviderCost += other.GiftProviderCost
	a.UnpricedCalls += other.UnpricedCalls
	a.EstimatedCalls += other.EstimatedCalls
	a.UnpricedCost += other.UnpricedCost
	a.ZeroQuotaCost += other.ZeroQuotaCost
	return a
}

func buildMarginBucket(role int64, found bool, paid bool, topUpQuota int64) string {
	if !found {
		if paid {
			return "deleted_paid"
		}
		return "deleted_no_payment"
	}
	if role >= 10 {
		return "staff_or_root"
	}
	if paid {
		return "customer_paid"
	}
	if topUpQuota > 0 {
		return "manual_or_test_credit"
	}
	return "customer_free"
}

func clampGiftQuota(grantQuota, beforeQuota, periodQuota float64) float64 {
	remaining := grantQuota - beforeQuota
	if remaining <= 0 || periodQuota <= 0 {
		return 0
	}
	if remaining > periodQuota {
		return periodQuota
	}
	return remaining
}

func marginMoney(quota float64) float64 {
	return quota / float64(util.TokensPerUSD)
}

func marginPercent(profit, revenue float64) float64 {
	if revenue <= 0 {
		return 0
	}
	return profit / revenue * 100
}

// MarginAnalysisService owns the accounting read model. It deliberately does
// not write NewAPI tables: the plugin is an observability and reconciliation
// layer, not a second billing engine.
type MarginAnalysisService struct {
	db    *database.Manager
	logDB *database.Manager
}

func NewMarginAnalysisService() *MarginAnalysisService {
	return &MarginAnalysisService{db: database.Get(), logDB: database.GetLog()}
}

func (s *MarginAnalysisService) GetMarginAnalysis(params MarginAnalysisParams) (*MarginAnalysisResult, error) {
	if params.StartTime <= 0 || params.EndTime < params.StartTime {
		return nil, fmt.Errorf("invalid margin analysis range")
	}
	if params.Limit < 1 || params.Limit > 500 {
		params.Limit = 100
	}

	cacheKey := fmt.Sprintf("margin-analysis:%d:%d:%d", params.StartTime, params.EndTime, params.Limit)
	cm := cache.Get()
	if !params.NoCache {
		var cached MarginAnalysisResult
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return &cached, nil
		}
	}

	rows, err := s.loadMarginRows(params)
	if err != nil {
		return nil, err
	}
	result, err := s.buildMarginResult(params, rows)
	if err != nil {
		return nil, err
	}
	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

func (s *MarginAnalysisService) loadMarginRows(params MarginAnalysisParams) ([]marginGroupRow, error) {
	tzOffset := localTZOffset()
	dayGroup := fmt.Sprintf("FLOOR((created_at + %d) / 86400)", tzOffset)
	query := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT user_id, COALESCE(username, '') AS username,
			%s AS day_group, COALESCE(model_name, '') AS model_name,
			COALESCE(channel_id, 0) AS channel_id,
			COALESCE(channel_name, '') AS channel_name,
			COUNT(*) AS requests,
			COALESCE(SUM(quota), 0) AS quota,
			COALESCE(SUM(cost), 0) AS cost,
			COALESCE(SUM(CASE WHEN other LIKE '%%"unpriced":true%%' OR other LIKE '%%"unpriced": true%%' THEN 1 ELSE 0 END), 0) AS unpriced_calls,
			COALESCE(SUM(CASE WHEN other LIKE '%%"cost_source":"estimated"%%' OR other LIKE '%%"cost_source": "estimated"%%' THEN 1 ELSE 0 END), 0) AS estimated_calls
		FROM logs
		WHERE type = 2 AND created_at >= ? AND created_at <= ?
		GROUP BY user_id, username, %s, model_name, channel_id, channel_name
		ORDER BY day_group ASC`, dayGroup, dayGroup))
	dbRows, err := s.logDB.QueryWithTimeout(marginAnalysisTimeout, query, params.StartTime, params.EndTime)
	if err != nil {
		return nil, fmt.Errorf("margin log aggregation failed: %w", err)
	}
	rows := make([]marginGroupRow, 0, len(dbRows))
	for _, row := range dbRows {
		rows = append(rows, marginGroupRow{
			UserID:      toInt64(row["user_id"]),
			Username:    toString(row["username"]),
			DayGroup:    toInt64(row["day_group"]),
			ModelName:   strings.TrimSpace(toString(row["model_name"])),
			ChannelID:   toInt64(row["channel_id"]),
			ChannelName: strings.TrimSpace(toString(row["channel_name"])),
			Requests:    toInt64(row["requests"]),
			Quota:       toFloat64(row["quota"]),
			Cost:        toFloat64(row["cost"]),
			Unpriced:    toInt64(row["unpriced_calls"]),
			Estimated:   toInt64(row["estimated_calls"]),
		})
	}
	return rows, nil
}

func buildIDInQuery(db *database.Manager, prefix string, ids []int64) (string, []interface{}) {
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return db.RebindQuery(prefix + " IN (" + strings.Join(placeholders, ",") + ")"), args
}

func distinctMarginUserIDs(rows []marginGroupRow) []int64 {
	seen := make(map[int64]bool, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.UserID <= 0 || seen[row.UserID] {
			continue
		}
		seen[row.UserID] = true
		ids = append(ids, row.UserID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (s *MarginAnalysisService) loadBeforeQuota(startTime int64, ids []int64) (map[int64]float64, error) {
	result := make(map[int64]float64, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	query, args := buildIDInQuery(s.logDB,
		"SELECT user_id, COALESCE(SUM(quota), 0) AS quota FROM logs WHERE type = 2 AND created_at < ? AND user_id", ids)
	args = append([]interface{}{startTime}, args...)
	query += " GROUP BY user_id"
	rows, err := s.logDB.QueryWithTimeout(marginAnalysisTimeout, query, args...)
	if err != nil {
		return nil, fmt.Errorf("margin historical quota query failed: %w", err)
	}
	for _, row := range rows {
		result[toInt64(row["user_id"])] = toFloat64(row["quota"])
	}
	return result, nil
}

func (s *MarginAnalysisService) loadGiftQuotas(ids []int64) (map[int64]float64, error) {
	result := make(map[int64]float64, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	query, args := buildIDInQuery(s.logDB,
		"SELECT user_id, content FROM logs WHERE type = 4 AND content LIKE ? AND user_id", ids)
	args = append([]interface{}{"%新用户注册赠送%"}, args...)
	rows, err := s.logDB.QueryWithTimeout(marginAnalysisTimeout, query, args...)
	if err != nil {
		return nil, fmt.Errorf("margin signup grant query failed: %w", err)
	}
	for _, row := range rows {
		match := signupGiftAmountPattern.FindStringSubmatch(toString(row["content"]))
		if len(match) != 2 {
			continue
		}
		amount, parseErr := strconv.ParseFloat(match[1], 64)
		if parseErr != nil || amount < 0 || math.IsInf(amount, 0) || math.IsNaN(amount) {
			continue
		}
		id := toInt64(row["user_id"])
		quota := amount * float64(util.TokensPerUSD)
		if quota > result[id] {
			result[id] = quota
		}
	}
	return result, nil
}

func (s *MarginAnalysisService) loadUsers(ids []int64) (map[int64]marginUser, error) {
	users := make(map[int64]marginUser, len(ids))
	if len(ids) == 0 {
		return users, nil
	}
	query, args := buildIDInQuery(s.db,
		"SELECT id, COALESCE(username, '') AS username, COALESCE(role, 1) AS role, COALESCE(topup_quota, 0) AS topup_quota, COALESCE(granted_quota, 0) AS granted_quota FROM users WHERE id", ids)
	rows, err := s.db.QueryWithTimeout(30*time.Second, query, args...)
	if err != nil {
		// granted_quota was added by a recent NewAPI migration. The fallback keeps
		// this plugin useful against an older compatible database.
		query, args = buildIDInQuery(s.db,
			"SELECT id, COALESCE(username, '') AS username, COALESCE(role, 1) AS role, COALESCE(topup_quota, 0) AS topup_quota, 0 AS granted_quota FROM users WHERE id", ids)
		rows, err = s.db.QueryWithTimeout(30*time.Second, query, args...)
	}
	if err != nil {
		// Very old NewAPI installations do not have topup_quota either. They can
		// still produce a useful cost report; those accounts are conservatively
		// treated as unpaid unless a settled top-up row proves otherwise.
		query, args = buildIDInQuery(s.db,
			"SELECT id, COALESCE(username, '') AS username, COALESCE(role, 1) AS role, 0 AS topup_quota, 0 AS granted_quota FROM users WHERE id", ids)
		rows, err = s.db.QueryWithTimeout(30*time.Second, query, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("margin user query failed: %w", err)
	}
	for _, row := range rows {
		id := toInt64(row["id"])
		users[id] = marginUser{ID: id, Username: toString(row["username"]), Role: toInt64(row["role"]), TopUpQuota: toInt64(row["topup_quota"]), GrantedQuota: toInt64(row["granted_quota"])}
	}
	return users, nil
}

func (s *MarginAnalysisService) loadPaidUsers(ids []int64) (map[int64]bool, error) {
	paid := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return paid, nil
	}
	query, args := buildIDInQuery(s.db,
		"SELECT DISTINCT user_id FROM top_ups WHERE (LOWER(TRIM(status)) IN ('success', 'completed') OR TRIM(status) = '1') AND user_id", ids)
	rows, err := s.db.QueryWithTimeout(30*time.Second, query, args...)
	if err != nil {
		return nil, fmt.Errorf("margin paid user query failed: %w", err)
	}
	for _, row := range rows {
		paid[toInt64(row["user_id"])] = true
	}
	return paid, nil
}

func (s *MarginAnalysisService) buildMarginResult(params MarginAnalysisParams, rows []marginGroupRow) (*MarginAnalysisResult, error) {
	ids := distinctMarginUserIDs(rows)
	users, err := s.loadUsers(ids)
	if err != nil {
		return nil, err
	}
	paidUsers, err := s.loadPaidUsers(ids)
	if err != nil {
		return nil, err
	}
	beforeQuota, err := s.loadBeforeQuota(params.StartTime, ids)
	if err != nil {
		return nil, err
	}
	giftQuotas, err := s.loadGiftQuotas(ids)
	if err != nil {
		return nil, err
	}

	states := make(map[int64]marginUserState, len(ids))
	for _, row := range rows {
		state, ok := states[row.UserID]
		if !ok {
			user, found := users[row.UserID]
			state = marginUserState{
				UserID:      row.UserID,
				Username:    row.Username,
				Bucket:      buildMarginBucket(user.Role, found, paidUsers[row.UserID], user.TopUpQuota),
				BeforeQuota: beforeQuota[row.UserID],
				GrantQuota:  giftQuotas[row.UserID],
			}
			if found && float64(user.GrantedQuota) > state.GrantQuota {
				state.GrantQuota = float64(user.GrantedQuota)
			}
			if found && state.Username == "" {
				state.Username = user.Username
			}
			states[row.UserID] = state
		}
		state.PeriodQuota += row.Quota
		state.PeriodCost += row.Cost
		if state.Username == "" && row.Username != "" {
			state.Username = row.Username
		}
		states[row.UserID] = state
	}
	for id, state := range states {
		state.GiftQuota = clampGiftQuota(state.GrantQuota, state.BeforeQuota, state.PeriodQuota)
		states[id] = state
	}

	var total marginAccumulator
	daily := make(map[string]*marginAccumulator)
	models := make(map[string]*marginAccumulator)
	channels := make(map[string]*marginAccumulator)
	usersAcc := make(map[int64]*marginAccumulator)
	modelNames := make(map[string]string)
	channelNames := make(map[string]string)
	for _, row := range rows {
		state := states[row.UserID]
		giftRatio := 0.0
		if state.PeriodQuota > 0 {
			giftRatio = state.GiftQuota / state.PeriodQuota
		}
		giftQuota := row.Quota * giftRatio
		giftCost := 0.0
		if row.Quota > 0 {
			giftCost = row.Cost * giftRatio
		}
		groupAcc := marginAccumulator{}
		addMarginGroup(&groupAcc, row, state, giftQuota, giftCost)
		total = total.merge(groupAcc)

		date := marginDayLabel(row.DayGroup)
		if _, ok := daily[date]; !ok {
			daily[date] = &marginAccumulator{}
		}
		daily[date] = ptrMerge(daily[date], groupAcc)

		modelKey := row.ModelName
		if modelKey == "" {
			modelKey = "unknown"
		}
		modelNames[modelKey] = modelKey
		if _, ok := models[modelKey]; !ok {
			models[modelKey] = &marginAccumulator{}
		}
		models[modelKey] = ptrMerge(models[modelKey], groupAcc)

		channelKey := fmt.Sprintf("%d", row.ChannelID)
		if row.ChannelID == 0 {
			channelKey = "unknown"
		}
		if row.ChannelName != "" {
			channelNames[channelKey] = row.ChannelName
		} else if _, ok := channelNames[channelKey]; !ok {
			channelNames[channelKey] = channelKey
		}
		if _, ok := channels[channelKey]; !ok {
			channels[channelKey] = &marginAccumulator{}
		}
		channels[channelKey] = ptrMerge(channels[channelKey], groupAcc)

		if _, ok := usersAcc[row.UserID]; !ok {
			usersAcc[row.UserID] = &marginAccumulator{}
		}
		usersAcc[row.UserID] = ptrMerge(usersAcc[row.UserID], groupAcc)
	}

	paidRevenue := marginMoney(total.RevenueQuota)
	providerCost := marginMoney(total.ProviderCost)
	grossProfit := paidRevenue - providerCost
	paidTrafficCost := marginMoney(total.PaidTrafficCost)
	giftFreeCost := marginMoney(total.GiftFreeCost)
	internalCost := marginMoney(total.InternalCost)
	result := &MarginAnalysisResult{
		Range: MarginAnalysisRange{
			Start:     params.StartTime,
			End:       params.EndTime,
			StartDate: time.Unix(params.StartTime, 0).In(time.Local).Format("2006-01-02"),
			EndDate:   time.Unix(params.EndTime, 0).In(time.Local).Format("2006-01-02"),
			Timezone:  time.Local.String(),
			DayCount:  int(math.Ceil(float64(params.EndTime-params.StartTime+1) / 86400)),
		},
		QuotaPerUnit: util.TokensPerUSD,
		Currency:     "USD",
		Summary: MarginSummary{
			Requests:              total.Requests,
			RealizedRevenueUSD:    paidRevenue,
			ProviderCostUSD:       providerCost,
			GrossProfitUSD:        grossProfit,
			GrossMarginPercent:    marginPercent(grossProfit, paidRevenue),
			ExternalProfitUSD:     paidRevenue - paidTrafficCost - giftFreeCost,
			ExternalMarginPercent: marginPercent(paidRevenue-paidTrafficCost-giftFreeCost, paidRevenue),
			PaidTrafficCostUSD:    paidTrafficCost,
			GiftAndFreeCostUSD:    giftFreeCost,
			InternalCostUSD:       internalCost,
			GiftNominalUSD:        marginMoney(total.GiftNominalQuota),
			GiftProviderCostUSD:   marginMoney(total.GiftProviderCost),
			PaidCustomerCount:     countMarginBuckets(states, "customer_paid", "deleted_paid"),
			FreeCustomerCount:     countMarginBuckets(states, "customer_free", "deleted_no_payment"),
			ManualCreditUserCount: countMarginBuckets(states, "manual_or_test_credit"),
			InternalUserCount:     countMarginBuckets(states, "staff_or_root"),
			UnpricedCalls:         total.UnpricedCalls,
			EstimatedCalls:        total.EstimatedCalls,
			UnpricedCostUSD:       marginMoney(total.UnpricedCost),
			ZeroQuotaCostCalls:    total.ZeroQuotaCost,
		},
		Daily:    buildMarginDaily(daily, params.StartTime, params.EndTime),
		Models:   buildMarginBreakdowns(models, modelNames, nil, params.Limit),
		Channels: buildMarginBreakdowns(channels, channelNames, nil, params.Limit),
		Users:    buildMarginUserBreakdowns(usersAcc, states, params.Limit),
		Notes: []string{
			"收入按已结算消费日志计算，未使用的充值余额不计入。",
			"注册赠额从收入中剔除；赠额与免费账号产生的供应商成本仍计入。",
			"unpriced 成本不会被当成免费，需结合供应商账单继续对账。",
			"管理员、测试和手工授信账号单独列为内部成本，不自动视为现金收入。",
		},
	}
	return result, nil
}

func ptrMerge(dst *marginAccumulator, src marginAccumulator) *marginAccumulator {
	merged := dst.merge(src)
	return &merged
}

func countMarginBuckets(states map[int64]marginUserState, buckets ...string) int64 {
	wanted := make(map[string]bool, len(buckets))
	for _, bucket := range buckets {
		wanted[bucket] = true
	}
	var count int64
	for _, state := range states {
		if wanted[state.Bucket] {
			count++
		}
	}
	return count
}

func marginDayLabel(dayGroup int64) string {
	if dayGroup <= 0 {
		return "unknown"
	}
	tzOffset := int64(localTZOffset())
	return time.Unix(dayGroup*86400-tzOffset, 0).In(time.Local).Format("2006-01-02")
}

func buildMarginDaily(values map[string]*marginAccumulator, startTime, endTime int64) []MarginDailyPoint {
	loc := time.Local
	start := time.Unix(startTime, 0).In(loc)
	end := time.Unix(endTime, 0).In(loc)
	cursor := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	last := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	result := make([]MarginDailyPoint, 0, int(last.Sub(cursor).Hours()/24)+1)
	for !cursor.After(last) {
		key := cursor.Format("2006-01-02")
		acc := values[key]
		if acc == nil {
			acc = &marginAccumulator{}
		}
		revenue := marginMoney(acc.RevenueQuota)
		result = append(result, MarginDailyPoint{
			Date:               key,
			Requests:           acc.Requests,
			RevenueUSD:         revenue,
			ProviderCostUSD:    marginMoney(acc.ProviderCost),
			GrossProfitUSD:     revenue - marginMoney(acc.ProviderCost),
			GiftAndFreeCostUSD: marginMoney(acc.GiftFreeCost),
			InternalCostUSD:    marginMoney(acc.InternalCost),
		})
		cursor = cursor.AddDate(0, 0, 1)
	}
	return result
}

func buildMarginBreakdowns(values map[string]*marginAccumulator, names map[string]string, buckets map[string]string, limit int) []MarginBreakdown {
	result := make([]MarginBreakdown, 0, len(values))
	for key, acc := range values {
		revenue := marginMoney(acc.RevenueQuota)
		cost := marginMoney(acc.ProviderCost)
		name := names[key]
		if name == "" {
			name = key
		}
		bucket := ""
		if buckets != nil {
			bucket = buckets[key]
		}
		result = append(result, MarginBreakdown{
			Key: key, Name: name, Bucket: bucket,
			Requests: acc.Requests, RevenueUSD: revenue, ProviderCostUSD: cost,
			GrossProfitUSD: revenue - cost, MarginPercent: marginPercent(revenue-cost, revenue),
			GiftAndFreeCostUSD: marginMoney(acc.GiftFreeCost), InternalCostUSD: marginMoney(acc.InternalCost),
			UnpricedCalls: acc.UnpricedCalls, EstimatedCalls: acc.EstimatedCalls,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].GrossProfitUSD != result[j].GrossProfitUSD {
			return result[i].GrossProfitUSD > result[j].GrossProfitUSD
		}
		return result[i].Requests > result[j].Requests
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func buildMarginUserBreakdowns(values map[int64]*marginAccumulator, states map[int64]marginUserState, limit int) []MarginBreakdown {
	names := make(map[string]string, len(states))
	buckets := make(map[string]string, len(states))
	for id, state := range states {
		key := strconv.FormatInt(id, 10)
		names[key] = state.Username
		buckets[key] = state.Bucket
	}
	converted := make(map[string]*marginAccumulator, len(values))
	for id, acc := range values {
		converted[strconv.FormatInt(id, 10)] = acc
	}
	result := buildMarginBreakdowns(converted, names, buckets, limit)
	for i := range result {
		result[i].UserID, _ = strconv.ParseInt(result[i].Key, 10, 64)
	}
	return result
}
