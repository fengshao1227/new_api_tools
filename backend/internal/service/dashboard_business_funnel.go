package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Conversion: of the accounts registered in the window, how many used the API
// and how many paid. The cohort is fixed by signup time; "used" and "paid" are
// whatever those accounts did up to now, so a 7-day window answers "of last
// week's signups, who converted", not "what happened last week".

// BusinessConversionBucket is one acquisition source or signup country.
type BusinessConversionBucket struct {
	Key            string  `json:"key"`
	Signups        int64   `json:"signups"`
	Activated      int64   `json:"activated"`
	Paid           int64   `json:"paid"`
	ActivationRate float64 `json:"activation_rate"`
	PaidRate       float64 `json:"paid_rate"`
}

// BusinessConversion is the signup → used → paid funnel for a window cohort.
type BusinessConversion struct {
	Window         DashboardWindow `json:"window"`
	Signups        int64           `json:"signups"`
	Activated      int64           `json:"activated"`
	Paid           int64           `json:"paid"`
	ActivationRate float64         `json:"activation_rate"`
	PaidRate       float64         `json:"paid_rate"`
	// PaidOfActivatedRate is the share of activated accounts that also paid:
	// how many people who tried the API went on to pay. An account that paid
	// without ever calling the API counts in Paid but not here.
	PaidOfActivatedRate float64                    `json:"paid_of_activated_rate"`
	BySource            []BusinessConversionBucket `json:"by_source"`
	ByCountry           []BusinessConversionBucket `json:"by_country"`
	// AttributionAvailable is false on gateways without the
	// acquisition_source / signup_country columns; the splits are then empty.
	AttributionAvailable bool `json:"attribution_available"`
}

type conversionUser struct {
	ID        int64
	Source    string
	Country   string
	UsedQuota int64
	Requests  int64
}

const (
	conversionSplitLimit   = 8
	conversionUnattributed = "unattributed"
	conversionUnknownPlace = "unknown"
	conversionLogChunk     = 500
)

// GetConversion returns the signup → used → paid funnel for the window cohort.
func (s *BusinessDashboardService) GetConversion(window string, noCache bool) (BusinessConversion, error) {
	w := ResolveDashboardWindow(window, time.Now())
	return businessCached("dashboard:beat:conversion:"+w.Key, 5*time.Minute, noCache, func() (BusinessConversion, error) {
		users, attribution, err := s.loadCohort(w)
		if err != nil {
			return BusinessConversion{}, err
		}
		active, err := s.loadLogActiveUsers(w, users)
		if err != nil {
			return BusinessConversion{}, err
		}
		paid, err := s.loadCohortPayers(w)
		if err != nil {
			return BusinessConversion{}, err
		}
		result := buildConversion(users, active, paid, conversionSplitLimit)
		result.Window = w
		result.AttributionAvailable = attribution
		if !attribution {
			result.BySource = []BusinessConversionBucket{}
			result.ByCountry = []BusinessConversionBucket{}
		}
		return result, nil
	})
}

func (s *BusinessDashboardService) loadCohort(w DashboardWindow) ([]conversionUser, bool, error) {
	wl, wlArgs := whitelistAnd("id")
	args := append([]interface{}{w.Start, w.End}, wlArgs...)
	where := "deleted_at IS NULL AND created_at >= ? AND created_at <= ?" + wl
	full := fmt.Sprintf(`SELECT id, COALESCE(acquisition_source, '') AS source, COALESCE(signup_country, '') AS country,
			COALESCE(used_quota, 0) AS used_quota, COALESCE(request_count, 0) AS requests
		FROM users WHERE %s`, where)
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(full), args...)
	attribution := true
	if isMissingSchemaErr(err) {
		// Gateways without the Beat signup columns: keep the funnel, drop the
		// splits, and let the logs answer who used the API.
		attribution = false
		legacy := fmt.Sprintf(`SELECT id, '' AS source, '' AS country, COALESCE(used_quota, 0) AS used_quota, 0 AS requests
			FROM users WHERE %s`, where)
		rows, err = s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(legacy), args...)
	}
	if err != nil {
		return nil, false, fmt.Errorf("signup cohort query failed: %w", err)
	}
	users := make([]conversionUser, 0, len(rows))
	for _, r := range rows {
		users = append(users, conversionUser{
			ID: toInt64(r["id"]), Source: toString(r["source"]), Country: toString(r["country"]),
			UsedQuota: toInt64(r["used_quota"]), Requests: toInt64(r["requests"]),
		})
	}
	return users, attribution, nil
}

// loadLogActiveUsers finds, among cohort accounts whose counters show no use,
// the ones that nevertheless have a consumption log — calls to free models
// bill nothing and so never move used_quota.
func (s *BusinessDashboardService) loadLogActiveUsers(w DashboardWindow, users []conversionUser) (map[int64]bool, error) {
	ids := make([]int64, 0)
	for _, u := range users {
		if u.UsedQuota <= 0 && u.Requests <= 0 && u.ID > 0 {
			ids = append(ids, u.ID)
		}
	}
	active := make(map[int64]bool)
	for start := 0; start < len(ids); start += conversionLogChunk {
		end := start + conversionLogChunk
		if end > len(ids) {
			end = len(ids)
		}
		query, args := buildIDInQuery(s.logDB, "SELECT DISTINCT user_id FROM logs WHERE type = 2 AND created_at >= ? AND user_id", ids[start:end])
		rows, err := s.logDB.QueryWithTimeout(businessQueryTimeout, query, append([]interface{}{w.Start}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("cohort usage query failed: %w", err)
		}
		for _, r := range rows {
			active[toInt64(r["user_id"])] = true
		}
	}
	return active, nil
}

func (s *BusinessDashboardService) loadCohortPayers(w DashboardWindow) (map[int64]bool, error) {
	query := fmt.Sprintf(`SELECT DISTINCT user_id FROM top_ups WHERE %s AND user_id IN (
			SELECT id FROM users WHERE deleted_at IS NULL AND created_at >= ? AND created_at <= ?)`, successStatusCondition())
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(query), w.Start, w.End)
	if err != nil {
		return nil, fmt.Errorf("cohort payer query failed: %w", err)
	}
	paid := make(map[int64]bool, len(rows))
	for _, r := range rows {
		paid[toInt64(r["user_id"])] = true
	}
	return paid, nil
}

// buildConversion folds cohort accounts into the funnel and its splits. An
// account is activated when its counters show use or it has a consumption
// log, and paid when it has a settled top-up.
func buildConversion(users []conversionUser, logActive, paid map[int64]bool, limit int) BusinessConversion {
	var result BusinessConversion
	var activatedPaid int64
	sources := map[string]*BusinessConversionBucket{}
	countries := map[string]*BusinessConversionBucket{}
	add := func(set map[string]*BusinessConversionBucket, key string, activated, paying bool) {
		b, ok := set[key]
		if !ok {
			b = &BusinessConversionBucket{Key: key}
			set[key] = b
		}
		b.Signups++
		if activated {
			b.Activated++
		}
		if paying {
			b.Paid++
		}
	}
	for _, u := range users {
		activated := u.UsedQuota > 0 || u.Requests > 0 || logActive[u.ID]
		paying := paid[u.ID]
		result.Signups++
		if activated {
			result.Activated++
		}
		if paying {
			result.Paid++
		}
		if activated && paying {
			activatedPaid++
		}
		source := strings.ToLower(strings.TrimSpace(u.Source))
		if source == "" {
			source = conversionUnattributed
		}
		country := strings.ToUpper(strings.TrimSpace(u.Country))
		if country == "" {
			country = conversionUnknownPlace
		}
		add(sources, source, activated, paying)
		add(countries, country, activated, paying)
	}
	result.ActivationRate = ratio(result.Activated, result.Signups)
	result.PaidRate = ratio(result.Paid, result.Signups)
	result.PaidOfActivatedRate = ratio(activatedPaid, result.Activated)
	result.BySource = conversionBuckets(sources, limit)
	result.ByCountry = conversionBuckets(countries, limit)
	return result
}

func conversionBuckets(set map[string]*BusinessConversionBucket, limit int) []BusinessConversionBucket {
	out := make([]BusinessConversionBucket, 0, len(set))
	for _, b := range set {
		b.ActivationRate = ratio(b.Activated, b.Signups)
		b.PaidRate = ratio(b.Paid, b.Signups)
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signups != out[j].Signups {
			return out[i].Signups > out[j].Signups
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
