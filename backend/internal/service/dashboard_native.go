package service

import (
	"fmt"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
)

func (s *DashboardService) GetSystemOverview(period string, noCache bool) (map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:overview:%s", period)
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime, _ := parsePeriodToTimestamps(period)
	result := map[string]interface{}{}

	// Combined query 1: users + tokens counts (reduces 4 queries → 1)
	userTokenQuery := s.db.RebindQuery(`
		SELECT
			(SELECT COUNT(*) FROM users WHERE deleted_at IS NULL) as total_users,
			(SELECT COUNT(*) FROM tokens WHERE deleted_at IS NULL) as total_tokens`)
	row, err := s.db.QueryOneWithTimeout(15*time.Second, userTokenQuery)
	if err == nil && row != nil {
		result["total_users"] = row["total_users"]
		result["total_tokens"] = row["total_tokens"]
	}

	// active_users lives in the logs table → query the log DB separately
	// (logs may be on a different database via LOG_SQL_DSN, so it can't be a
	// subquery alongside the users/tokens counts above).
	activeQuery := s.logDB.RebindQuery(`
		SELECT COUNT(DISTINCT CASE WHEN user_id > 0 THEN user_id END) AS active_users,
			COUNT(DISTINCT CASE WHEN token_id > 0 THEN token_id END) AS active_tokens
		FROM logs
		WHERE created_at >= ? AND type IN (2, 5)`)
	if activeRow, aErr := s.logDB.QueryOneWithTimeout(15*time.Second, activeQuery, startTime); aErr == nil && activeRow != nil {
		result["active_users"] = activeRow["active_users"]
		result["active_tokens"] = activeRow["active_tokens"]
		result["active_users_24h"] = activeRow["active_users"]
		result["active_tokens_24h"] = activeRow["active_tokens"]
	}

	// Combined query 2: channels
	channelQuery := `SELECT COUNT(*) as total, SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END) as active FROM channels`
	row, err = s.db.QueryOneWithTimeout(10*time.Second, channelQuery)
	if err == nil && row != nil {
		result["total_channels"] = row["total"]
		result["active_channels"] = row["active"]
	}

	// Models count
	row, err = s.db.QueryOneWithTimeout(10*time.Second,
		`SELECT COUNT(DISTINCT a.model) as count
		 FROM abilities a
		 INNER JOIN channels c ON c.id = a.channel_id
		 WHERE c.status = 1`)
	if err == nil && row != nil {
		result["total_models"] = row["count"]
	} else {
		row, err = s.db.QueryOneWithTimeout(10*time.Second,
			"SELECT COUNT(*) as count FROM models WHERE deleted_at IS NULL")
		if err == nil && row != nil {
			result["total_models"] = row["count"]
		}
	}

	// Redemption counts
	row, err = s.db.QueryOneWithTimeout(10*time.Second,
		`SELECT COUNT(*) as total,
		 SUM(CASE WHEN status = 1 THEN 1 ELSE 0 END) as unused
		 FROM redemptions WHERE deleted_at IS NULL`)
	if err == nil && row != nil {
		result["total_redemptions"] = row["total"]
		result["unused_redemptions"] = row["unused"]
	}

	cm.Set(cacheKey, result, 3*time.Minute)
	return result, nil
}

func (s *DashboardService) GetUsageStatistics(period string, noCache bool) (map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:usage:%s", period)
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime, endTime := parsePeriodToTimestamps(period)

	// Only type=2 (success) for usage stats, matching Python backend
	query := s.logDB.RebindQuery(`
		SELECT
			COUNT(*) as total_requests,
			COALESCE(SUM(quota), 0) as total_quota_used,
			COALESCE(SUM(prompt_tokens), 0) as total_prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) as total_completion_tokens,
			COALESCE(AVG(use_time), 0) as avg_response_time
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type = 2`)

	row, err := s.logDB.QueryOneWithTimeout(15*time.Second, query, startTime, endTime)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"total_requests":          0,
		"total_quota_used":        0,
		"total_prompt_tokens":     0,
		"total_completion_tokens": 0,
		"average_response_time":   float64(0),
		"period":                  period,
	}

	if row != nil {
		result["total_requests"] = row["total_requests"]
		result["total_quota_used"] = row["total_quota_used"]
		result["total_prompt_tokens"] = row["total_prompt_tokens"]
		result["total_completion_tokens"] = row["total_completion_tokens"]
		// Average response time in milliseconds
		if avgTime, ok := row["avg_response_time"]; ok {
			result["average_response_time"] = toFloat64(avgTime)
		}
	}

	cm.Set(cacheKey, result, 3*time.Minute)
	return result, nil
}

func (s *DashboardService) GetModelUsage(period string, limit int, noCache bool) ([]map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:models:%s:%d", period, limit)
	if !noCache {
		var cached []map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime, endTime := parsePeriodToTimestamps(period)

	query := s.logDB.RebindQuery(`
		SELECT model_name,
			COUNT(*) as request_count,
			COALESCE(SUM(quota), 0) as quota_used,
			COALESCE(SUM(prompt_tokens), 0) as prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) as completion_tokens
		FROM logs
		WHERE created_at >= ? AND created_at <= ? AND type = 2
		GROUP BY model_name
		ORDER BY request_count DESC
		LIMIT ?`)

	rows, err := s.logDB.QueryWithTimeout(15*time.Second, query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	cm.Set(cacheKey, rows, 3*time.Minute)
	return rows, nil
}

func (s *DashboardService) GetDailyTrends(days int, noCache bool) ([]map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:daily:%d", days)
	if !noCache {
		var cached []map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	now := time.Now()
	startTime := now.AddDate(0, 0, -days).Unix()
	tzOffset := localTZOffset()

	// Group by local-time day using pure unix arithmetic — timezone-safe
	dayGroupExpr := fmt.Sprintf("FLOOR((created_at + %d) / 86400)", tzOffset)

	var rows []map[string]interface{}
	var err error

	if IsQuotaDataAvailable() {
		query := s.db.RebindQuery(fmt.Sprintf(`
			SELECT %s as day_group,
				COALESCE(SUM(count), 0) as request_count,
				COALESCE(SUM(quota), 0) as quota_used,
				COUNT(DISTINCT user_id) as unique_users
			FROM quota_data
			WHERE created_at >= ?
			GROUP BY %s
			ORDER BY day_group ASC`,
			dayGroupExpr, dayGroupExpr))
		rows, err = s.db.QueryWithTimeout(30*time.Second, query, startTime)
	} else {
		query := s.logDB.RebindQuery(fmt.Sprintf(`
			SELECT %s as day_group,
				COUNT(*) as request_count,
				COALESCE(SUM(quota), 0) as quota_used,
				COUNT(DISTINCT user_id) as unique_users
			FROM logs
			WHERE created_at >= ? AND type = 2
			GROUP BY %s
			ORDER BY day_group ASC`,
			dayGroupExpr, dayGroupExpr))
		rows, err = s.logDB.QueryWithTimeout(30*time.Second, query, startTime)
	}

	if err != nil {
		return nil, err
	}

	rows = fillDailyGaps(rows, days, tzOffset)

	cm.Set(cacheKey, rows, 5*time.Minute)
	return rows, nil
}

func (s *DashboardService) GetHourlyTrends(hours int, noCache bool) ([]map[string]interface{}, error) {
	cm := cache.Get()
	cacheKey := fmt.Sprintf("dashboard:hourly:%d", hours)
	if !noCache {
		var cached []map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}

	startTime := time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
	tzOffset := localTZOffset()

	// Group by local-time hour using pure unix arithmetic — timezone-safe
	hourGroupExpr := fmt.Sprintf("FLOOR((created_at + %d) / 3600)", tzOffset)

	query := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT %s as hour_group,
			COUNT(*) as request_count,
			COALESCE(SUM(quota), 0) as quota_used
		FROM logs
		WHERE created_at >= ? AND type = 2
		GROUP BY %s
		ORDER BY hour_group ASC`,
		hourGroupExpr, hourGroupExpr))

	rows, err := s.logDB.QueryWithTimeout(15*time.Second, query, startTime)
	if err != nil {
		return nil, err
	}

	rows = fillHourlyGaps(rows, hours, tzOffset)

	cm.Set(cacheKey, rows, 2*time.Minute)
	return rows, nil
}

func fillDailyGaps(rows []map[string]interface{}, days int, tzOffset int) []map[string]interface{} {
	now := time.Now()
	loc := now.Location()

	// Build lookup keyed by day_group integer
	lookup := make(map[int64]map[string]interface{}, len(rows))
	for _, row := range rows {
		group := toInt64(row["day_group"])
		if group > 0 {
			lookup[group] = row
		}
	}

	result := make([]map[string]interface{}, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		// Compute the same day_group as the SQL expression
		expectedGroup := (dayStart.Unix() + int64(tzOffset)) / 86400
		dateStr := dayStart.Format("2006-01-02")
		ts := dayStart.Unix()

		if existing, ok := lookup[expectedGroup]; ok {
			existing["date"] = dateStr
			existing["timestamp"] = ts
			delete(existing, "day_group")
			result = append(result, existing)
		} else {
			result = append(result, map[string]interface{}{
				"date":          dateStr,
				"timestamp":     ts,
				"request_count": int64(0),
				"quota_used":    int64(0),
				"unique_users":  int64(0),
			})
		}
	}
	return result
}

func fillHourlyGaps(rows []map[string]interface{}, hours int, tzOffset int) []map[string]interface{} {
	now := time.Now()
	loc := now.Location()

	// Build lookup keyed by hour_group integer
	lookup := make(map[int64]map[string]interface{}, len(rows))
	for _, row := range rows {
		group := toInt64(row["hour_group"])
		if group > 0 {
			lookup[group] = row
		}
	}

	result := make([]map[string]interface{}, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		t := now.Add(-time.Duration(i) * time.Hour)
		hourStart := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		// Compute the same hour_group as the SQL expression
		expectedGroup := (hourStart.Unix() + int64(tzOffset)) / 3600
		hourStr := hourStart.Format("2006-01-02 15:00")
		ts := hourStart.Unix()

		if existing, ok := lookup[expectedGroup]; ok {
			existing["hour"] = hourStr
			existing["timestamp"] = ts
			delete(existing, "hour_group")
			result = append(result, existing)
		} else {
			result = append(result, map[string]interface{}{
				"hour":          hourStr,
				"timestamp":     ts,
				"request_count": int64(0),
				"quota_used":    int64(0),
			})
		}
	}
	return result
}

// GetNativeDashboard returns the original resource and usage blocks that remain
// useful alongside the Beat business view. The 24h window drives the resource
// activity cards, while the daily series stays at 30 days for a stable trend.
func (s *DashboardService) GetNativeDashboard(period string, noCache bool) (map[string]interface{}, error) {
	cacheKey := fmt.Sprintf("dashboard:native:%s", period)
	cm := cache.Get()
	if !noCache {
		var cached map[string]interface{}
		if found, _ := cm.GetJSON(cacheKey, &cached); found {
			return cached, nil
		}
	}
	overview, err := s.GetSystemOverview(period, noCache)
	if err != nil {
		return nil, err
	}
	usage, err := s.GetUsageStatistics(period, noCache)
	if err != nil {
		return nil, err
	}
	models, err := s.GetModelUsage(period, 12, noCache)
	if err != nil {
		return nil, err
	}
	daily, err := s.GetDailyTrends(30, noCache)
	if err != nil {
		return nil, err
	}
	hourly, err := s.GetHourlyTrends(24, noCache)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		"overview": overview,
		"usage":    usage,
		"models":   models,
		"daily":    daily,
		"hourly":   hourly,
	}
	cm.Set(cacheKey, result, 2*time.Minute)
	return result, nil
}
