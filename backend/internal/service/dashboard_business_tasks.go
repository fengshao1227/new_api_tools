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
// origin_model_name). A task still running is neither success nor failure;
// in-flight counts are shown on their own. Refunds are logs.type = 6, the
// credit handed back for failed tasks.
//
// Failures are attributed the gateway's way (ClassifyFailure): a refused
// prompt, an unusable input image or a parameter the model does not accept is
// the customer's own failure. Those are counted as user failures and left out
// of the failure rate entirely — out of the failures and out of the finished
// tasks it divides by, as the gateway's customer failure rate does.

// BusinessTaskRow is one platform / model pair.
type BusinessTaskRow struct {
	Platform    string  `json:"platform"`
	Model       string  `json:"model"`
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Failure     int64   `json:"failure"`
	UserFailure int64   `json:"user_failure"`
	InFlight    int64   `json:"in_flight"`
	FailureRate float64 `json:"failure_rate"`
}

// BusinessTaskReason is one normalised failure reason.
type BusinessTaskReason struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// BusinessTasks is the task-health section of the business view. Failure and
// Reasons are the channel / upstream side; UserFailure and UserReasons the
// customer's own.
type BusinessTasks struct {
	Window      DashboardWindow      `json:"window"`
	Available   bool                 `json:"available"`
	Total       int64                `json:"total"`
	Success     int64                `json:"success"`
	Failure     int64                `json:"failure"`
	UserFailure int64                `json:"user_failure"`
	InFlight    int64                `json:"in_flight"`
	FailureRate float64              `json:"failure_rate"`
	Rows        []BusinessTaskRow    `json:"rows"`
	Reasons     []BusinessTaskReason `json:"reasons"`
	UserReasons []BusinessTaskReason `json:"user_reasons"`
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
	return businessCached("dashboard:beat:tasks:v2:"+w.Key, 3*time.Minute, noCache, func() (BusinessTasks, error) {
		result := BusinessTasks{Window: w, Rows: []BusinessTaskRow{}, Reasons: []BusinessTaskReason{}, UserReasons: []BusinessTaskReason{}}
		rows, err := s.loadTaskRows(w)
		if isMissingSchemaErr(err) {
			return s.withRefunds(result, w)
		}
		if err != nil {
			return result, err
		}
		result.Available = true
		failures, err := s.loadTaskFailures(w)
		if err != nil {
			return result, err
		}
		channelReasons, userReasons := splitTaskFailures(rows, failures)
		foldTaskRows(&result, rows, businessTaskRowLimit)
		result.Reasons = topFailReasons(channelReasons, businessTaskReasonLimit)
		result.UserReasons = topFailReasons(userReasons, businessTaskReasonLimit)
		return s.withRefunds(result, w)
	})
}

func (s *BusinessDashboardService) loadTaskRows(w DashboardWindow) ([]BusinessTaskRow, error) {
	counts := `COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status = 'SUCCESS' THEN 1 ELSE 0 END), 0) AS success,
			COALESCE(SUM(CASE WHEN status = 'FAILURE' THEN 1 ELSE 0 END), 0) AS failure`
	rows, err := queryTasksWithModel(s.db, func(modelSelect, modelGroup string) string {
		return fmt.Sprintf(`SELECT COALESCE(platform, '') AS platform, %s AS model, %s
			FROM tasks WHERE created_at >= ? AND created_at <= ?
			GROUP BY platform%s`, modelSelect, counts, modelGroup)
	}, w.Start, w.End)
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

// taskFailureGroup is the failed tasks of one platform / model sharing one
// fail_reason.
type taskFailureGroup struct {
	Platform, Model, Reason string
	Count                   int64
}

func (s *BusinessDashboardService) loadTaskFailures(w DashboardWindow) ([]taskFailureGroup, error) {
	rows, err := queryTasksWithModel(s.db, func(modelSelect, modelGroup string) string {
		return fmt.Sprintf(`SELECT COALESCE(platform, '') AS platform, %s AS model,
				COALESCE(fail_reason, '') AS reason, COUNT(*) AS n
			FROM tasks WHERE status = 'FAILURE' AND created_at >= ? AND created_at <= ?
			GROUP BY platform, fail_reason%s`, modelSelect, modelGroup)
	}, w.Start, w.End)
	if err != nil {
		return nil, fmt.Errorf("task failure reason query failed: %w", err)
	}
	out := make([]taskFailureGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, taskFailureGroup{
			Platform: toString(r["platform"]), Model: toString(r["model"]),
			Reason: toString(r["reason"]), Count: toInt64(r["n"]),
		})
	}
	return out, nil
}

// splitTaskFailures moves the customer's own failures out of each row's
// Failure into UserFailure, and returns the normalised reasons of both sides.
func splitTaskFailures(rows []BusinessTaskRow, failures []taskFailureGroup) (channelReasons, userReasons map[string]int64) {
	index := make(map[string]int, len(rows))
	for i, r := range rows {
		index[r.Platform+"\x00"+r.Model] = i
	}
	channelReasons, userReasons = map[string]int64{}, map[string]int64{}
	for _, f := range failures {
		reason := normalizeFailReason(f.Reason)
		if ClassifyFailure(f.Reason).Counted() {
			channelReasons[reason] += f.Count
			continue
		}
		userReasons[reason] += f.Count
		if i, ok := index[f.Platform+"\x00"+f.Model]; ok {
			moved := min(f.Count, rows[i].Failure)
			rows[i].Failure -= moved
			rows[i].UserFailure += moved
		}
	}
	return channelReasons, userReasons
}

// foldTaskRows fills totals and keeps the rows that matter most: the most
// channel-side failures first, then the busiest.
func foldTaskRows(result *BusinessTasks, rows []BusinessTaskRow, limit int) {
	for i := range rows {
		r := &rows[i]
		r.InFlight = r.Total - r.Success - r.Failure - r.UserFailure
		r.FailureRate = ratio(r.Failure, r.Success+r.Failure)
		result.Total += r.Total
		result.Success += r.Success
		result.Failure += r.Failure
		result.UserFailure += r.UserFailure
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

func topFailReasons(counts map[string]int64, limit int) []BusinessTaskReason {
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
