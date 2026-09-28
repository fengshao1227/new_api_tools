package service

import (
	"fmt"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// WindowSeconds maps time window strings to seconds
var WindowSeconds = map[string]int64{
	"1h":  3600,
	"3h":  10800,
	"6h":  21600,
	"12h": 43200,
	"24h": 86400,
	"3d":  259200,
	"7d":  604800,
}

// IPMonitoringService handles IP analysis queries
type IPMonitoringService struct {
	db    *database.Manager
	logDB *database.Manager
}

const ipMonitoringQueryTimeout = 30 * time.Second

// NewIPMonitoringService creates a new IPMonitoringService
func NewIPMonitoringService() *IPMonitoringService {
	return &IPMonitoringService{db: database.Get(), logDB: database.GetLog()}
}

// GetIPStats returns IP recording statistics matching the Python format:
// {total_users, enabled_count, disabled_count, enabled_percentage, unique_ips_24h}
func (s *IPMonitoringService) GetIPStats() (map[string]interface{}, error) {
	// Query total users and those with IP recording enabled
	var userSQL string
	if s.db.IsPG {
		userSQL = `
			SELECT
				COUNT(*) as total_users,
				SUM(CASE
					WHEN setting IS NOT NULL AND setting <> ''
						 AND setting::jsonb->>'record_ip_log' = 'true' THEN 1
					ELSE 0
				END) as enabled_count
			FROM users
			WHERE deleted_at IS NULL`
	} else {
		userSQL = `
			SELECT
				COUNT(*) as total_users,
				SUM(CASE
					WHEN setting IS NOT NULL AND setting <> ''
						 AND JSON_EXTRACT(setting, '$.record_ip_log') = true THEN 1
					ELSE 0
				END) as enabled_count
			FROM users
			WHERE deleted_at IS NULL`
	}

	row, err := s.db.QueryOneWithTimeout(ipMonitoringQueryTimeout, userSQL)
	if err != nil {
		return map[string]interface{}{
			"total_users":        0,
			"enabled_count":      0,
			"disabled_count":     0,
			"enabled_percentage": 0.0,
			"unique_ips_24h":     0,
		}, nil
	}

	totalUsers := int64(0)
	enabledCount := int64(0)
	if row != nil {
		totalUsers = toInt64(row["total_users"])
		enabledCount = toInt64(row["enabled_count"])
	}
	disabledCount := totalUsers - enabledCount
	enabledPercentage := 0.0
	if totalUsers > 0 {
		enabledPercentage = float64(enabledCount) / float64(totalUsers) * 100
	}

	// Get unique IPs in last 24h
	startTime := time.Now().Unix() - 86400
	ipRow, _ := s.logDB.QueryOneWithTimeout(ipMonitoringQueryTimeout, s.logDB.RebindQuery(
		"SELECT COUNT(DISTINCT ip) as unique_ips FROM logs WHERE created_at >= ? AND ip IS NOT NULL AND ip <> ''"),
		startTime)
	uniqueIPs := int64(0)
	if ipRow != nil {
		uniqueIPs = toInt64(ipRow["unique_ips"])
	}

	return map[string]interface{}{
		"total_users":        totalUsers,
		"enabled_count":      enabledCount,
		"disabled_count":     disabledCount,
		"enabled_percentage": enabledPercentage,
		"unique_ips_24h":     uniqueIPs,
	}, nil
}

// LookupIPUsers finds all users/tokens using a specific IP
func (s *IPMonitoringService) LookupIPUsers(ip, window string, limit int, includeGeo bool) (map[string]interface{}, error) {
	seconds, ok := WindowSeconds[window]
	if !ok {
		seconds = 86400
	}
	startTime := time.Now().Unix() - seconds

	statsQuery := s.logDB.RebindQuery(`
		SELECT COUNT(*) as total_requests,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(DISTINCT token_id) as unique_tokens
		FROM logs
		WHERE created_at >= ? AND ip = ?`)
	statsRow, err := s.logDB.QueryOneWithTimeout(ipMonitoringQueryTimeout, statsQuery, startTime, ip)
	if err != nil {
		return nil, err
	}

	query := s.logDB.RebindQuery(`
		SELECT l.user_id, COALESCE(l.username, '') as username,
			l.token_id, COALESCE(l.token_name, '') as token_name,
			COUNT(*) as request_count,
			MIN(l.created_at) as first_seen, MAX(l.created_at) as last_seen
		FROM logs l
		WHERE l.created_at >= ? AND l.ip = ?
		GROUP BY l.user_id, l.username, l.token_id, l.token_name
			ORDER BY request_count DESC
			LIMIT ?`)

	rows, err := s.logDB.QueryWithTimeout(ipMonitoringQueryTimeout, query, startTime, ip, limit)
	if err != nil {
		return nil, err
	}

	totalRequests := toInt64(statsRow["total_requests"])
	uniqueUsers := toInt64(statsRow["unique_users"])
	uniqueTokens := toInt64(statsRow["unique_tokens"])

	// Get model usage for this IP
	modelQuery := s.logDB.RebindQuery(`
		SELECT model_name as model, COUNT(*) as count
		FROM logs
		WHERE created_at >= ? AND ip = ? AND model_name IS NOT NULL AND model_name <> ''
		GROUP BY model_name
		ORDER BY count DESC
		LIMIT 20`)
	modelRows, _ := s.logDB.QueryWithTimeout(ipMonitoringQueryTimeout, modelQuery, startTime, ip)
	if modelRows == nil {
		modelRows = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"ip":             ip,
		"items":          rows,
		"total":          len(rows),
		"window":         window,
		"total_requests": totalRequests,
		"unique_users":   uniqueUsers,
		"unique_tokens":  uniqueTokens,
		"models":         modelRows,
	}
	if includeGeo {
		result["geo"] = FormatIPGeoInfo(LookupIPGeo(ip))
	}
	return result, nil
}

// EnableAllIPRecording enables IP recording for all users by updating the setting JSON field
//
// 保留：网关风控依赖 logs.ip。删除这个每 10 分钟强制开启 record_ip_log 的任务之前，
// 必须先让网关默认开启 IP 记录，否则新用户的日志会丢失 IP。
func (s *IPMonitoringService) EnableAllIPRecording() (map[string]interface{}, error) {
	var updateSQL string
	if s.db.IsPG {
		updateSQL = `
			UPDATE users SET setting =
				CASE
					WHEN setting IS NULL OR setting = '' THEN '{"record_ip_log":true}'::jsonb::text
					ELSE (setting::jsonb || '{"record_ip_log":true}'::jsonb)::text
				END
			WHERE deleted_at IS NULL
			AND (setting IS NULL OR setting = '' OR setting::jsonb->>'record_ip_log' IS NULL OR setting::jsonb->>'record_ip_log' != 'true')`
	} else {
		updateSQL = `
			UPDATE users SET setting =
				CASE
					WHEN setting IS NULL OR setting = '' THEN '{"record_ip_log":true}'
					ELSE JSON_SET(setting, '$.record_ip_log', true)
				END
			WHERE deleted_at IS NULL
			AND (setting IS NULL OR setting = '' OR JSON_EXTRACT(setting, '$.record_ip_log') IS NULL OR JSON_EXTRACT(setting, '$.record_ip_log') != true)`
	}

	affected, err := s.db.Execute(updateSQL)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"affected": affected,
		"message":  fmt.Sprintf("已为 %d 个用户开启 IP 记录", affected),
	}, nil
}

// GetIPIndexStatus returns existing IP-related indexes and non-mutating recommendations.
func (s *IPMonitoringService) GetIPIndexStatus() (map[string]interface{}, error) {
	type indexSpec struct {
		Name        string
		Columns     []string
		Purpose     string
		Recommended bool
	}

	specs := []indexSpec{
		{
			Name:        "idx_logs_user_created_ip",
			Columns:     []string{"user_id", "created_at", "ip"},
			Purpose:     "用户 IP 列表和用户风险分析",
			Recommended: false,
		},
		{
			Name:        "idx_logs_created_token_ip",
			Columns:     []string{"created_at", "token_id", "ip"},
			Purpose:     "多 IP 令牌统计",
			Recommended: false,
		},
		{
			Name:        "idx_logs_created_ip_token",
			Columns:     []string{"created_at", "ip", "token_id"},
			Purpose:     "共享 IP 与窗口聚合",
			Recommended: false,
		},
		{
			Name:        "idx_logs_ip",
			Columns:     []string{"ip"},
			Purpose:     "精确 IP 反查基础过滤",
			Recommended: false,
		},
		{
			Name:        "idx_logs_ip_created_token_user",
			Columns:     []string{"ip", "created_at", "token_id", "user_id"},
			Purpose:     "高频 IP 反查建议索引，请在生产手动评估后创建",
			Recommended: true,
		},
	}

	existingNames := map[string]bool{}
	var query string
	if s.logDB.IsCH {
		query = `SELECT name FROM system.data_skipping_indices WHERE database = currentDatabase() AND table = 'logs'`
	} else if s.logDB.IsPG {
		query = `SELECT indexname as name FROM pg_indexes WHERE tablename = 'logs'`
	} else {
		query = `SELECT DISTINCT index_name as name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'logs'`
	}
	rows, err := s.logDB.QueryWithTimeout(10*time.Second, query)
	if err == nil {
		for _, row := range rows {
			name := toString(row["name"])
			if name != "" {
				existingNames[name] = true
			}
		}
	}

	items := make([]map[string]interface{}, 0, len(specs))
	existing := 0
	recommended := 0
	for _, spec := range specs {
		exists := existingNames[spec.Name]
		if exists {
			existing++
		}
		if spec.Recommended {
			recommended++
		}
		items = append(items, map[string]interface{}{
			"name":        spec.Name,
			"table":       "logs",
			"columns":     spec.Columns,
			"existing":    exists,
			"recommended": spec.Recommended,
			"auto_create": false,
			"purpose":     spec.Purpose,
		})
	}

	inspectionError := ""
	if err != nil {
		inspectionError = err.Error()
	}

	return map[string]interface{}{
		"indexes":           items,
		"total":             len(items),
		"existing":          existing,
		"recommended":       recommended,
		"auto_create":       false,
		"inspection_error":  inspectionError,
		"recommendation":    "新增重索引不会自动创建，请结合生产 EXPLAIN 与低峰期手动评估。",
		"has_status_source": err == nil,
	}, nil
}
