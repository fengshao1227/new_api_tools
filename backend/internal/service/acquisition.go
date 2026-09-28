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
//
// Accounts on the panel whitelist (internal accounts, and admins by default)
// are left out, the same rule every other operating page applies.
type AcquisitionSourceService struct{ db *database.Manager }

type AcquisitionBucket struct {
	Source      string  `json:"source"`
	Detail      string  `json:"detail,omitempty"`
	Users       int64   `json:"users"`
	PaidUsers   int64   `json:"paid_users"`
	UnpaidUsers int64   `json:"unpaid_users"`
	PaidRate    float64 `json:"paid_rate"`
	Kind        string  `json:"kind,omitempty"`
	// GrantedUSD is the signup credit the bucket's accounts were handed
	// (users.granted_quota), GrantedMaxUSD the largest single grant — the
	// tier's amount unless it changed or a risk hold cut it. Only grant
	// region buckets carry them.
	GrantedUSD    float64 `json:"granted_usd,omitempty"`
	GrantedMaxUSD float64 `json:"granted_max_usd,omitempty"`
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
	// RegionStatus is "schema_missing" on gateways without the signup_country
	// / grant_region / granted_quota columns; the two splits are then empty.
	RegionStatus  string              `json:"region_status"`
	ByCountry     []AcquisitionBucket `json:"by_country"`
	ByGrantRegion []AcquisitionBucket `json:"by_grant_region"`
}

// acquisitionUnrecorded labels accounts whose signup country or grant region
// was never written: registered before the gateway recorded it, or created by
// an operator.
const acquisitionUnrecorded = "未记录"

type acquisitionRow struct {
	source, detail       string
	inviter, paid, users int64
}

type acquisitionRegionRow struct {
	country, region     string
	paid, users         int64
	granted, grantedMax int64
}

type acquisitionTotals struct {
	users, paid         int64
	granted, grantedMax int64
	kind                string
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

// acquisitionCohort is the FROM/WHERE shared by every acquisition query: the
// window's non-deleted, non-whitelisted accounts, joined to whether each has a
// settled top-up.
func acquisitionCohort(days int) (string, []any) {
	where := "u.deleted_at IS NULL"
	args := []any{}
	if days > 0 {
		where += " AND u.created_at >= ?"
		args = append(args, time.Now().AddDate(0, 0, -days).Unix())
	}
	wl, wlArgs := whitelistAnd("u.id")
	where += wl
	args = append(args, wlArgs...)
	from := fmt.Sprintf(`FROM users u
		LEFT JOIN (SELECT user_id FROM top_ups WHERE %s GROUP BY user_id) p ON p.user_id = u.id
		WHERE %s`, successStatusCondition(), where)
	return from, args
}

func (s *AcquisitionSourceService) queryRows(days int) ([]acquisitionRow, bool, error) {
	from, args := acquisitionCohort(days)
	query := fmt.Sprintf(`
		SELECT COALESCE(u.acquisition_source, '') AS acquisition_source,
		       COALESCE(u.acquisition_detail, '') AS acquisition_detail,
		       COALESCE(u.inviter_id, 0) AS inviter_id,
		       CASE WHEN p.user_id IS NULL THEN 0 ELSE 1 END AS paid,
		       COUNT(*) AS users
		%s
		GROUP BY u.acquisition_source, u.acquisition_detail, u.inviter_id, p.user_id`, from)
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
		%s
		GROUP BY u.inviter_id, p.user_id`, from)
	legacyRows, legacyErr := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(legacyQuery), args...)
	if legacyErr != nil {
		return nil, false, fmt.Errorf("query acquisition cohorts: %w; legacy fallback: %v", err, legacyErr)
	}
	return mapAcquisitionRows(legacyRows), false, nil
}

// queryRegions splits the cohort by signup country and by the grant ladder
// rung that priced its signup credit. It reports false when the gateway has
// no such columns.
func (s *AcquisitionSourceService) queryRegions(days int) ([]acquisitionRegionRow, bool, error) {
	from, args := acquisitionCohort(days)
	query := fmt.Sprintf(`
		SELECT COALESCE(u.signup_country, '') AS country,
		       COALESCE(u.grant_region, '') AS grant_region,
		       CASE WHEN p.user_id IS NULL THEN 0 ELSE 1 END AS paid,
		       COUNT(*) AS users,
		       COALESCE(SUM(u.granted_quota), 0) AS granted,
		       COALESCE(MAX(u.granted_quota), 0) AS granted_max
		%s
		GROUP BY u.signup_country, u.grant_region, p.user_id`, from)
	rows, err := s.db.QueryWithTimeout(30*time.Second, s.db.RebindQuery(query), args...)
	if isMissingSchemaErr(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query acquisition regions: %w", err)
	}
	result := make([]acquisitionRegionRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, acquisitionRegionRow{
			country:    toString(row["country"]),
			region:     toString(row["grant_region"]),
			paid:       toInt64(row["paid"]),
			users:      toInt64(row["users"]),
			granted:    toInt64(row["granted"]),
			grantedMax: toInt64(row["granted_max"]),
		})
	}
	return result, true, nil
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
	regions, regionsAvailable, err := s.queryRegions(days)
	if err != nil {
		return nil, err
	}

	result := &AcquisitionOverview{WindowDays: days, AttributionStatus: "ready", RegionStatus: "ready"}
	if !columnsAvailable {
		result.AttributionStatus = "schema_missing"
	}
	if !regionsAvailable {
		result.RegionStatus = "schema_missing"
	}
	foldAcquisitionSources(result, rows, columnsAvailable)
	result.ByCountry, result.ByGrantRegion = foldAcquisitionRegions(regions)
	return result, nil
}

// foldAcquisitionSources fills the totals and the source / detail splits.
func foldAcquisitionSources(result *AcquisitionOverview, rows []acquisitionRow, columnsAvailable bool) {
	sourceTotals := map[string]acquisitionTotals{}
	detailTotals := map[string]acquisitionTotals{}
	for _, row := range rows {
		source, detail, kind := acquisitionLabel(row.source, row.detail, row.inviter, columnsAvailable)
		paid := row.paid * row.users
		addAcquisitionTotals(sourceTotals, source, kind, row.users, paid, 0, 0)
		addAcquisitionTotals(detailTotals, source+"\x00"+detail, kind, row.users, paid, 0, 0)
		result.TotalUsers += row.users
		result.PaidUsers += paid
		switch kind {
		case "captured":
			result.SourceUsers += row.users
		case "unattributed", "schema_missing":
			result.UnattributedUsers += row.users
		case "legacy_referral":
			result.HistoricalReferralUsers += row.users
		}
	}
	result.UnpaidUsers = result.TotalUsers - result.PaidUsers
	if result.TotalUsers > 0 {
		result.PaidRate = float64(result.PaidUsers) / float64(result.TotalUsers)
	}
	result.BySource = []AcquisitionBucket{}
	for source, value := range sourceTotals {
		result.BySource = append(result.BySource, acquisitionBucket(source, "", value.kind, value.users, value.paid))
	}
	result.ByDetail = []AcquisitionBucket{}
	for key, value := range detailTotals {
		parts := strings.SplitN(key, "\x00", 2)
		result.ByDetail = append(result.ByDetail, acquisitionBucket(parts[0], parts[1], value.kind, value.users, value.paid))
	}
	sortAcquisitionBuckets(result.BySource)
	sortAcquisitionBuckets(result.ByDetail)
}

// foldAcquisitionRegions folds the region rows into the country and grant
// region splits. Countries are ISO codes (upper-cased); an empty value on
// either axis is "未记录".
func foldAcquisitionRegions(rows []acquisitionRegionRow) ([]AcquisitionBucket, []AcquisitionBucket) {
	countries := map[string]acquisitionTotals{}
	regions := map[string]acquisitionTotals{}
	for _, row := range rows {
		country := strings.ToUpper(strings.TrimSpace(row.country))
		if country == "" {
			country = acquisitionUnrecorded
		}
		region := strings.TrimSpace(row.region)
		if region == "" {
			region = acquisitionUnrecorded
		}
		paid := row.paid * row.users
		addAcquisitionTotals(countries, country, "", row.users, paid, 0, 0)
		addAcquisitionTotals(regions, region, "", row.users, paid, row.granted, row.grantedMax)
	}
	byCountry := make([]AcquisitionBucket, 0, len(countries))
	for key, value := range countries {
		byCountry = append(byCountry, acquisitionBucket(key, "", "", value.users, value.paid))
	}
	byRegion := make([]AcquisitionBucket, 0, len(regions))
	for key, value := range regions {
		bucket := acquisitionBucket(key, "", "", value.users, value.paid)
		bucket.GrantedUSD = marginMoney(float64(value.granted))
		bucket.GrantedMaxUSD = marginMoney(float64(value.grantedMax))
		byRegion = append(byRegion, bucket)
	}
	sortAcquisitionBuckets(byCountry)
	sortAcquisitionBuckets(byRegion)
	return byCountry, byRegion
}

func addAcquisitionTotals(set map[string]acquisitionTotals, key, kind string, users, paid, granted, grantedMax int64) {
	t := set[key]
	t.users += users
	t.paid += paid
	t.granted += granted
	if grantedMax > t.grantedMax {
		t.grantedMax = grantedMax
	}
	t.kind = kind
	set[key] = t
}

// sortAcquisitionBuckets orders by users, then by name so equal buckets keep
// a stable order between refreshes.
func sortAcquisitionBuckets(buckets []AcquisitionBucket) {
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Users != buckets[j].Users {
			return buckets[i].Users > buckets[j].Users
		}
		if buckets[i].Source != buckets[j].Source {
			return buckets[i].Source < buckets[j].Source
		}
		return buckets[i].Detail < buckets[j].Detail
	})
}
