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

// parsePeriodToTimestamps converts period strings like "24h", "7d" to start/end timestamps
func parsePeriodToTimestamps(period string) (int64, int64) {
	now := time.Now().Unix()
	var duration time.Duration

	switch period {
	case "1h":
		duration = 1 * time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	case "3d":
		duration = 3 * 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "14d":
		duration = 14 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	default:
		duration = 7 * 24 * time.Hour
	}

	start := now - int64(duration.Seconds())
	return start, now
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

// GetIPDistribution returns IP access distribution statistics.
// Total counters are computed from the full time window; geographic breakdowns
// use a top-IP sample so large logs tables stay responsive.
func (s *DashboardService) GetIPDistribution(window string, noCache bool) (map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:ip_distribution:%s", window)
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime, endTime := parsePeriodToTimestamps(window)
	geoAvailable := IsIPGeoAvailable()

	statsQuery := s.logDB.RebindQuery(`
		SELECT
			COUNT(DISTINCT ip) as total_ips,
			COUNT(*) as total_requests
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type IN (2, 5) AND ip IS NOT NULL AND ip <> ''`)
	statsRow, err := s.logDB.QueryOneWithTimeout(ipDistributionQueryTimeout, statsQuery, startTime, endTime)
	if err != nil {
		return nil, err
	}
	totalIPs := toInt64(statsRow["total_ips"])
	totalRequests := toInt64(statsRow["total_requests"])

	// Step 1: Query distinct IPs with request counts and user counts
	ipQuery := s.logDB.RebindQuery(`
		SELECT ip,
			COUNT(*) as request_count,
			COUNT(DISTINCT user_id) as user_count
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type IN (2, 5) AND ip IS NOT NULL AND ip <> ''
		GROUP BY ip
		ORDER BY request_count DESC
		LIMIT ?`)

	rows, err := s.logDB.QueryWithTimeout(ipDistributionQueryTimeout, ipQuery, startTime, endTime, ipDistributionSampleLimit)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		result := map[string]interface{}{
			"total_ips":           0,
			"total_requests":      0,
			"sampled_ip_limit":    ipDistributionSampleLimit,
			"sampled_ips":         int64(0),
			"sampled_requests":    int64(0),
			"coverage_percentage": float64(0),
			"geo_available":       geoAvailable,
			"domestic_percentage": 0.0,
			"overseas_percentage": 0.0,
			"by_country":          []map[string]interface{}{},
			"by_province":         []map[string]interface{}{},
			"top_cities":          []map[string]interface{}{},
			"snapshot_time":       time.Now().Unix(),
		}
		result["total_ips"] = totalIPs
		result["total_requests"] = totalRequests
		cm.Set(cacheKey, result, 5*time.Minute)
		return result, nil
	}

	// Step 2: Collect IPs and look up GeoIP
	type ipStat struct {
		IP           string
		RequestCount int64
		UserCount    int64
	}

	var ipStats []ipStat
	var ips []string
	for _, row := range rows {
		ip := fmt.Sprintf("%v", row["ip"])
		if ip == "" || ip == "<nil>" {
			continue
		}
		ipStats = append(ipStats, ipStat{
			IP:           ip,
			RequestCount: toInt64(row["request_count"]),
			UserCount:    toInt64(row["user_count"]),
		})
		ips = append(ips, ip)
	}

	geoResults := LookupIPGeoBatch(ips)

	// Step 3: Aggregate by country, province, city
	type countryAgg struct {
		CountryCode  string
		IPCount      int64
		RequestCount int64
		UserCount    int64
	}
	type provinceAgg struct {
		Country      string
		CountryCode  string
		IPCount      int64
		RequestCount int64
		UserCount    int64
	}
	type cityAgg struct {
		Country      string
		CountryCode  string
		Region       string
		City         string
		IPCount      int64
		RequestCount int64
		UserCount    int64
	}

	byCountry := map[string]*countryAgg{}
	byProvince := map[string]*provinceAgg{}
	byCity := map[string]*cityAgg{}

	var sampledIPs int64
	var sampledRequests int64
	var domesticRequests int64
	var overseasRequests int64

	for _, stat := range ipStats {
		geo := geoResults[stat.IP]
		country := geo.Country
		countryCode := geo.CountryCode
		region := geo.Region
		city := geo.City

		if !geo.Success || country == "" {
			country = "未知"
			countryCode = "XX"
		}

		sampledIPs++
		sampledRequests += stat.RequestCount

		// Domestic vs overseas
		if domesticCountryCodes[countryCode] {
			domesticRequests += stat.RequestCount
		} else {
			overseasRequests += stat.RequestCount
		}

		// By country
		if _, ok := byCountry[country]; !ok {
			byCountry[country] = &countryAgg{CountryCode: countryCode}
		}
		byCountry[country].IPCount++
		byCountry[country].RequestCount += stat.RequestCount
		byCountry[country].UserCount += stat.UserCount

		// By province (Chinese mainland only)
		if countryCode == "CN" && region != "" {
			if _, ok := byProvince[region]; !ok {
				byProvince[region] = &provinceAgg{Country: country, CountryCode: countryCode}
			}
			byProvince[region].IPCount++
			byProvince[region].RequestCount += stat.RequestCount
			byProvince[region].UserCount += stat.UserCount
		}

		// By city
		if city != "" {
			cityKey := fmt.Sprintf("%s:%s:%s", country, region, city)
			if _, ok := byCity[cityKey]; !ok {
				byCity[cityKey] = &cityAgg{Country: country, CountryCode: countryCode, Region: region, City: city}
			}
			byCity[cityKey].IPCount++
			byCity[cityKey].RequestCount += stat.RequestCount
			byCity[cityKey].UserCount += stat.UserCount
		}
	}

	coveragePct := float64(0)
	if totalRequests > 0 {
		coveragePct = math.Round(float64(sampledRequests)/float64(totalRequests)*10000) / 100
	}

	// Step 4: Convert to sorted lists
	countryList := make([]map[string]interface{}, 0, len(byCountry))
	for name, agg := range byCountry {
		pct := float64(0)
		if sampledRequests > 0 {
			pct = float64(agg.RequestCount) / float64(sampledRequests) * 100
		}
		countryList = append(countryList, map[string]interface{}{
			"country":       name,
			"country_code":  agg.CountryCode,
			"ip_count":      agg.IPCount,
			"request_count": agg.RequestCount,
			"user_count":    agg.UserCount,
			"percentage":    math.Round(pct*100) / 100,
		})
	}
	sortByRequestCount(countryList)

	provinceList := make([]map[string]interface{}, 0, len(byProvince))
	for name, agg := range byProvince {
		pct := float64(0)
		if sampledRequests > 0 {
			pct = float64(agg.RequestCount) / float64(sampledRequests) * 100
		}
		provinceList = append(provinceList, map[string]interface{}{
			"country":       agg.Country,
			"country_code":  agg.CountryCode,
			"region":        name,
			"ip_count":      agg.IPCount,
			"request_count": agg.RequestCount,
			"user_count":    agg.UserCount,
			"percentage":    math.Round(pct*100) / 100,
		})
	}
	sortByRequestCount(provinceList)

	cityList := make([]map[string]interface{}, 0, len(byCity))
	for _, agg := range byCity {
		pct := float64(0)
		if sampledRequests > 0 {
			pct = float64(agg.RequestCount) / float64(sampledRequests) * 100
		}
		cityList = append(cityList, map[string]interface{}{
			"country":       agg.Country,
			"country_code":  agg.CountryCode,
			"region":        agg.Region,
			"city":          agg.City,
			"ip_count":      agg.IPCount,
			"request_count": agg.RequestCount,
			"user_count":    agg.UserCount,
			"percentage":    math.Round(pct*100) / 100,
		})
	}
	sortByRequestCount(cityList)

	// Domestic/overseas percentage
	domesticPct := float64(0)
	overseasPct := float64(0)
	if sampledRequests > 0 {
		domesticPct = math.Round(float64(domesticRequests)/float64(sampledRequests)*10000) / 100
		overseasPct = math.Round(float64(overseasRequests)/float64(sampledRequests)*10000) / 100
	}

	result := map[string]interface{}{
		"total_ips":           totalIPs,
		"total_requests":      totalRequests,
		"sampled_ip_limit":    ipDistributionSampleLimit,
		"sampled_ips":         sampledIPs,
		"sampled_requests":    sampledRequests,
		"coverage_percentage": coveragePct,
		"geo_available":       geoAvailable,
		"domestic_percentage": domesticPct,
		"overseas_percentage": overseasPct,
		"by_country":          countryList,
		"by_province":         provinceList,
		"top_cities":          cityList,
		"snapshot_time":       time.Now().Unix(),
	}
	cm.Set(cacheKey, result, 5*time.Minute)
	return result, nil
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
