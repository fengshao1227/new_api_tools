package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

var ErrInsightsUserNotFound = errors.New("user not found")

const insightsTimeout = 30 * time.Second

type UserInsightsService struct {
	db    *database.Manager
	logDB *database.Manager
}

func NewUserInsightsService() *UserInsightsService {
	return &UserInsightsService{db: database.Get(), logDB: database.GetLog()}
}

// insightsQuery always uses the caller's cancellable context, including probes
// and page counts. SQL interpolations are fixed column names, never user input.
func insightsQuery(ctx context.Context, db *database.Manager, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.DB.QueryxContext(ctx, db.RebindQuery(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return nil, err
		}
		for key, value := range row {
			if b, ok := value.([]byte); ok {
				row[key] = string(b)
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func insightsProbe(ctx context.Context, db *database.Manager, query string) (bool, error) {
	_, err := insightsQuery(ctx, db, query)
	if isMissingSchemaErr(err) {
		return false, nil
	}
	return err == nil, err
}

func insightsInt(value any) *int64 {
	if value == nil {
		return nil
	}
	n := toInt64(value)
	return &n
}

func insightsTime(value any) *int64 {
	n := insightsInt(value)
	if n == nil || *n <= 0 {
		return nil
	}
	return n
}

func insightsString(value any) *string {
	if value == nil {
		return nil
	}
	s := strings.TrimSpace(toString(value))
	if s == "" {
		return nil
	}
	return &s
}

func (s *UserInsightsService) requireUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return ErrInsightsUserNotFound
	}
	var id int64
	err := s.db.DB.GetContext(ctx, &id, s.db.RebindQuery("SELECT id FROM users WHERE id = ? AND deleted_at IS NULL"), userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInsightsUserNotFound
	}
	return err
}

func (p UserInsightsParams) logWhere() (string, []any) {
	where := "user_id = ? AND created_at >= ? AND created_at < ? AND type IN (2,5,6)"
	args := []any{p.UserID, p.Window.StartTime, p.Window.EndTime}
	if p.Window.Model != "" {
		where += " AND model_name = ?"
		args = append(args, p.Window.Model)
	}
	switch p.Type {
	case "consume":
		where += " AND type = 2"
	case "error":
		where += " AND type = 5"
	case "refund":
		where += " AND type = 6"
	}
	return where, args
}

func (s *UserInsightsService) Report(ctx context.Context, p UserInsightsParams) (*UserInsightsReport, error) {
	ctx, cancel := context.WithTimeout(ctx, insightsTimeout)
	defer cancel()
	result := &UserInsightsReport{Window: p.Window, Models: []UserInsightsModel{}, Daily: []UserInsightsDay{},
		Availability: UserInsightsAvailability{Warnings: []string{}}}
	if err := s.loadInsightsProfile(ctx, p.UserID, result); err != nil {
		return nil, err
	}
	if err := s.loadInsightsPayments(ctx, p, result); err != nil {
		return nil, err
	}
	if err := s.loadInsightsUsage(ctx, p, result); err != nil {
		return nil, err
	}
	tasks, available, err := s.insightsTaskSummary(ctx, p)
	if err != nil {
		return nil, err
	}
	result.Tasks, result.Availability.Tasks = tasks, available
	if !available {
		result.Availability.Warnings = append(result.Availability.Warnings, "tasks_unavailable")
	}
	if s.logDB == s.db && s.db.Config != nil && s.db.Config.HasSeparateLogDB() {
		result.Availability.Warnings = append(result.Availability.Warnings, "log_database_fallback_may_be_stale")
	}
	return result, nil
}

// SQL numeric JSON extraction is deliberately dialect-specific. Missing or
// nonnumeric values stay NULL, preserving unknown vs explicitly recorded zero.
func insightsJSONNumber(db *database.Manager, column, field string) string {
	if db.IsCH {
		raw := fmt.Sprintf("JSONExtractRaw(%s, '%s')", column, field)
		return fmt.Sprintf("if(isValidJSON(%s) AND match(%s, '^[0-9]{1,15}$'), toInt64OrNull(%s), NULL)", column, raw, raw)
	}
	if db.IsPG {
		base := fmt.Sprintf("(CASE WHEN pg_input_is_valid(CAST(%s AS TEXT), 'jsonb') THEN CAST(%s AS TEXT) ELSE '{}' END)::jsonb", column, column)
		return insightsPGJSONNumber(base, field)
	}
	if strings.Contains(db.DB.DriverName(), "sqlite") {
		return fmt.Sprintf("CASE WHEN json_valid(%s) THEN CASE WHEN json_type(%s, '$.%s') = 'integer' AND json_extract(%s, '$.%s') BETWEEN 0 AND 999999999999999 THEN json_extract(%s, '$.%s') END END", column, column, field, column, field, column, field)
	}
	return fmt.Sprintf("CASE WHEN JSON_VALID(%s) THEN CASE WHEN JSON_TYPE(JSON_EXTRACT(%s, '$.%s')) = 'INTEGER' AND JSON_UNQUOTE(JSON_EXTRACT(%s, '$.%s')) REGEXP '^[0-9]{1,15}$' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(%s, '$.%s')) AS DECIMAL(30,0)) END END", column, column, field, column, field, column, field)
}

// The base may be a materialized, already validated jsonb column. Keeping this
// extraction separate avoids reparsing the complete metadata for every field.
func insightsPGJSONNumber(base, field string) string {
	return fmt.Sprintf("CASE WHEN jsonb_typeof(%s->'%s') = 'number' AND (%s->>'%s') ~ '^[0-9]{1,15}$' THEN (%s->>'%s')::bigint END", base, field, base, field, base, field)
}

func insightsPGJSONText(base, field string) string {
	if field == "claude" {
		return fmt.Sprintf("CASE WHEN jsonb_typeof(%s->'claude') = 'boolean' THEN %s->>'claude' END", base, base)
	}
	return fmt.Sprintf("(%s->>'%s')", base, field)
}

func insightsJSONText(db *database.Manager, column, field string) string {
	if db.IsCH {
		if field == "claude" {
			return fmt.Sprintf("if(JSONExtractBool(%s, 'claude'), 'true', '')", column)
		}
		return fmt.Sprintf("JSONExtractString(%s, '%s')", column, field)
	}
	if db.IsPG {
		base := fmt.Sprintf("(CASE WHEN pg_input_is_valid(CAST(%s AS TEXT), 'jsonb') THEN CAST(%s AS TEXT) ELSE '{}' END)::jsonb", column, column)
		return insightsPGJSONText(base, field)
	}
	if strings.Contains(db.DB.DriverName(), "sqlite") {
		if field == "claude" {
			return fmt.Sprintf("CASE WHEN json_valid(%s) THEN CASE WHEN json_type(%s, '$.claude') = 'true' THEN 'true' END END", column, column)
		}
		return fmt.Sprintf("CASE WHEN json_valid(%s) THEN CAST(json_extract(%s, '$.%s') AS TEXT) END", column, column, field)
	}
	if field == "claude" {
		return fmt.Sprintf("CASE WHEN JSON_VALID(%s) THEN CASE WHEN JSON_TYPE(JSON_EXTRACT(%s, '$.claude')) = 'BOOLEAN' THEN JSON_UNQUOTE(JSON_EXTRACT(%s, '$.claude')) END END", column, column, column)
	}
	return fmt.Sprintf("CASE WHEN JSON_VALID(%s) THEN JSON_UNQUOTE(JSON_EXTRACT(%s, '$.%s')) END", column, column, field)
}
