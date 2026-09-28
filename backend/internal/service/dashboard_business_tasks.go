package service

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Async task health: image/video/music jobs from the gateway's `tasks` table,
// by platform and requested model (tasks keep the model in properties JSON,
// origin_model_name). Failure rate is failures over finished tasks — a task
// still running is neither — and in-flight counts are shown on their own.
// Refunds are logs.type = 6, the credit handed back for failed tasks.

// BusinessTaskRow is one platform / model pair.
type BusinessTaskRow struct {
	Platform    string  `json:"platform"`
	Model       string  `json:"model"`
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Failure     int64   `json:"failure"`
	InFlight    int64   `json:"in_flight"`
	FailureRate float64 `json:"failure_rate"`
}

// BusinessTaskReason is one normalised failure reason.
type BusinessTaskReason struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// BusinessTasks is the task-health section of the business view.
type BusinessTasks struct {
	Window      DashboardWindow      `json:"window"`
	Available   bool                 `json:"available"`
	Total       int64                `json:"total"`
	Success     int64                `json:"success"`
	Failure     int64                `json:"failure"`
	InFlight    int64                `json:"in_flight"`
	FailureRate float64              `json:"failure_rate"`
	Rows        []BusinessTaskRow    `json:"rows"`
	Reasons     []BusinessTaskReason `json:"reasons"`
	RefundCount int64                `json:"refund_count"`
	RefundUSD   float64              `json:"refund_usd"`
}

const (
	businessTaskRowLimit    = 15
	businessTaskReasonLimit = 8
	businessTaskReasonRunes = 90
)

var (
	failReasonUUID   = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	failReasonToken  = regexp.MustCompile(`[A-Za-z0-9_\-]{24,}`)
	failReasonDigits = regexp.MustCompile(`\d{3,}`)
	failReasonSpace  = regexp.MustCompile(`\s+`)
)

// normalizeFailReason folds reasons that differ only by ids, numbers or
// whitespace into one bucket, cut to a readable length.
func normalizeFailReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "(empty)"
	}
	reason = failReasonUUID.ReplaceAllString(reason, "<id>")
	reason = failReasonToken.ReplaceAllString(reason, "<id>")
	reason = failReasonDigits.ReplaceAllString(reason, "#")
	reason = failReasonSpace.ReplaceAllString(reason, " ")
	return truncateRunes(reason, businessTaskReasonRunes)
}

// GetTasks returns task outcomes, failure reasons and refunds for a window.
func (s *BusinessDashboardService) GetTasks(window string, noCache bool) (BusinessTasks, error) {
	w := ResolveDashboardWindow(window, time.Now())
	return businessCached("dashboard:beat:tasks:"+w.Key, 3*time.Minute, noCache, func() (BusinessTasks, error) {
		result := BusinessTasks{Window: w, Rows: []BusinessTaskRow{}, Reasons: []BusinessTaskReason{}}
		rows, err := s.loadTaskRows(w)
		if isMissingSchemaErr(err) {
			return s.withRefunds(result, w)
		}
		if err != nil {
			return result, err
		}
		result.Available = true
		foldTaskRows(&result, rows, businessTaskRowLimit)
		reasons, err := s.loadTaskReasons(w)
		if err != nil {
			return result, err
		}
		result.Reasons = reasons
		return s.withRefunds(result, w)
	})
}

func (s *BusinessDashboardService) loadTaskRows(w DashboardWindow) ([]BusinessTaskRow, error) {
	counts := `COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 'SUCCESS' THEN 1 ELSE 0 END), 0) AS success,
			COALESCE(SUM(CASE WHEN status = 'FAILURE' THEN 1 ELSE 0 END), 0) AS failure`
	model := jsonTextExpr(s.db, "properties", "origin_model_name")
	query := fmt.Sprintf(`SELECT COALESCE(platform, '') AS platform, COALESCE(%s, '') AS model, %s
		FROM tasks WHERE created_at >= ? AND created_at <= ?
		GROUP BY platform, %s`, model, counts, model)
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(query), w.Start, w.End)
	if err != nil && !isMissingSchemaErr(err) {
		// properties that the engine cannot read as JSON: keep the platform
		// split rather than lose the section.
		query = fmt.Sprintf(`SELECT COALESCE(platform, '') AS platform, '' AS model, %s
			FROM tasks WHERE created_at >= ? AND created_at <= ?
			GROUP BY platform`, counts)
		rows, err = s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(query), w.Start, w.End)
	}
	if err != nil {
		return nil, err
	}
	out := make([]BusinessTaskRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, BusinessTaskRow{
			Platform: toString(r["platform"]), Model: toString(r["model"]),
			Total: toInt64(r["total"]), Success: toInt64(r["success"]), Failure: toInt64(r["failure"]),
		})
	}
	return out, nil
}

// foldTaskRows fills totals and keeps the rows that matter most: the most
// failures first, then the busiest.
func foldTaskRows(result *BusinessTasks, rows []BusinessTaskRow, limit int) {
	for i := range rows {
		r := &rows[i]
		r.InFlight = r.Total - r.Success - r.Failure
		r.FailureRate = ratio(r.Failure, r.Success+r.Failure)
		result.Total += r.Total
		result.Success += r.Success
		result.Failure += r.Failure
		result.InFlight += r.InFlight
	}
	result.FailureRate = ratio(result.Failure, result.Success+result.Failure)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Failure != rows[j].Failure {
			return rows[i].Failure > rows[j].Failure
		}
		if rows[i].Total != rows[j].Total {
			return rows[i].Total > rows[j].Total
		}
		return rows[i].Platform+rows[i].Model < rows[j].Platform+rows[j].Model
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	result.Rows = rows
}

func (s *BusinessDashboardService) loadTaskReasons(w DashboardWindow) ([]BusinessTaskReason, error) {
	rows, err := s.db.QueryWithTimeout(businessQueryTimeout, s.db.RebindQuery(`
		SELECT COALESCE(fail_reason, '') AS reason, COUNT(*) AS n
		FROM tasks WHERE status = 'FAILURE' AND created_at >= ? AND created_at <= ?
		GROUP BY fail_reason
		ORDER BY n DESC
		LIMIT 200`), w.Start, w.End)
	if err != nil {
		return nil, fmt.Errorf("task failure reason query failed: %w", err)
	}
	return foldFailReasons(rows, businessTaskReasonLimit), nil
}

func foldFailReasons(rows []map[string]interface{}, limit int) []BusinessTaskReason {
	counts := map[string]int64{}
	for _, r := range rows {
		counts[normalizeFailReason(toString(r["reason"]))] += toInt64(r["n"])
	}
	out := make([]BusinessTaskReason, 0, len(counts))
	for reason, n := range counts {
		out = append(out, BusinessTaskReason{Reason: reason, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *BusinessDashboardService) withRefunds(result BusinessTasks, w DashboardWindow) (BusinessTasks, error) {
	row, err := s.logDB.QueryOneWithTimeout(businessQueryTimeout, s.logDB.RebindQuery(`
		SELECT COUNT(*) AS n, COALESCE(SUM(quota), 0) AS quota
		FROM logs WHERE type = 6 AND created_at >= ? AND created_at <= ?`), w.Start, w.End)
	if err != nil {
		return result, fmt.Errorf("refund query failed: %w", err)
	}
	result.RefundCount = toInt64(row["n"])
	result.RefundUSD = marginMoney(toFloat64(row["quota"]))
	return result, nil
}
