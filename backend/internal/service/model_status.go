package service

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/database"
)

// Constants for model status
var (
	AvailableTimeWindows      = []string{"1h", "6h", "12h", "24h"}
	DefaultTimeWindow         = "24h"
	AvailableRefreshIntervals = []int{0, 30, 60, 120, 300}
	AvailableSortModes        = []string{"default", "availability", "custom"}
)

// Time window slot configurations: {totalSeconds, numSlots, slotSeconds}
// Must match Python backend and frontend TIME_WINDOWS exactly
type timeWindowConfig struct {
	totalSeconds int64
	numSlots     int
	slotSeconds  int64
}

var timeWindowConfigs = map[string]timeWindowConfig{
	"1h":  {3600, 60, 60},    // 1 hour, 60 slots, 1 minute each
	"6h":  {21600, 24, 900},  // 6 hours, 24 slots, 15 minutes each
	"12h": {43200, 24, 1800}, // 12 hours, 24 slots, 30 minutes each
	"24h": {86400, 24, 3600}, // 24 hours, 24 slots, 1 hour each
}

// getStatusColor determines status color based on success rate (matches Python backend)
func getStatusColor(successRate float64, totalRequests int64) string {
	if totalRequests == 0 {
		return "green" // No requests = no issues
	}
	if successRate >= 95 {
		return "green"
	} else if successRate >= 80 {
		return "yellow"
	}
	return "red"
}

// roundRate rounds a float to 2 decimal places
func roundRate(rate float64) float64 {
	return math.Round(rate*100) / 100
}

// ModelStatusService handles model availability monitoring
type ModelStatusService struct {
	db    *database.Manager
	logDB *database.Manager
}

// NewModelStatusService creates a new ModelStatusService
func NewModelStatusService() *ModelStatusService {
	return &ModelStatusService{db: database.Get(), logDB: database.GetLog()}
}

// GetAvailableModels returns all models with 24h request counts
func (s *ModelStatusService) GetAvailableModels() ([]map[string]interface{}, error) {
	cm := cache.Get()
	var cached []map[string]interface{}
	found, _ := cm.GetJSON("model_status:available_models", &cached)
	if found {
		return cached, nil
	}

	startTime := time.Now().Unix() - 86400

	query := s.logDB.RebindQuery(`
		SELECT model_name, COUNT(*) as request_count_24h
		FROM logs
		WHERE type IN (2, 5) AND model_name != '' AND created_at >= ?
		GROUP BY model_name
		ORDER BY request_count_24h DESC`)

	rows, err := s.logDB.Query(query, startTime)
	if err != nil {
		return nil, err
	}

	cm.Set("model_status:available_models", rows, 5*time.Minute)
	return rows, nil
}

// GetModelStatus returns status for a specific model.
//
// The success rate is what customers got, counted the gateway's way
// (ops_alert_patrol.go opsFailureSpikeTallies): a synchronous request by
// whether any of its attempts was served — retries on other channels are not
// failures — and a task by its final status in tasks. A task's receipt says the
// task was accepted, not that it succeeded, so a model that runs tasks is
// measured by its tasks alone. Failures that were the caller's own doing
// (ClassifyFailure: moderation, parameters, inputs, their balance, hanging up)
// are left out of both sides and reported as user_error_count.
func (s *ModelStatusService) GetModelStatus(modelName, window string) (map[string]interface{}, error) {
	cacheKey := fmt.Sprintf("model_status:v2:%s:%s", modelName, window)
	cm := cache.Get()
	var cached map[string]interface{}
	found, _ := cm.GetJSON(cacheKey, &cached)
	if found {
		return cached, nil
	}

	// Get window configuration (dynamic slot count per window)
	twConfig, ok := timeWindowConfigs[window]
	if !ok {
		twConfig = timeWindowConfigs["24h"]
	}
	now := time.Now().Unix()
	startTime := now - twConfig.totalSeconds
	slots, err := s.modelStatusSlots(modelName, twConfig, startTime, now)
	if err != nil {
		return nil, err
	}

	slotData := make([]map[string]interface{}, 0, len(slots))
	var total statusSlot
	for i, slot := range slots {
		slotStart := startTime + int64(i)*twConfig.slotSeconds
		requests, rate := slot.successRate()
		slotData = append(slotData, map[string]interface{}{
			"slot":             i,
			"start_time":       slotStart,
			"end_time":         slotStart + twConfig.slotSeconds,
			"total_requests":   requests,
			"success_count":    slot.success,
			"failure_count":    slot.failure,
			"user_error_count": slot.userErrors,
			"empty_count":      slot.empty,
			"success_rate":     roundRate(rate),
			"status":           getStatusColor(rate, requests),
		})
		total.add(slot)
	}

	totalRequests, overallRate := total.successRate()
	result := map[string]interface{}{
		"model_name":       modelName,
		"display_name":     modelName,
		"time_window":      window,
		"total_requests":   totalRequests,
		"success_count":    total.success,
		"failure_count":    total.failure,
		"user_error_count": total.userErrors,
		"empty_count":      total.empty,
		"success_rate":     roundRate(overallRate),
		"current_status":   getStatusColor(overallRate, totalRequests),
		"slot_data":        slotData,
	}

	cm.Set(cacheKey, result, 30*time.Second)
	return result, nil
}

// GetMultipleModelsStatus returns status for multiple models
func (s *ModelStatusService) GetMultipleModelsStatus(modelNames []string, window string) ([]map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0, len(modelNames))
	for _, name := range modelNames {
		status, err := s.GetModelStatus(name, window)
		if err != nil {
			continue
		}
		results = append(results, status)
	}
	return results, nil
}

// GetAllModelsStatus returns status for all models that have requests
func (s *ModelStatusService) GetAllModelsStatus(window string) ([]map[string]interface{}, error) {
	models, err := s.GetAvailableModels()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(models))
	for _, m := range models {
		if name, ok := m["model_name"].(string); ok {
			names = append(names, name)
		}
	}

	return s.GetMultipleModelsStatus(names, window)
}

// GetTokenGroups 返回令牌分组列表及其关联的模型（基于 abilities 表）
func (s *ModelStatusService) GetTokenGroups() ([]map[string]interface{}, error) {
	cm := cache.Get()
	var cached []map[string]interface{}
	found, _ := cm.GetJSON("model_status:token_groups", &cached)
	if found {
		return cached, nil
	}

	// 从 abilities 表获取分组及其模型列表（abilities 表定义了 group-model-channel 的映射）
	// 注意：不再过滤 c.status = 1，否则 ManuallyDisabled / AutoDisabled 的渠道会
	// 让分组里临时不可用的模型从下拉中消失，与用户"这个分组本来就有这个模型"的心智不符。
	groupCol := s.getGroupCol()
	query := s.db.RebindQuery(fmt.Sprintf(`
		SELECT COALESCE(NULLIF(a.%s, ''), 'default') as group_name,
			COUNT(DISTINCT a.model) as model_count
		FROM abilities a
		INNER JOIN channels c ON c.id = a.channel_id
		GROUP BY COALESCE(NULLIF(a.%s, ''), 'default')
		ORDER BY model_count DESC`, groupCol, groupCol))

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}

	// 一次性读出 NewAPI 的分组描述（UserUsableGroups）和倍率（GroupRatio）
	descMap, ratioMap := s.loadGroupMetadata()

	// 为每个分组获取其模型列表
	results := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		groupName := fmt.Sprintf("%v", row["group_name"])

		modelsQuery := s.db.RebindQuery(fmt.Sprintf(`
			SELECT DISTINCT a.model as model_name
			FROM abilities a
			INNER JOIN channels c ON c.id = a.channel_id
			WHERE COALESCE(NULLIF(a.%s, ''), 'default') = ?
			ORDER BY a.model`, groupCol))

		modelRows, err := s.db.Query(modelsQuery, groupName)
		if err != nil {
			continue
		}

		modelNames := make([]string, 0, len(modelRows))
		for _, mr := range modelRows {
			if name, ok := mr["model_name"].(string); ok && name != "" {
				modelNames = append(modelNames, name)
			}
		}

		entry := map[string]interface{}{
			"group_name":  groupName,
			"model_count": row["model_count"],
			"models":      modelNames,
		}
		if d, ok := descMap[groupName]; ok && d != "" && d != groupName {
			entry["description"] = d
		}
		if r, ok := ratioMap[groupName]; ok {
			entry["ratio"] = r
		}
		results = append(results, entry)
	}

	cm.Set("model_status:token_groups", results, 5*time.Minute)
	return results, nil
}

// loadGroupMetadata 一次性从 NewAPI 的 options 表读出分组描述和倍率配置。
// 返回两张 map，缺失时为 nil 不影响主流程。
func (s *ModelStatusService) loadGroupMetadata() (descMap map[string]string, ratioMap map[string]float64) {
	descMap = map[string]string{}
	ratioMap = map[string]float64{}

	keyCol := `"key"`
	if !s.db.IsPG {
		keyCol = "`key`"
	}
	query := s.db.RebindQuery(fmt.Sprintf(
		`SELECT %s as opt_key, value FROM options WHERE %s IN ('UserUsableGroups', 'GroupRatio')`,
		keyCol, keyCol))

	rows, err := s.db.Query(query)
	if err != nil {
		return
	}
	for _, row := range rows {
		key := fmt.Sprintf("%v", row["opt_key"])
		val, _ := row["value"].(string)
		if val == "" {
			continue
		}
		switch key {
		case "UserUsableGroups":
			_ = json.Unmarshal([]byte(val), &descMap)
		case "GroupRatio":
			// GroupRatio 的值可能是 number 或 string number，先按 number 解
			raw := map[string]interface{}{}
			if err := json.Unmarshal([]byte(val), &raw); err == nil {
				for k, v := range raw {
					switch n := v.(type) {
					case float64:
						ratioMap[k] = n
					case json.Number:
						if f, err := n.Float64(); err == nil {
							ratioMap[k] = f
						}
					}
				}
			}
		}
	}
	return
}

// getGroupCol 返回正确引用的 group 列名（group 是保留字）
func (s *ModelStatusService) getGroupCol() string {
	if s.db.IsPG {
		return `"group"`
	}
	return "`group`"
}

// Config management via cache

// GetSelectedModels returns selected model names from cache
func (s *ModelStatusService) GetSelectedModels() []string {
	cm := cache.Get()
	var models []string
	found, _ := cm.GetJSON("model_status:selected_models", &models)
	if found {
		return models
	}
	return []string{}
}

// SetSelectedModels saves selected models to cache
func (s *ModelStatusService) SetSelectedModels(models []string) {
	cm := cache.Get()
	cm.Set("model_status:selected_models", models, 0) // no expiry
}

// GetConfig returns all model status config
func (s *ModelStatusService) GetConfig() map[string]interface{} {
	cm := cache.Get()

	var timeWindow string
	found, _ := cm.GetJSON("model_status:time_window", &timeWindow)
	if !found {
		timeWindow = DefaultTimeWindow
	}

	var refreshInterval int
	found, _ = cm.GetJSON("model_status:refresh_interval", &refreshInterval)
	if !found {
		refreshInterval = 60
	}

	var sortMode string
	found, _ = cm.GetJSON("model_status:sort_mode", &sortMode)
	if !found {
		sortMode = "default"
	}

	var customOrder []string
	cm.GetJSON("model_status:custom_order", &customOrder)

	var customGroups []map[string]interface{}
	found, _ = cm.GetJSON("model_status:custom_groups", &customGroups)
	if !found {
		customGroups = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"time_window":      timeWindow,
		"refresh_interval": refreshInterval,
		"sort_mode":        sortMode,
		"custom_order":     customOrder,
		"selected_models":  s.GetSelectedModels(),
		"custom_groups":    customGroups,
	}
}

// SetTimeWindow saves time window to cache
func (s *ModelStatusService) SetTimeWindow(window string) {
	cm := cache.Get()
	cm.Set("model_status:time_window", window, 0)
}

// SetRefreshInterval saves refresh interval to cache
func (s *ModelStatusService) SetRefreshInterval(interval int) {
	cm := cache.Get()
	cm.Set("model_status:refresh_interval", interval, 0)
}

// SetSortMode saves sort mode to cache
func (s *ModelStatusService) SetSortMode(mode string) {
	cm := cache.Get()
	cm.Set("model_status:sort_mode", mode, 0)
}

// SetCustomOrder saves custom order to cache
func (s *ModelStatusService) SetCustomOrder(order []string) {
	cm := cache.Get()
	cm.Set("model_status:custom_order", order, 0)
}

// GetCustomGroups returns custom model groups from cache
func (s *ModelStatusService) GetCustomGroups() []map[string]interface{} {
	cm := cache.Get()
	var groups []map[string]interface{}
	found, _ := cm.GetJSON("model_status:custom_groups", &groups)
	if found {
		return groups
	}
	return []map[string]interface{}{}
}

// SetCustomGroups saves custom model groups to cache
func (s *ModelStatusService) SetCustomGroups(groups []map[string]interface{}) {
	cm := cache.Get()
	cm.Set("model_status:custom_groups", groups, 0) // no expiry
}
