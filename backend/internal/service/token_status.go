package service

import (
	"fmt"
	"strings"
	"time"
)

// Token status codes as new-api stores them (common.TokenStatus*).
const (
	TokenStatusEnabled   = 1
	TokenStatusDisabled  = 2 // disabled by hand
	TokenStatusExpired   = 3
	TokenStatusExhausted = 4
)

// Effective token states. new-api only writes status 3/4 when it runs without
// Redis (model.ValidateUserToken); with Redis a token past its expiry or out
// of quota keeps status 1 and is refused at request time. So a state is the
// stored status or, for status 1, what the gateway's own checks would say —
// expiry first, then quota, in the gateway's order.
const (
	TokenStateActive    = "active"
	TokenStateDisabled  = "disabled"
	TokenStateExpired   = "expired"
	TokenStateExhausted = "exhausted"
	TokenStateUnknown   = "unknown"
)

// TokenStatistics holds aggregate token counts by effective state.
type TokenStatistics struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Disabled  int64 `json:"disabled"`  // status 2 only: disabled by hand
	Expired   int64 `json:"expired"`   // status 3, or status 1 past expired_time
	Exhausted int64 `json:"exhausted"` // status 4, or status 1 with no quota left
}

// tokenStateConditions returns one SQL condition per effective state for the
// tokens table under alias (e.g. "t." or ""). now is inlined: it is an
// integer the server computed, never input.
func tokenStateConditions(alias string, now int64) map[string]string {
	pastExpiry := fmt.Sprintf("(COALESCE(%[1]sexpired_time, -1) <> -1 AND COALESCE(%[1]sexpired_time, -1) < %[2]d)", alias, now)
	outOfQuota := fmt.Sprintf("(COALESCE(%[1]sunlimited_quota, FALSE) = FALSE AND COALESCE(%[1]sremain_quota, 0) <= 0)", alias)
	return map[string]string{
		TokenStateActive:   fmt.Sprintf("(%sstatus = %d AND NOT %s AND NOT %s)", alias, TokenStatusEnabled, pastExpiry, outOfQuota),
		TokenStateDisabled: fmt.Sprintf("(%sstatus = %d)", alias, TokenStatusDisabled),
		TokenStateExpired: fmt.Sprintf("(%[1]sstatus = %[2]d OR (%[1]sstatus = %[3]d AND %[4]s))",
			alias, TokenStatusExpired, TokenStatusEnabled, pastExpiry),
		TokenStateExhausted: fmt.Sprintf("(%[1]sstatus = %[2]d OR (%[1]sstatus = %[3]d AND NOT %[4]s AND %[5]s))",
			alias, TokenStatusExhausted, TokenStatusEnabled, pastExpiry, outOfQuota),
	}
}

// tokenStatusCondition is the list filter for a state name, or "" for none.
func tokenStatusCondition(state string, now int64) string {
	return tokenStateConditions("t.", now)[state]
}

// tokenEffectiveState classifies one token the way tokenStateConditions does.
func tokenEffectiveState(status, expiredTime int64, unlimited bool, remainQuota, now int64) string {
	switch status {
	case TokenStatusDisabled:
		return TokenStateDisabled
	case TokenStatusExpired:
		return TokenStateExpired
	case TokenStatusExhausted:
		return TokenStateExhausted
	case TokenStatusEnabled:
		if expiredTime != -1 && expiredTime < now {
			return TokenStateExpired
		}
		if !unlimited && remainQuota <= 0 {
			return TokenStateExhausted
		}
		return TokenStateActive
	}
	return TokenStateUnknown
}

// tokenRowState classifies a queried token row (status, expired_time,
// unlimited_quota, remain_quota).
func tokenRowState(row map[string]interface{}, now int64) string {
	return tokenEffectiveState(toInt64(row["status"]), toInt64(row["expired_time"]),
		toBool(row["unlimited_quota"]), toInt64(row["remain_quota"]), now)
}

// toBool reads a boolean column: PostgreSQL returns bool, MySQL and SQLite
// return an integer.
func toBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		return s == "1" || s == "t" || s == "true"
	case nil:
		return false
	}
	return toInt64(v) != 0
}

// GetTokenStatistics counts tokens by effective state, leaving out the panel
// whitelist's accounts the way the default token list does.
func (s *TokenService) GetTokenStatistics() (*TokenStatistics, error) {
	conds := tokenStateConditions("", time.Now().Unix())
	where := "deleted_at IS NULL"
	cond, args := PanelWhitelistNotInClause("user_id")
	if cond != "" {
		where += " AND " + cond
	}
	query := s.db.RebindQuery(fmt.Sprintf(`
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS active,
			COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS disabled,
			COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS expired,
			COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS exhausted
		FROM tokens
		WHERE %s`,
		conds[TokenStateActive], conds[TokenStateDisabled], conds[TokenStateExpired], conds[TokenStateExhausted], where))

	row, err := s.db.QueryOne(query, args...)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &TokenStatistics{}, nil
	}
	return &TokenStatistics{
		Total:     toInt64(row["total"]),
		Active:    toInt64(row["active"]),
		Disabled:  toInt64(row["disabled"]),
		Expired:   toInt64(row["expired"]),
		Exhausted: toInt64(row["exhausted"]),
	}, nil
}
