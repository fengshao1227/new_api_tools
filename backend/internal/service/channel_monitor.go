package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// ChannelMonitorService 渠道健康监控：余额/性能/错误率/能力矩阵。
// 只读 channels/abilities/logs/tasks 表；绝不查询 channels.key（渠道密钥）。
//
// 错误率口径与网关一致（见 failure_attribution.go）：按「尝试」算，每次尝试
// 记在各自渠道上；只有渠道/上游侧失败进分子，用户侧失败（参数、内容审核、
// 素材打不开、用户余额、客户先断开）和有并发上限渠道的 429（排队）既不进
// 分子也不进分母，单独列出。任务按 tasks 终态算，任务回执不算成功。
type ChannelMonitorService struct {
	db    *database.Manager
	logDB *database.Manager
}

func NewChannelMonitorService() *ChannelMonitorService {
	return &ChannelMonitorService{db: database.Get(), logDB: database.GetLog()}
}

const channelMonitorCacheTTL = time.Minute

// GetChannelOverview 返回全部渠道的运营字段（不含密钥）
func (s *ChannelMonitorService) GetChannelOverview() ([]map[string]interface{}, error) {
	groupCol := s.db.QuoteIdentifier("group")
	query := fmt.Sprintf(`
		SELECT id, COALESCE(name, '') as name, type, status,
			COALESCE(priority, 0) as priority,
			COALESCE(weight, 0) as weight,
			COALESCE(balance, 0) as balance,
			COALESCE(balance_updated_time, 0) as balance_updated_time,
			COALESCE(response_time, 0) as response_time,
			COALESCE(test_time, 0) as test_time,
			COALESCE(used_quota, 0) as used_quota,
			COALESCE(%s, '') as channel_group,
			COALESCE(tag, '') as tag,
			COALESCE(models, '') as models,
			COALESCE(created_time, 0) as created_time
		FROM channels
		ORDER BY status ASC, priority DESC, id ASC`, groupCol)

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		// models 是逗号分隔文本，只返回数量，避免超长响应
		modelsStr := fmt.Sprintf("%v", row["models"])
		modelCount := 0
		if modelsStr != "" {
			modelCount = len(strings.Split(modelsStr, ","))
		}
		items = append(items, map[string]interface{}{
			"id":                   row["id"],
			"name":                 row["name"],
			"type":                 row["type"],
			"status":               row["status"],
			"priority":             row["priority"],
			"weight":               row["weight"],
			"balance":              row["balance"],
			"balance_updated_time": row["balance_updated_time"],
			"response_time":        row["response_time"],
			"test_time":            row["test_time"],
			"used_quota":           row["used_quota"],
			"group":                row["channel_group"],
			"tag":                  row["tag"],
			"model_count":          modelCount,
			"created_time":         row["created_time"],
		})
	}
	return items, nil
}

// channelWindow 把小时数换成 [since, until)。
func channelWindow(hours int) (int, int64, int64) {
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	now := time.Now().Unix()
	return hours, now - int64(hours)*3600, now + 1
}

// attemptKey 是一条渠道服务一个模型。
type attemptKey struct {
	channelID int64
	model     string
}

// attemptStats 是窗口期内一条渠道为一个模型做的事。
type attemptStats struct {
	tally        failureTally
	syncSuccess  int64
	empty        int64
	useTimeSum   int64
	maxUseTime   int64
	buckets      [4]int64
	taskFinished int64
	taskSeconds  int64
	taskTimed    int64
}

func (a *attemptStats) merge(b *attemptStats) {
	a.tally.merge(b.tally)
	a.syncSuccess += b.syncSuccess
	a.empty += b.empty
	a.useTimeSum += b.useTimeSum
	a.maxUseTime = max(a.maxUseTime, b.maxUseTime)
	for i := range a.buckets {
		a.buckets[i] += b.buckets[i]
	}
	a.taskFinished += b.taskFinished
	a.taskSeconds += b.taskSeconds
	a.taskTimed += b.taskTimed
}

func (a *attemptStats) avgUseTime() *float64 {
	if a.syncSuccess == 0 {
		return nil
	}
	v := float64(a.useTimeSum) / float64(a.syncSuccess)
	return &v
}

func (a *attemptStats) avgTaskSeconds() *float64 {
	if a.taskTimed == 0 {
		return nil
	}
	v := float64(a.taskSeconds) / float64(a.taskTimed)
	return &v
}

type attemptSet map[attemptKey]*attemptStats

func (set attemptSet) get(channelID int64, model string) *attemptStats {
	key := attemptKey{channelID, model}
	if set[key] == nil {
		set[key] = &attemptStats{tally: newFailureTally()}
	}
	return set[key]
}

// loadAttemptSet 读窗口期内的全部尝试：同步成功（排除任务回执）、每条错误日志、
// 每个结束的任务，并按网关口径归属。
func (s *ChannelMonitorService) loadAttemptSet(since, until int64) (attemptSet, error) {
	set := attemptSet{}
	if err := s.addServedRequests(set, since, until); err != nil {
		return nil, err
	}
	capped, err := loadCappedChannels(s.db)
	if err != nil {
		return nil, fmt.Errorf("channel setting query failed: %w", err)
	}
	attempts, err := loadFailedAttempts(s.logDB, since, until, "")
	if err != nil {
		return nil, fmt.Errorf("error log query failed: %w", err)
	}
	classes, err := attributeAttempts(s.logDB, attempts, capped, since)
	if err != nil {
		return nil, fmt.Errorf("accepted request query failed: %w", err)
	}
	for i, attempt := range attempts {
		set.get(attempt.ChannelID, attempt.Model).tally.addFailure(classes[i], 1)
	}
	if err := s.addTaskOutcomes(set, capped, since, until); err != nil {
		return nil, err
	}
	return set, nil
}

// addServedRequests 计入同步成功（一条消费日志一次）及其耗时分布；任务回执不算。
func (s *ChannelMonitorService) addServedRequests(set attemptSet, since, until int64) error {
	rows, err := s.logDB.QueryWithTimeout(businessQueryTimeout, s.logDB.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(channel_id, 0) AS channel_id, COALESCE(model_name, '') AS model_name,
			COUNT(*) AS success,
			SUM(CASE WHEN completion_tokens = 0 THEN 1 ELSE 0 END) AS empty_count,
			SUM(use_time) AS use_time_sum,
			MAX(use_time) AS max_use_time,
			SUM(CASE WHEN use_time < 3 THEN 1 ELSE 0 END) AS bucket_fast,
			SUM(CASE WHEN use_time >= 3 AND use_time < 10 THEN 1 ELSE 0 END) AS bucket_mid,
			SUM(CASE WHEN use_time >= 10 AND use_time < 30 THEN 1 ELSE 0 END) AS bucket_slow,
			SUM(CASE WHEN use_time >= 30 THEN 1 ELSE 0 END) AS bucket_very_slow
		FROM logs
		WHERE %s AND created_at >= ? AND created_at < ?
		GROUP BY channel_id, model_name`, syncSuccessCond)), taskReceiptLike, since, until)
	if err != nil {
		return fmt.Errorf("served request query failed: %w", err)
	}
	for _, r := range rows {
		a := set.get(toInt64(r["channel_id"]), toString(r["model_name"]))
		a.syncSuccess = toInt64(r["success"])
		a.tally.Success += a.syncSuccess
		a.empty = toInt64(r["empty_count"])
		a.useTimeSum = toInt64(r["use_time_sum"])
		a.maxUseTime = toInt64(r["max_use_time"])
		a.buckets = [4]int64{toInt64(r["bucket_fast"]), toInt64(r["bucket_mid"]), toInt64(r["bucket_slow"]), toInt64(r["bucket_very_slow"])}
	}
	return nil
}

// addTaskOutcomes 把窗口期内结束的任务按渠道、模型计入：成功算成功，失败按
// fail_reason 归属（有并发上限渠道的限流算排队）。没有 tasks 表就跳过。
func (s *ChannelMonitorService) addTaskOutcomes(set attemptSet, capped map[int64]bool, since, until int64) error {
	rows, err := queryTasksWithModel(s.db, func(modelSelect, modelGroup string) string {
		return fmt.Sprintf(`
			SELECT COALESCE(channel_id, 0) AS channel_id, %s AS model, status,
				COALESCE(fail_reason, '') AS reason, COUNT(*) AS n,
				SUM(CASE WHEN submit_time > 0 AND finish_time >= submit_time THEN finish_time - submit_time ELSE 0 END) AS seconds,
				SUM(CASE WHEN submit_time > 0 AND finish_time >= submit_time THEN 1 ELSE 0 END) AS timed
			FROM tasks
			WHERE %s AND finish_time >= ? AND finish_time < ?
			GROUP BY channel_id, status, fail_reason%s`, modelSelect, finishedTaskCond, modelGroup)
	}, since, until)
	if isMissingSchemaErr(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("task outcome query failed: %w", err)
	}
	for _, r := range rows {
		channelID := toInt64(r["channel_id"])
		a := set.get(channelID, toString(r["model"]))
		n := toInt64(r["n"])
		a.taskFinished += n
		if toString(r["status"]) == "SUCCESS" {
			a.tally.Success += n
			a.taskSeconds += toInt64(r["seconds"])
			a.taskTimed += toInt64(r["timed"])
			continue
		}
		a.tally.addFailure(AttributeAttempt(ClassifyFailure(toString(r["reason"])), false, capped[channelID]), n)
	}
	return nil
}

// ChannelHealthStat 是一条渠道在窗口期内的健康度。
type ChannelHealthStat struct {
	ChannelID int64 `json:"channel_id"`
	// Attempts 是全部尝试（含用户侧失败与排队），ErrorRate 只看渠道侧。
	Attempts int64 `json:"attempts"`
	failureTally
	ErrorRate      float64  `json:"error_rate"`
	Level          string   `json:"level"`
	AvgUseTime     *float64 `json:"avg_use_time"`
	AvgTaskSeconds *float64 `json:"avg_task_seconds"`
	TaskFinished   int64    `json:"task_finished"`
}

// GetChannelLogStats 按渠道统计窗口期内的尝试、渠道侧错误率、用户侧失败与平均耗时
func (s *ChannelMonitorService) GetChannelLogStats(hours int) ([]ChannelHealthStat, error) {
	hours, since, until := channelWindow(hours)
	return businessCached(fmt.Sprintf("channel_monitor:log_stats:%d", hours), channelMonitorCacheTTL, false, func() ([]ChannelHealthStat, error) {
		return s.channelHealthStats(since, until)
	})
}

func (s *ChannelMonitorService) channelHealthStats(since, until int64) ([]ChannelHealthStat, error) {
	set, err := s.loadAttemptSet(since, until)
	if err != nil {
		return nil, err
	}
	byChannel := map[int64]*attemptStats{}
	for key, a := range set {
		if byChannel[key.channelID] == nil {
			byChannel[key.channelID] = &attemptStats{tally: newFailureTally()}
		}
		byChannel[key.channelID].merge(a)
	}
	out := make([]ChannelHealthStat, 0, len(byChannel))
	for channelID, a := range byChannel {
		out = append(out, ChannelHealthStat{
			ChannelID: channelID, Attempts: a.tally.attempts(), failureTally: a.tally,
			ErrorRate: a.tally.errorRate(), Level: healthLevel(a.tally.Success, a.tally.Errors),
			AvgUseTime: a.avgUseTime(), AvgTaskSeconds: a.avgTaskSeconds(), TaskFinished: a.taskFinished,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out, nil
}

// SinglePointModel 是只有一个启用渠道支撑的模型。
type SinglePointModel struct {
	Model       string `json:"model"`
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Groups      string `json:"group"`
}

// GetAbilityMatrix 渠道能力矩阵 + 单点模型（仅一个启用渠道支撑的模型，每个模型一行）
func (s *ChannelMonitorService) GetAbilityMatrix() (map[string]interface{}, error) {
	groupCol := s.db.QuoteIdentifier("group")
	query := fmt.Sprintf(`
		SELECT a.%s as ability_group, a.model, a.channel_id, a.enabled,
			COALESCE(a.priority, 0) as priority,
			COALESCE(c.name, '') as channel_name
		FROM abilities a
		LEFT JOIN channels c ON c.id = a.channel_id
		ORDER BY a.model ASC, a.%s ASC`, groupCol, groupCol)

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]interface{}{}
	}

	channelsByModel := map[string]map[int64]bool{}
	groupsByModel := map[string][]string{}
	names := map[int64]string{}
	for _, row := range rows {
		enabled := fmt.Sprintf("%v", row["enabled"])
		if enabled != "true" && enabled != "1" {
			continue
		}
		model := toString(row["model"])
		channelID := toInt64(row["channel_id"])
		if channelsByModel[model] == nil {
			channelsByModel[model] = map[int64]bool{}
		}
		channelsByModel[model][channelID] = true
		groupsByModel[model] = append(groupsByModel[model], toString(row["ability_group"]))
		names[channelID] = toString(row["channel_name"])
	}
	singlePoint := make([]SinglePointModel, 0)
	for model, channels := range channelsByModel {
		if len(channels) != 1 {
			continue
		}
		for channelID := range channels {
			singlePoint = append(singlePoint, SinglePointModel{
				Model: model, ChannelID: channelID, ChannelName: names[channelID],
				Groups: strings.Join(groupsByModel[model], ", "),
			})
		}
	}
	sort.Slice(singlePoint, func(i, j int) bool { return singlePoint[i].Model < singlePoint[j].Model })

	return map[string]interface{}{
		"abilities":           rows,
		"single_point_models": singlePoint,
	}, nil
}

// ModelHealthStat 是一个模型在窗口期内的健康度（按尝试，口径同渠道）。
type ModelHealthStat struct {
	ModelName string `json:"model_name"`
	Attempts  int64  `json:"attempts"`
	failureTally
	ErrorRate float64 `json:"error_rate"`
	Level     string  `json:"level"`
	// SyncSuccess 是同步成功数（空回复率的分母）；任务没有 token 与耗时。
	SyncSuccess    int64    `json:"sync_success"`
	EmptyCount     int64    `json:"empty_count"`
	AvgUseTime     *float64 `json:"avg_use_time"`
	MaxUseTime     *int64   `json:"max_use_time"`
	BucketFast     int64    `json:"bucket_fast"`
	BucketMid      int64    `json:"bucket_mid"`
	BucketSlow     int64    `json:"bucket_slow"`
	BucketVerySlow int64    `json:"bucket_very_slow"`
	AvgTaskSeconds *float64 `json:"avg_task_seconds"`
	TaskFinished   int64    `json:"task_finished"`
}

const modelHealthLimit = 100

// GetModelHealth 按模型统计窗口期内渠道侧错误率/用户侧失败/空回复率/耗时分布
func (s *ChannelMonitorService) GetModelHealth(hours int) ([]ModelHealthStat, error) {
	hours, since, until := channelWindow(hours)
	return businessCached(fmt.Sprintf("channel_monitor:model_health:%d", hours), channelMonitorCacheTTL, false, func() ([]ModelHealthStat, error) {
		return s.modelHealthStats(since, until)
	})
}

func (s *ChannelMonitorService) modelHealthStats(since, until int64) ([]ModelHealthStat, error) {
	set, err := s.loadAttemptSet(since, until)
	if err != nil {
		return nil, err
	}
	byModel := map[string]*attemptStats{}
	for key, a := range set {
		if byModel[key.model] == nil {
			byModel[key.model] = &attemptStats{tally: newFailureTally()}
		}
		byModel[key.model].merge(a)
	}
	out := make([]ModelHealthStat, 0, len(byModel))
	for model, a := range byModel {
		row := ModelHealthStat{
			ModelName: model, Attempts: a.tally.attempts(), failureTally: a.tally,
			ErrorRate: a.tally.errorRate(), Level: healthLevel(a.tally.Success, a.tally.Errors),
			SyncSuccess: a.syncSuccess, EmptyCount: a.empty, AvgUseTime: a.avgUseTime(),
			BucketFast: a.buckets[0], BucketMid: a.buckets[1], BucketSlow: a.buckets[2], BucketVerySlow: a.buckets[3],
			AvgTaskSeconds: a.avgTaskSeconds(), TaskFinished: a.taskFinished,
		}
		if a.syncSuccess > 0 {
			maxUseTime := a.maxUseTime
			row.MaxUseTime = &maxUseTime
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Attempts != out[j].Attempts {
			return out[i].Attempts > out[j].Attempts
		}
		return out[i].ModelName < out[j].ModelName
	})
	if len(out) > modelHealthLimit {
		out = out[:modelHealthLimit]
	}
	return out, nil
}

// ErrorSample 是一条错误日志及其归属。
type ErrorSample struct {
	CreatedAt int64       `json:"created_at"`
	ModelName string      `json:"model_name"`
	ChannelID int64       `json:"channel_id"`
	Username  string      `json:"username"`
	Content   string      `json:"content"`
	Side      FailureSide `json:"side"`
	Category  string      `json:"category"`
}

// ErrorAnalysis 是窗口期内错误日志的归属分布与最近样本。
type ErrorAnalysis struct {
	Total             int64            `json:"total"`
	ChannelErrors     int64            `json:"channel_errors"`
	UserErrors        int64            `json:"user_errors"`
	QueueFull         int64            `json:"queue_full"`
	ChannelCategories map[string]int64 `json:"channel_categories"`
	UserCategories    map[string]int64 `json:"user_categories"`
	Samples           []ErrorSample    `json:"samples"`
	Sampled           int              `json:"sampled"`
}

// GetErrorAnalysis 读窗口期内全部错误日志：分布按渠道侧 / 用户侧 / 排队分组，
// 样本取最近 limit 条。
func (s *ChannelMonitorService) GetErrorAnalysis(hours, limit int) (ErrorAnalysis, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	hours, since, until := channelWindow(hours)
	return businessCached(fmt.Sprintf("channel_monitor:error_analysis:%d:%d", hours, limit), channelMonitorCacheTTL, false, func() (ErrorAnalysis, error) {
		return s.errorAnalysis(since, until, limit)
	})
}

func (s *ChannelMonitorService) errorAnalysis(since, until int64, limit int) (ErrorAnalysis, error) {
	result := ErrorAnalysis{ChannelCategories: map[string]int64{}, UserCategories: map[string]int64{}, Samples: []ErrorSample{}}
	capped, err := loadCappedChannels(s.db)
	if err != nil {
		return result, fmt.Errorf("channel setting query failed: %w", err)
	}
	attempts, err := loadFailedAttempts(s.logDB, since, until, "")
	if err != nil {
		return result, fmt.Errorf("error log query failed: %w", err)
	}
	classes, err := attributeAttempts(s.logDB, attempts, capped, since)
	if err != nil {
		return result, fmt.Errorf("accepted request query failed: %w", err)
	}
	tally := newFailureTally()
	for _, class := range classes {
		tally.addFailure(class, 1)
	}
	result.Total = int64(len(attempts))
	result.ChannelErrors, result.UserErrors, result.QueueFull = tally.Errors, tally.UserErrors, tally.QueueFull
	result.ChannelCategories, result.UserCategories = tally.ChannelCategories, tally.UserCategories
	for i := len(attempts) - 1; i >= 0 && len(result.Samples) < limit; i-- {
		attempt := attempts[i]
		result.Samples = append(result.Samples, ErrorSample{
			CreatedAt: attempt.CreatedAt, ModelName: attempt.Model, ChannelID: attempt.ChannelID,
			Username: attempt.Username, Content: truncateRunes(attempt.Content, 300),
			Side: classes[i].Side, Category: classes[i].Category,
		})
	}
	result.Sampled = len(result.Samples)
	return result, nil
}
