package service

import (
	"fmt"
	"strings"
)

// Per-row facts the user list adds to the users table, read with one query per
// fact for the whole page rather than per user.

// Risk statuses as the gateway's risk_events table spells them.
const (
	userRiskOpen = "open"
	userRiskNone = "none"
)

// userRiskBrief is the gateway risk engine's verdict on an account: "open"
// (waiting for review, with the free credit held back meanwhile), otherwise
// the latest human decision — confirmed / released / withheld / dismissed —
// or "none".
type userRiskBrief struct {
	Status     string  `json:"status"`
	OpenCases  int64   `json:"open_cases,omitempty"`
	HeldUSD    float64 `json:"held_usd,omitempty"`
	ResolvedAt int64   `json:"resolved_at,omitempty"`
}

// LoginSourceOption is one choice of the list's login source filter.
type LoginSourceOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // builtin | custom | password
}

// freeCreditQuota is the gateway's RiskFreeCredit: how much of an unpaid
// account's balance is signup credit. Accounts from before grants were
// recorded (granted 0) never paid, so all of their balance is free.
func freeCreditQuota(quota, granted int64) int64 {
	if quota <= 0 {
		return 0
	}
	if granted <= 0 || granted > quota {
		return quota
	}
	return granted
}

// enrichUserRows adds paid / free credit / risk / login sources to each row
// and drops the raw OAuth columns. It reports whether risk_events could be
// read; a gateway without it shows no risk column instead of failing.
func (s *UserManagementService) enrichUserRows(rows []map[string]interface{}, schema userListSchema) (bool, error) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if id := toInt64(row["id"]); id > 0 {
			ids = append(ids, id)
		}
	}
	paid, err := s.paidUserIDs(ids)
	if err != nil {
		return false, err
	}
	risk, riskAvailable := s.userRiskBriefs(ids)
	slugs := s.userOAuthSlugs(ids, schema)

	for _, row := range rows {
		id := toInt64(row["id"])
		applyPaidAndCredit(row, paid[id])
		sources := userLoginSources(row, schema, slugs[id])
		row["login_sources"] = sources
		row["source"] = sources[0]
		for _, c := range schema.loginColumns {
			delete(row, c.column)
		}
		if riskAvailable {
			if brief, ok := risk[id]; ok {
				row["risk"] = brief
			} else {
				row["risk"] = &userRiskBrief{Status: userRiskNone}
			}
		} else {
			row["risk"] = nil
		}
		if toInt64(row["request_count"]) == 0 {
			row["activity_level"] = ActivityNever
		} else {
			row["activity_level"] = ActivityActive
		}
		row["last_request_time"] = nil
	}
	return riskAvailable, nil
}

// applyPaidAndCredit: an account has paid when it has a settled top-up, or
// its lifetime credit (topup_quota) is positive — a ToB account credited by
// hand has no top-up row. Only unpaid accounts hold free credit.
func applyPaidAndCredit(row map[string]interface{}, hasTopUp bool) {
	topUpQuota := toInt64(row["topup_quota"])
	paidVia := ""
	switch {
	case hasTopUp:
		paidVia = "top_up"
	case topUpQuota > 0:
		paidVia = "credited"
	}
	row["paid"] = paidVia != ""
	row["paid_via"] = paidVia
	free := int64(0)
	if paidVia == "" {
		free = freeCreditQuota(toInt64(row["quota"]), toInt64(row["granted_quota"]))
	}
	row["free_credit_usd"] = marginMoney(float64(free))
}

// userLoginSources lists every way the account signs in, built-in columns
// first; "password" when there is none.
func userLoginSources(row map[string]interface{}, schema userListSchema, slugs []string) []string {
	seen := map[string]bool{}
	sources := []string{}
	add := func(source string) {
		if source != "" && !seen[source] {
			seen[source] = true
			sources = append(sources, source)
		}
	}
	for _, c := range schema.loginColumns {
		if strings.TrimSpace(toString(row[c.column])) != "" {
			add(c.source)
		}
	}
	for _, slug := range slugs {
		add(slug)
	}
	if len(sources) == 0 {
		sources = append(sources, "password")
	}
	return sources
}

func (s *UserManagementService) paidUserIDs(ids []int64) (map[int64]bool, error) {
	paid := make(map[int64]bool, len(ids))
	if len(ids) == 0 {
		return paid, nil
	}
	query, args := buildIDInQuery(s.db, fmt.Sprintf("SELECT DISTINCT user_id FROM top_ups WHERE %s AND user_id", successStatusCondition()), ids)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("paid users query failed: %w", err)
	}
	for _, row := range rows {
		paid[toInt64(row["user_id"])] = true
	}
	return paid, nil
}

// userRiskBriefs reads the risk verdict of each account. It reports false
// when risk_events cannot be read, which on gateways without the risk engine
// is the normal case.
func (s *UserManagementService) userRiskBriefs(ids []int64) (map[int64]*userRiskBrief, bool) {
	briefs := make(map[int64]*userRiskBrief, len(ids))
	if len(ids) == 0 {
		return briefs, true
	}
	openQuery, args := buildIDInQuery(s.db,
		"SELECT user_id, COUNT(*) AS open_cases, COALESCE(SUM(held_quota), 0) AS held FROM risk_events WHERE status = 'open' AND user_id", ids)
	rows, err := s.db.Query(openQuery+" GROUP BY user_id", args...)
	if err != nil {
		if !isMissingSchemaErr(err) {
			logWarn(fmt.Sprintf("risk_events open query failed: %v", err))
		}
		return nil, false
	}
	for _, row := range rows {
		briefs[toInt64(row["user_id"])] = &userRiskBrief{
			Status:    userRiskOpen,
			OpenCases: toInt64(row["open_cases"]),
			HeldUSD:   marginMoney(toFloat64(row["held"])),
		}
	}

	resolvedQuery, args := buildIDInQuery(s.db,
		"SELECT user_id, status, resolved_at FROM risk_events WHERE status IN ('confirmed', 'released', 'withheld', 'dismissed') AND user_id", ids)
	rows, err = s.db.Query(resolvedQuery+" ORDER BY resolved_at DESC, id DESC", args...)
	if err != nil {
		logWarn(fmt.Sprintf("risk_events decision query failed: %v", err))
		return nil, false
	}
	for _, row := range rows {
		id := toInt64(row["user_id"])
		if _, known := briefs[id]; known {
			continue // an open case, or a later decision, already speaks for it
		}
		briefs[id] = &userRiskBrief{Status: toString(row["status"]), ResolvedAt: toInt64(row["resolved_at"])}
	}
	return briefs, true
}

// userOAuthSlugs reads the console-configured OAuth providers each account is
// bound to, by provider slug.
func (s *UserManagementService) userOAuthSlugs(ids []int64, schema userListSchema) map[int64][]string {
	out := make(map[int64][]string)
	if !schema.bindings || len(ids) == 0 {
		return out
	}
	query, args := buildIDInQuery(s.db,
		"SELECT b.user_id, LOWER(p.slug) AS slug FROM user_oauth_bindings b JOIN custom_oauth_providers p ON p.id = b.provider_id WHERE b.user_id", ids)
	rows, err := s.db.Query(query+" ORDER BY b.id", args...)
	if err != nil {
		logWarn(fmt.Sprintf("user_oauth_bindings query failed: %v", err))
		return out
	}
	for _, row := range rows {
		id := toInt64(row["user_id"])
		out[id] = append(out[id], toString(row["slug"]))
	}
	return out
}

// GetLoginSources lists the login source filter's choices: the built-in
// OAuth columns this gateway has, its console-configured providers, and
// password.
func (s *UserManagementService) GetLoginSources() []LoginSourceOption {
	schema := s.userListSchema()
	seen := map[string]bool{}
	options := []LoginSourceOption{}
	for _, c := range schema.loginColumns {
		seen[c.source] = true
		options = append(options, LoginSourceOption{Key: c.source, Label: builtinLoginLabels[c.source], Kind: "builtin"})
	}
	if schema.bindings {
		rows, err := s.db.Query("SELECT LOWER(slug) AS slug, name FROM custom_oauth_providers ORDER BY id")
		if err != nil {
			logWarn(fmt.Sprintf("custom_oauth_providers query failed: %v", err))
		}
		for _, row := range rows {
			slug := toString(row["slug"])
			if slug == "" || seen[slug] {
				continue
			}
			seen[slug] = true
			label := toString(row["name"])
			if label == "" {
				label = slug
			}
			options = append(options, LoginSourceOption{Key: slug, Label: label, Kind: "custom"})
		}
	}
	return append(options, LoginSourceOption{Key: "password", Label: "密码注册", Kind: "password"})
}
