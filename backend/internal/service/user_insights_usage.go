package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// insightsUsageSQL normalizes token facts before SQL aggregation. We never sum
// billing_tokens.p/c: those exclude categories based on the pricing expression.
func (s *UserInsightsService) insightsUsageSQL(ctx context.Context, p UserInsightsParams) (string, []any, bool, error) {
	other, err := insightsProbe(ctx, s.logDB, "SELECT other FROM logs WHERE 1=0")
	if err != nil {
		return "", nil, false, err
	}
	if other && s.logDB.IsPG {
		_, err := insightsQuery(ctx, s.logDB, "SELECT pg_input_is_valid('{}', 'jsonb')")
		if err != nil {
			// PostgreSQL before 16 cannot validate arbitrary legacy JSON safely
			// in portable SQL. Preserve numeric columns and mark detail unknown.
			if strings.Contains(err.Error(), "42883") || strings.Contains(err.Error(), "function pg_input_is_valid") {
				other = false
			} else {
				return "", nil, false, err
			}
		}
	}
	columns := "id,created_at,type,model_name,quota,prompt_tokens,completion_tokens"
	if other {
		for _, field := range []string{"cache_tokens", "cache_write_tokens", "cache_creation_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h", "input_tokens_total"} {
			columns += "," + insightsJSONNumber(s.logDB, "other", field) + " AS " + field
		}
		columns += "," + insightsJSONText(s.logDB, "other", "usage_semantic") + " AS usage_semantic"
		columns += "," + insightsJSONText(s.logDB, "other", "claude") + " AS claude_semantic"
	} else {
		columns += ",NULL AS cache_tokens,NULL AS cache_write_tokens,NULL AS cache_creation_tokens,NULL AS cache_creation_tokens_5m,NULL AS cache_creation_tokens_1h,NULL AS input_tokens_total,NULL AS usage_semantic,NULL AS claude_semantic"
	}
	where, args := p.logWhere()
	base := "SELECT " + columns + " FROM logs WHERE " + where
	split := "(COALESCE(cache_creation_tokens_5m,0)+COALESCE(cache_creation_tokens_1h,0))"
	write := fmt.Sprintf(`CASE WHEN cache_write_tokens IS NOT NULL THEN cache_write_tokens
		WHEN cache_creation_tokens IS NULL AND cache_creation_tokens_5m IS NULL AND cache_creation_tokens_1h IS NULL THEN NULL
		WHEN COALESCE(cache_creation_tokens,0) > %s THEN cache_creation_tokens ELSE %s END`, split, split)
	cache := "SELECT source_rows.*, " + write + " AS cache_write FROM (" + base + ") source_rows"
	input := `CASE WHEN input_tokens_total IS NOT NULL THEN input_tokens_total
		WHEN usage_semantic = 'anthropic' OR claude_semantic IN ('true','1')
		THEN prompt_tokens + COALESCE(cache_tokens,0) + COALESCE(cache_write,0) ELSE prompt_tokens END`
	return "(SELECT cache_rows.*, " + input + " AS input_total FROM (" + cache + ") cache_rows) usage_rows", args, other, nil
}

const insightsMetricColumns = `
	COALESCE(SUM(CASE WHEN type=2 THEN 1 ELSE 0 END),0) AS billing_records,
	COALESCE(SUM(CASE WHEN type=5 THEN 1 ELSE 0 END),0) AS error_records,
	COALESCE(SUM(CASE WHEN type=6 THEN 1 ELSE 0 END),0) AS refund_records,
	COALESCE(SUM(CASE WHEN type=2 THEN quota ELSE 0 END),0) AS charged_quota,
	COALESCE(SUM(CASE WHEN type=6 THEN quota ELSE 0 END),0) AS refund_quota,
	COALESCE(SUM(CASE WHEN type=2 THEN prompt_tokens ELSE 0 END),0) AS raw_prompt_tokens,
	COALESCE(SUM(CASE WHEN type=2 THEN input_total ELSE 0 END),0) AS input_tokens,
	COALESCE(SUM(CASE WHEN type=2 THEN completion_tokens ELSE 0 END),0) AS output_tokens,
	SUM(CASE WHEN type=2 THEN cache_tokens ELSE NULL END) AS cache_read_tokens,
	SUM(CASE WHEN type=2 THEN cache_write ELSE NULL END) AS cache_write_tokens,
	COALESCE(SUM(CASE WHEN type=2 AND cache_tokens IS NOT NULL THEN 1 ELSE 0 END),0) AS cache_read_records,
	COALESCE(SUM(CASE WHEN type=2 AND cache_write IS NOT NULL THEN 1 ELSE 0 END),0) AS cache_write_records,
	COALESCE(SUM(CASE WHEN type=2 AND (cache_tokens IS NOT NULL OR cache_write IS NOT NULL OR input_tokens_total IS NOT NULL) THEN 1 ELSE 0 END),0) AS token_details_recorded,
	COUNT(DISTINCT NULLIF(model_name,'')) AS models_count`

func insightsMetrics(r map[string]any) UserInsightsMetrics {
	m := UserInsightsMetrics{
		BillingRecords: toInt64(r["billing_records"]), ErrorRecords: toInt64(r["error_records"]), RefundRecords: toInt64(r["refund_records"]),
		ChargedQuota: toInt64(r["charged_quota"]), RefundQuota: toInt64(r["refund_quota"]),
		RawPromptTokens: toInt64(r["raw_prompt_tokens"]), InputTokens: toInt64(r["input_tokens"]), OutputTokens: toInt64(r["output_tokens"]),
		CacheReadRecords: toInt64(r["cache_read_records"]), CacheWriteRecords: toInt64(r["cache_write_records"]),
		TokenDetailsRecorded: toInt64(r["token_details_recorded"]), ModelsCount: toInt64(r["models_count"]),
	}
	if m.CacheReadRecords > 0 {
		m.CacheReadTokens = insightsInt(r["cache_read_tokens"])
	}
	if m.CacheWriteRecords > 0 {
		m.CacheWriteTokens = insightsInt(r["cache_write_tokens"])
	}
	m.ChargedUSD, m.RefundUSD = marginMoney(float64(m.ChargedQuota)), marginMoney(float64(m.RefundQuota))
	return m
}

func (s *UserInsightsService) loadInsightsUsage(ctx context.Context, p UserInsightsParams, out *UserInsightsReport) error {
	ok, err := insightsProbe(ctx, s.logDB, "SELECT id,user_id,created_at,type,model_name,quota,prompt_tokens,completion_tokens FROM logs WHERE 1=0")
	if err != nil {
		return err
	}
	if !ok {
		out.Availability.Warnings = append(out.Availability.Warnings, "logs_unavailable")
		return nil
	}
	from, args, details, err := s.insightsUsageSQL(ctx, p)
	if err != nil {
		return err
	}
	rows, err := insightsQuery(ctx, s.logDB, "SELECT "+insightsMetricColumns+",MIN(CASE WHEN type IN (2,5) THEN created_at END) AS first_record,MAX(CASE WHEN type IN (2,5) THEN created_at END) AS last_record,COUNT(DISTINCT CASE WHEN type IN (2,5) THEN FLOOR(created_at / 86400.0) END) AS active_days FROM "+from, args...)
	if err != nil {
		return fmt.Errorf("usage aggregate: %w", err)
	}
	m := insightsMetrics(rows[0])
	out.Summary = &m
	out.Activity = &UserInsightsActivity{FirstRecordAt: insightsTime(rows[0]["first_record"]), LastRecordAt: insightsTime(rows[0]["last_record"]), ActiveDays: toInt64(rows[0]["active_days"])}
	where, args := p.logWhere()
	latest, err := insightsQuery(ctx, s.logDB, "SELECT model_name FROM logs WHERE "+where+" AND type IN (2,5) ORDER BY created_at DESC,id DESC LIMIT 1", args...)
	if err != nil {
		return err
	}
	if len(latest) > 0 {
		out.Activity.LastModel = insightsString(latest[0]["model_name"])
	}
	models, err := insightsQuery(ctx, s.logDB, "SELECT COALESCE(model_name,'') AS model_name,"+insightsMetricColumns+",MAX(created_at) AS last_record FROM "+from+" GROUP BY model_name ORDER BY charged_quota DESC,model_name", args...)
	if err != nil {
		return err
	}
	for _, row := range models {
		out.Models = append(out.Models, UserInsightsModel{ModelName: toString(row["model_name"]), UserInsightsMetrics: insightsMetrics(row), LastRecordAt: insightsTime(row["last_record"])})
	}
	days, err := insightsQuery(ctx, s.logDB, "SELECT FLOOR(created_at / 86400.0) AS day_bucket,"+insightsMetricColumns+" FROM "+from+" GROUP BY FLOOR(created_at / 86400.0) ORDER BY day_bucket", args...)
	if err != nil {
		return err
	}
	for _, row := range days {
		out.Daily = append(out.Daily, UserInsightsDay{Date: time.Unix(toInt64(row["day_bucket"])*86400, 0).UTC().Format("2006-01-02"), UserInsightsMetrics: insightsMetrics(row)})
	}
	out.Availability.Logs, out.Availability.TokenDetails = true, details
	if !details {
		out.Availability.Warnings = append(out.Availability.Warnings, "token_details_unavailable")
	}
	out.Availability.Warnings = append(out.Availability.Warnings, "reasoning_tokens_not_recorded")
	return nil
}
