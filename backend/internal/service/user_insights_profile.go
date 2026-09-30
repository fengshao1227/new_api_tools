package service

import (
	"context"
	"fmt"
	"strings"
)

func (s *UserInsightsService) loadInsightsProfile(ctx context.Context, userID int64, out *UserInsightsReport) error {
	columns := []string{"id", "username", "display_name", "email", "status", "role", s.db.QuoteIdentifier("group") + " AS user_group", "remark", "quota", "used_quota"}
	optional := []string{"created_at", "last_login_at", "signup_country", "signup_language", "acquisition_source", "acquisition_detail", "inviter_id", "topup_quota", "granted_quota"}
	for _, login := range userLoginColumns {
		optional = append(optional, login.column)
	}
	available := map[string]bool{}
	for _, column := range optional {
		ok, err := insightsProbe(ctx, s.db, "SELECT "+column+" FROM users WHERE 1 = 0")
		if err != nil {
			return err
		}
		available[column] = ok
		if ok {
			columns = append(columns, column)
		} else {
			columns = append(columns, "NULL AS "+column)
		}
	}
	rows, err := insightsQuery(ctx, s.db, "SELECT "+strings.Join(columns, ",")+" FROM users WHERE id = ? AND deleted_at IS NULL", userID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return ErrInsightsUserNotFound
	}
	r := rows[0]
	out.Profile = UserInsightsProfile{ID: userID, Username: toString(r["username"]), DisplayName: toString(r["display_name"]), Email: toString(r["email"]),
		Status: toInt64(r["status"]), Role: toInt64(r["role"]), Group: toString(r["user_group"]), Remark: toString(r["remark"]),
		CreatedAt: insightsTime(r["created_at"]), LastLoginAt: insightsTime(r["last_login_at"]), SignupCountry: insightsString(r["signup_country"]),
		SignupLanguage: insightsString(r["signup_language"]), AcquisitionSource: insightsString(r["acquisition_source"]),
		AcquisitionDetail: insightsString(r["acquisition_detail"]), InviterID: insightsInt(r["inviter_id"]), LoginSources: []string{}}
	out.Balances = UserInsightsBalances{Quota: toInt64(r["quota"]), UsedQuota: toInt64(r["used_quota"]), TopupQuota: insightsInt(r["topup_quota"]), GrantedQuota: insightsInt(r["granted_quota"])}
	out.Balances.BalanceUSD = marginMoney(float64(out.Balances.Quota))
	out.Balances.LifetimeUsedUSD = marginMoney(float64(out.Balances.UsedQuota))
	out.Availability.Profile = true
	out.Availability.Acquisition = available["acquisition_source"] && available["acquisition_detail"]
	if !out.Availability.Acquisition {
		out.Availability.Warnings = append(out.Availability.Warnings, "acquisition_schema_missing")
	}
	if !available["created_at"] || !available["last_login_at"] {
		out.Availability.Warnings = append(out.Availability.Warnings, "account_times_incomplete")
	}
	seen := map[string]bool{}
	for _, login := range userLoginColumns {
		if insightsString(r[login.column]) != nil {
			out.Profile.LoginSources = append(out.Profile.LoginSources, login.source)
			seen[login.source] = true
		}
	}
	bindings, err := insightsQuery(ctx, s.db, `SELECT LOWER(p.slug) AS slug FROM user_oauth_bindings b
		JOIN custom_oauth_providers p ON p.id = b.provider_id WHERE b.user_id = ? ORDER BY b.id`, userID)
	if err != nil && !isMissingSchemaErr(err) {
		return err
	}
	if isMissingSchemaErr(err) {
		out.Availability.Warnings = append(out.Availability.Warnings, "oauth_bindings_unavailable")
	}
	for _, binding := range bindings {
		slug := toString(binding["slug"])
		if slug != "" && !seen[slug] {
			out.Profile.LoginSources = append(out.Profile.LoginSources, slug)
			seen[slug] = true
		}
	}
	// No OAuth binding is not proof of a usable password (passkeys also exist).
	return s.loadInsightsRisk(ctx, userID, out)
}

func (s *UserInsightsService) loadInsightsRisk(ctx context.Context, userID int64, out *UserInsightsReport) error {
	rows, err := insightsQuery(ctx, s.db, `SELECT COUNT(*) AS n, COALESCE(SUM(held_quota),0) AS held
		FROM risk_events WHERE user_id = ? AND status = 'open'`, userID)
	if isMissingSchemaErr(err) {
		out.Availability.Warnings = append(out.Availability.Warnings, "risk_unavailable")
		return nil
	}
	if err != nil {
		return err
	}
	out.Availability.Risk = true
	brief := &userRiskBrief{Status: userRiskNone}
	if toInt64(rows[0]["n"]) > 0 {
		brief.Status, brief.OpenCases = userRiskOpen, toInt64(rows[0]["n"])
		brief.HeldUSD = marginMoney(toFloat64(rows[0]["held"]))
	} else {
		resolved, err := insightsQuery(ctx, s.db, `SELECT status, resolved_at FROM risk_events
			WHERE user_id = ? AND status IN ('confirmed','released','withheld','dismissed')
			ORDER BY resolved_at DESC, id DESC LIMIT 1`, userID)
		if err != nil {
			return fmt.Errorf("risk detail: %w", err)
		}
		if len(resolved) > 0 {
			brief.Status = toString(resolved[0]["status"])
			brief.ResolvedAt = toInt64(resolved[0]["resolved_at"])
		}
	}
	out.Profile.Risk = brief
	return nil
}

// Payments are successfully settled cash orders; neither grants nor redemption
// codes are revenue. This profile's date window is independent of model filters.
func (s *UserInsightsService) loadInsightsPayments(ctx context.Context, p UserInsightsParams, out *UserInsightsReport) error {
	ok, err := insightsProbe(ctx, s.db, "SELECT id,user_id,money,amount,payment_method,status,create_time,complete_time,trade_no FROM top_ups WHERE 1=0")
	if err != nil {
		return err
	}
	if !ok {
		out.Availability.Warnings = append(out.Availability.Warnings, "payments_unavailable")
		return nil
	}
	provider, err := insightsProbe(ctx, s.db, "SELECT payment_provider FROM top_ups WHERE 1=0")
	if err != nil {
		return err
	}
	providerCol := "''"
	if provider {
		providerCol = "t.payment_provider"
	}
	c := topUpSQLColsWithProvider("t", providerCol)
	join := ""
	credited := fmt.Sprintf("CASE WHEN %s IS NULL THEN NULL WHEN %s THEN 0 WHEN SUBSTR(t.trade_no,1,5) = 'auto_' THEN NULL ELSE %s END", c.currency(), c.subscription(), c.creditedUSD())
	attempts, err := insightsProbe(ctx, s.db, "SELECT trade_no,amount_quota FROM auto_top_up_attempts WHERE 1=0")
	if err != nil {
		return err
	}
	if attempts {
		// Grouping is defensive: this read can never multiply orders even on a
		// legacy table missing the unique index expected by the gateway.
		join = " LEFT JOIN (SELECT trade_no, MAX(amount_quota) AS amount_quota FROM auto_top_up_attempts GROUP BY trade_no) a ON a.trade_no = t.trade_no"
		credited = fmt.Sprintf("CASE WHEN a.amount_quota IS NOT NULL THEN a.amount_quota / 500000.0 ELSE %s END", credited)
	}
	query := fmt.Sprintf(`SELECT COALESCE(%s, 'unknown') AS currency, COUNT(*) AS n,
		COALESCE(SUM(t.money),0) AS amount, COALESCE(SUM(%s),0) AS paid_usd,
		SUM(%s) AS credited_usd, SUM(CASE WHEN %s IS NULL THEN 1 ELSE 0 END) AS unknown_credit,
		MIN(%s) AS first_paid, MAX(%s) AS last_paid
		FROM top_ups t%s WHERE t.user_id = ? AND %s`, c.currency(), c.paidUSD(), credited, credited, c.paidAt(), c.paidAt(), join, successStatusCondition("t.status"))
	payments := &UserInsightsPayments{CNYPerUSD: cnyPerUSD()}
	for i := range 2 {
		where, args := "", []any{p.UserID}
		if i == 0 {
			where = fmt.Sprintf(" AND %s >= ? AND %s < ?", c.paidAt(), c.paidAt())
			args = append(args, p.Window.StartTime, p.Window.EndTime)
		}
		rows, err := insightsQuery(ctx, s.db, query+where+" GROUP BY currency ORDER BY currency", args...)
		if err != nil {
			return err
		}
		total := UserInsightsPaymentTotals{ByCurrency: []UserInsightsCurrency{}}
		credit, unknownCredit := float64(0), int64(0)
		for _, row := range rows {
			count, currency := toInt64(row["n"]), toString(row["currency"])
			total.PaidCount += count
			total.PaidUSD += toFloat64(row["paid_usd"])
			total.ByCurrency = append(total.ByCurrency, UserInsightsCurrency{Currency: currency, Count: count, Amount: toFloat64(row["amount"])})
			if currency == "unknown" {
				total.UnknownCurrencyCount += count
			}
			credit += toFloat64(row["credited_usd"])
			unknownCredit += toInt64(row["unknown_credit"])
			if i == 1 {
				first, last := insightsTime(row["first_paid"]), insightsTime(row["last_paid"])
				if first != nil && (payments.FirstPaidAt == nil || *first < *payments.FirstPaidAt) {
					payments.FirstPaidAt = first
				}
				if last != nil && (payments.LastPaidAt == nil || *last > *payments.LastPaidAt) {
					payments.LastPaidAt = last
				}
			}
		}
		if unknownCredit == 0 {
			total.CreditedUSD = &credit
		} else if i == 1 {
			out.Availability.Warnings = append(out.Availability.Warnings, "credited_amount_incomplete")
		}
		if i == 0 {
			payments.Window = total
		} else {
			payments.Lifetime = total
		}
	}
	out.Payments, out.Availability.Payments = payments, true
	paid, via := payments.Lifetime.PaidCount > 0, ""
	if paid {
		via = "top_up"
	} else if out.Balances.TopupQuota != nil && *out.Balances.TopupQuota > 0 {
		paid, via = true, "credited"
	}
	out.Profile.Paid, out.Profile.PaidVia = &paid, &via
	return nil
}
