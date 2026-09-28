package service

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// RiskMonitoringService builds the read-only per-user risk analysis
type RiskMonitoringService struct {
	db    *database.Manager
	logDB *database.Manager
}

// NewRiskMonitoringService creates a new RiskMonitoringService
func NewRiskMonitoringService() *RiskMonitoringService {
	return &RiskMonitoringService{db: database.Get(), logDB: database.GetLog()}
}

func (s *RiskMonitoringService) enrichChannelNames(rows []map[string]interface{}) {
	if len(rows) == 0 {
		return
	}
	ids := make([]interface{}, 0, len(rows))
	seen := make(map[int64]bool)
	for _, row := range rows {
		id := toInt64(row["channel_id"])
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}

	ph := make([]string, len(ids))
	for i := range ids {
		ph[i] = s.db.Placeholder(i + 1)
	}
	query := fmt.Sprintf("SELECT id, COALESCE(name, '') as name FROM channels WHERE id IN (%s)", strings.Join(ph, ","))
	channelRows, err := s.db.Query(query, ids...)
	if err != nil {
		return
	}
	names := make(map[int64]string, len(channelRows))
	for _, row := range channelRows {
		names[toInt64(row["id"])] = toString(row["name"])
	}
	for _, row := range rows {
		if name, ok := names[toInt64(row["channel_id"])]; ok {
			row["channel_name"] = name
		}
	}
}

// GetUserAnalysis returns detailed risk analysis for a user
func (s *RiskMonitoringService) GetUserAnalysis(userID int64, windowSeconds int64, endTime *int64) (map[string]interface{}, error) {
	now := time.Now().Unix()
	if endTime != nil {
		now = *endTime
	}
	startTime := now - windowSeconds

	// User info
	groupCol := s.db.QuoteIdentifier("group")
	userRow, _ := s.db.QueryOne(s.db.RebindQuery(
		fmt.Sprintf("SELECT id, username, display_name, email, status, %s, remark, request_count FROM users WHERE id = ? AND deleted_at IS NULL", groupCol)), userID)

	// Build user object
	userInfo := map[string]interface{}{
		"id":           userID,
		"username":     "",
		"display_name": nil,
		"email":        nil,
		"status":       1,
		"group":        nil,
		"remark":       nil,
	}
	if userRow != nil {
		userInfo["id"] = userRow["id"]
		userInfo["username"] = userRow["username"]
		userInfo["display_name"] = userRow["display_name"]
		userInfo["email"] = userRow["email"]
		userInfo["status"] = userRow["status"]
		userInfo["group"] = userRow["group"]
		userInfo["remark"] = userRow["remark"]
	}

	// Usage stats in window
	uniqueIPsExpr := s.logDB.CountDistinctNonEmpty("l.ip")
	statsQuery := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT COUNT(*) as total_requests,
			SUM(CASE WHEN l.type = 2 THEN 1 ELSE 0 END) as success_requests,
			SUM(CASE WHEN l.type = 5 THEN 1 ELSE 0 END) as failure_requests,
			COALESCE(SUM(l.quota), 0) as quota_used,
			COALESCE(SUM(l.prompt_tokens), 0) as prompt_tokens,
			COALESCE(SUM(l.completion_tokens), 0) as completion_tokens,
			%s as unique_ips,
			COUNT(DISTINCT l.token_id) as unique_tokens,
			COUNT(DISTINCT l.model_name) as unique_models,
			COUNT(DISTINCT l.channel_id) as unique_channels,
			SUM(CASE WHEN l.type = 2 AND l.completion_tokens = 0 THEN 1 ELSE 0 END) as empty_count
		FROM logs l
		WHERE l.user_id = ? AND l.created_at >= ? AND l.created_at <= ? AND l.type IN (2, 5)`, uniqueIPsExpr))

	statsRow, _ := s.logDB.QueryOne(statsQuery, userID, startTime, now)

	totalRequests := int64(0)
	successRequests := int64(0)
	failureRequests := int64(0)
	quotaUsed := int64(0)
	promptTokens := int64(0)
	completionTokens := int64(0)
	uniqueIPs := int64(0)
	uniqueTokens := int64(0)
	uniqueModels := int64(0)
	uniqueChannels := int64(0)
	emptyCount := int64(0)

	if statsRow != nil {
		totalRequests = toInt64(statsRow["total_requests"])
		successRequests = toInt64(statsRow["success_requests"])
		failureRequests = toInt64(statsRow["failure_requests"])
		quotaUsed = toInt64(statsRow["quota_used"])
		promptTokens = toInt64(statsRow["prompt_tokens"])
		completionTokens = toInt64(statsRow["completion_tokens"])
		uniqueIPs = toInt64(statsRow["unique_ips"])
		uniqueTokens = toInt64(statsRow["unique_tokens"])
		uniqueModels = toInt64(statsRow["unique_models"])
		uniqueChannels = toInt64(statsRow["unique_channels"])
		emptyCount = toInt64(statsRow["empty_count"])
	}

	// Calculate rates
	failureRate := 0.0
	emptyRate := 0.0
	if totalRequests > 0 {
		failureRate = float64(failureRequests) / float64(totalRequests)
	}
	if successRequests > 0 {
		emptyRate = float64(emptyCount) / float64(successRequests)
	}

	// Average use time
	avgUseTimeQuery := s.logDB.RebindQuery(`
		SELECT COALESCE(AVG(use_time), 0) as avg_use_time
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ? AND type = 2`)
	avgRow, _ := s.logDB.QueryOne(avgUseTimeQuery, userID, startTime, now)
	avgUseTime := 0.0
	if avgRow != nil {
		if v, ok := avgRow["avg_use_time"].(float64); ok {
			avgUseTime = v
		} else {
			avgUseTime = float64(toInt64(avgRow["avg_use_time"]))
		}
	}

	// Summary
	summary := map[string]interface{}{
		"total_requests":    totalRequests,
		"success_requests":  successRequests,
		"failure_requests":  failureRequests,
		"quota_used":        quotaUsed,
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"avg_use_time":      avgUseTime,
		"unique_ips":        uniqueIPs,
		"unique_tokens":     uniqueTokens,
		"unique_models":     uniqueModels,
		"unique_channels":   uniqueChannels,
		"empty_count":       emptyCount,
		"failure_rate":      failureRate,
		"empty_rate":        emptyRate,
	}

	// Risk analysis
	windowMinutes := float64(windowSeconds) / 60.0
	requestsPerMinute := 0.0
	if windowMinutes > 0 {
		requestsPerMinute = float64(totalRequests) / windowMinutes
	}

	avgQuotaPerRequest := 0.0
	if totalRequests > 0 {
		avgQuotaPerRequest = float64(quotaUsed) / float64(totalRequests)
	}

	// IP switch analysis — fetch IP sequence ordered by time
	ipSeqQuery := s.logDB.RebindQuery(`
		SELECT created_at, ip
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ?
			AND type IN (2, 5) AND ip IS NOT NULL AND ip != ''
		ORDER BY created_at ASC`)
	ipSequence, _ := s.logDB.QueryWithTimeout(30*time.Second, ipSeqQuery, userID, startTime, now)
	if ipSequence == nil {
		ipSequence = []map[string]interface{}{}
	}
	ipSwitchAnalysis := analyzeIPSwitches(ipSequence)

	// Geo 聚合：distinct IP batch lookup，用于「同城抖动 vs 跨城跳跃」分层（#20）
	distinctIPs := collectDistinctIPs(ipSequence)
	geoAvailable := IsIPGeoAvailable()
	var geoMap map[string]IPGeoInfo
	if geoAvailable && len(distinctIPs) > 0 {
		geoMap = LookupIPGeoBatch(distinctIPs)
	} else {
		geoMap = map[string]IPGeoInfo{}
	}
	geoAnalysis := analyzeIPGeoFromSequence(ipSequence, geoMap, geoAvailable)
	if details, ok := ipSwitchAnalysis["switch_details"].([]map[string]interface{}); ok && len(details) > 0 {
		enrichSwitchDetailsWithGeo(details, geoMap)
	}

	// Risk flags（非 IP 类）
	riskFlags := []string{}
	if requestsPerMinute > 5.0 {
		riskFlags = append(riskFlags, "HIGH_RPM")
	}
	if failureRate > 50.0 && totalRequests > 10 {
		riskFlags = append(riskFlags, "HIGH_FAILURE_RATE")
	}
	// IP / 地理相关 flags（同城多 IP 不再直接 MANY_IPS）
	riskFlags = appendGeoAwareIPRiskFlags(riskFlags, uniqueIPs, ipSwitchAnalysis, geoAnalysis)

	risk := map[string]interface{}{
		"requests_per_minute":   requestsPerMinute,
		"avg_quota_per_request": avgQuotaPerRequest,
		"risk_flags":            riskFlags,
		"ip_switch_analysis":    ipSwitchAnalysis,
		"ip_geo_analysis":       geoAnalysis,
	}

	// Top models
	modelsQuery := s.logDB.RebindQuery(`
		SELECT COALESCE(model_name, 'unknown') as model_name, COUNT(*) as requests,
			COALESCE(SUM(quota), 0) as quota_used,
			SUM(CASE WHEN type = 2 THEN 1 ELSE 0 END) as success_requests,
			SUM(CASE WHEN type = 5 THEN 1 ELSE 0 END) as failure_requests,
			SUM(CASE WHEN type = 2 AND completion_tokens = 0 THEN 1 ELSE 0 END) as empty_count
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ? AND type IN (2, 5)
		GROUP BY COALESCE(model_name, 'unknown')
		ORDER BY requests DESC
		LIMIT 10`)

	topModels, _ := s.logDB.Query(modelsQuery, userID, startTime, now)
	if topModels == nil {
		topModels = []map[string]interface{}{}
	}

	// ClickHouse logs omit channel_name, so enrich those rows from the main DB.
	channelNameExpr := "COALESCE(MAX(channel_name), '')"
	if s.logDB.IsCH {
		channelNameExpr = "''"
	}
	channelsQuery := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT channel_id, %s as channel_name,
			COUNT(*) as requests,
			COALESCE(SUM(quota), 0) as quota_used
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ? AND type IN (2, 5)
		GROUP BY channel_id
		ORDER BY requests DESC
		LIMIT 10`, channelNameExpr))

	topChannels, _ := s.logDB.Query(channelsQuery, userID, startTime, now)
	if topChannels == nil {
		topChannels = []map[string]interface{}{}
	}
	if s.logDB.IsCH {
		s.enrichChannelNames(topChannels)
	}

	// Top IPs
	ipsQuery := s.logDB.RebindQuery(`
		SELECT ip, COUNT(*) as requests
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ? AND ip IS NOT NULL AND ip != ''
		GROUP BY ip
		ORDER BY requests DESC
		LIMIT 20`)

	topIPs, _ := s.logDB.QueryWithTimeout(30*time.Second, ipsQuery, userID, startTime, now)
	if topIPs == nil {
		topIPs = []map[string]interface{}{}
	}
	// 给 Top IP 补地理标签（复用上面的 geoMap；Top 里可能有序列外 IP，再查一次缺口）
	if geoAvailable && len(topIPs) > 0 {
		need := make([]string, 0)
		for _, row := range topIPs {
			ip := fmt.Sprintf("%v", row["ip"])
			if ip == "" {
				continue
			}
			if _, ok := geoMap[ip]; !ok {
				need = append(need, ip)
			}
		}
		if len(need) > 0 {
			extra := LookupIPGeoBatch(need)
			for ip, info := range extra {
				geoMap[ip] = info
			}
		}
		for _, row := range topIPs {
			ip := fmt.Sprintf("%v", row["ip"])
			info := geoMap[ip]
			row["city"] = info.City
			row["region"] = info.Region
			row["country"] = info.Country
			row["country_code"] = info.CountryCode
			row["geo_label"] = geoDisplayLabel(info)
		}
	}

	// ClickHouse compatibility ids are commonly zero, so use the real sort key.
	recentChannelNameExpr := "COALESCE(channel_name, '')"
	recentOrder := "id DESC"
	if s.logDB.IsCH {
		recentChannelNameExpr = "''"
		recentOrder = "created_at DESC, request_id DESC"
	}
	recentLogsQuery := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT id, created_at, type, COALESCE(model_name,'') as model_name,
			COALESCE(quota, 0) as quota,
			COALESCE(prompt_tokens, 0) as prompt_tokens,
			COALESCE(completion_tokens, 0) as completion_tokens,
			COALESCE(use_time, 0) as use_time,
			COALESCE(ip, '') as ip,
			COALESCE(channel_id, 0) as channel_id,
			%s as channel_name,
			COALESCE(token_id, 0) as token_id,
			COALESCE(token_name, '') as token_name
		FROM logs
		WHERE user_id = ? AND created_at >= ? AND created_at <= ? AND type IN (2, 5)
		ORDER BY %s
		LIMIT 50`, recentChannelNameExpr, recentOrder))

	recentLogs, _ := s.logDB.Query(recentLogsQuery, userID, startTime, now)
	if recentLogs == nil {
		recentLogs = []map[string]interface{}{}
	}
	if s.logDB.IsCH {
		s.enrichChannelNames(recentLogs)
	}

	result := map[string]interface{}{
		"range": map[string]interface{}{
			"start_time":     startTime,
			"end_time":       now,
			"window_seconds": windowSeconds,
		},
		"user":         userInfo,
		"summary":      summary,
		"risk":         risk,
		"top_models":   topModels,
		"top_channels": topChannels,
		"top_ips":      topIPs,
		"recent_logs":  recentLogs,
	}

	return result, nil
}

// ========== IP Switch Analysis ==========

// getIPVersion returns "v4" or "v6" based on the IP string
func getIPVersion(ip string) string {
	if strings.Contains(ip, ":") {
		return "v6"
	}
	return "v4"
}

// analyzeIPSwitches detects IP switching patterns from a time-ordered IP sequence.
// Matches Python's _analyze_ip_switches logic.
func analyzeIPSwitches(ipSequence []map[string]interface{}) map[string]interface{} {
	empty := map[string]interface{}{
		"switch_count":        int64(0),
		"real_switch_count":   int64(0),
		"rapid_switch_count":  int64(0),
		"dual_stack_switches": int64(0),
		"avg_ip_duration":     float64(0),
		"min_switch_interval": int64(0),
		"switch_details":      []map[string]interface{}{},
	}

	if len(ipSequence) < 2 {
		return empty
	}

	type switchDetail struct {
		Time        int64  `json:"time"`
		FromIP      string `json:"from_ip"`
		ToIP        string `json:"to_ip"`
		Interval    int64  `json:"interval"`
		IsDualStack bool   `json:"is_dual_stack"`
		FromVersion string `json:"from_version"`
		ToVersion   string `json:"to_version"`
	}

	var switches []switchDetail
	ipDurations := map[string][]int64{} // track usage duration per IP
	var rapidSwitches int64
	var dualStackSwitches int64

	var prevIP string
	var prevTime int64
	var ipStartTime int64

	for _, row := range ipSequence {
		currentIP := fmt.Sprintf("%v", row["ip"])
		currentTime := toInt64(row["created_at"])
		if currentIP == "" || currentTime == 0 {
			continue
		}

		if prevIP == "" {
			prevIP = currentIP
			prevTime = currentTime
			ipStartTime = currentTime
			continue
		}

		if currentIP != prevIP {
			switchInterval := currentTime - prevTime

			prevVersion := getIPVersion(prevIP)
			currVersion := getIPVersion(currentIP)

			// Detect dual-stack switch (v4 <-> v6)
			isDualStack := false
			isV4V6Switch := (prevVersion == "v4" && currVersion == "v6") ||
				(prevVersion == "v6" && currVersion == "v4")
			if isV4V6Switch {
				// Simple heuristic: v4/v6 switch within 60s is likely dual-stack
				if switchInterval <= 60 {
					isDualStack = true
				}
			}

			switches = append(switches, switchDetail{
				Time:        currentTime,
				FromIP:      prevIP,
				ToIP:        currentIP,
				Interval:    switchInterval,
				IsDualStack: isDualStack,
				FromVersion: prevVersion,
				ToVersion:   currVersion,
			})

			if isDualStack {
				dualStackSwitches++
			} else if switchInterval <= 60 {
				rapidSwitches++
			}

			// Record IP usage duration
			ipDuration := currentTime - ipStartTime
			ipDurations[prevIP] = append(ipDurations[prevIP], ipDuration)

			prevIP = currentIP
			ipStartTime = currentTime
		}

		prevTime = currentTime
	}

	switchCount := int64(len(switches))
	realSwitchCount := switchCount - dualStackSwitches

	// Min switch interval (excluding dual-stack)
	var minSwitchInterval int64
	first := true
	for _, s := range switches {
		if !s.IsDualStack {
			if first || s.Interval < minSwitchInterval {
				minSwitchInterval = s.Interval
				first = false
			}
		}
	}

	// Average IP duration
	var allDurations []int64
	for _, durations := range ipDurations {
		allDurations = append(allDurations, durations...)
	}
	avgIPDuration := float64(0)
	if len(allDurations) > 0 {
		var sum int64
		for _, d := range allDurations {
			sum += d
		}
		avgIPDuration = math.Round(float64(sum)/float64(len(allDurations))*10) / 10
	}

	// Return last 10 switch details
	detailLimit := 10
	startIdx := 0
	if len(switches) > detailLimit {
		startIdx = len(switches) - detailLimit
	}
	recentSwitches := make([]map[string]interface{}, 0, detailLimit)
	for _, s := range switches[startIdx:] {
		recentSwitches = append(recentSwitches, map[string]interface{}{
			"time":          s.Time,
			"from_ip":       s.FromIP,
			"to_ip":         s.ToIP,
			"interval":      s.Interval,
			"is_dual_stack": s.IsDualStack,
			"from_version":  s.FromVersion,
			"to_version":    s.ToVersion,
		})
	}

	return map[string]interface{}{
		"switch_count":        switchCount,
		"real_switch_count":   realSwitchCount,
		"rapid_switch_count":  rapidSwitches,
		"dual_stack_switches": dualStackSwitches,
		"avg_ip_duration":     avgIPDuration,
		"min_switch_interval": minSwitchInterval,
		"switch_details":      recentSwitches,
	}
}
