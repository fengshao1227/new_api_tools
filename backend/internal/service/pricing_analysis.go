package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/config"
	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

const pricingAnalysisTimeout = 90 * time.Second

type PricingScenario struct {
	ModelName            string  `json:"model_name"`
	Tier                 string  `json:"tier"`
	ConditionHint        string  `json:"condition_hint,omitempty"`
	RetailUSD            float64 `json:"retail_usd"`
	LowestCostUSD        float64 `json:"lowest_cost_usd"`
	HighestCostUSD       float64 `json:"highest_cost_usd"`
	HighestMarginPercent float64 `json:"highest_margin_percent"`
	LowestMarginPercent  float64 `json:"lowest_margin_percent"`
	LowestCostChannel    string  `json:"lowest_cost_channel,omitempty"`
	HighestCostChannel   string  `json:"highest_cost_channel,omitempty"`
	CostRoutes           int     `json:"cost_routes"`
	UnpricedRoutes       int     `json:"unpriced_routes"`
	ServedRoutes         int     `json:"served_routes"`
	Status               string  `json:"status"`
}

type PricingModelAnalysis struct {
	ModelName               string            `json:"model_name"`
	Mode                    string            `json:"mode"`
	Expression              string            `json:"expression,omitempty"`
	PromptUSDPerMillion     float64           `json:"prompt_usd_per_million,omitempty"`
	CompletionUSDPerMillion float64           `json:"completion_usd_per_million,omitempty"`
	Scenarios               []PricingScenario `json:"scenarios"`
	BestMarginPercent       float64           `json:"best_margin_percent"`
	WorstMarginPercent      float64           `json:"worst_margin_percent"`
	BestScenario            string            `json:"best_scenario,omitempty"`
	WorstScenario           string            `json:"worst_scenario,omitempty"`
	Unpriced                bool              `json:"unpriced"`
}

type PricingAnalysisResult struct {
	QuotaPerUnit int64                  `json:"quota_per_unit"`
	Currency     string                 `json:"currency"`
	Models       []PricingModelAnalysis `json:"models"`
	Best         []PricingScenario      `json:"best"`
	Worst        []PricingScenario      `json:"worst"`
	Notes        []string               `json:"notes"`
}

type pricingProbe struct {
	Label  string
	Tokens string
}

type costBaselineEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Rows         []costBaselineRow `json:"rows"`
		QuotaPerUnit float64           `json:"quota_per_unit"`
	} `json:"data"`
	Message string `json:"message"`
}

type costBaselineRow struct {
	ModelName   string             `json:"model_name"`
	ChannelID   int64              `json:"channel_id"`
	ChannelName string             `json:"channel_name"`
	Currency    string             `json:"currency"`
	Tiers       []costBaselineTier `json:"tiers"`
}

type costBaselineTier struct {
	Tier        string  `json:"tier"`
	MatchedTier string  `json:"matched_tier"`
	Cost        float64 `json:"cost"`
	Unpriced    bool    `json:"unpriced"`
	Serves      bool    `json:"serves"`
}

type priceBookEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Rows []priceBookRow `json:"rows"`
	} `json:"data"`
	Message string `json:"message"`
}

type priceBookRow struct {
	ModelName               string          `json:"model_name"`
	Mode                    string          `json:"mode"`
	Expr                    string          `json:"expr"`
	Tiers                   []priceBookTier `json:"tiers"`
	UnnamedTiers            []priceBookTier `json:"unnamed_tiers"`
	PerCallUSD              float64         `json:"per_call_usd"`
	PromptUSDPerMillion     float64         `json:"prompt_usd_per_million"`
	CompletionUSDPerMillion float64         `json:"completion_usd_per_million"`
}

type priceBookTier struct {
	Tier string  `json:"tier"`
	USD  float64 `json:"usd"`
}

type pricingHTTPClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func newPricingHTTPClient() (*pricingHTTPClient, error) {
	cfg := config.Get()
	key := strings.TrimSpace(cfg.NewAPIKey)
	if key == "" {
		return nil, fmt.Errorf("NEWAPI_API_KEY 未配置，无法读取 new-api 的定价解析结果")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.NewAPIBaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("NEWAPI_BASEURL 未配置")
	}
	return &pricingHTTPClient{baseURL: base, apiKey: key, client: &http.Client{Timeout: pricingAnalysisTimeout}}, nil
}

func (c *pricingHTTPClient) getJSON(ctx context.Context, path string, query url.Values, target interface{}) error {
	endpoint := c.baseURL + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("New-Api-User", "1")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 new-api 定价接口失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("new-api 定价接口返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("解析 new-api 定价响应失败: %w", err)
	}
	return nil
}

// GetPricingAnalysis uses new-api's own price/cost evaluators as the source of
// truth, then turns every served route into a margin range. This keeps the
// plugin from maintaining a second expression interpreter while still making
// the operator-facing report independent from the new-api dashboard UI.
func GetPricingAnalysis() (*PricingAnalysisResult, error) {
	cm := cache.Get()
	const cacheKey = "margin-analysis:pricing"
	var cached PricingAnalysisResult
	if found, _ := cm.GetJSON(cacheKey, &cached); found {
		return &cached, nil
	}
	client, err := newPricingHTTPClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pricingAnalysisTimeout)
	defer cancel()

	probe := url.Values{}
	probe.Set("tokens", "p:1000000,c:1000000,len:1000000,cr:0,cc:0")
	probe.Set("usage", "duration:1,seconds:1,n:1,credits:1")
	costEnvelope, err := fetchCostBaseline(ctx, client, probe)
	if err != nil {
		return nil, err
	}

	modelSet := make(map[string]bool)
	for _, row := range costEnvelope.Data.Rows {
		if strings.TrimSpace(row.ModelName) != "" {
			modelSet[row.ModelName] = true
		}
	}
	for _, modelName := range configuredModelNames() {
		modelSet[modelName] = true
	}
	models := make([]string, 0, len(modelSet))
	for modelName := range modelSet {
		models = append(models, modelName)
	}
	sort.Strings(models)

	priceRows, err := fetchPriceBookRows(ctx, client, models, probe)
	if err != nil {
		return nil, err
	}
	quotaPerUnit := costEnvelope.Data.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = util.TokensPerUSD
	}
	result := combinePricingAnalysis(costEnvelope.Data.Rows, priceRows, quotaPerUnit, "", false)
	// Token expressions need separate input/output/cache scenarios. Probing p
	// and c together produces misleading values such as $12,000,000 for a
	// $2/$10 per-million-token price.
	for _, tokenProbe := range []pricingProbe{
		{Label: "输入 1M token", Tokens: "p:1000000,c:0,len:1000000,cr:0,cc:0"},
		{Label: "输出 1M token", Tokens: "p:0,c:1000000,len:1000000,cr:0,cc:0"},
		{Label: "缓存读取 1M", Tokens: "p:0,c:0,len:1000000,cr:1000000,cc:0"},
		{Label: "缓存创建 1M", Tokens: "p:0,c:0,len:1000000,cr:0,cc:1000000"},
	} {
		probe := url.Values{}
		probe.Set("tokens", tokenProbe.Tokens)
		probe.Set("usage", "duration:1,seconds:1,n:1,credits:1")
		probeCost, probeErr := fetchCostBaseline(ctx, client, probe)
		if probeErr != nil {
			return nil, probeErr
		}
		probePrices, probeErr := fetchPriceBookRows(ctx, client, models, probe)
		if probeErr != nil {
			return nil, probeErr
		}
		mergeTokenProbe(result, combinePricingAnalysis(probeCost.Data.Rows, probePrices, quotaPerUnit, tokenProbe.Label, true), tokenProbe.Label)
	}
	rebuildPricingExtremes(result)
	result.Notes = []string{
		"成本基准与价格簿由 new-api 当前计费引擎解析，插件只负责汇总和展示。",
		"最高毛利使用可服务渠道中的最低成本；最低毛利使用可服务渠道中的最高成本。",
		"成本范围按供应商成本计算，尚未叠加用户分组折扣；实际账单毛利以消费日志为准。",
		"按 token、按次、按秒/时长、分辨率和表达式 tier 均单独列出，未定价不会被当成零成本。",
	}
	cm.Set(cacheKey, result, 10*time.Minute)
	return result, nil
}

func fetchCostBaseline(ctx context.Context, client *pricingHTTPClient, probe url.Values) (costBaselineEnvelope, error) {
	var envelope costBaselineEnvelope
	if err := client.getJSON(ctx, "/api/data/cost-baseline", probe, &envelope); err != nil {
		return envelope, err
	}
	if !envelope.Success {
		return envelope, fmt.Errorf("new-api 成本基准返回失败: %s", envelope.Message)
	}
	return envelope, nil
}

func configuredModelNames() []string {
	queries := []string{
		"SELECT model_name FROM models WHERE deleted_at IS NULL AND model_name <> ''",
		"SELECT DISTINCT model FROM abilities WHERE model <> ''",
	}
	for _, rawQuery := range queries {
		rows, err := database.Get().QueryWithTimeout(15*time.Second, rawQuery)
		if err != nil {
			continue
		}
		seen := make(map[string]bool, len(rows))
		result := make([]string, 0, len(rows))
		for _, row := range rows {
			name := strings.TrimSpace(toString(row["model_name"]))
			if name == "" {
				name = strings.TrimSpace(toString(row["model"]))
			}
			if name != "" && !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return nil
}

func fetchPriceBookRows(ctx context.Context, client *pricingHTTPClient, models []string, probe url.Values) ([]priceBookRow, error) {
	rows := make([]priceBookRow, 0)
	if len(models) == 0 {
		return rows, nil
	}
	for start := 0; start < len(models); start += 200 {
		end := start + 200
		if end > len(models) {
			end = len(models)
		}
		query := url.Values{}
		query.Set("models", strings.Join(models[start:end], ","))
		query.Set("tokens", probe.Get("tokens"))
		query.Set("usage", probe.Get("usage"))
		var envelope priceBookEnvelope
		if err := client.getJSON(ctx, "/api/data/price-book", query, &envelope); err != nil {
			return nil, err
		}
		if !envelope.Success {
			return nil, fmt.Errorf("new-api 价格簿返回失败: %s", envelope.Message)
		}
		rows = append(rows, envelope.Data.Rows...)
	}
	return rows, nil
}

type costCandidate struct {
	CostUSD     float64
	ChannelName string
	Unpriced    bool
	Serves      bool
}

func combinePricingAnalysis(costRows []costBaselineRow, priceRows []priceBookRow, quotaPerUnit float64, scenarioPrefix string, normalizeToken bool) *PricingAnalysisResult {
	costs := make(map[string]map[string][]costCandidate)
	for _, row := range costRows {
		if costs[row.ModelName] == nil {
			costs[row.ModelName] = make(map[string][]costCandidate)
		}
		for _, tier := range row.Tiers {
			key := tier.Tier
			if key == "" {
				key = tier.MatchedTier
			}
			costs[row.ModelName][key] = append(costs[row.ModelName][key], costCandidate{
				CostUSD:     tier.Cost / quotaPerUnit,
				ChannelName: row.ChannelName,
				Unpriced:    tier.Unpriced,
				Serves:      tier.Serves,
			})
		}
	}

	result := &PricingAnalysisResult{QuotaPerUnit: int64(quotaPerUnit), Currency: "USD", Models: make([]PricingModelAnalysis, 0, len(priceRows))}
	for _, price := range priceRows {
		model := PricingModelAnalysis{
			ModelName:               price.ModelName,
			Mode:                    price.Mode,
			Expression:              price.Expr,
			PromptUSDPerMillion:     price.PromptUSDPerMillion,
			CompletionUSDPerMillion: price.CompletionUSDPerMillion,
			Scenarios:               make([]PricingScenario, 0),
		}
		switch price.Mode {
		case "tiered_expr":
			conditionHints := pricingConditionHints(price.Expr)
			for _, tier := range append(append([]priceBookTier{}, price.Tiers...), price.UnnamedTiers...) {
				retail := tier.USD
				if normalizeToken && hasTokenExpression(price.Expr) {
					retail /= 1_000_000
				}
				tierName := tier.Tier
				if scenarioPrefix != "" {
					tierName = scenarioPrefix + " · " + tierName
				}
				model.Scenarios = append(model.Scenarios, makePricingScenario(price.ModelName, tierName, conditionHints[tier.Tier], retail, costs[price.ModelName][tier.Tier]))
			}
		case "per_call":
			model.Scenarios = append(model.Scenarios, makePricingScenario(price.ModelName, scenarioName(scenarioPrefix, "per_call"), "每次请求固定价", price.PerCallUSD, flattenCandidates(costs[price.ModelName])))
		case "per_token":
			model.Scenarios = append(model.Scenarios, makePricingScenario(price.ModelName, scenarioName(scenarioPrefix, "input_1m"), "每 1M 输入 token", price.PromptUSDPerMillion, flattenCandidates(costs[price.ModelName])))
			model.Scenarios = append(model.Scenarios, makePricingScenario(price.ModelName, scenarioName(scenarioPrefix, "output_1m"), "每 1M 输出 token", price.CompletionUSDPerMillion, flattenCandidates(costs[price.ModelName])))
		default:
			model.Unpriced = true
		}
		for _, scenario := range model.Scenarios {
			if len(model.Scenarios) == 0 || scenario.Status == "unpriced" {
				continue
			}
			if len(model.BestScenario) == 0 || scenario.HighestMarginPercent > model.BestMarginPercent {
				model.BestMarginPercent = scenario.HighestMarginPercent
				model.BestScenario = scenario.Tier
			}
			if len(model.WorstScenario) == 0 || scenario.LowestMarginPercent < model.WorstMarginPercent {
				model.WorstMarginPercent = scenario.LowestMarginPercent
				model.WorstScenario = scenario.Tier
			}
		}
		result.Models = append(result.Models, model)
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].WorstMarginPercent < result.Models[j].WorstMarginPercent })
	for _, model := range result.Models {
		for _, scenario := range model.Scenarios {
			if scenario.Status == "unpriced" {
				continue
			}
			result.Worst = append(result.Worst, scenario)
			result.Best = append(result.Best, scenario)
		}
	}
	sort.Slice(result.Worst, func(i, j int) bool { return result.Worst[i].LowestMarginPercent < result.Worst[j].LowestMarginPercent })
	sort.Slice(result.Best, func(i, j int) bool { return result.Best[i].HighestMarginPercent > result.Best[j].HighestMarginPercent })
	if len(result.Worst) > 20 {
		result.Worst = result.Worst[:20]
	}
	if len(result.Best) > 20 {
		result.Best = result.Best[:20]
	}
	return result
}

func hasTokenExpression(expr string) bool {
	for _, token := range []string{"p", "c", "cr", "cc", "cc1h", "img", "img_o", "ai", "ao", "len"} {
		if strings.Contains(expr, token+" *") || strings.Contains(expr, token+"*") || strings.Contains(expr, token+" ") {
			return true
		}
	}
	return false
}

func scenarioName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + " · " + name
}

func mergeTokenProbe(base, probe *PricingAnalysisResult, prefix string) {
	for i := range base.Models {
		if !hasTokenExpression(base.Models[i].Expression) {
			continue
		}
		if prefix == "输入 1M token" {
			base.Models[i].Scenarios = nil
		}
		for _, probeModel := range probe.Models {
			if probeModel.ModelName != base.Models[i].ModelName {
				continue
			}
			if base.Models[i].Scenarios == nil {
				base.Models[i].Scenarios = make([]PricingScenario, 0)
			}
			for _, scenario := range probeModel.Scenarios {
				if !strings.HasPrefix(scenario.Tier, prefix+" · ") {
					continue
				}
				base.Models[i].Scenarios = append(base.Models[i].Scenarios, scenario)
			}
			break
		}
	}
}

func rebuildPricingExtremes(result *PricingAnalysisResult) {
	result.Best = result.Best[:0]
	result.Worst = result.Worst[:0]
	for i := range result.Models {
		model := &result.Models[i]
		model.BestMarginPercent = 0
		model.WorstMarginPercent = 0
		model.BestScenario = ""
		model.WorstScenario = ""
		for _, scenario := range model.Scenarios {
			if scenario.Status == "unpriced" {
				continue
			}
			if model.BestScenario == "" || scenario.HighestMarginPercent > model.BestMarginPercent {
				model.BestMarginPercent = scenario.HighestMarginPercent
				model.BestScenario = scenario.Tier
			}
			if model.WorstScenario == "" || scenario.LowestMarginPercent < model.WorstMarginPercent {
				model.WorstMarginPercent = scenario.LowestMarginPercent
				model.WorstScenario = scenario.Tier
			}
			result.Best = append(result.Best, scenario)
			result.Worst = append(result.Worst, scenario)
		}
	}
	sort.Slice(result.Best, func(i, j int) bool { return result.Best[i].HighestMarginPercent > result.Best[j].HighestMarginPercent })
	sort.Slice(result.Worst, func(i, j int) bool { return result.Worst[i].LowestMarginPercent < result.Worst[j].LowestMarginPercent })
	if len(result.Best) > 20 {
		result.Best = result.Best[:20]
	}
	if len(result.Worst) > 20 {
		result.Worst = result.Worst[:20]
	}
}

func flattenCandidates(byTier map[string][]costCandidate) []costCandidate {
	result := make([]costCandidate, 0)
	for _, candidates := range byTier {
		result = append(result, candidates...)
	}
	return result
}

func makePricingScenario(model, tier, condition string, retail float64, candidates []costCandidate) PricingScenario {
	scenario := PricingScenario{ModelName: model, Tier: tier, ConditionHint: condition, RetailUSD: retail}
	minCost := math.Inf(1)
	maxCost := math.Inf(-1)
	for _, candidate := range candidates {
		if candidate.Unpriced {
			scenario.UnpricedRoutes++
		}
		if !candidate.Serves {
			continue
		}
		scenario.ServedRoutes++
		if candidate.Unpriced {
			continue
		}
		scenario.CostRoutes++
		if candidate.CostUSD < minCost {
			minCost = candidate.CostUSD
			scenario.LowestCostChannel = candidate.ChannelName
		}
		if candidate.CostUSD > maxCost {
			maxCost = candidate.CostUSD
			scenario.HighestCostChannel = candidate.ChannelName
		}
	}
	if retail <= 0 || scenario.CostRoutes == 0 || math.IsInf(minCost, 0) {
		scenario.Status = "unpriced"
		return scenario
	}
	scenario.LowestCostUSD = minCost
	scenario.HighestCostUSD = maxCost
	scenario.HighestMarginPercent = (retail - minCost) / retail * 100
	scenario.LowestMarginPercent = (retail - maxCost) / retail * 100
	switch {
	case scenario.LowestMarginPercent < 0:
		scenario.Status = "loss"
	case scenario.LowestMarginPercent < 20:
		scenario.Status = "thin"
	default:
		scenario.Status = "healthy"
	}
	return scenario
}

// pricingConditionHints recovers the path condition for each tier in the
// small ternary expression language used by new-api's price book. A simple
// "last ?" lookup is incorrect for chained or nested ternaries: it labels
// every branch with the first condition (for example, 1K as 2K and 272k_plus
// as len <= 272000). The parser below only understands the operators needed
// for display and deliberately leaves the original expression as the source
// of truth for evaluation.
func pricingConditionHints(expr string) map[string]string {
	hints := make(map[string]string)
	collectPricingConditions(strings.TrimSpace(expr), "", hints)
	return hints
}

func collectPricingConditions(expr, inherited string, hints map[string]string) {
	expr = strings.TrimSpace(expr)
	for {
		unwrapped := unwrapPricingParens(expr)
		if unwrapped == expr {
			break
		}
		expr = unwrapped
	}

	question := findTopLevelPricingQuestion(expr)
	if question >= 0 {
		colon := findMatchingPricingColon(expr, question)
		if colon >= 0 {
			condition := strings.TrimSpace(expr[:question])
			collectPricingConditions(expr[question+1:colon], joinPricingConditions(inherited, condition), hints)
			collectPricingConditions(expr[colon+1:], joinPricingConditions(inherited, "否则（"+condition+"）"), hints)
			return
		}
	}

	for offset := 0; offset < len(expr); {
		index := strings.Index(expr[offset:], `tier("`)
		if index < 0 {
			break
		}
		index += offset + len(`tier("`)
		end := strings.IndexByte(expr[index:], '"')
		if end < 0 {
			break
		}
		name := expr[index : index+end]
		condition := inherited
		if condition == "" {
			condition = "默认/直接 tier 分支"
		}
		addPricingCondition(hints, name, condition)
		offset = index + end
	}

	for index := 0; index < len(expr); index++ {
		if expr[index] != '(' {
			continue
		}
		end := matchingPricingParen(expr, index)
		if end < 0 {
			break
		}
		inner := expr[index+1 : end]
		if strings.Contains(inner, "?") || strings.Contains(inner, `tier("`) {
			collectPricingConditions(inner, inherited, hints)
		}
		index = end
	}
}

func addPricingCondition(hints map[string]string, tier, condition string) {
	if previous := hints[tier]; previous != "" && previous != condition {
		hints[tier] = previous + "；或；" + condition
		return
	}
	hints[tier] = condition
}

func joinPricingConditions(parent, child string) string {
	parent = strings.TrimSpace(parent)
	child = strings.TrimSpace(child)
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return parent + " 且 " + child
}

func unwrapPricingParens(expr string) string {
	if len(expr) < 2 || expr[0] != '(' {
		return expr
	}
	if matchingPricingParen(expr, 0) != len(expr)-1 {
		return expr
	}
	return strings.TrimSpace(expr[1 : len(expr)-1])
}

func findTopLevelPricingQuestion(expr string) int {
	depth := 0
	for index := 0; index < len(expr); index++ {
		switch expr[index] {
		case '"':
			index = skipPricingString(expr, index)
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '?':
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func findMatchingPricingColon(expr string, question int) int {
	depth := 0
	nestedQuestions := 0
	for index := question + 1; index < len(expr); index++ {
		switch expr[index] {
		case '"':
			index = skipPricingString(expr, index)
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '?':
			if depth == 0 {
				nestedQuestions++
			}
		case ':':
			if depth == 0 {
				if nestedQuestions == 0 {
					return index
				}
				nestedQuestions--
			}
		}
	}
	return -1
}

func matchingPricingParen(expr string, start int) int {
	depth := 0
	for index := start; index < len(expr); index++ {
		switch expr[index] {
		case '"':
			index = skipPricingString(expr, index)
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func skipPricingString(expr string, start int) int {
	for index := start + 1; index < len(expr); index++ {
		if expr[index] == '\\' {
			index++
			continue
		}
		if expr[index] == '"' {
			return index
		}
	}
	return len(expr) - 1
}
