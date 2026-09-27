package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// AcquisitionSourceService reports first-touch source dimensions stored by the
// BeatAPI gateway. Paid means at least one successfully settled top-up.
type AcquisitionSourceService struct {
	db *database.Manager
}

type AcquisitionBucket struct {
	Source      string  `json:"source"`
	Detail      string  `json:"detail,omitempty"`
	Users       int64   `json:"users"`
	PaidUsers   int64   `json:"paid_users"`
	UnpaidUsers int64   `json:"unpaid_users"`
	PaidRate    float64 `json:"paid_rate"`
}

type AcquisitionOverview struct {
	WindowDays  int                 `json:"window_days"`
	TotalUsers  int64               `json:"total_users"`
	PaidUsers   int64               `json:"paid_users"`
	UnpaidUsers int64               `json:"unpaid_users"`
	PaidRate    float64             `json:"paid_rate"`
	BySource    []AcquisitionBucket `json:"by_source"`
	ByDetail    []AcquisitionBucket `json:"by_detail"`
}

type acquisitionRow struct {
	source  string
	detail  string
	inviter int64
	paid    int64
	users   int64
}

func NewAcquisitionSourceService() *AcquisitionSourceService {
	return &AcquisitionSourceService{db: database.Get()}
}

func acquisitionLabel(source, detail string, inviter int64) (string, string) {
	source = strings.ToLower(strings.TrimSpace(source))
	detail = strings.ToLower(strings.TrimSpace(detail))
	if source == "" {
		if inviter > 0 {
			source = "referral"
		} else {
			source = "unknown"
		}
	}
	if detail == "" {
		if inviter > 0 {
			detail = fmt.Sprintf("inviter:%d", inviter)
		} else {
			detail = "unattributed"
		}
	}
	return source, detail
}

func acquisitionBucket(source, detail string, users, paid int64) AcquisitionBucket {
	unpaid := users - paid
	if unpaid < 0 {
		unpaid = 0
	}
	rate := float64(0)
	if users > 0 {
		rate = float64(paid) / float64(users)
	}
	return AcquisitionBucket{
		Source:      source,
		Detail:      detail,
		Users:       users,
		PaidUsers:   paid,
		UnpaidUsers: unpaid,
		PaidRate:    rate,
	}
}

func (s *AcquisitionSourceService) queryRows(days int) ([]acquisitionRow, error) {
	where := "u.deleted_at IS NULL"
	args := []any{}
	if days > 0 {
		where += " AND u.created_at >= ?"
		args = append(args, time.Now().AddDate(0, 0, -days).Unix())
	}

	query := fmt.Sprintf(`
		SELECT COALESCE(NULLIF(TRIM(u.acquisition_source), ''), '') AS acquisition_source,
		       COALESCE(NULLIF(TRIM(u.acquisition_detail), ''), '') AS acquisition_detail,
		       COALESCE(u.inviter_id, 0) AS inviter_id,
		       CASE WHEN COALESCE(p.paid_orders, 0) > 0 THEN 1 ELSE 0 END AS paid,
		       COUNT(*) AS users
		FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS paid_orders
			FROM top_ups
			WHERE status = 'success'
			GROUP BY user_id
		) p ON p.user_id = u.id
		WHERE %s
		GROUP BY u.acquisition_source, u.acquisition_detail, u.inviter_id, p.paid_orders
	`, where)
	rows, err := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(query), args...)
	if err == nil {
		return mapAcquisitionRows(rows), nil
	}

	// A tool deployed before the gateway migration can still provide useful
	// referral/paid data from the legacy schema; source labels fall back to the
	// inviter/unknown dimensions until the gateway adds the two columns.
	legacyQuery := fmt.Sprintf(`
		SELECT '' AS acquisition_source, '' AS acquisition_detail,
		       COALESCE(u.inviter_id, 0) AS inviter_id,
		       CASE WHEN COALESCE(p.paid_orders, 0) > 0 THEN 1 ELSE 0 END AS paid,
		       COUNT(*) AS users
		FROM users u
		LEFT JOIN (
			SELECT user_id, COUNT(*) AS paid_orders
			FROM top_ups
			WHERE status = 'success'
			GROUP BY user_id
		) p ON p.user_id = u.id
		WHERE %s
		GROUP BY u.inviter_id, p.paid_orders
	`, where)
	legacyRows, legacyErr := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(legacyQuery), args...)
	if legacyErr != nil {
		return nil, err
	}
	return mapAcquisitionRows(legacyRows), nil
}

func mapAcquisitionRows(rows []map[string]interface{}) []acquisitionRow {
	result := make([]acquisitionRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, acquisitionRow{
			source:  stringValue(row["acquisition_source"]),
			detail:  stringValue(row["acquisition_detail"]),
			inviter: toInt64(row["inviter_id"]),
			paid:    toInt64(row["paid"]),
			users:   toInt64(row["users"]),
		})
	}
	return result
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// GetOverview returns both source-level and source/detail-level aggregates.
func (s *AcquisitionSourceService) GetOverview(days int) (*AcquisitionOverview, error) {
	if days < 0 || days > 3650 {
		days = 0
	}
	rows, err := s.queryRows(days)
	if err != nil {
		return nil, err
	}

	type totals struct{ users, paid int64 }
	sourceTotals := map[string]totals{}
	detailTotals := map[string]struct {
		source string
		users  int64
		paid   int64
	}{}
	for _, row := range rows {
		source, detail := acquisitionLabel(row.source, row.detail, row.inviter)
		sourceValue := sourceTotals[source]
		sourceValue.users += row.users
		sourceValue.paid += row.paid * row.users
		sourceTotals[source] = sourceValue
		detailKey := source + "\x00" + detail
		detailValue := detailTotals[detailKey]
		detailValue.source = source
		detailValue.users += row.users
		detailValue.paid += row.paid * row.users
		detailTotals[detailKey] = detailValue
	}

	bySource := make([]AcquisitionBucket, 0, len(sourceTotals))
	var totalUsers, totalPaid int64
	for source, value := range sourceTotals {
		bySource = append(bySource, acquisitionBucket(source, "", value.users, value.paid))
		totalUsers += value.users
		totalPaid += value.paid
	}
	byDetail := make([]AcquisitionBucket, 0, len(detailTotals))
	for key, value := range detailTotals {
		detail := strings.SplitN(key, "\x00", 2)[1]
		byDetail = append(byDetail, acquisitionBucket(value.source, detail, value.users, value.paid))
	}
	sort.Slice(bySource, func(i, j int) bool {
		if bySource[i].Users != bySource[j].Users {
			return bySource[i].Users > bySource[j].Users
		}
		return bySource[i].Source < bySource[j].Source
	})
	sort.Slice(byDetail, func(i, j int) bool {
		if byDetail[i].Users != byDetail[j].Users {
			return byDetail[i].Users > byDetail[j].Users
		}
		if byDetail[i].Source != byDetail[j].Source {
			return byDetail[i].Source < byDetail[j].Source
		}
		return byDetail[i].Detail < byDetail[j].Detail
	})

	rate := float64(0)
	if totalUsers > 0 {
		rate = float64(totalPaid) / float64(totalUsers)
	}
	return &AcquisitionOverview{
		WindowDays:  days,
		TotalUsers:  totalUsers,
		PaidUsers:   totalPaid,
		UnpaidUsers: totalUsers - totalPaid,
		PaidRate:    rate,
		BySource:    bySource,
		ByDetail:    byDetail,
	}, nil
}
