package service

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/database"
)

// DashboardService handles dashboard analytics queries
type DashboardService struct {
	db    *database.Manager
	logDB *database.Manager
}

var ipDistributionSampleLimit = 3000

const ipDistributionQueryTimeout = 30 * time.Second

// NewDashboardService creates a new DashboardService
func NewDashboardService() *DashboardService {
	return &DashboardService{db: database.Get(), logDB: database.GetLog()}
}

// parsePeriodToTimestamps converts a window such as "24h" or "7d" to
// start/end timestamps. It reads WindowSeconds, the table the handler
// validates against, so every accepted window means what it says (3h and 12h
// used to fall through to 7 days). An unknown window is 7 days.
func parsePeriodToTimestamps(period string) (int64, int64) {
	now := time.Now().Unix()
	seconds, ok := WindowSeconds[period]
	if !ok {
		seconds = WindowSeconds["7d"]
	}
	return now - seconds, now
}

// localTZOffset returns the local timezone offset in seconds (e.g. 28800 for UTC+8).
func localTZOffset() int {
	_, offset := time.Now().Zone()
	return offset
}

// InvalidateDashboardCache clears all dashboard-related caches
func (s *DashboardService) InvalidateDashboardCache() {
	cm := cache.Get()
	cm.DeleteByPrefix("dashboard:")
}

// GetIPDistribution returns where traffic comes from, country by country.
// Total counters are computed from the full time window; the country
// breakdown uses a top-IP sample so large logs tables stay responsive, and
// the sample's size and coverage are returned with it.
//
// Beat serves an overseas audience, so there is no domestic/overseas split,
// province or city breakdown: the country is the unit.
func (s *DashboardService) GetIPDistribution(window string, noCache bool) (map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:ip_distribution:v2:%s", window)
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime, endTime := parsePeriodToTimestamps(window)
	statsRow, err := s.logDB.QueryOneWithTimeout(ipDistributionQueryTimeout, s.logDB.RebindQuery(`
		SELECT
			COUNT(DISTINCT ip) as total_ips,
			COUNT(*) as total_requests
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type IN (2, 5) AND ip IS NOT NULL AND ip <> ''`), startTime, endTime)
	if err != nil {
		return nil, err
	}
	totalRequests := toInt64(statsRow["total_requests"])

	rows, err := s.logDB.QueryWithTimeout(ipDistributionQueryTimeout, s.logDB.RebindQuery(`
		SELECT ip,
			COUNT(*) as request_count,
			COUNT(DISTINCT user_id) as user_count
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type IN (2, 5) AND ip IS NOT NULL AND ip <> ''
		GROUP BY ip
		ORDER BY request_count DESC
		LIMIT ?`), startTime, endTime, ipDistributionSampleLimit)
	if err != nil {
		return nil, err
	}

	byCountry, sampledIPs, sampledRequests := aggregateIPSampleByCountry(rows)
	coveragePct := float64(0)
	if totalRequests > 0 {
		coveragePct = math.Round(float64(sampledRequests)/float64(totalRequests)*10000) / 100
	}
	result := map[string]interface{}{
		"total_ips":           toInt64(statsRow["total_ips"]),
		"total_requests":      totalRequests,
		"sampled_ip_limit":    ipDistributionSampleLimit,
		"sampled_ips":         sampledIPs,
		"sampled_requests":    sampledRequests,
		"coverage_percentage": coveragePct,
		"geo_available":       IsIPGeoAvailable(),
		"by_country":          byCountry,
		"snapshot_time":       time.Now().Unix(),
	}
	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
}

// aggregateIPSampleByCountry looks up each sampled IP and folds the sample
// into countries, sorted by requests. Countries are keyed by ISO code; IPs the
// GeoIP database cannot place are one "未知" (XX) row. user_count adds up each
// IP's distinct users, so a user seen on two IPs in a country counts twice.
func aggregateIPSampleByCountry(rows []map[string]interface{}) ([]map[string]interface{}, int64, int64) {
	type ipStat struct {
		ip                  string
		requests, userCount int64
	}
	stats := make([]ipStat, 0, len(rows))
	ips := make([]string, 0, len(rows))
	for _, row := range rows {
		ip := fmt.Sprintf("%v", row["ip"])
		if ip == "" || ip == "<nil>" {
			continue
		}
		stats = append(stats, ipStat{ip: ip, requests: toInt64(row["request_count"]), userCount: toInt64(row["user_count"])})
		ips = append(ips, ip)
	}
	geo := map[string]IPGeoInfo{}
	if len(ips) > 0 {
		geo = LookupIPGeoBatch(ips)
	}

	type countryAgg struct {
		name, code                   string
		ipCount, requests, userCount int64
	}
	byCode := map[string]*countryAgg{}
	var sampledIPs, sampledRequests int64
	for _, stat := range stats {
		info := geo[stat.ip]
		name, code := info.Country, info.CountryCode
		if !info.Success || name == "" {
			name, code = "未知", "XX"
		}
		key := code
		if key == "" {
			key = name
		}
		agg, ok := byCode[key]
		if !ok {
			agg = &countryAgg{name: name, code: code}
			byCode[key] = agg
		}
		agg.ipCount++
		agg.requests += stat.requests
		agg.userCount += stat.userCount
		sampledIPs++
		sampledRequests += stat.requests
	}

	countries := make([]map[string]interface{}, 0, len(byCode))
	for _, agg := range byCode {
		pct := float64(0)
		if sampledRequests > 0 {
			pct = math.Round(float64(agg.requests)/float64(sampledRequests)*10000) / 100
		}
		countries = append(countries, map[string]interface{}{
			"country":       agg.name,
			"country_code":  agg.code,
			"ip_count":      agg.ipCount,
			"request_count": agg.requests,
			"user_count":    agg.userCount,
			"percentage":    pct,
		})
	}
	sortByRequestCount(countries)
	return countries, sampledIPs, sampledRequests
}

// sortByRequestCount sorts a slice of maps by request_count descending using sort.Slice
func sortByRequestCount(list []map[string]interface{}) {
	sort.Slice(list, func(i, j int) bool {
		return toInt64(list[i]["request_count"]) > toInt64(list[j]["request_count"])
	})
}

// toFloat64 safely converts interface{} to float64
func toFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case int32:
		return float64(val)
	case int16:
		return float64(val)
	case int8:
		return float64(val)
	case uint64:
		return float64(val)
	case uint:
		return float64(val)
	case uint32:
		return float64(val)
	case uint16:
		return float64(val)
	case uint8:
		return float64(val)
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	case []byte:
		var f float64
		fmt.Sscanf(string(val), "%f", &f)
		return f
	default:
		return 0
	}
}
