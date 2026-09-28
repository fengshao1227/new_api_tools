package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/new-api-tools/backend/internal/database"
)

// The SQL half of failure attribution, shared by the channel monitor, the
// model status page and the business view. It follows the gateway's alert
// patrol (model/ops_alert.go ListOpsAlertLogs / ListOpsAlertFinishedTasks):
//
//   - logs type 5 is one failed attempt on one channel; every retry writes
//     its own line, on its own channel.
//   - logs type 2 is a served request — except a task's receipt
//     (other.is_task = true), which says the task was accepted, not that it
//     succeeded. A task's outcome is its row in tasks.
//   - tasks with status SUCCESS / FAILURE are finished; fail_reason is
//     attributed by the same ClassifyFailure as an error line.
//
// The verdict itself — whose failure — is ClassifyFailure in Go: the gateway
// decides it with regular expressions and by unwrapping nested JSON, which no
// portable SQL LIKE can reproduce. SQL selects the rows; Go attributes them.

// taskReceiptLike matches a task's receipt in logs.other: bind it for the ?
// in taskReceiptCond / syncSuccessCond. Same spelling as the gateway's own
// patrol query.
const taskReceiptLike = `%"is_task":true%`

// taskReceiptCond is true for a consume line that is a task's receipt.
const taskReceiptCond = "COALESCE(other, '') LIKE ?"

// syncSuccessCond selects consume lines that are a served request.
const syncSuccessCond = "type = 2 AND NOT (" + taskReceiptCond + ")"

// failedAttemptCond selects error lines.
const failedAttemptCond = "type = 5"

// finishedTaskCond selects tasks that have an outcome.
const finishedTaskCond = "status IN ('SUCCESS', 'FAILURE')"

const (
	// failureTextChars bounds how much of a stored failure is read: the
	// verdict never needs more, and some upstreams answer with whole pages.
	failureTextChars = 2000
	// failedAttemptLimit bounds one read of error lines; production writes a
	// few hundred a day.
	failedAttemptLimit = 50000
	requestIDBatch     = 500
)

// failedAttempt is one error line.
type failedAttempt struct {
	CreatedAt int64
	ChannelID int64
	Model     string
	RequestID string
	Username  string
	Content   string
}

// loadFailedAttempts reads the error lines written in [since, until), oldest
// first. extraCond (with its args) narrows them further, e.g. to one model.
func loadFailedAttempts(logDB *database.Manager, since, until int64, extraCond string, extraArgs ...interface{}) ([]failedAttempt, error) {
	cond := ""
	if extraCond != "" {
		cond = " AND " + extraCond
	}
	query := fmt.Sprintf(`
		SELECT created_at, COALESCE(channel_id, 0) AS channel_id, COALESCE(model_name, '') AS model_name,
			COALESCE(request_id, '') AS request_id, COALESCE(username, '') AS username,
			SUBSTR(COALESCE(content, ''), 1, %d) AS content
		FROM logs
		WHERE %s AND created_at >= ? AND created_at < ?%s
		ORDER BY created_at, id
		LIMIT %d`, failureTextChars, failedAttemptCond, cond, failedAttemptLimit)
	args := append([]interface{}{since, until}, extraArgs...)
	rows, err := logDB.QueryWithTimeout(businessQueryTimeout, logDB.RebindQuery(query), args...)
	if err != nil {
		return nil, err
	}
	out := make([]failedAttempt, 0, len(rows))
	for _, r := range rows {
		out = append(out, failedAttempt{
			CreatedAt: toInt64(r["created_at"]),
			ChannelID: toInt64(r["channel_id"]),
			Model:     toString(r["model_name"]),
			RequestID: toString(r["request_id"]),
			Username:  toString(r["username"]),
			Content:   toString(r["content"]),
		})
	}
	return out, nil
}

// queryTasksWithModel runs a tasks query built around the requested model
// (properties.origin_model_name): build gets the select expression and the
// ", <expr>" to append to GROUP BY. properties the engine cannot read as JSON
// cost the model split, not the query; a missing tasks table is returned as
// the error for isMissingSchemaErr.
func queryTasksWithModel(db *database.Manager, build func(modelSelect, modelGroup string) string, args ...interface{}) ([]map[string]interface{}, error) {
	model := jsonTextExpr(db, "properties", "origin_model_name")
	rows, err := db.QueryWithTimeout(businessQueryTimeout, db.RebindQuery(build("COALESCE("+model+", '')", ", "+model)), args...)
	if err != nil && !isMissingSchemaErr(err) {
		rows, err = db.QueryWithTimeout(businessQueryTimeout, db.RebindQuery(build("''", "")), args...)
	}
	return rows, err
}

// acceptedRequests returns which of requestIDs some channel accepted: a
// consume line (a served request, or a task's receipt) under the same request
// id. A consume line is written when the request finishes, so it is never
// older than the failures before it and since bounds the scan.
func acceptedRequests(logDB *database.Manager, requestIDs []string, since int64) (map[string]bool, error) {
	accepted := map[string]bool{}
	seen := map[string]bool{}
	ids := make([]string, 0, len(requestIDs))
	for _, id := range requestIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for start := 0; start < len(ids); start += requestIDBatch {
		batch := ids[start:min(start+requestIDBatch, len(ids))]
		args := make([]interface{}, 0, len(batch)+1)
		args = append(args, since)
		for _, id := range batch {
			args = append(args, id)
		}
		query := fmt.Sprintf(`SELECT DISTINCT request_id FROM logs WHERE type = 2 AND created_at >= ? AND request_id IN (%s)`,
			strings.TrimSuffix(strings.Repeat("?,", len(batch)), ","))
		rows, err := logDB.QueryWithTimeout(businessQueryTimeout, logDB.RebindQuery(query), args...)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			accepted[toString(r["request_id"])] = true
		}
	}
	return accepted, nil
}

// loadCappedChannels returns the channels with a task concurrency cap
// (channels.setting max_concurrent_tasks > 0). A 429 from one of them is the
// gateway's queue at work. A gateway without the column has no caps.
func loadCappedChannels(db *database.Manager) (map[int64]bool, error) {
	capped := map[int64]bool{}
	rows, err := db.QueryWithTimeout(businessQueryTimeout, db.RebindQuery(
		`SELECT id, COALESCE(setting, '') AS setting FROM channels WHERE setting LIKE ?`), "%max_concurrent_tasks%")
	if isMissingSchemaErr(err) {
		return capped, nil
	}
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		var setting struct {
			MaxConcurrentTasks int `json:"max_concurrent_tasks"`
		}
		if json.Unmarshal([]byte(toString(r["setting"])), &setting) == nil && setting.MaxConcurrentTasks > 0 {
			capped[toInt64(r["id"])] = true
		}
	}
	return capped, nil
}

// attributeAttempts attributes error lines as the gateway's channel tally
// does: each line on its own channel, a parameter refusal overruled when the
// same request was accepted elsewhere, a capped channel's 429 set aside.
func attributeAttempts(logDB *database.Manager, attempts []failedAttempt, capped map[int64]bool, since int64) ([]FailureClass, error) {
	classes := make([]FailureClass, len(attempts))
	memo := map[string]FailureClass{}
	refused := []string{}
	for i, attempt := range attempts {
		class, ok := memo[attempt.Content]
		if !ok {
			class = ClassifyFailure(attempt.Content)
			memo[attempt.Content] = class
		}
		classes[i] = class
		if class.Category == FailureCatInvalidRequest && attempt.RequestID != "" {
			refused = append(refused, attempt.RequestID)
		}
	}
	accepted, err := acceptedRequests(logDB, refused, since)
	if err != nil {
		return nil, err
	}
	for i, attempt := range attempts {
		classes[i] = AttributeAttempt(classes[i], accepted[attempt.RequestID], capped[attempt.ChannelID])
	}
	return classes, nil
}

// failureTally counts outcomes by attribution. Only channel-side failures
// are errors; user-side ones and a full queue are counted beside them.
type failureTally struct {
	Success           int64            `json:"success"`
	Errors            int64            `json:"errors"`
	UserErrors        int64            `json:"user_errors"`
	QueueFull         int64            `json:"queue_full"`
	ChannelCategories map[string]int64 `json:"channel_categories"`
	UserCategories    map[string]int64 `json:"user_categories"`
}

func newFailureTally() failureTally {
	return failureTally{ChannelCategories: map[string]int64{}, UserCategories: map[string]int64{}}
}

func (t *failureTally) addFailure(class FailureClass, n int64) {
	switch class.Side {
	case FailureSideUser:
		t.UserErrors += n
		t.UserCategories[class.Category] += n
	case FailureSideQueue:
		t.QueueFull += n
	default:
		t.Errors += n
		t.ChannelCategories[class.Category] += n
	}
}

func (t *failureTally) merge(other failureTally) {
	t.Success += other.Success
	t.Errors += other.Errors
	t.UserErrors += other.UserErrors
	t.QueueFull += other.QueueFull
	for k, v := range other.ChannelCategories {
		t.ChannelCategories[k] += v
	}
	for k, v := range other.UserCategories {
		t.UserCategories[k] += v
	}
}

// attempts is every outcome, counted or not.
func (t failureTally) attempts() int64 {
	return t.Success + t.Errors + t.UserErrors + t.QueueFull
}

// errorRate is channel-side failures over success plus channel-side
// failures, in percent: the caller's own failures are in neither.
func (t failureTally) errorRate() float64 {
	return roundRate(ratio(t.Errors, t.Success+t.Errors) * 100)
}

// Health thresholds are the gateway's channel alert (ops_alert_setting.go
// defaults): at least 5 counted attempts before judging, red from 80 %
// failures (the "channel failing" alert line), and not healthy again until
// below 20 % (its recovery line), which is yellow here.
const (
	healthMinSamples = 5
	healthRedPct     = 80
	healthYellowPct  = 20
)

// Health levels.
const (
	HealthGreen  = "green"
	HealthYellow = "yellow"
	HealthRed    = "red"
	// HealthIdle is too few counted attempts to judge: one failure out of
	// one is not a dead channel.
	HealthIdle = "idle"
)

func healthLevel(success, errors int64) string {
	counted := success + errors
	switch {
	case counted < healthMinSamples:
		return HealthIdle
	case errors*100 >= healthRedPct*counted:
		return HealthRed
	case errors*100 >= healthYellowPct*counted:
		return HealthYellow
	default:
		return HealthGreen
	}
}
