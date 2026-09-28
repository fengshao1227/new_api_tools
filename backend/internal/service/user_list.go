package service

import (
	"fmt"
	"strings"
	"sync"

	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/logger"
)

// ListUsersParams defines parameters for listing users
type ListUsersParams struct {
	Page           int    `json:"page"`
	PageSize       int    `json:"page_size"`
	ActivityFilter string `json:"activity_filter"`
	GroupFilter    string `json:"group_filter"`
	SourceFilter   string `json:"source_filter"`
	Search         string `json:"search"`
	OrderBy        string `json:"order_by"`
	OrderDir       string `json:"order_dir"`
}

// userLoginColumn is one of new-api's built-in OAuth id columns on users.
type userLoginColumn struct{ column, source string }

// userLoginColumns are the built-in OAuth columns the list reads. Providers
// configured in the console (GitHub, Google on Beat) are not columns: they
// live in user_oauth_bindings, see userOAuthSlugs.
var userLoginColumns = []userLoginColumn{
	{"github_id", "github"}, {"wechat_id", "wechat"}, {"telegram_id", "telegram"},
	{"discord_id", "discord"}, {"oidc_id", "oidc"},
}

var builtinLoginLabels = map[string]string{
	"github": "GitHub", "wechat": "微信", "telegram": "Telegram", "discord": "Discord", "oidc": "OIDC",
}

// oauthBindingExists is the correlated sub-query "the account has a custom
// OAuth binding", with room for one more condition.
const oauthBindingExists = `EXISTS (SELECT 1 FROM user_oauth_bindings b JOIN custom_oauth_providers p ON p.id = b.provider_id WHERE b.user_id = u.id%s)`

// userListSchema is which optional gateway tables and columns this database
// has; older gateways lack the Beat signup columns and the OAuth bindings.
type userListSchema struct {
	loginColumns  []userLoginColumn
	bindings      bool
	signupCountry bool
	grantRegion   bool
	grantedQuota  bool
	topupQuota    bool
}

type schemaProbeKey struct {
	db   *database.Manager
	name string
}

var schemaProbes sync.Map

// schemaAvailable runs a zero-row probe once per database and remembers
// whether what it names exists. A failure that is not "no such table/column"
// (the database being down, say) is not remembered.
func schemaAvailable(db *database.Manager, name, probe string) bool {
	key := schemaProbeKey{db: db, name: name}
	if known, ok := schemaProbes.Load(key); ok {
		return known.(bool)
	}
	_, err := db.Query(probe)
	if err != nil && !isMissingSchemaErr(err) {
		logWarn(fmt.Sprintf("schema probe %s failed: %v", name, err))
		return false
	}
	schemaProbes.Store(key, err == nil)
	return err == nil
}

// userColumnAvailable probes one users column; column is always a constant.
func userColumnAvailable(db *database.Manager, column string) bool {
	return schemaAvailable(db, "users."+column, fmt.Sprintf("SELECT %s FROM users WHERE 1 = 0", column))
}

func oauthBindingsAvailable(db *database.Manager) bool {
	return schemaAvailable(db, "user_oauth_bindings",
		"SELECT b.user_id, p.slug FROM user_oauth_bindings b JOIN custom_oauth_providers p ON p.id = b.provider_id WHERE 1 = 0")
}

// logWarn logs through the app logger when it is initialised (it is not in
// unit tests).
func logWarn(msg string) {
	if logger.L != nil {
		logger.L.Warn(msg)
	}
}

func (s *UserManagementService) userListSchema() userListSchema {
	schema := userListSchema{
		bindings:      oauthBindingsAvailable(s.db),
		signupCountry: userColumnAvailable(s.db, "signup_country"),
		grantRegion:   userColumnAvailable(s.db, "grant_region"),
		grantedQuota:  userColumnAvailable(s.db, "granted_quota"),
		topupQuota:    userColumnAvailable(s.db, "topup_quota"),
	}
	for _, c := range userLoginColumns {
		if userColumnAvailable(s.db, c.column) {
			schema.loginColumns = append(schema.loginColumns, c)
		}
	}
	return schema
}

func normalizeListUsersParams(params ListUsersParams) ListUsersParams {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}
	allowedOrderBy := map[string]bool{"id": true, "username": true, "request_count": true, "quota": true, "used_quota": true}
	if !allowedOrderBy[params.OrderBy] {
		params.OrderBy = "request_count"
	}
	params.OrderDir = strings.ToUpper(params.OrderDir)
	if params.OrderDir != "ASC" {
		params.OrderDir = "DESC"
	}
	return params
}

// GetUsers returns a page of users, each with whether it paid, its unspent
// free credit, signup country and grant rung, the gateway's risk verdict and
// every way it can sign in.
func (s *UserManagementService) GetUsers(params ListUsersParams) (map[string]interface{}, error) {
	params = normalizeListUsersParams(params)
	schema := s.userListSchema()
	where, args := s.userListWhere(params, schema)

	countRow, err := s.db.QueryOne(s.db.RebindQuery("SELECT COUNT(*) AS count FROM users u WHERE "+where), args...)
	if err != nil {
		return nil, err
	}
	total := int64(0)
	if countRow != nil {
		total = toInt64(countRow["count"])
	}

	offset := (params.Page - 1) * params.PageSize
	query := s.db.RebindQuery(fmt.Sprintf("SELECT %s FROM users u WHERE %s ORDER BY u.%s %s LIMIT ? OFFSET ?",
		s.userListColumns(schema), where, params.OrderBy, params.OrderDir))
	rows, err := s.db.Query(query, append(args, params.PageSize, offset)...)
	if err != nil {
		logWarn(fmt.Sprintf("GetUsers 查询失败: %v", err))
		return nil, err
	}
	if rows == nil {
		rows = []map[string]interface{}{}
	}
	riskAvailable, err := s.enrichUserRows(rows, schema)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"items":                    rows,
		"total":                    total,
		"page":                     params.Page,
		"page_size":                params.PageSize,
		"total_pages":              int((total + int64(params.PageSize) - 1) / int64(params.PageSize)),
		"risk_available":           riskAvailable,
		"oauth_bindings_available": schema.bindings,
	}, nil
}

// userListWhere builds the list filter with ? placeholders.
func (s *UserManagementService) userListWhere(params ListUsersParams, schema userListSchema) (string, []interface{}) {
	where := []string{"u.deleted_at IS NULL"}
	args := []interface{}{}

	if params.Search == "" {
		// 全局面板白名单：用户列表默认隐藏（按用户名/邮箱精确找人仍可用）
		if cond, wlArgs := PanelWhitelistNotInClause("u.id"); cond != "" {
			where = append(where, cond)
			args = append(args, wlArgs...)
		}
	} else {
		like := "LIKE"
		if s.db.IsPG {
			like = "ILIKE"
		}
		pattern := "%" + params.Search + "%"
		fields := []string{"u.username", "COALESCE(u.display_name, '')", "COALESCE(u.email, '')", "COALESCE(u.aff_code, '')"}
		parts := make([]string, len(fields))
		for i, field := range fields {
			parts[i] = field + " " + like + " ?"
			args = append(args, pattern)
		}
		where = append(where, "("+strings.Join(parts, " OR ")+")")
	}
	if params.GroupFilter != "" {
		where = append(where, fmt.Sprintf("u.%s = ?", s.db.QuoteIdentifier("group")))
		args = append(args, params.GroupFilter)
	}
	if params.ActivityFilter == ActivityNever {
		where = append(where, "u.request_count = 0")
	}
	if cond, condArgs := loginSourceCondition(params.SourceFilter, schema); cond != "" {
		where = append(where, cond)
		args = append(args, condArgs...)
	}
	return strings.Join(where, " AND "), args
}

// loginSourceCondition filters by a way of signing in: a built-in OAuth
// column, a console-configured provider slug (either may be "github"), or
// "password" for accounts with neither.
func loginSourceCondition(filter string, schema userListSchema) (string, []interface{}) {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return "", nil
	}
	if filter == "password" {
		parts := []string{}
		for _, c := range schema.loginColumns {
			parts = append(parts, fmt.Sprintf("(u.%s IS NULL OR u.%s = '')", c.column, c.column))
		}
		if schema.bindings {
			parts = append(parts, "NOT "+fmt.Sprintf(oauthBindingExists, ""))
		}
		if len(parts) == 0 {
			return "", nil
		}
		return "(" + strings.Join(parts, " AND ") + ")", nil
	}
	parts := []string{}
	args := []interface{}{}
	for _, c := range schema.loginColumns {
		if c.source == filter {
			parts = append(parts, fmt.Sprintf("(u.%s IS NOT NULL AND u.%s <> '')", c.column, c.column))
		}
	}
	if schema.bindings {
		parts = append(parts, fmt.Sprintf(oauthBindingExists, " AND LOWER(p.slug) = ?"))
		args = append(args, filter)
	}
	if len(parts) == 0 {
		return "1 = 0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (s *UserManagementService) userListColumns(schema userListSchema) string {
	groupCol := s.db.QuoteIdentifier("group")
	cols := []string{
		"u.id", "u.username", "u.display_name", "u.email", "u.role", "u.status", "u.quota",
		"u.used_quota", "u.request_count", "u." + groupCol, "u.aff_code", "u.remark",
	}
	for _, c := range schema.loginColumns {
		cols = append(cols, "u."+c.column)
	}
	cols = append(cols,
		optionalColumn(schema.signupCountry, "u.signup_country", "''", "signup_country"),
		optionalColumn(schema.grantRegion, "u.grant_region", "''", "grant_region"),
		optionalColumn(schema.grantedQuota, "u.granted_quota", "0", "granted_quota"),
		optionalColumn(schema.topupQuota, "u.topup_quota", "0", "topup_quota"),
	)
	return strings.Join(cols, ", ")
}

func optionalColumn(available bool, expr, fallback, alias string) string {
	if available {
		return fmt.Sprintf("COALESCE(%s, %s) AS %s", expr, fallback, alias)
	}
	return fallback + " AS " + alias
}
