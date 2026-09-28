package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Supply side: the supplier accounts the gateway watches (upstream_monitors),
// the operational alerts it still has open (ops_alert_states), and pricing
// gaps — channels that would record a call's supplier cost as zero.
//
// Pricing follows the gateway's own resolution (ADR 0001): a model is priced
// by the channel's cost_expr entry, else by the global default
// (options cost_setting.default_cost_expr), else the call is logged unpriced.
// A gap is therefore an enabled channel with no expression at all, or one
// serving models that neither its cost_expr nor the global default covers —
// the usual result of adding a model to a channel in the console.

// BusinessUpstream is one watched supplier account.
type BusinessUpstream struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Balance       float64 `json:"balance"`
	LowBalance    float64 `json:"low_balance"`
	Low           bool    `json:"low"`
	Currency      string  `json:"currency"`
	Status        int64   `json:"status"`
	LastError     string  `json:"last_error"`
	LastCheckedAt int64   `json:"last_checked_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

// BusinessAlert is one open operational alert.
type BusinessAlert struct {
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	FirstAt    int64  `json:"first_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	Count      int64  `json:"count"`
}

// BusinessSupply is the supplier-and-alert section.
type BusinessSupply struct {
	UpstreamAvailable bool               `json:"upstream_available"`
	Upstreams         []BusinessUpstream `json:"upstreams"`
	AlertsAvailable   bool               `json:"alerts_available"`
	Alerts            []BusinessAlert    `json:"alerts"`
}

// Upstream monitor status values, as the gateway writes them.
const upstreamMonitorStatusError = 2

// GetSupply returns watched supplier accounts and open alerts. Neither is
// windowed: both are the state right now.
func (s *BusinessDashboardService) GetSupply(noCache bool) (BusinessSupply, error) {
	return businessCached("dashboard:beat:supply", time.Minute, noCache, func() (BusinessSupply, error) {
		result := BusinessSupply{Upstreams: []BusinessUpstream{}, Alerts: []BusinessAlert{}}
		upstreams, err := s.loadUpstreams()
		if err != nil && !isMissingSchemaErr(err) {
			return result, err
		}
		if err == nil {
			result.UpstreamAvailable = true
			result.Upstreams = upstreams
		}
		alerts, err := s.loadAlerts()
		if err != nil && !isMissingSchemaErr(err) {
			return result, err
		}
		if err == nil {
			result.AlertsAvailable = true
			result.Alerts = alerts
		}
		return result, nil
	})
}

func (s *BusinessDashboardService) loadUpstreams() ([]BusinessUpstream, error) {
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT id, COALESCE(name, '') AS name, COALESCE(balance, 0) AS balance,
			COALESCE(low_balance, 0) AS low_balance, COALESCE(status, 0) AS status,
			COALESCE(last_error, '') AS last_error, COALESCE(last_checked_at, 0) AS last_checked_at,
			COALESCE(updated_at, 0) AS updated_at, COALESCE(currency, '') AS currency,
			COALESCE(quota_display_type, '') AS quota_display_type
		FROM upstream_monitors WHERE enabled = ?`), true)
	if err != nil {
		return nil, err
	}
	out := make([]BusinessUpstream, 0, len(rows))
	for _, r := range rows {
		u := BusinessUpstream{
			ID: toInt64(r["id"]), Name: toString(r["name"]),
			Balance: toFloat64(r["balance"]), LowBalance: toFloat64(r["low_balance"]),
			Status: toInt64(r["status"]), LastError: toString(r["last_error"]),
			LastCheckedAt: toInt64(r["last_checked_at"]), UpdatedAt: toInt64(r["updated_at"]),
			// The operator's override wins over what the panel declares:
			// several resellers publish yuan as USD.
			Currency: toString(r["currency"]),
		}
		if u.Currency == "" {
			u.Currency = toString(r["quota_display_type"])
		}
		u.Low = u.LowBalance > 0 && u.Balance < u.LowBalance
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := upstreamNeedsAttention(out[i]), upstreamNeedsAttention(out[j])
		if pi != pj {
			return pi
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func upstreamNeedsAttention(u BusinessUpstream) bool {
	return u.Low || u.Status == upstreamMonitorStatusError
}

func (s *BusinessDashboardService) loadAlerts() ([]BusinessAlert, error) {
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT alert_key, COALESCE(kind, '') AS kind, COALESCE(title, '') AS title,
			COALESCE(first_at, 0) AS first_at, COALESCE(last_seen_at, 0) AS last_seen_at,
			COALESCE(count, 0) AS alert_count
		FROM ops_alert_states WHERE is_open = ?
		ORDER BY first_at DESC
		LIMIT 50`), true)
	if err != nil {
		return nil, err
	}
	out := make([]BusinessAlert, 0, len(rows))
	for _, r := range rows {
		out = append(out, BusinessAlert{
			Key: toString(r["alert_key"]), Kind: toString(r["kind"]), Title: toString(r["title"]),
			FirstAt: toInt64(r["first_at"]), LastSeenAt: toInt64(r["last_seen_at"]), Count: toInt64(r["alert_count"]),
		})
	}
	return out, nil
}

// BusinessPricingChannel is an enabled channel with at least one model that
// would be logged unpriced.
type BusinessPricingChannel struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Priority      int64    `json:"priority"`
	NoCostExpr    bool     `json:"no_cost_expr"`
	ModelCount    int      `json:"model_count"`
	MissingModels []string `json:"missing_models"`
}

// BusinessUnpricedRow is calls logged without a supplier cost.
type BusinessUnpricedRow struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Model       string `json:"model"`
	Calls       int64  `json:"calls"`
}

// BusinessPricingGaps is the pricing-gap section. Both counts should be zero.
type BusinessPricingGaps struct {
	Window            DashboardWindow          `json:"window"`
	ChannelsAvailable bool                     `json:"channels_available"`
	Channels          []BusinessPricingChannel `json:"channels"`
	// UnpricedSource is "quota_data" (the gateway's hourly buckets), "logs"
	// (admin_info.unpriced on each consumption log) or "" when neither exists.
	UnpricedSource string                `json:"unpriced_source"`
	UnpricedCalls  int64                 `json:"unpriced_calls"`
	Unpriced       []BusinessUnpricedRow `json:"unpriced"`
}

type pricingChannelRow struct {
	ID       int64
	Name     string
	Status   int64
	Priority int64
	Models   string
	CostExpr string
}

const businessUnpricedRowLimit = 20

// GetPricingGaps returns channels missing cost expressions and the calls
// logged unpriced in the window.
func (s *BusinessDashboardService) GetPricingGaps(window string, noCache bool) (BusinessPricingGaps, error) {
	w := ResolveDashboardWindow(window, time.Now())
	return businessCached("dashboard:beat:pricing-gaps:"+w.Key, 3*time.Minute, noCache, func() (BusinessPricingGaps, error) {
		result := BusinessPricingGaps{Window: w, Channels: []BusinessPricingChannel{}, Unpriced: []BusinessUnpricedRow{}}
		channels, err := s.loadPricingChannels()
		if err != nil && !isMissingSchemaErr(err) {
			return result, err
		}
		names := map[int64]string{}
		if err == nil {
			result.ChannelsAvailable = true
			for _, c := range channels {
				names[c.ID] = c.Name
			}
			result.Channels = findPricingGaps(channels, s.loadDefaultCostExpr())
		}
		source, rows, err := s.loadUnpriced(w)
		if err != nil {
			return result, err
		}
		result.UnpricedSource = source
		for i := range rows {
			rows[i].ChannelName = names[rows[i].ChannelID]
			result.UnpricedCalls += rows[i].Calls
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Calls > rows[j].Calls })
		if len(rows) > businessUnpricedRowLimit {
			rows = rows[:businessUnpricedRowLimit]
		}
		result.Unpriced = rows
		return result, nil
	})
}

func (s *BusinessDashboardService) loadPricingChannels() ([]pricingChannelRow, error) {
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, `
		SELECT id, COALESCE(name, '') AS name, COALESCE(status, 0) AS status,
			COALESCE(priority, 0) AS priority, COALESCE(models, '') AS models, cost_expr
		FROM channels`)
	if err != nil {
		return nil, err
	}
	out := make([]pricingChannelRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, pricingChannelRow{
			ID: toInt64(r["id"]), Name: toString(r["name"]), Status: toInt64(r["status"]),
			Priority: toInt64(r["priority"]), Models: toString(r["models"]), CostExpr: toString(r["cost_expr"]),
		})
	}
	return out, nil
}

// loadDefaultCostExpr reads the global fallback expressions. Any failure
// means "no fallback", which can only make the gap list longer, never hide a
// gap.
func (s *BusinessDashboardService) loadDefaultCostExpr() map[string]string {
	query := s.db.RebindQuery(fmt.Sprintf("SELECT value FROM options WHERE %s = ?", s.db.QuoteIdentifier("key")))
	row, err := s.db.QueryOneWithTimeout(businessQueryTimeout, query, "cost_setting.default_cost_expr")
	if err != nil || row == nil {
		return map[string]string{}
	}
	return parseCostExprMap(toString(row["value"]))
}

// parseCostExprMap reads a {"model": "expr"} map, dropping blank expressions.
// Malformed JSON is an empty map: the gateway treats it the same way.
func parseCostExprMap(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return out
	}
	for model, expr := range parsed {
		if strings.TrimSpace(expr) != "" {
			out[strings.TrimSpace(model)] = expr
		}
	}
	return out
}

// findPricingGaps lists enabled channels serving a model with no cost
// expression, either on the channel or in the global defaults.
func findPricingGaps(channels []pricingChannelRow, defaults map[string]string) []BusinessPricingChannel {
	out := make([]BusinessPricingChannel, 0)
	for _, c := range channels {
		if c.Status != 1 {
			continue
		}
		exprs := parseCostExprMap(c.CostExpr)
		models := splitChannelModels(c.Models)
		missing := make([]string, 0)
		for _, m := range models {
			if _, ok := exprs[m]; ok {
				continue
			}
			if _, ok := defaults[m]; ok {
				continue
			}
			missing = append(missing, m)
		}
		if len(missing) == 0 {
			// Includes a channel with an empty cost_expr whose models the
			// global defaults all price: the gateway prices those calls.
			continue
		}
		out = append(out, BusinessPricingChannel{
			ID: c.ID, Name: c.Name, Priority: c.Priority, NoCostExpr: len(exprs) == 0,
			ModelCount: len(models), MissingModels: missing,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func splitChannelModels(raw string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, m := range strings.Split(raw, ",") {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// loadUnpriced counts unpriced calls per channel and model, preferring the
// gateway's quota_data buckets and falling back to the logs when that table
// is missing or has nothing for the window.
func (s *BusinessDashboardService) loadUnpriced(w DashboardWindow) (string, []BusinessUnpricedRow, error) {
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT COALESCE(channel_id, 0) AS channel_id, COALESCE(model_name, '') AS model_name,
			COALESCE(SUM(unpriced_count), 0) AS unpriced
		FROM quota_data WHERE created_at >= ? AND created_at <= ?
		GROUP BY channel_id, model_name`), w.Start, w.End)
	if err != nil && !isMissingSchemaErr(err) {
		return "", nil, fmt.Errorf("unpriced bucket query failed: %w", err)
	}
	if err == nil && len(rows) > 0 {
		return "quota_data", unpricedRows(rows), nil
	}

	unpriced := marginJSONFieldCondition(s.logDB, "unpriced", "true")
	rows, err = s.logDB.QueryWithTimeout(businessQueryTimeout, s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(channel_id, 0) AS channel_id, COALESCE(model_name, '') AS model_name, COUNT(*) AS unpriced
		FROM logs WHERE type = 2 AND created_at >= ? AND created_at <= ? AND %s
		GROUP BY channel_id, model_name`, unpriced)), w.Start, w.End)
	if isMissingSchemaErr(err) {
		return "", []BusinessUnpricedRow{}, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("unpriced log query failed: %w", err)
	}
	return "logs", unpricedRows(rows), nil
}

func unpricedRows(rows []map[string]interface{}) []BusinessUnpricedRow {
	out := make([]BusinessUnpricedRow, 0)
	for _, r := range rows {
		calls := toInt64(r["unpriced"])
		if calls <= 0 {
			continue
		}
		out = append(out, BusinessUnpricedRow{
			ChannelID: toInt64(r["channel_id"]), Model: toString(r["model_name"]), Calls: calls,
		})
	}
	return out
}
