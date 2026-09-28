package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func approxPct(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSellGroupsPicksFirstAutoGroupThenCheapest(t *testing.T) {
	cfg := pricingGroupConfig{
		ratios:     map[string]float64{"claude": 0.8, "claude-vip": 0.5, "tob": 0.4, "default": 1},
		autoGroups: []string{"default", "claude", "claude-vip"},
		modelGroups: map[string][]string{
			"claude-x": {"claude", "claude-vip", "tob"},
			"b2b-only": {"tob", "mystery"},
		},
	}
	groups := cfg.sellGroups("claude-x")
	if len(groups) != 3 || groups[0].Group != "claude" || !groups[0].Primary || groups[1].Group != "claude-vip" || groups[2].Group != "tob" {
		t.Fatalf("auto order should win over ratio: %+v", groups)
	}
	if groups[2].AutoIndex != -1 || groups[0].AutoIndex != 1 {
		t.Fatalf("auto index = %+v", groups)
	}
	// No auto group: the cheapest group is primary; an unknown group bills at 1.
	b2b := cfg.sellGroups("b2b-only")
	if b2b[0].Group != "tob" || b2b[0].Ratio != 0.4 || !b2b[0].Primary {
		t.Fatalf("fallback primary = %+v", b2b)
	}
	if b2b[1].Group != "mystery" || b2b[1].Ratio != 1 || !b2b[1].RatioMissing {
		t.Fatalf("missing ratio = %+v", b2b[1])
	}
	if got := cfg.sellGroups("unrouted"); len(got) != 0 {
		t.Fatalf("unrouted model groups = %+v", got)
	}
}

func TestModelGroupsFromAbilitiesPrefersEnabledChannels(t *testing.T) {
	got := modelGroupsFromAbilities([]map[string]interface{}{
		{"group_name": "claude", "model_name": "m", "enabled": int64(1)},
		{"group_name": "old", "model_name": "m", "enabled": int64(0)},
		{"group_name": "auto", "model_name": "m", "enabled": int64(1)},
		{"group_name": "dead-a", "model_name": "dead", "enabled": int64(0)},
		{"group_name": "dead-b", "model_name": "dead", "enabled": int64(0)},
	})
	if len(got["m"]) != 1 || got["m"][0] != "claude" {
		t.Fatalf("m groups = %v, want only the enabled non-auto group", got["m"])
	}
	if len(got["dead"]) != 2 {
		t.Fatalf("model with no enabled channel keeps all its groups: %v", got["dead"])
	}
}

func TestMakeGroupPricedScenarioMarginsUseTheGroupPrice(t *testing.T) {
	sell := []PricingSellGroup{{Group: "chat", Ratio: 0.8, Primary: true}, {Group: "chat-vip", Ratio: 0.6}, {Group: "same", Ratio: 0.8}}
	s := makeGroupPricedScenario("chat", "per_call", "", 0.10, []costCandidate{
		{ChannelID: 1, ChannelName: "cheap", ChannelStatus: 1, CostUSD: 0.04, Serves: true},
		{ChannelID: 2, ChannelName: "dear", ChannelStatus: 1, CostUSD: 0.05, Serves: true},
	}, sell)
	if s.ListUSD != 0.10 || !approxPct(s.RetailUSD, 0.08) || s.Group != "chat" || s.GroupRatio != 0.8 {
		t.Fatalf("scenario prices = list %v retail %v group %q ratio %v", s.ListUSD, s.RetailUSD, s.Group, s.GroupRatio)
	}
	// 0.08 retail: dear route 37.5%, cheap route 50%. At list price it would
	// have read 50%..60% — the overstatement this page used to show.
	if !approxPct(s.LowestMarginPercent, 37.5) || !approxPct(s.HighestMarginPercent, 50) {
		t.Fatalf("margins = %v..%v, want 37.5..50", s.LowestMarginPercent, s.HighestMarginPercent)
	}
	if len(s.OtherGroups) != 1 || s.OtherGroups[0].Group != "chat-vip" {
		t.Fatalf("other groups = %+v, want chat-vip only (same ratio is not repeated)", s.OtherGroups)
	}
	vip := s.OtherGroups[0]
	if !approxPct(vip.RetailUSD, 0.06) || !approxPct(vip.LowestMarginPercent, 100.0/6) || !approxPct(vip.HighestMarginPercent, 100.0/3) {
		t.Fatalf("vip price = %+v", vip)
	}
}

func TestMakeGroupPricedScenarioWithoutGroupUsesListPriceAndZeroRatioIsFree(t *testing.T) {
	candidates := []costCandidate{{ChannelID: 1, ChannelStatus: 1, CostUSD: 0.01, Serves: true}}
	list := makeGroupPricedScenario("m", "1K", "", 0.02, candidates, nil)
	if list.RetailUSD != 0.02 || list.GroupRatio != 1 || list.Group != "" || !approxPct(list.LowestMarginPercent, 50) {
		t.Fatalf("no-group scenario = %+v", list)
	}
	free := makeGroupPricedScenario("m", "1K", "", 0.02, candidates, []PricingSellGroup{{Group: "free", Ratio: 0, Primary: true}})
	if free.Status != "free" || pricingScenarioHasMargin(free) || free.LowestCostUSD != 0.01 {
		t.Fatalf("zero-ratio scenario = status %q cost %v", free.Status, free.LowestCostUSD)
	}
}

// TestBuildPricingAnalysisAppliesGroupRatios runs a whole build against a
// fake gateway and real routing tables.
func TestBuildPricingAnalysisAppliesGroupRatios(t *testing.T) {
	business := newBusinessTestService(t,
		"CREATE TABLE abilities (`group` TEXT, model TEXT, channel_id INTEGER, enabled INTEGER)",
		`CREATE TABLE channels (id INTEGER PRIMARY KEY, status INTEGER)`,
		"CREATE TABLE options (`key` TEXT PRIMARY KEY, value TEXT)",
		`INSERT INTO channels VALUES (1, 1), (2, 1), (3, 2)`,
		`INSERT INTO abilities VALUES ('img-group', 'img', 1, 1), ('chat', 'chat', 2, 1), ('chat-vip', 'chat', 2, 1), ('retired', 'chat', 3, 0)`,
		`INSERT INTO options VALUES ('GroupRatio', '{"default":1,"img-group":0.5,"chat":"0.8","chat-vip":0.6,"retired":0.1}')`,
		`INSERT INTO options VALUES ('AutoGroups', '["default","img-group","chat","chat-vip"]')`,
	)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body interface{}
		switch r.URL.Path {
		case "/api/data/cost-baseline":
			body = map[string]interface{}{"success": true, "data": map[string]interface{}{"quota_per_unit": 500000, "rows": []interface{}{
				map[string]interface{}{"model_name": "img", "channel_id": 1, "channel_name": "img-ch", "channel_status": 1, "tiers": []interface{}{
					map[string]interface{}{"tier": "1K", "matched_tier": "1K", "cost": 0.004 * 500000, "serves": true},
				}},
				map[string]interface{}{"model_name": "chat", "channel_id": 2, "channel_name": "chat-ch", "channel_status": 1, "tiers": []interface{}{
					map[string]interface{}{"tier": "", "matched_tier": "", "cost": 0.05 * 500000, "serves": true},
				}},
			}}}
		case "/api/data/price-book":
			body = map[string]interface{}{"success": true, "data": map[string]interface{}{"rows": []interface{}{
				map[string]interface{}{"model_name": "img", "mode": "tiered_expr", "expr": `tier("1K", u("n") * 0.02)`, "tiers": []interface{}{map[string]interface{}{"tier": "1K", "usd": 0.02}}},
				map[string]interface{}{"model_name": "chat", "mode": "per_call", "per_call_usd": 0.10},
			}}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer gateway.Close()

	groups, err := loadPricingGroupConfig(business.db)
	if err != nil {
		t.Fatal(err)
	}
	client := &pricingHTTPClient{gateway: newAPIAdminTarget{baseURL: gateway.URL, apiKey: "test"}, client: gateway.Client()}
	result, err := buildPricingAnalysisWith(context.Background(), client, groups)
	if err != nil {
		t.Fatal(err)
	}
	byModel := map[string]PricingModelAnalysis{}
	for _, m := range result.Models {
		byModel[m.ModelName] = m
	}
	img := byModel["img"]
	if len(img.Scenarios) != 1 || img.Scenarios[0].ListUSD != 0.02 || !approxPct(img.Scenarios[0].RetailUSD, 0.01) || img.Scenarios[0].Group != "img-group" {
		t.Fatalf("img scenario = %+v", img.Scenarios)
	}
	if !approxPct(img.Scenarios[0].LowestMarginPercent, 60) {
		t.Fatalf("img margin = %v, want 60 (0.004 cost on 0.01 retail, not 80 on the 0.02 list)", img.Scenarios[0].LowestMarginPercent)
	}
	chat := byModel["chat"]
	if len(chat.SellGroups) != 2 || chat.SellGroups[0].Group != "chat" || chat.SellGroups[0].Ratio != 0.8 {
		t.Fatalf("chat groups = %+v (disabled channel's group must not count)", chat.SellGroups)
	}
	s := chat.Scenarios[0]
	if !approxPct(s.RetailUSD, 0.08) || !approxPct(s.LowestMarginPercent, 37.5) || len(s.OtherGroups) != 1 || !approxPct(s.OtherGroups[0].RetailUSD, 0.06) {
		t.Fatalf("chat scenario = %+v", s)
	}
}
