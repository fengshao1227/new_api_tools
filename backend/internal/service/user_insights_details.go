package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (s *UserInsightsService) Logs(ctx context.Context, p UserInsightsParams) (*UserInsightsPage[UserInsightsLog], error) {
	ctx, cancel := context.WithTimeout(ctx, insightsTimeout)
	defer cancel()
	if err := s.requireUser(ctx, p.UserID); err != nil {
		return nil, err
	}
	result := &UserInsightsPage[UserInsightsLog]{Items: []UserInsightsLog{}, Page: p.Page, PageSize: p.PageSize, Warnings: []string{}}
	available, err := insightsProbe(ctx, s.logDB, "SELECT id,user_id,created_at,type,model_name,quota,prompt_tokens,completion_tokens,token_id,token_name,channel_id,use_time,is_stream,ip,content FROM logs WHERE 1=0")
	if err != nil {
		return nil, err
	}
	if !available {
		result.Warnings = append(result.Warnings, "logs_unavailable")
		return result, nil
	}
	columns := "id,created_at,type,model_name,quota,prompt_tokens,completion_tokens,token_id,token_name,channel_id,use_time,is_stream,ip,SUBSTR(content,1,2000) AS diagnostic"
	for _, column := range []string{"request_id", "cost", "other"} {
		ok, err := insightsProbe(ctx, s.logDB, "SELECT "+column+" FROM logs WHERE 1=0")
		if err != nil {
			return nil, err
		}
		if !ok {
			columns += ",NULL AS " + column
			continue
		}
		if column == "other" {
			columns += ",SUBSTR(other,1,65536) AS other"
		} else {
			columns += "," + column
		}
	}
	where, args := p.logWhere()
	count, err := insightsQuery(ctx, s.logDB, "SELECT COUNT(*) AS n FROM logs WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	result.Total, result.Available = toInt64(count[0]["n"]), true
	rows, err := insightsQuery(ctx, s.logDB, "SELECT "+columns+" FROM logs WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", append(args, p.PageSize, (p.Page-1)*p.PageSize)...)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result.Items = append(result.Items, insightsLog(r))
		// Invalid historical metadata and a bounded/truncated metadata read both
		// leave only the raw prompt count. Callers must not mistake that fallback
		// for a complete normalized input or measured zero cache usage.
		if raw := strings.TrimSpace(toString(r["other"])); raw != "" && !json.Valid([]byte(raw)) && len(result.Warnings) == 0 {
			result.Warnings = append(result.Warnings, "log_token_details_unavailable")
		}
	}
	return result, nil
}

func insightsJSONInt(value any) *int64 {
	number, ok := value.(json.Number)
	if !ok {
		return nil
	}
	n, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || n < 0 || n > 999999999999999 {
		return nil
	}
	return &n
}

func insightsLog(r map[string]any) UserInsightsLog {
	l := UserInsightsLog{ID: toInt64(r["id"]), CreatedAt: toInt64(r["created_at"]), Type: toInt64(r["type"]), ModelName: toString(r["model_name"]),
		RequestID: insightsString(r["request_id"]), TokenID: toInt64(r["token_id"]), TokenName: toString(r["token_name"]), ChannelID: toInt64(r["channel_id"]),
		PromptTokens: toInt64(r["prompt_tokens"]), CompletionTokens: toInt64(r["completion_tokens"]), Quota: toInt64(r["quota"]),
		Cost: insightsInt(r["cost"]), UseTime: toInt64(r["use_time"]), IP: toString(r["ip"])}
	l.InputTokens = l.PromptTokens
	l.IsStream = r["is_stream"] == true || toInt64(r["is_stream"]) == 1
	l.QuotaUSD = marginMoney(float64(l.Quota))
	if l.Cost != nil {
		cost := marginMoney(float64(*l.Cost))
		l.CostUSD = &cost
	}
	switch l.Type {
	case 2:
		l.Content = "消费记录"
	case 6:
		l.Content = "退款流水"
	case 5:
		l.Content = string(ClassifyFailure(toString(r["diagnostic"])).Category)
	}
	var other map[string]any
	raw := toString(r["other"])
	if !json.Valid([]byte(raw)) {
		return l
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&other) != nil {
		return l
	}
	l.CacheReadTokens = insightsJSONInt(other["cache_tokens"])
	l.CacheWriteTokens = insightsJSONInt(other["cache_write_tokens"])
	if l.CacheWriteTokens == nil {
		creation, five, hour := insightsJSONInt(other["cache_creation_tokens"]), insightsJSONInt(other["cache_creation_tokens_5m"]), insightsJSONInt(other["cache_creation_tokens_1h"])
		if creation != nil || five != nil || hour != nil {
			total := int64(0)
			if five != nil {
				total += *five
			}
			if hour != nil {
				total += *hour
			}
			if creation != nil && *creation > total {
				total = *creation
			}
			l.CacheWriteTokens = &total
		}
	}
	if input := insightsJSONInt(other["input_tokens_total"]); input != nil {
		l.InputTokens = *input
	} else if other["usage_semantic"] == "anthropic" || other["claude"] == true {
		if l.CacheReadTokens != nil {
			l.InputTokens += *l.CacheReadTokens
		}
		if l.CacheWriteTokens != nil {
			l.InputTokens += *l.CacheWriteTokens
		}
	}
	l.TaskID = insightsString(other["task_id"])
	l.IsTask = other["is_task"] == true
	if admin, ok := other["admin_info"].(map[string]any); ok {
		l.VoidedQuota = insightsJSONInt(admin["voided_quota"])
		if admin["unpriced"] == true {
			l.Cost, l.CostUSD = nil, nil
		}
	}
	return l
}

func (s *UserInsightsService) insightsTaskWhere(ctx context.Context, p UserInsightsParams) (string, []any, string, bool, error) {
	available, err := insightsProbe(ctx, s.db, "SELECT id,task_id,user_id,platform,action,status,progress,submit_time,start_time,finish_time,quota,fail_reason FROM tasks WHERE 1=0")
	if err != nil || !available {
		return "", nil, "", false, err
	}
	properties, err := insightsProbe(ctx, s.db, "SELECT properties FROM tasks WHERE 1=0")
	if err != nil {
		return "", nil, "", false, err
	}
	model := "NULL"
	if properties {
		model = jsonTextExpr(s.db, "properties", "origin_model_name")
		if !s.db.IsPG {
			model = insightsJSONText(s.db, "properties", "origin_model_name")
		}
	}
	if !properties && p.Window.Model != "" {
		return "", nil, "", false, nil
	}
	where := "user_id = ? AND submit_time >= ? AND submit_time < ?"
	args := []any{p.UserID, p.Window.StartTime, p.Window.EndTime}
	if p.Window.Model != "" {
		where += " AND " + model + " = ?"
		args = append(args, p.Window.Model)
	}
	if p.Status == "active" {
		where += " AND status NOT IN ('SUCCESS','FAILURE')"
	} else if p.Status != "" && p.Status != "all" {
		where += " AND status = ?"
		args = append(args, p.Status)
	}
	return where, args, model, true, nil
}

func (s *UserInsightsService) insightsTaskSummary(ctx context.Context, p UserInsightsParams) (*UserInsightsTaskTotals, bool, error) {
	where, args, _, available, err := s.insightsTaskWhere(ctx, p)
	if err != nil || !available {
		return nil, false, err
	}
	rows, err := insightsQuery(ctx, s.db, `SELECT COUNT(*) AS total,
		COALESCE(SUM(CASE WHEN status='SUCCESS' THEN 1 ELSE 0 END),0) AS success,
		COALESCE(SUM(CASE WHEN status='FAILURE' THEN 1 ELSE 0 END),0) AS failed
		FROM tasks WHERE `+where, args...)
	if err != nil {
		return nil, false, err
	}
	r := rows[0]
	result := &UserInsightsTaskTotals{Total: toInt64(r["total"]), Success: toInt64(r["success"]), Failed: toInt64(r["failed"])}
	result.InProgress = result.Total - result.Success - result.Failed
	return result, true, nil
}

func (s *UserInsightsService) Tasks(ctx context.Context, p UserInsightsParams) (*UserInsightsPage[UserInsightsTask], error) {
	ctx, cancel := context.WithTimeout(ctx, insightsTimeout)
	defer cancel()
	if err := s.requireUser(ctx, p.UserID); err != nil {
		return nil, err
	}
	result := &UserInsightsPage[UserInsightsTask]{Items: []UserInsightsTask{}, Page: p.Page, PageSize: p.PageSize, Warnings: []string{}}
	where, args, model, available, err := s.insightsTaskWhere(ctx, p)
	if err != nil {
		return nil, err
	}
	if !available {
		result.Warnings = append(result.Warnings, "tasks_unavailable")
		return result, nil
	}
	counts, err := insightsQuery(ctx, s.db, "SELECT COUNT(*) AS n FROM tasks WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	result.Total, result.Available = toInt64(counts[0]["n"]), true
	query := fmt.Sprintf(`SELECT id,task_id,platform,%s AS model_name,action,status,progress,submit_time,start_time,finish_time,quota,
		SUBSTR(fail_reason,1,2000) AS diagnostic FROM tasks WHERE %s ORDER BY submit_time DESC,id DESC LIMIT ? OFFSET ?`, model, where)
	rows, err := insightsQuery(ctx, s.db, query, append(args, p.PageSize, (p.Page-1)*p.PageSize)...)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		item := UserInsightsTask{ID: toInt64(r["id"]), TaskID: toString(r["task_id"]), Platform: toString(r["platform"]), ModelName: insightsString(r["model_name"]),
			Action: toString(r["action"]), Status: toString(r["status"]), Progress: toString(r["progress"]), SubmitTime: toInt64(r["submit_time"]),
			StartTime: toInt64(r["start_time"]), FinishTime: toInt64(r["finish_time"]), Quota: toInt64(r["quota"])}
		item.QuotaUSD = marginMoney(float64(item.Quota))
		if item.Status == "FAILURE" {
			item.FailReason = string(ClassifyFailure(toString(r["diagnostic"])).Category)
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}
