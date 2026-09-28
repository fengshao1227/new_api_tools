package service

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/new-api-tools/backend/internal/database"
	_ "modernc.org/sqlite"
)

// newBusinessTestService opens a one-connection in-memory SQLite (every new
// connection to ":memory:" is a new, empty database), runs the statements and
// points both the service and the package-level manager at it. The panel
// whitelist is reset to its default (exclude admins) so another test's saved
// IDs cannot filter these fixtures.
func newBusinessTestService(t *testing.T, statements ...string) *BusinessDashboardService {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	m := &database.Manager{DB: db}
	database.SetForTesting(m)

	globalPanelWL.mu.Lock()
	savedCfg := globalPanelWL.cfg
	globalPanelWL.cfg = PanelWhitelistConfig{UserIDs: []int64{}, ExcludeAdmins: true}
	globalPanelWL.resolved = nil
	globalPanelWL.resolvedAt = time.Time{}
	globalPanelWL.mu.Unlock()

	t.Cleanup(func() {
		globalPanelWL.mu.Lock()
		globalPanelWL.cfg = savedCfg
		globalPanelWL.resolved = nil
		globalPanelWL.resolvedAt = time.Time{}
		globalPanelWL.mu.Unlock()
		_ = db.Close()
	})
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("fixture %q: %v", statement, err)
		}
	}
	return &BusinessDashboardService{db: m, logDB: m}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func sqlf(format string, args ...interface{}) string { return fmt.Sprintf(format, args...) }

func TestResolveDashboardWindowIsCalendarAligned(t *testing.T) {
	now := time.Date(2026, 9, 28, 15, 30, 0, 0, time.Local)
	midnight := time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local)
	cases := map[string]struct {
		start time.Time
		days  int
	}{
		"today": {midnight, 1},
		"7d":    {midnight.AddDate(0, 0, -6), 7},
		"30d":   {midnight.AddDate(0, 0, -29), 30},
		"bogus": {midnight.AddDate(0, 0, -6), 7},
	}
	for key, want := range cases {
		w := ResolveDashboardWindow(key, now)
		if w.Start != want.start.Unix() || w.End != now.Unix() || w.Days != want.days {
			t.Fatalf("%s: got %+v, want start %d days %d", key, w, want.start.Unix(), want.days)
		}
	}
	if IsDashboardWindow("24h") || !IsDashboardWindow("30d") {
		t.Fatal("IsDashboardWindow accepts the wrong keys")
	}
}

func TestIsMissingSchemaErrMatchesTablesAndColumnsOnly(t *testing.T) {
	missing := []string{
		`ERROR: relation "risk_events" does not exist (SQLSTATE 42P01)`,
		`ERROR: column "cost_expr" does not exist (SQLSTATE 42703)`,
		`Error 1146 (42S02): Table 'newapi.tasks' doesn't exist`,
		`Error 1054 (42S22): Unknown column 'signup_country' in 'field list'`,
		`SQL logic error: no such table: upstream_monitors (1)`,
		`SQL logic error: no such column: acquisition_source (1)`,
	}
	for _, msg := range missing {
		if !isMissingSchemaErr(errors.New(msg)) {
			t.Errorf("not recognised as missing schema: %s", msg)
		}
	}
	real := []string{
		`ERROR: operator does not exist: text ->> unknown (SQLSTATE 42883)`,
		`Error 1305 (42000): FUNCTION newapi.JSON_FOO does not exist`,
		`context deadline exceeded`,
	}
	for _, msg := range real {
		if isMissingSchemaErr(errors.New(msg)) {
			t.Errorf("real failure hidden as missing schema: %s", msg)
		}
	}
	if isMissingSchemaErr(nil) {
		t.Error("nil error treated as missing schema")
	}
}

func TestModelRankingExcludesFreeModels(t *testing.T) {
	ranking := buildBusinessModelRanking([]MarginBreakdown{
		{Name: "jev-1.13-free", Requests: 5000, BilledUSD: 0, ProviderCostUSD: 0.8},
		{Name: "gpt-6-sol", Requests: 40, BilledUSD: 12, RevenueUSD: 9, ProviderCostUSD: 7, GrossProfitUSD: 2},
		{Name: "claude-opus-5-5", Requests: 10, BilledUSD: 6, RevenueUSD: 6, ProviderCostUSD: 2, GrossProfitUSD: 4},
		{Name: "seedance", Requests: 3, BilledUSD: 1, RevenueUSD: 0, ProviderCostUSD: 9, GrossProfitUSD: -9},
	}, 2)

	for name, rows := range map[string][]BusinessModelRow{"billed": ranking.ByBilled, "cost": ranking.ByCost, "profit": ranking.ByProfit} {
		if len(rows) != 2 {
			t.Fatalf("%s ranking has %d rows, want the limit 2", name, len(rows))
		}
		for _, row := range rows {
			if row.Model == "jev-1.13-free" {
				t.Fatalf("free model ranked by %s: %+v", name, rows)
			}
		}
	}
	if ranking.ByBilled[0].Model != "gpt-6-sol" || ranking.ByCost[0].Model != "seedance" || ranking.ByProfit[0].Model != "claude-opus-5-5" {
		t.Fatalf("rank order wrong: billed %s, cost %s, profit %s",
			ranking.ByBilled[0].Model, ranking.ByCost[0].Model, ranking.ByProfit[0].Model)
	}
	if len(ranking.ExcludedFreeModels) != 1 || ranking.ExcludedFreeModels[0] != "jev-1.13-free" ||
		ranking.ExcludedFreeRequests != 5000 || !approx(ranking.ExcludedFreeCostUSD, 0.8) {
		t.Fatalf("excluded free summary = %+v", ranking)
	}
}

func TestBuildConversionComputesFunnelAndSplits(t *testing.T) {
	users := []conversionUser{
		{ID: 1, Source: "Linux.DO", Country: "cn", UsedQuota: 100}, // used + paid
		{ID: 2, Source: "linux.do", Country: "CN", Requests: 3},    // used via counter
		{ID: 3, Source: "", Country: "US"},                         // used via free-model log
		{ID: 4, Source: "google", Country: ""},                     // never used
		{ID: 5, Source: "google", Country: "US", UsedQuota: 0},     // paid without use
	}
	result := buildConversion(users, map[int64]bool{3: true}, map[int64]bool{1: true, 5: true}, 8)

	if result.Signups != 5 || result.Activated != 3 || result.Paid != 2 {
		t.Fatalf("funnel = %d/%d/%d, want 5/3/2", result.Signups, result.Activated, result.Paid)
	}
	if !approx(result.ActivationRate, 0.6) || !approx(result.PaidRate, 0.4) || !approx(result.PaidOfActivatedRate, 1.0/3) {
		t.Fatalf("rates = %v/%v/%v", result.ActivationRate, result.PaidRate, result.PaidOfActivatedRate)
	}
	sources := map[string]BusinessConversionBucket{}
	for _, b := range result.BySource {
		sources[b.Key] = b
	}
	if b := sources["linux.do"]; b.Signups != 2 || b.Activated != 2 || b.Paid != 1 || !approx(b.PaidRate, 0.5) {
		t.Fatalf("linux.do bucket = %+v", b)
	}
	if b := sources[conversionUnattributed]; b.Signups != 1 || b.Activated != 1 {
		t.Fatalf("unattributed bucket = %+v", b)
	}
	countries := map[string]BusinessConversionBucket{}
	for _, b := range result.ByCountry {
		countries[b.Key] = b
	}
	if countries["CN"].Signups != 2 || countries["US"].Signups != 2 || countries[conversionUnknownPlace].Signups != 1 {
		t.Fatalf("country buckets = %+v", result.ByCountry)
	}
	if empty := buildConversion(nil, nil, nil, 8); empty.ActivationRate != 0 || empty.PaidRate != 0 || empty.PaidOfActivatedRate != 0 {
		t.Fatalf("empty cohort rates = %+v", empty)
	}
}

func TestGetConversionReadsCohortLogsAndTopUps(t *testing.T) {
	now := time.Now().Unix()
	old := now - 90*86400
	svc := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, role INTEGER, deleted_at INTEGER, created_at INTEGER,
			acquisition_source TEXT, signup_country TEXT, used_quota INTEGER, request_count INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, user_id INTEGER, type INTEGER, quota INTEGER, created_at INTEGER)`,
		sqlf(`INSERT INTO users VALUES (1, 1, NULL, %d, 'linux.do', 'CN', 500, 2)`, now-60),
		sqlf(`INSERT INTO users VALUES (2, 1, NULL, %d, 'linux.do', 'CN', 0, 0)`, now-60),
		sqlf(`INSERT INTO users VALUES (3, 1, NULL, %d, '', 'US', 0, 0)`, now-60),
		sqlf(`INSERT INTO users VALUES (4, 100, NULL, %d, '', 'US', 900, 9)`, now-60),  // admin: excluded
		sqlf(`INSERT INTO users VALUES (5, 1, NULL, %d, 'google', 'US', 800, 8)`, old), // outside window
		`INSERT INTO top_ups VALUES (1, 1, 'success')`,
		`INSERT INTO top_ups VALUES (2, 3, 'pending')`,
		`INSERT INTO top_ups VALUES (3, 5, 'success')`,
		sqlf(`INSERT INTO logs VALUES (1, 2, 2, 0, %d)`, now-30), // free-model call
	)

	result, err := svc.GetConversion(DashboardWindow7d, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Signups != 3 || result.Activated != 2 || result.Paid != 1 || !result.AttributionAvailable {
		t.Fatalf("conversion = %+v", result)
	}
}

func TestPricingGapsListsUncoveredModelsAndCountsUnpriced(t *testing.T) {
	now := time.Now().Unix()
	svc := newBusinessTestService(t,
		`CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT, status INTEGER, priority INTEGER, models TEXT, cost_expr TEXT)`,
		"CREATE TABLE options (`key` TEXT PRIMARY KEY, value TEXT)",
		`CREATE TABLE quota_data (id INTEGER PRIMARY KEY, channel_id INTEGER, model_name TEXT, created_at INTEGER, count INTEGER, unpriced_count INTEGER)`,
		`INSERT INTO channels VALUES (1, 'priced', 1, 10, 'gpt-a,gpt-b', '{"gpt-a":"p*1","gpt-b":"p*2"}')`,
		`INSERT INTO channels VALUES (2, 'added-a-model', 1, 20, 'gpt-a, gpt-new', '{"gpt-a":"p*1"}')`,
		`INSERT INTO channels VALUES (3, 'empty-expr', 1, 5, 'luna', NULL)`,
		`INSERT INTO channels VALUES (4, 'defaults-cover-it', 1, 5, 'shared-model', '')`,
		`INSERT INTO channels VALUES (5, 'disabled', 2, 99, 'gpt-x', NULL)`,
		`INSERT INTO options VALUES ('cost_setting.default_cost_expr', '{"shared-model":"p*3","blank":"  "}')`,
		sqlf(`INSERT INTO quota_data VALUES (1, 2, 'gpt-new', %d, 7, 4)`, now-60),
		sqlf(`INSERT INTO quota_data VALUES (2, 1, 'gpt-a', %d, 9, 0)`, now-60),
		sqlf(`INSERT INTO quota_data VALUES (3, 3, 'luna', %d, 2, 2)`, now-60),
		sqlf(`INSERT INTO quota_data VALUES (4, 3, 'luna', %d, 5, 5)`, now-90*86400), // outside window
	)

	gaps, err := svc.GetPricingGaps(DashboardWindow7d, true)
	if err != nil {
		t.Fatal(err)
	}
	if !gaps.ChannelsAvailable || len(gaps.Channels) != 2 {
		t.Fatalf("gap channels = %+v", gaps.Channels)
	}
	byID := map[int64]BusinessPricingChannel{}
	for _, c := range gaps.Channels {
		byID[c.ID] = c
	}
	if c := byID[2]; c.NoCostExpr || len(c.MissingModels) != 1 || c.MissingModels[0] != "gpt-new" {
		t.Fatalf("partially priced channel = %+v", c)
	}
	if c := byID[3]; !c.NoCostExpr || len(c.MissingModels) != 1 || c.MissingModels[0] != "luna" {
		t.Fatalf("unpriced channel = %+v", c)
	}
	if gaps.UnpricedSource != "quota_data" || gaps.UnpricedCalls != 6 || len(gaps.Unpriced) != 2 {
		t.Fatalf("unpriced = source %q calls %d rows %+v", gaps.UnpricedSource, gaps.UnpricedCalls, gaps.Unpriced)
	}
	if top := gaps.Unpriced[0]; top.ChannelID != 2 || top.ChannelName != "added-a-model" || top.Model != "gpt-new" || top.Calls != 4 {
		t.Fatalf("top unpriced row = %+v", top)
	}
}

func TestTasksHealthFoldsReasonsAndRefunds(t *testing.T) {
	now := time.Now().Unix()
	svc := newBusinessTestService(t,
		`CREATE TABLE tasks (id INTEGER PRIMARY KEY, created_at INTEGER, platform TEXT, status TEXT, fail_reason TEXT, properties TEXT)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, user_id INTEGER, type INTEGER, quota INTEGER, created_at INTEGER)`,
		sqlf(`INSERT INTO tasks VALUES (1, %d, 'kling', 'SUCCESS', '', '{"origin_model_name":"kling-v3"}')`, now-60),
		sqlf(`INSERT INTO tasks VALUES (2, %d, 'kling', 'FAILURE', 'upstream task 123456 timed out', '{"origin_model_name":"kling-v3"}')`, now-60),
		sqlf(`INSERT INTO tasks VALUES (3, %d, 'kling', 'FAILURE', 'upstream task 998877 timed out', '{"origin_model_name":"kling-v3"}')`, now-60),
		sqlf(`INSERT INTO tasks VALUES (4, %d, 'suno', 'IN_PROGRESS', '', '{"origin_model_name":"suno-v5"}')`, now-60),
		sqlf(`INSERT INTO tasks VALUES (5, %d, 'suno', 'FAILURE', 'bad prompt', '{}')`, now-90*86400), // outside window
		sqlf(`INSERT INTO logs VALUES (1, 7, 6, 250000, %d)`, now-60),
		sqlf(`INSERT INTO logs VALUES (2, 7, 2, 900000, %d)`, now-60),
	)

	tasks, err := svc.GetTasks(DashboardWindow7d, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tasks.Available || tasks.Total != 4 || tasks.Success != 1 || tasks.Failure != 2 || tasks.InFlight != 1 {
		t.Fatalf("task totals = %+v", tasks)
	}
	if !approx(tasks.FailureRate, 2.0/3) {
		t.Fatalf("failure rate = %v, want failures over finished tasks", tasks.FailureRate)
	}
	if len(tasks.Rows) != 2 || tasks.Rows[0].Model != "kling-v3" || tasks.Rows[0].Failure != 2 {
		t.Fatalf("task rows = %+v", tasks.Rows)
	}
	if len(tasks.Reasons) != 1 || tasks.Reasons[0].Count != 2 || tasks.Reasons[0].Reason != "upstream task # timed out" {
		t.Fatalf("failure reasons = %+v", tasks.Reasons)
	}
	if tasks.RefundCount != 1 || !approx(tasks.RefundUSD, 0.5) {
		t.Fatalf("refunds = %d / %v", tasks.RefundCount, tasks.RefundUSD)
	}
}

func TestBusinessSectionsDegradeWhenTablesAreMissing(t *testing.T) {
	now := time.Now().Unix()
	// A plain new-api: no tasks, risk_events, upstream_monitors,
	// ops_alert_states, channels.cost_expr or quota_data, and logs without the
	// admin_info column.
	svc := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, role INTEGER, status INTEGER, deleted_at INTEGER, created_at INTEGER,
			quota INTEGER, topup_quota INTEGER, granted_quota INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, user_id INTEGER, type INTEGER, quota INTEGER, created_at INTEGER, channel_id INTEGER, model_name TEXT)`,
		sqlf(`INSERT INTO users VALUES (1, 1, 1, NULL, %d, 250000, 0, 500000)`, now-60),
		sqlf(`INSERT INTO users VALUES (2, 1, 1, NULL, %d, 1000000, 0, 500000)`, now-60),
		sqlf(`INSERT INTO users VALUES (3, 1, 1, NULL, %d, 5000000, 5000000, 0)`, now-90*86400),
		`INSERT INTO top_ups VALUES (1, 2, 'success')`,
	)

	tasks, err := svc.GetTasks(DashboardWindow7d, true)
	if err != nil || tasks.Available || len(tasks.Rows) != 0 {
		t.Fatalf("tasks without a tasks table = %+v, %v", tasks, err)
	}
	supply, err := svc.GetSupply(true)
	if err != nil || supply.UpstreamAvailable || supply.AlertsAvailable || supply.Upstreams == nil || supply.Alerts == nil {
		t.Fatalf("supply without monitor tables = %+v, %v", supply, err)
	}
	giftsRisk, err := svc.GetGiftsRisk(DashboardWindow7d, true)
	if err != nil || giftsRisk.Risk.Available {
		t.Fatalf("risk without risk_events = %+v, %v", giftsRisk, err)
	}
	gifts := giftsRisk.Gifts
	if !gifts.Available || gifts.Signups != 2 || gifts.GrantedUsers != 2 || !approx(gifts.GrantedUSD, 2) {
		t.Fatalf("gifts granted = %+v", gifts)
	}
	// Only user 1 is an enabled, never-paid regular account with a balance.
	if gifts.LiabilityUsers != 1 || !approx(gifts.LiabilityUSD, 0.5) {
		t.Fatalf("gift liability = %+v", gifts)
	}
	gaps, err := svc.GetPricingGaps(DashboardWindow7d, true)
	if err != nil || gaps.ChannelsAvailable || gaps.UnpricedSource != "" || gaps.UnpricedCalls != 0 {
		t.Fatalf("pricing gaps without channels/quota_data = %+v, %v", gaps, err)
	}
}

func TestLoadCashConvertsCNYAndSkipsUnknownRails(t *testing.T) {
	now := time.Now().Unix()
	svc := newBusinessTestService(t,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT, money REAL, amount INTEGER,
			payment_method TEXT, trade_no TEXT, create_time INTEGER, complete_time INTEGER)`,
		sqlf(`INSERT INTO top_ups VALUES (1, 1, 'success', 70, 10, 'alipay', 't1', %d, %d)`, now-120, now-60),
		sqlf(`INSERT INTO top_ups VALUES (2, 2, 'success', 10, 10, 'stripe', 't2', %d, 0)`, now-60), // no complete_time
		sqlf(`INSERT INTO top_ups VALUES (3, 2, 'pending', 99, 99, 'stripe', 't3', %d, 0)`, now-60),
		sqlf(`INSERT INTO top_ups VALUES (4, 3, 'success', 50, 50, 'mystery-rail', 't4', %d, %d)`, now-60, now-60),
		sqlf(`INSERT INTO top_ups VALUES (5, 4, 'success', 10, 10, 'stripe', 't5', %d, %d)`, now-90*86400, now-90*86400),
	)
	cash, byDay, err := svc.loadCash(ResolveDashboardWindow(DashboardWindow7d, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if !approx(cash.RevenueUSD, 20) || cash.Orders != 3 || cash.Payers != 3 || cash.UnknownCurrencyOrders != 1 {
		t.Fatalf("cash = %+v", cash)
	}
	var total float64
	for _, v := range byDay {
		total += v
	}
	if !approx(total, 20) {
		t.Fatalf("daily cash sums to %v, want 20", total)
	}
}

func TestMarginAccumulatorTracksBilledAndUnpaidBilled(t *testing.T) {
	var acc marginAccumulator
	addMarginGroup(&acc, marginGroupRow{Requests: 1, Quota: 100, Cost: 40}, marginUserState{Bucket: "customer_paid"}, 0, 0)
	addMarginGroup(&acc, marginGroupRow{Requests: 1, Quota: 30, Cost: 20}, marginUserState{Bucket: "customer_free"}, 30, 20)
	addMarginGroup(&acc, marginGroupRow{Requests: 1, Quota: 50, Cost: 10}, marginUserState{Bucket: "staff_or_root"}, 0, 0)
	addMarginGroup(&acc, marginGroupRow{Requests: 1, Quota: 0, Cost: 5}, marginUserState{Bucket: "deleted_no_payment"}, 0, 0)

	if acc.BilledQuota != 180 {
		t.Fatalf("billed = %v, want every bucket's quota (180)", acc.BilledQuota)
	}
	if acc.FreeBilledQuota != 30 {
		t.Fatalf("unpaid billed = %v, want only never-paid accounts (30)", acc.FreeBilledQuota)
	}
	merged := marginAccumulator{}.merge(acc)
	if merged.BilledQuota != 180 || merged.FreeBilledQuota != 30 {
		t.Fatalf("merge dropped billed fields: %+v", merged)
	}
}

func TestNormalizeFailReasonFoldsIDs(t *testing.T) {
	a := normalizeFailReason("task 3f2c1a9e-1b2c-4d5e-8f90-123456789abc failed:\n  quota   exceeded")
	b := normalizeFailReason("task 00000000-aaaa-bbbb-cccc-000000000000 failed: quota exceeded")
	if a != b || a != "task <id> failed: quota exceeded" {
		t.Fatalf("normalised reasons differ: %q vs %q", a, b)
	}
	if normalizeFailReason("   ") != "(empty)" {
		t.Fatal("blank reason not labelled")
	}
}
