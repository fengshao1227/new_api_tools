package service

import (
	"fmt"
	"time"
)

// Gift credit and the gateway's risk engine.
//
// Granted is what the window's signups were handed (users.granted_quota);
// liability is the free balance still sitting on enabled, never-paid regular
// accounts — credit that becomes supplier cost the moment it is spent. How
// much gift the unpaid accounts burned in the window comes from the margin
// report (MarginSummary.FreeUserBilledUSD / GiftAndFreeCostUSD), which the
// finance section already carries, so it is not recomputed here.
//
// Risk figures read risk_events as the gateway writes them: an open case
// holds its held_quota either "under review" (held_for hold_grant, or empty on
// rows that predate the column) or as "one more account of the same person"
// (deny_grant). Decisions are counted by when they were made.

// BusinessGiftFigures is gift credit handed out and still outstanding.
type BusinessGiftFigures struct {
	Available      bool    `json:"available"`
	Signups        int64   `json:"signups"`
	GrantedUsers   int64   `json:"granted_users"`
	GrantedUSD     float64 `json:"granted_usd"`
	LiabilityUsers int64   `json:"liability_users"`
	LiabilityUSD   float64 `json:"liability_usd"`
}

// BusinessRiskHold is one bucket of open cases.
type BusinessRiskHold struct {
	Cases   int64   `json:"cases"`
	Users   int64   `json:"users"`
	HeldUSD float64 `json:"held_usd"`
}

// BusinessRiskFigures is the review queue now and the decisions in the window.
type BusinessRiskFigures struct {
	Available bool             `json:"available"`
	Review    BusinessRiskHold `json:"review"`
	Deny      BusinessRiskHold `json:"deny"`
	Flagged   int64            `json:"flagged"`
	Confirmed int64            `json:"confirmed"`
	Released  int64            `json:"released"`
	Withheld  int64            `json:"withheld"`
	Dismissed int64            `json:"dismissed"`
}

// BusinessGiftsRisk is the gift-and-risk section of the business view.
type BusinessGiftsRisk struct {
	Window DashboardWindow     `json:"window"`
	Gifts  BusinessGiftFigures `json:"gifts"`
	Risk   BusinessRiskFigures `json:"risk"`
}

// GetGiftsRisk returns gift credit issued and outstanding plus the risk queue.
func (s *BusinessDashboardService) GetGiftsRisk(window string, noCache bool) (BusinessGiftsRisk, error) {
	w := ResolveDashboardWindow(window, time.Now())
	return businessCached("dashboard:beat:gifts-risk:"+w.Key, 3*time.Minute, noCache, func() (BusinessGiftsRisk, error) {
		gifts, err := s.loadGifts(w)
		if err != nil {
			return BusinessGiftsRisk{}, err
		}
		risk, err := s.loadRisk(w)
		if err != nil {
			return BusinessGiftsRisk{}, err
		}
		return BusinessGiftsRisk{Window: w, Gifts: gifts, Risk: risk}, nil
	})
}

func (s *BusinessDashboardService) loadGifts(w DashboardWindow) (BusinessGiftFigures, error) {
	gifts := BusinessGiftFigures{Available: true}
	wl, wlArgs := whitelistAnd("id")
	granted, err := s.db.QueryOneWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT COUNT(*) AS signups,
			COALESCE(SUM(CASE WHEN granted_quota > 0 THEN 1 ELSE 0 END), 0) AS granted_users,
			COALESCE(SUM(granted_quota), 0) AS granted
		FROM users WHERE deleted_at IS NULL AND created_at >= ? AND created_at <= ?`+wl),
		append([]interface{}{w.Start, w.End}, wlArgs...)...)
	if isMissingSchemaErr(err) {
		return BusinessGiftFigures{}, nil
	}
	if err != nil {
		return gifts, fmt.Errorf("gift grant query failed: %w", err)
	}
	gifts.Signups = toInt64(granted["signups"])
	gifts.GrantedUsers = toInt64(granted["granted_users"])
	gifts.GrantedUSD = marginMoney(toFloat64(granted["granted"]))

	wlU, wlUArgs := whitelistAnd("u.id")
	liability, err := s.db.QueryOneWithTimeout(businessQueryTimeout, s.db.RebindQuery(fmt.Sprintf(`
		SELECT COUNT(*) AS users, COALESCE(SUM(u.quota), 0) AS quota
		FROM users u
		WHERE u.deleted_at IS NULL AND u.status = 1 AND u.role < 10 AND u.quota > 0
			AND COALESCE(u.topup_quota, 0) = 0
			AND NOT EXISTS (SELECT 1 FROM top_ups t WHERE t.user_id = u.id AND %s)%s`,
		successStatusCondition("t.status"), wlU)), wlUArgs...)
	if isMissingSchemaErr(err) {
		// No topup_quota column: the gateway predates paid-credit tracking,
		// so "never paid" cannot be told apart from "credited by an admin".
		return gifts, nil
	}
	if err != nil {
		return gifts, fmt.Errorf("gift liability query failed: %w", err)
	}
	gifts.LiabilityUsers = toInt64(liability["users"])
	gifts.LiabilityUSD = marginMoney(toFloat64(liability["quota"]))
	return gifts, nil
}

func (s *BusinessDashboardService) loadRisk(w DashboardWindow) (BusinessRiskFigures, error) {
	risk := BusinessRiskFigures{Available: true}
	bucket := "CASE WHEN held_for = 'deny_grant' THEN 'deny' ELSE 'review' END"
	open, err := s.db.QueryWithTimeout(businessQueryTimeout, fmt.Sprintf(`
		SELECT %s AS bucket, COUNT(*) AS cases, COUNT(DISTINCT user_id) AS users,
			COALESCE(SUM(held_quota), 0) AS held
		FROM risk_events WHERE status = 'open'
		GROUP BY %s`, bucket, bucket))
	if isMissingSchemaErr(err) {
		return BusinessRiskFigures{}, nil
	}
	if err != nil {
		return risk, fmt.Errorf("risk queue query failed: %w", err)
	}
	for _, r := range open {
		hold := BusinessRiskHold{Cases: toInt64(r["cases"]), Users: toInt64(r["users"]), HeldUSD: marginMoney(toFloat64(r["held"]))}
		if toString(r["bucket"]) == "deny" {
			risk.Deny = hold
		} else {
			risk.Review = hold
		}
	}

	decided, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT status, COUNT(*) AS n FROM risk_events
		WHERE status IN ('confirmed', 'released', 'withheld', 'dismissed') AND resolved_at >= ? AND resolved_at <= ?
		GROUP BY status`), w.Start, w.End)
	if err != nil {
		return risk, fmt.Errorf("risk decision query failed: %w", err)
	}
	for _, r := range decided {
		n := toInt64(r["n"])
		switch toString(r["status"]) {
		case "confirmed":
			risk.Confirmed = n
		case "released":
			risk.Released = n
		case "withheld":
			risk.Withheld = n
		case "dismissed":
			risk.Dismissed = n
		}
	}

	flagged, err := s.db.QueryOneWithTimeout(businessQueryTimeout, s.db.RebindQuery(
		"SELECT COUNT(*) AS n FROM risk_events WHERE created_at >= ? AND created_at <= ? AND status <> 'none'"), w.Start, w.End)
	if err != nil {
		return risk, fmt.Errorf("risk flagged query failed: %w", err)
	}
	risk.Flagged = toInt64(flagged["n"])
	return risk, nil
}
