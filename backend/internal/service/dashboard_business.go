package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/database"
)

// The Beat business view: one page that joins what the gateway's own console
// keeps in separate tables — money, conversion, gift credit, risk holds, task
// health, supplier accounts and pricing gaps — for one time window. It does not
// repeat the gateway's detail pages; every section is a summary with enough
// breakdown to know where to look next.
//
// Every section is its own endpoint and its own query set, so a slow or
// missing table costs one card, not the page. Tables that only exist on the
// Beat fork of new-api (risk_events, upstream_monitors, ops_alert_states) and
// tables some deployments never create (tasks) degrade to "no data" instead of
// failing: see isMissingSchemaErr.

// Dashboard window keys accepted by the business endpoints.
const (
	DashboardWindowToday = "today"
	DashboardWindow7d    = "7d"
	DashboardWindow30d   = "30d"
)

const businessQueryTimeout = 30 * time.Second

// DashboardWindow is a calendar-aligned range ending now: today, or the last
// 7 / 30 local days including today. Calendar alignment keeps every daily
// series in a section the same length as the window it claims to cover.
type DashboardWindow struct {
	Key   string `json:"key"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Days  int    `json:"days"`
}

// IsDashboardWindow reports whether key names a supported window.
func IsDashboardWindow(key string) bool {
	switch key {
	case DashboardWindowToday, DashboardWindow7d, DashboardWindow30d:
		return true
	}
	return false
}

// ResolveDashboardWindow turns a window key into a range ending at now. An
// unknown key is the 7-day window.
func ResolveDashboardWindow(key string, now time.Time) DashboardWindow {
	days := 7
	switch key {
	case DashboardWindowToday:
		days = 1
	case DashboardWindow30d:
		days = 30
	default:
		key = DashboardWindow7d
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := midnight.AddDate(0, 0, -(days - 1))
	return DashboardWindow{Key: key, Start: start.Unix(), End: now.Unix(), Days: days}
}

// BusinessDashboardService serves the business sections. db is the main
// database, logDB the one holding `logs` (the same one unless LOG_SQL_DSN
// splits them).
type BusinessDashboardService struct {
	db    *database.Manager
	logDB *database.Manager
}

// NewBusinessDashboardService binds the service to the configured databases.
func NewBusinessDashboardService() *BusinessDashboardService {
	return &BusinessDashboardService{db: database.Get(), logDB: database.GetLog()}
}

// isMissingSchemaErr recognises "this table / column does not exist" on the
// three engines the tool meets (PostgreSQL 42P01/42703, MySQL 1146/1054 and
// the SQLite used by tests). It matches the error codes rather than a phrase
// like "does not exist", which PostgreSQL also uses for a missing function or
// operator — a query bug, not an absent table. Anything else is a real
// failure and is returned.
func isMissingSchemaErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"no such table", "no such column", // SQLite
		"error 1146", "error 1054", // MySQL: table, column
		"sqlstate 42p01", "sqlstate 42703", // PostgreSQL: table, column
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// jsonTextExpr extracts a top-level JSON string field as text in each
// dialect's own spelling — the same three spellings the gateway uses for
// tasks.properties.
func jsonTextExpr(db *database.Manager, column, field string) string {
	switch {
	case db.IsPG:
		return fmt.Sprintf("(%s->>'%s')", column, field)
	case db.DB != nil && strings.Contains(db.DB.DriverName(), "sqlite"):
		return fmt.Sprintf("json_extract(%s, '$.%s')", column, field)
	default:
		return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '$.%s'))", column, field)
	}
}

// whitelistAnd returns " AND <column> NOT IN (...)" for the panel whitelist
// (internal accounts, admins by default) plus its arguments, or nothing.
func whitelistAnd(column string) (string, []interface{}) {
	cond, args := PanelWhitelistNotInClause(column)
	if cond == "" {
		return "", nil
	}
	return " AND " + cond, args
}

// businessCached reads key from the cache into dest, or runs load, stores its
// result and copies it into dest.
func businessCached[T any](key string, ttl time.Duration, noCache bool, load func() (T, error)) (T, error) {
	cm := cache.Get()
	if !noCache {
		var cached T
		if found, _ := cm.GetJSON(key, &cached); found {
			return cached, nil
		}
	}
	value, err := load()
	if err != nil {
		return value, err
	}
	cm.Set(key, value, ttl)
	return value, nil
}

func ratio(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

// truncateRunes cuts s to at most n runes, marking the cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
