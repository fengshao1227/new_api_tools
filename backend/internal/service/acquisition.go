package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// AcquisitionSourceService reports registration-cohort source dimensions and
// whether each cohort has at least one successfully settled top-up.
type AcquisitionSourceService struct{ db *database.Manager }

type AcquisitionBucket struct {
	Source      string  `json:"source"`
	Detail      string  `json:"detail,omitempty"`
	Users       int64   `json:"users"`
	PaidUsers   int64   `json:"paid_users"`
	UnpaidUsers int64   `json:"unpaid_users"`
	PaidRate    float64 `json:"paid_rate"`
	Kind        string  `json:"kind,omitempty"`
}

type AcquisitionOverview struct {
	WindowDays              int                 `json:"window_days"`
	TotalUsers              int64               `json:"total_users"`
	PaidUsers               int64               `json:"paid_users"`
	UnpaidUsers             int64               `json:"unpaid_users"`
	PaidRate                float64             `json:"paid_rate"`
	SourceUsers             int64               `json:"source_users"`
	UnattributedUsers       int64               `json:"unattributed_users"`
	HistoricalReferralUsers int64               `json:"historical_referral_users"`
	AttributionStatus       string              `json:"attribution_status"`
	BySource                []AcquisitionBucket `json:"by_source"`
	ByDetail                []AcquisitionBucket `json:"by_detail"`
}

type acquisitionRow struct {
	source, detail       string
	inviter, paid, users int64
}

func NewAcquisitionSourceService() *AcquisitionSourceService {
	return &AcquisitionSourceService{db: database.Get()}
}

func acquisitionBucket(source, detail, kind string, users, paid int64) AcquisitionBucket {
	if paid < 0 {
		paid = 0
	}
	if paid > users {
		paid = users
	}
	rate := float64(0)
	if users > 0 {
		rate = float64(paid) / float64(users)
	}
	return AcquisitionBucket{
		Source: source, Detail: detail, Kind: kind, Users: users,
		PaidUsers: paid, UnpaidUsers: users - paid, PaidRate: rate,
	}
}

func acquisitionLabel(source, detail string, inviter int64, columnsAvailable bool) (string, string, string) {
	source = strings.ToLower(strings.TrimSpace(source))
	detail = strings.ToLower(strings.TrimSpace(detail))
	if source != "" {
		if detail == "" {
			detail = "未细分"
		}
		return source, detail, "captured"
	}
	if inviter > 0 {
		if columnsAvailable {
			return "历史邀请", "历史邀请码归因", "legacy_referral"
		}
		return "历史邀请", "历史数据（邀请码）", "legacy_referral"
	}
	if columnsAvailable {
		return "未采集", "注册时未采集", "unattributed"
	}
	return "未采集", "来源字段未上线", "schema_missing"
}

func (s *AcquisitionSourceService) queryRows(days int) ([]acquisitionRow, bool, error) {
	where := "u.deleted_at IS NULL"
	args := []any{}
	if days > 0 {
		where += " AND u.created_at >= ?"
		args = append(args, time.Now().AddDate(0, 0, -days).Unix())
	}
	paidJoin := `LEFT JOIN (SELECT user_id FROM top_ups WHERE status = 'success' GROUP BY user_id) p ON p.user_id = u.id`
	query := fmt.Sprintf(`
		SELECT COALESCE(u.acquisition_source, '') AS acquisition_source,
		       COALESCE(u.acquisition_detail, '') AS acquisition_detail,
		       COALESCE(u.inviter_id, 0) AS inviter_id,
		       CASE WHEN p.user_id IS NULL THEN 0 ELSE 1 END AS paid,
		       COUNT(*) AS users
		FROM users u %s
		WHERE %s
		GROUP BY u.acquisition_source, u.acquisition_detail, u.inviter_id, p.user_id`, paidJoin, where)
	rows, err := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(query), args...)
	if err == nil {
		return mapAcquisitionRows(rows), true, nil
	}

	// Older gateways do not have the acquisition columns. Keep the historical
	// referral/paid facts, but mark the response so the UI never calls it a
	// successful automatic attribution capture.
	legacyQuery := fmt.Sprintf(`
		SELECT '' AS acquisition_source, '' AS acquisition_detail,
		       COALESCE(u.inviter_id, 0) AS inviter_id,
		       CASE WHEN p.user_id IS NULL THEN 0 ELSE 1 END AS paid,
		       COUNT(*) AS users
		FROM users u %s
		WHERE %s
		GROUP BY u.inviter_id, p.user_id`, paidJoin, where)
	legacyRows, legacyErr := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(legacyQuery), args...)
	if legacyErr != nil {
		return nil, false, fmt.Errorf("query acquisition cohorts: %w; legacy fallback: %v", err, legacyErr)
	}
	return mapAcquisitionRows(legacyRows), false, nil
}

func mapAcquisitionRows(rows []map[string]interface{}) []acquisitionRow {
	result := make([]acquisitionRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, acquisitionRow{
			source:  toString(row["acquisition_source"]),
			detail:  toString(row["acquisition_detail"]),
			inviter: toInt64(row["inviter_id"]),
			paid:    toInt64(row["paid"]),
			users:   toInt64(row["users"]),
		})
	}
	return result
}

func (s *AcquisitionSourceService) GetOverview(days int) (*AcquisitionOverview, error) {
	if days < 0 || days > 3650 {
		days = 0
	}
	rows, columnsAvailable, err := s.queryRows(days)
	if err != nil {
		return nil, err
	}

	type totals struct {
		users, paid int64
		kind        string
	}
	sourceTotals := map[string]totals{}
	detailTotals := map[string]totals{}
	result := &AcquisitionOverview{WindowDays: days, AttributionStatus: "ready"}
	if !columnsAvailable {
		result.AttributionStatus = "schema_missing"
	}
	for _, row := range rows {
		source, detail, kind := acquisitionLabel(row.source, row.detail, row.inviter, columnsAvailable)
		key := source + "\x00" + detail
		st := sourceTotals[source]
		st.users += row.users
		st.paid += row.paid * row.users
		st.kind = kind
		sourceTotals[source] = st
		dt := detailTotals[key]
		dt.users += row.users
		dt.paid += row.paid * row.users
		dt.kind = kind
		detailTotals[key] = dt
		result.TotalUsers += row.users
		result.PaidUsers += row.paid * row.users
		if kind == "captured" {
			result.SourceUsers += row.users
		}
		if kind == "unattributed" || kind == "schema_missing" {
			result.UnattributedUsers += row.users
		}
		if kind == "legacy_referral" {
			result.HistoricalReferralUsers += row.users
		}
	}
	result.UnpaidUsers = result.TotalUsers - result.PaidUsers
	if result.TotalUsers > 0 {
		result.PaidRate = float64(result.PaidUsers) / float64(result.TotalUsers)
	}
	for source, value := range sourceTotals {
		result.BySource = append(result.BySource, acquisitionBucket(source, "", value.kind, value.users, value.paid))
	}
	for key, value := range detailTotals {
		parts := strings.SplitN(key, "\x00", 2)
		result.ByDetail = append(result.ByDetail, acquisitionBucket(parts[0], parts[1], value.kind, value.users, value.paid))
	}
	sort.Slice(result.BySource, func(i, j int) bool { return result.BySource[i].Users > result.BySource[j].Users })
	sort.Slice(result.ByDetail, func(i, j int) bool { return result.ByDetail[i].Users > result.ByDetail[j].Users })
	return result, nil
}
