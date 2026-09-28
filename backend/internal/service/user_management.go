package service

import (
	"fmt"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// Activity level constants
const (
	ActivityActive       = "active"
	ActivityInactive     = "inactive"
	ActivityVeryInactive = "very_inactive"
	ActivityNever        = "never"

	ActiveThreshold   = 7 * 24 * 3600  // 7 days
	InactiveThreshold = 30 * 24 * 3600 // 30 days
)

// UserManagementService handles user queries and operations
type UserManagementService struct {
	db    *database.Manager
	logDB *database.Manager
}

// NewUserManagementService creates a new UserManagementService
func NewUserManagementService() *UserManagementService {
	return &UserManagementService{db: database.Get(), logDB: database.GetLog()}
}

// activeUserIDsSince returns the set of user_ids that have at least one billable
// log entry (type 2/5) since `since`. It queries the log DB directly, so it stays
// correct when logs live in a separate database (LOG_SQL_DSN) — a cross-DB
// EXISTS(...) subquery against the users table is impossible there.
func (s *UserManagementService) activeUserIDsSince(since int64) (map[int64]bool, error) {
	rows, err := s.logDB.QueryWithTimeout(60*time.Second, s.logDB.RebindQuery(
		"SELECT DISTINCT user_id FROM logs WHERE type IN (2,5) AND created_at >= ? AND user_id > 0"), since)
	if err != nil {
		return nil, err
	}
	set := make(map[int64]bool, len(rows))
	for _, r := range rows {
		set[toInt64(r["user_id"])] = true
	}
	return set, nil
}

// GetActivityStats returns user activity statistics
func (s *UserManagementService) GetActivityStats(quick bool) (map[string]interface{}, error) {
	now := time.Now().Unix()
	activeThreshold := now - ActiveThreshold
	inactiveThreshold := now - InactiveThreshold

	// Total users (not deleted)
	totalRow, err := s.db.QueryOne("SELECT COUNT(*) as count FROM users WHERE deleted_at IS NULL")
	if err != nil {
		return nil, err
	}
	totalUsers := totalRow["count"]

	if quick {
		// Quick mode: only total + never requested
		neverRow, _ := s.db.QueryOne(
			"SELECT COUNT(*) as count FROM users WHERE deleted_at IS NULL AND request_count = 0")
		neverCount := int64(0)
		if neverRow != nil {
			neverCount = toInt64(neverRow["count"])
		}
		return map[string]interface{}{
			"total_users":         totalUsers,
			"active_users":        0,
			"inactive_users":      0,
			"very_inactive_users": 0,
			"never_requested":     neverCount,
			"quick_mode":          true,
		}, nil
	}

	// Full stats: classify users by their most recent billable log.
	// Logs may live in a separate DB, so we can't use a cross-DB EXISTS subquery.
	// Instead: pull the active/recent user-id sets from the log DB, then count
	// against the users table in Go.
	activeSet, err := s.activeUserIDsSince(activeThreshold) // active in last 7d
	if err != nil {
		return nil, err
	}
	recentSet, err := s.activeUserIDsSince(inactiveThreshold) // active in last 30d
	if err != nil {
		return nil, err
	}

	// All non-deleted users that have ever made a request.
	requestedRows, err := s.db.Query("SELECT id FROM users WHERE deleted_at IS NULL AND request_count > 0")
	if err != nil {
		return nil, err
	}

	var activeCount, inactiveCount int64
	for _, r := range requestedRows {
		uid := toInt64(r["id"])
		switch {
		case activeSet[uid]:
			// last request within 7d
			activeCount++
		case recentSet[uid]:
			// last request within 7-30d (in recent set but not active set)
			inactiveCount++
		}
	}

	// Never requested
	neverRow, _ := s.db.QueryOne("SELECT COUNT(*) as count FROM users WHERE deleted_at IS NULL AND request_count = 0")
	neverCount := int64(0)
	if neverRow != nil {
		neverCount = toInt64(neverRow["count"])
	}

	total := toInt64(totalUsers)
	veryInactive := total - activeCount - inactiveCount - neverCount

	return map[string]interface{}{
		"total_users":         total,
		"active_users":        activeCount,
		"inactive_users":      inactiveCount,
		"very_inactive_users": veryInactive,
		"never_requested":     neverCount,
	}, nil
}

// GetUserGroups returns every distinct user group with its member count, for
// the user list's group filter.
func (s *UserManagementService) GetUserGroups() ([]map[string]interface{}, error) {
	groupCol := s.db.QuoteIdentifier("group")
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT COALESCE(%s, 'default') as group_name, COUNT(*) as user_count
		FROM users
		WHERE deleted_at IS NULL
		GROUP BY COALESCE(%s, 'default')
		ORDER BY user_count DESC`, groupCol, groupCol))
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		result = append(result, map[string]interface{}{
			"group_name": toString(row["group_name"]),
			"user_count": toInt64(row["user_count"]),
		})
	}
	return result, nil
}

// toInt64 safely converts interface{} to int64
func toInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case int32:
		return int64(val)
	case int16:
		return int64(val)
	case int8:
		return int64(val)
	case uint64:
		return int64(val)
	case uint:
		return int64(val)
	case uint32:
		return int64(val)
	case uint16:
		return int64(val)
	case uint8:
		return int64(val)
	case float64:
		return int64(val)
	case float32:
		return int64(val)
	case string:
		var n int64
		fmt.Sscanf(val, "%d", &n)
		return n
	case []byte:
		var n int64
		fmt.Sscanf(string(val), "%d", &n)
		return n
	default:
		return 0
	}
}

// toString safely converts interface{} to string
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// GetInvitedUsers returns users invited by the specified user
func (s *UserManagementService) GetInvitedUsers(userID int64, page, pageSize int) (map[string]interface{}, error) {
	offset := (page - 1) * pageSize

	// Get inviter info
	inviterRow, err := s.db.QueryOne(s.db.RebindQuery(
		"SELECT id, username, display_name, aff_code, aff_count, aff_quota, aff_history FROM users WHERE id = ? AND deleted_at IS NULL"), userID)
	if err != nil || inviterRow == nil {
		return map[string]interface{}{
			"inviter":   nil,
			"items":     []interface{}{},
			"total":     0,
			"page":      page,
			"page_size": pageSize,
			"stats":     map[string]interface{}{},
		}, nil
	}

	inviter := map[string]interface{}{
		"user_id":      inviterRow["id"],
		"username":     inviterRow["username"],
		"display_name": inviterRow["display_name"],
		"aff_code":     inviterRow["aff_code"],
		"aff_count":    inviterRow["aff_count"],
		"aff_quota":    inviterRow["aff_quota"],
		"aff_history":  inviterRow["aff_history"],
	}

	// Count total invited
	countRow, _ := s.db.QueryOne(s.db.RebindQuery(
		"SELECT COUNT(*) as total FROM users WHERE inviter_id = ? AND deleted_at IS NULL"), userID)
	total := int64(0)
	if countRow != nil {
		total = toInt64(countRow["total"])
	}

	// Get invited users list
	groupCol := "`group`"
	if s.db.IsPG {
		groupCol = `"group"`
	}
	query := s.db.RebindQuery(fmt.Sprintf(`
		SELECT id, username, display_name, email, status,
			quota, used_quota, request_count, %s, role
		FROM users
		WHERE inviter_id = ? AND deleted_at IS NULL
		ORDER BY id DESC
		LIMIT ? OFFSET ?`,
		groupCol))

	rows, err := s.db.Query(query, userID, pageSize, offset)
	if err != nil {
		return nil, err
	}

	// Compute stats
	activeCount := 0
	bannedCount := 0
	totalUsedQuota := int64(0)
	totalRequests := int64(0)
	for _, row := range rows {
		if toInt64(row["request_count"]) > 0 {
			activeCount++
		}
		if toInt64(row["status"]) == 2 {
			bannedCount++
		}
		totalUsedQuota += toInt64(row["used_quota"])
		totalRequests += toInt64(row["request_count"])
	}

	return map[string]interface{}{
		"inviter":   inviter,
		"items":     rows,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"stats": map[string]interface{}{
			"total_invited":    total,
			"active_count":     activeCount,
			"banned_count":     bannedCount,
			"total_used_quota": totalUsedQuota,
			"total_requests":   totalRequests,
		},
	}, nil
}
