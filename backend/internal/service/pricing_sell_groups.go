package service

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
)

// Group ratios on the cost-baseline page.
//
// /api/data/price-book reports each model's list price "before the group
// ratio" (new-api service/model_price_book.go). A request is billed at
// list × GroupRatio[the group it is routed in] (relay/helper/price.go
// HandleGroupRatio), and BeatAPI discounts by editing that ratio, never the
// list price. A margin computed on the list price therefore overstates every
// discounted model, so the page prices each scenario at the model's primary
// group and lists the other groups it sells in.
//
// Primary group: the first AutoGroups entry that routes the model — where an
// auto token's request actually bills, and auto is how customers call. A model
// in no auto group falls back to its cheapest group, so the margin shown is
// the lower bound rather than a guess.
//
// Not modelled: per-customer ratios (GroupGroupRatio[userGroup][group]) and
// per-customer image prices (group_billing_expr). Both apply to individual
// B2B groups only.

// PricingSellGroup is one group a model is routed in.
type PricingSellGroup struct {
	Group string  `json:"group"`
	Ratio float64 `json:"ratio"`
	// RatioMissing: the group has no GroupRatio entry; the gateway bills it
	// at 1, and so does this page.
	RatioMissing bool `json:"ratio_missing,omitempty"`
	// AutoIndex is the group's position in AutoGroups, -1 when auto never
	// routes to it.
	AutoIndex int  `json:"auto_index"`
	Primary   bool `json:"primary,omitempty"`
}

// PricingGroupPrice is a scenario priced in one of the model's other groups.
type PricingGroupPrice struct {
	Group                string  `json:"group"`
	Ratio                float64 `json:"ratio"`
	RetailUSD            float64 `json:"retail_usd"`
	LowestMarginPercent  float64 `json:"lowest_margin_percent"`
	HighestMarginPercent float64 `json:"highest_margin_percent"`
}

// pricingGroupConfig is the gateway's routing groups as the database has them.
type pricingGroupConfig struct {
	ratios     map[string]float64
	autoGroups []string
	// modelGroups: model → groups that route it through an enabled channel,
	// or through any channel when none is enabled.
	modelGroups map[string][]string
}

const pricingGroupQueryTimeout = 10 * time.Second

// loadPricingGroupConfig reads abilities, GroupRatio and AutoGroups. An error
// leaves an empty config, which prices everything at the list price.
func loadPricingGroupConfig(db *database.Manager) (pricingGroupConfig, error) {
	cfg := pricingGroupConfig{ratios: map[string]float64{}, modelGroups: map[string][]string{}}
	groupCol := db.QuoteIdentifier("group")
	rows, err := db.QueryWithTimeout(pricingGroupQueryTimeout, fmt.Sprintf(`
		SELECT COALESCE(NULLIF(a.%s, ''), 'default') AS group_name, a.model AS model_name,
			MAX(CASE WHEN c.status = 1 THEN 1 ELSE 0 END) AS enabled
		FROM abilities a
		LEFT JOIN channels c ON c.id = a.channel_id
		WHERE a.model <> ''
		GROUP BY COALESCE(NULLIF(a.%s, ''), 'default'), a.model`, groupCol, groupCol))
	if err != nil {
		return cfg, fmt.Errorf("读取 abilities 失败: %w", err)
	}
	cfg.modelGroups = modelGroupsFromAbilities(rows)

	keyCol := db.QuoteIdentifier("key")
	options, err := db.QueryWithTimeout(pricingGroupQueryTimeout, db.RebindQuery(fmt.Sprintf(
		"SELECT %s AS opt_key, value FROM options WHERE %s IN (?, ?)", keyCol, keyCol)), "GroupRatio", "AutoGroups")
	if err != nil {
		return cfg, fmt.Errorf("读取分组倍率失败: %w", err)
	}
	for _, row := range options {
		value := strings.TrimSpace(toString(row["value"]))
		switch toString(row["opt_key"]) {
		case "GroupRatio":
			cfg.ratios = parseGroupRatios(value)
		case "AutoGroups":
			var groups []string
			if json.Unmarshal([]byte(value), &groups) == nil {
				cfg.autoGroups = groups
			}
		}
	}
	return cfg, nil
}

func modelGroupsFromAbilities(rows []map[string]interface{}) map[string][]string {
	enabled := map[string][]string{}
	all := map[string][]string{}
	for _, row := range rows {
		group := strings.TrimSpace(toString(row["group_name"]))
		model := strings.TrimSpace(toString(row["model_name"]))
		// "auto" is a token-level routing instruction, never a billing group;
		// stray abilities rows for it are ignored.
		if group == "" || group == "auto" || model == "" {
			continue
		}
		all[model] = append(all[model], group)
		if toInt64(row["enabled"]) > 0 {
			enabled[model] = append(enabled[model], group)
		}
	}
	result := make(map[string][]string, len(all))
	for model, groups := range all {
		if len(enabled[model]) > 0 {
			groups = enabled[model]
		}
		sort.Strings(groups)
		result[model] = groups
	}
	return result
}

// parseGroupRatios reads GroupRatio, whose values may be numbers or numeric
// strings. Anything else is dropped (and then bills at 1, like the gateway).
func parseGroupRatios(raw string) map[string]float64 {
	ratios := map[string]float64{}
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return ratios
	}
	for group, value := range parsed {
		switch v := value.(type) {
		case float64:
			ratios[group] = v
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				ratios[group] = f
			}
		}
	}
	return ratios
}

// sellGroups lists the model's groups, primary first, then the other auto
// groups in AutoGroups order, then the rest by ratio.
func (cfg pricingGroupConfig) sellGroups(model string) []PricingSellGroup {
	names := cfg.modelGroups[model]
	groups := make([]PricingSellGroup, 0, len(names))
	for _, name := range names {
		ratio, ok := cfg.ratios[name]
		if !ok || ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			ratio, ok = 1, false
		}
		groups = append(groups, PricingSellGroup{Group: name, Ratio: ratio, RatioMissing: !ok, AutoIndex: indexOf(cfg.autoGroups, name)})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if (a.AutoIndex >= 0) != (b.AutoIndex >= 0) {
			return a.AutoIndex >= 0
		}
		if a.AutoIndex != b.AutoIndex {
			return a.AutoIndex < b.AutoIndex
		}
		if a.Ratio != b.Ratio {
			return a.Ratio < b.Ratio
		}
		return a.Group < b.Group
	})
	if len(groups) > 0 {
		groups[0].Primary = true
	}
	return groups
}

func indexOf(list []string, value string) int {
	for i, item := range list {
		if item == value {
			return i
		}
	}
	return -1
}

// makeGroupPricedScenario prices one list price at the primary group's ratio
// and adds the other groups whose ratio differs. A model with no group is
// priced at the list price (ratio 1), which is what the gateway would bill.
func makeGroupPricedScenario(model, tier, condition string, list float64, candidates []costCandidate, sell []PricingSellGroup) PricingScenario {
	primary := PricingSellGroup{Ratio: 1}
	if len(sell) > 0 {
		primary = sell[0]
	}
	scenario := makePricingScenario(model, tier, condition, list*primary.Ratio, candidates)
	scenario.ListUSD = list
	scenario.Group = primary.Group
	scenario.GroupRatio = primary.Ratio
	if list > 0 && primary.Ratio == 0 {
		// A zero ratio gives the model away in that group: not unpriced,
		// and not a margin anybody can compare.
		scenario.Status = "free"
	}
	for _, group := range sell {
		if group.Primary || group.Ratio == primary.Ratio {
			continue
		}
		scenario.OtherGroups = append(scenario.OtherGroups, groupPrice(scenario, list, group))
	}
	return scenario
}

func groupPrice(scenario PricingScenario, list float64, group PricingSellGroup) PricingGroupPrice {
	price := PricingGroupPrice{Group: group.Group, Ratio: group.Ratio, RetailUSD: list * group.Ratio}
	if price.RetailUSD > 0 && scenario.CostRoutes > 0 {
		price.LowestMarginPercent = (price.RetailUSD - scenario.HighestCostUSD) / price.RetailUSD * 100
		price.HighestMarginPercent = (price.RetailUSD - scenario.LowestCostUSD) / price.RetailUSD * 100
	}
	return price
}

// pricingScenarioHasMargin reports whether a scenario takes part in the
// best/worst rankings: it needs both a retail price and a cost.
func pricingScenarioHasMargin(scenario PricingScenario) bool {
	return scenario.Status != "unpriced" && scenario.Status != "free"
}
