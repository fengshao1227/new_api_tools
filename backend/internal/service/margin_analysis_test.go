package service

import "testing"

func TestBuildMarginBucketSeparatesPaidFreeInternalAndDeletedUsers(t *testing.T) {
	tests := []struct {
		name  string
		role  int64
		found bool
		paid  bool
		topUp int64
		want  string
	}{
		{name: "paid customer", role: 1, found: true, paid: true, want: "customer_paid"},
		{name: "free customer", role: 1, found: true, want: "customer_free"},
		{name: "manual credit", role: 1, found: true, topUp: 500000, want: "manual_or_test_credit"},
		{name: "staff", role: 100, found: true, paid: true, want: "staff_or_root"},
		{name: "deleted paid", paid: true, want: "deleted_paid"},
		{name: "deleted free", want: "deleted_no_payment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildMarginBucket(tt.role, tt.found, tt.paid, tt.topUp); got != tt.want {
				t.Fatalf("buildMarginBucket() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClampGiftQuotaNeverExceedsPeriodSpend(t *testing.T) {
	tests := []struct {
		name                  string
		grant, before, period float64
		want                  float64
	}{
		{name: "all spend is gift", grant: 100, before: 0, period: 40, want: 40},
		{name: "gift is exhausted", grant: 100, before: 100, period: 40, want: 0},
		{name: "remaining gift crosses period", grant: 100, before: 75, period: 40, want: 25},
		{name: "negative inputs fail closed", grant: 100, before: 120, period: -1, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampGiftQuota(tt.grant, tt.before, tt.period); got != tt.want {
				t.Fatalf("clampGiftQuota() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddMarginGroupChargesFreeCostButNoFreeRevenue(t *testing.T) {
	state := marginUserState{Bucket: "customer_free"}
	row := marginGroupRow{Requests: 2, Quota: 100, Cost: 30, Unpriced: 1}
	var acc marginAccumulator
	addMarginGroup(&acc, row, state, 100, 20)

	if acc.RevenueQuota != 0 {
		t.Fatalf("free traffic created revenue: %v", acc.RevenueQuota)
	}
	if acc.ProviderCost != 30 || acc.GiftFreeCost != 30 {
		t.Fatalf("free cost accounting = provider %v/free %v, want 30/30", acc.ProviderCost, acc.GiftFreeCost)
	}
	if acc.GiftProviderCost != 20 || acc.GiftNominalQuota != 100 || acc.UnpricedCalls != 1 {
		t.Fatalf("gift diagnostics = cost %v/quota %v/unpriced %v", acc.GiftProviderCost, acc.GiftNominalQuota, acc.UnpricedCalls)
	}
}

func TestAddMarginGroupPaidRevenueExcludesGiftPortion(t *testing.T) {
	state := marginUserState{Bucket: "customer_paid"}
	row := marginGroupRow{Requests: 1, Quota: 100, Cost: 30}
	var acc marginAccumulator
	addMarginGroup(&acc, row, state, 25, 7.5)

	if acc.RevenueQuota != 75 {
		t.Fatalf("paid revenue = %v, want 75", acc.RevenueQuota)
	}
	if acc.PaidTrafficCost != 22.5 || acc.GiftProviderCost != 7.5 {
		t.Fatalf("paid cost split = traffic %v/gift %v", acc.PaidTrafficCost, acc.GiftProviderCost)
	}
}

func TestAddMarginGroupDeletedPaidAccountHasNoRevenue(t *testing.T) {
	state := marginUserState{Bucket: "deleted_paid"}
	row := marginGroupRow{Requests: 1, Quota: 500000, Cost: 400000}
	var acc marginAccumulator
	addMarginGroup(&acc, row, state, 0, 0)

	if acc.RevenueQuota != 0 {
		t.Fatalf("deleted paid revenue = %v, want 0", acc.RevenueQuota)
	}
	if acc.NonRevenueCost != 400000 || acc.ProviderCost != 400000 {
		t.Fatalf("deleted paid cost = non-revenue %v/provider %v", acc.NonRevenueCost, acc.ProviderCost)
	}
}

func TestMakePricingScenarioReportsBestAndWorstServedRoute(t *testing.T) {
	scenario := makePricingScenario("demo", "1080p", "resolution == 1080p", 1.0, []costCandidate{
		{CostUSD: 0.2, ChannelName: "cheap", Serves: true},
		{CostUSD: 0.6, ChannelName: "expensive", Serves: true},
		{CostUSD: 0.01, ChannelName: "unsupported", Serves: false},
	})

	if scenario.Status != "healthy" {
		t.Fatalf("scenario status = %q, want healthy", scenario.Status)
	}
	if scenario.LowestCostUSD != 0.2 || scenario.HighestCostUSD != 0.6 {
		t.Fatalf("cost range = %v..%v, want 0.2..0.6", scenario.LowestCostUSD, scenario.HighestCostUSD)
	}
	if scenario.HighestMarginPercent != 80 || scenario.LowestMarginPercent != 40 {
		t.Fatalf("margin range = %v..%v, want 80..40", scenario.HighestMarginPercent, scenario.LowestMarginPercent)
	}
	if scenario.LowestCostChannel != "cheap" || scenario.HighestCostChannel != "expensive" {
		t.Fatalf("route selection = %q/%q", scenario.LowestCostChannel, scenario.HighestCostChannel)
	}
}

func TestPricingConditionHintsFollowEachTernaryBranch(t *testing.T) {
	hints := pricingConditionHints(`u("resolution") == "4K" ? tier("4K", u("n") * 0.1) : u("resolution") == "2K" ? tier("2K", u("n") * 0.05) : tier("1K", u("n") * 0.05)`)
	want := map[string]string{
		"4K": `u("resolution") == "4K"`,
		"2K": `否则（u("resolution") == "4K"） 且 u("resolution") == "2K"`,
		"1K": `否则（u("resolution") == "4K"） 且 否则（u("resolution") == "2K"）`,
	}
	for tier, expected := range want {
		if hints[tier] != expected {
			t.Fatalf("condition for %s = %q, want %q", tier, hints[tier], expected)
		}
	}
}

func TestPricingConditionHintsHandleNestedTernaries(t *testing.T) {
	hints := pricingConditionHints(`u("credits") > 0 ? (u("resolution") == "1080p" ? tier("1080p", u("credits") * 0.005) : tier("720p", u("credits") * 0.004)) : tier("duration", u("duration") * 0.1)`)
	if got := hints["1080p"]; got != `u("credits") > 0 且 u("resolution") == "1080p"` {
		t.Fatalf("nested 1080p condition = %q", got)
	}
	if got := hints["720p"]; got != `u("credits") > 0 且 否则（u("resolution") == "1080p"）` {
		t.Fatalf("nested 720p condition = %q", got)
	}
	if got := hints["duration"]; got != `否则（u("credits") > 0）` {
		t.Fatalf("fallback condition = %q", got)
	}
}

func TestMakePricingScenarioExcludesDisabledChannelsFromMarginRange(t *testing.T) {
	scenario := makePricingScenario("image", "2K", `u("resolution") == "2K"`, 0.05, []costCandidate{
		{ChannelID: 1, ChannelName: "enabled", ChannelStatus: 1, Priority: 10, CostUSD: 0.05, Serves: true},
		{ChannelID: 2, ChannelName: "manually-disabled", ChannelStatus: 2, Priority: 1, CostUSD: 0.09, Serves: true},
	})

	if scenario.LowestCostUSD != 0.05 || scenario.HighestCostUSD != 0.05 {
		t.Fatalf("disabled channel changed cost range = %v..%v", scenario.LowestCostUSD, scenario.HighestCostUSD)
	}
	if scenario.LowestMarginPercent != 0 || scenario.HighestMarginPercent != 0 {
		t.Fatalf("margin range = %v..%v, want 0..0", scenario.LowestMarginPercent, scenario.HighestMarginPercent)
	}
	if scenario.ServedRoutes != 1 || scenario.UnservedRoutes != 0 || len(scenario.Routes) != 2 {
		t.Fatalf("route accounting = served %d/unserved %d/routes %d", scenario.ServedRoutes, scenario.UnservedRoutes, len(scenario.Routes))
	}
	if scenario.Routes[0].ChannelName != "enabled" || !scenario.Routes[0].Selectable || scenario.Routes[1].Status != "disabled" {
		t.Fatalf("route detail did not preserve selectable/disabled state: %+v", scenario.Routes)
	}
}
