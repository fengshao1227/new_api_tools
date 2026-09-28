package service

import "fmt"

// The model status page's three readings of one model in one window, counted
// as the gateway counts what its customers got: served synchronous requests
// and task receipts (logs), finished tasks (tasks), and the synchronous
// requests no attempt served (error lines, attributed by ClassifyFailure).

// statusSlot is one time slot of one model, in requests.
type statusSlot struct {
	success, failure, userErrors, empty int64
}

func (slot *statusSlot) add(other statusSlot) {
	slot.success += other.success
	slot.failure += other.failure
	slot.userErrors += other.userErrors
	slot.empty += other.empty
}

// successRate is successes over successes plus counted failures, in percent;
// a slot nobody used reads 100.
func (slot statusSlot) successRate() (requests int64, rate float64) {
	requests = slot.success + slot.failure
	if requests == 0 {
		return 0, 100
	}
	return requests, float64(slot.success) / float64(requests) * 100
}

// modelStatusSlots reads one model's window into slots. A model that ran
// tasks in the window (a receipt or a finished task) is measured by its tasks
// alone, as the gateway's customer failure rate does; any other by its served
// requests and the requests no attempt served.
func (s *ModelStatusService) modelStatusSlots(modelName string, cfg timeWindowConfig, start, end int64) ([]statusSlot, error) {
	slots := make([]statusSlot, cfg.numSlots)
	served, receipts, err := s.loadServedRequests(modelName, start, end, cfg.slotSeconds, len(slots))
	if err != nil {
		return nil, err
	}
	finished, err := s.addTaskOutcomes(slots, modelName, start, end, cfg.slotSeconds)
	if err != nil || receipts > 0 || finished > 0 {
		return slots, err
	}
	for i := range slots {
		slots[i].add(served[i])
	}
	slotOf := func(ts int64) int {
		idx := (ts - start) / cfg.slotSeconds
		if idx < 0 || idx >= int64(len(slots)) {
			return -1
		}
		return int(idx)
	}
	if err := s.addFailedRequests(slots, modelName, start, end, slotOf); err != nil {
		return nil, err
	}
	return slots, nil
}

// loadServedRequests counts the model's served synchronous requests per slot
// (one consume line each; completion_tokens = 0 is reported as empty but was
// still served) and its task receipts in the window.
func (s *ModelStatusService) loadServedRequests(modelName string, start, end, slotSeconds int64, numSlots int) ([]statusSlot, int64, error) {
	slotExpr := fmt.Sprintf("FLOOR((created_at - %d) / %d)", start, slotSeconds)
	query := s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT %s AS slot_idx,
			SUM(CASE WHEN %s THEN 0 ELSE 1 END) AS served,
			SUM(CASE WHEN %s THEN 0 WHEN completion_tokens = 0 THEN 1 ELSE 0 END) AS empty_count,
			SUM(CASE WHEN %s THEN 1 ELSE 0 END) AS receipts
		FROM logs
		WHERE model_name = ? AND type = 2 AND created_at >= ? AND created_at < ?
		GROUP BY %s`, slotExpr, taskReceiptCond, taskReceiptCond, taskReceiptCond, slotExpr))
	rows, err := s.logDB.Query(query, taskReceiptLike, taskReceiptLike, taskReceiptLike, modelName, start, end)
	if err != nil {
		return nil, 0, fmt.Errorf("served request query failed: %w", err)
	}
	served := make([]statusSlot, numSlots)
	var receipts int64
	for _, row := range rows {
		receipts += toInt64(row["receipts"])
		idx := toInt64(row["slot_idx"])
		if idx >= 0 && idx < int64(numSlots) {
			served[idx].success = toInt64(row["served"])
			served[idx].empty = toInt64(row["empty_count"])
		}
	}
	return served, receipts, nil
}

// addTaskOutcomes adds the model's tasks that finished in the window, at the
// slot they finished in, and returns how many there were. A gateway without a
// tasks table has none.
func (s *ModelStatusService) addTaskOutcomes(slots []statusSlot, modelName string, start, end, slotSeconds int64) (int64, error) {
	slotExpr := fmt.Sprintf("FLOOR((finish_time - %d) / %d)", start, slotSeconds)
	query := s.db.RebindQuery(fmt.Sprintf(`
		SELECT %s AS slot_idx, status, COALESCE(fail_reason, '') AS reason, COUNT(*) AS n
		FROM tasks
		WHERE %s AND finish_time >= ? AND finish_time < ? AND %s = ?
		GROUP BY %s, status, fail_reason`,
		slotExpr, finishedTaskCond, jsonTextExpr(s.db, "properties", "origin_model_name"), slotExpr))
	rows, err := s.db.Query(query, start, end, modelName)
	if isMissingSchemaErr(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("task outcome query failed: %w", err)
	}
	var finished int64
	for _, row := range rows {
		n := toInt64(row["n"])
		finished += n
		idx := toInt64(row["slot_idx"])
		if idx < 0 || idx >= int64(len(slots)) {
			continue
		}
		switch {
		case toString(row["status"]) == "SUCCESS":
			slots[idx].success += n
		case ClassifyFailure(toString(row["reason"])).Counted():
			slots[idx].failure += n
		default:
			slots[idx].userErrors += n
		}
	}
	return finished, nil
}

// addFailedRequests adds the synchronous requests that no attempt served, at
// the slot of their last attempt, attributed by that attempt's failure.
func (s *ModelStatusService) addFailedRequests(slots []statusSlot, modelName string, start, end int64, slotOf func(int64) int) error {
	attempts, err := loadFailedAttempts(s.logDB, start, end, "model_name = ?", modelName)
	if err != nil {
		return fmt.Errorf("error log query failed: %w", err)
	}
	last := map[string]failedAttempt{}
	ids := make([]string, 0, len(attempts))
	for i, attempt := range attempts {
		key := attempt.RequestID
		if key == "" {
			key = fmt.Sprintf("line:%d", i)
		} else {
			ids = append(ids, key)
		}
		last[key] = attempt
	}
	accepted, err := acceptedRequests(s.logDB, ids, start)
	if err != nil {
		return fmt.Errorf("accepted request query failed: %w", err)
	}
	for key, attempt := range last {
		idx := slotOf(attempt.CreatedAt)
		if accepted[key] || idx < 0 {
			continue
		}
		if ClassifyFailure(attempt.Content).Counted() {
			slots[idx].failure++
		} else {
			slots[idx].userErrors++
		}
	}
	return nil
}
