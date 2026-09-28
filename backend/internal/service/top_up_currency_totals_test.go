package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// mixedRailSeeds is one settled order on each kind of rail plus the statuses
// that must stay out of revenue.
func mixedRailSeeds() []topUpSeed {
	return []topUpSeed{
		{id: 1, provider: "epay", method: "alipay", amount: 10, money: 70, status: "success"},
		{id: 2, provider: "stripe", method: "stripe", amount: 10, money: 10, status: "success"},
		{id: 3, provider: "paypal", method: "paypal", amount: 5_000_000, money: 10, status: "success"},
		{id: 4, provider: "beatapi", method: "crypto", amount: 20, money: 20, status: "success"},
		{id: 5, provider: "stripe", method: "stripe", amount: 10, money: 10, status: "reviewing"},
		{id: 6, provider: "epay", method: "wxpay", amount: 10, money: 70, status: "pending"},
	}
}

func TestGetTopUpStatistics_SumsUSDAndSeparatesUnknownCurrency(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	insertTopUpSeeds(t, db, mixedRailSeeds())

	s, err := GetTopUpStatistics("", "")
	if err != nil {
		t.Fatalf("statistics: %v", err)
	}
	checks := []struct {
		name      string
		got, want float64
	}{
		{"total_count", float64(s.TotalCount), 6},
		{"success_count", float64(s.SuccessCount), 4},
		// ¥70 → $10, Stripe $10, PayPal $10; the unknown-currency $20 row stays out.
		{"success_money_usd", s.SuccessMoneyUSD, 30},
		// 10 + 10 (Stripe money) + 10 (PayPal quota ÷ 500000) + 20 (unknown still credits)
		{"success_amount_usd", s.SuccessAmountUSD, 50},
		{"success_cny_money", s.SuccessCNYMoney, 70},
		{"success_usd_money", s.SuccessUSDMoney, 20},
		{"success_unknown_currency_count", float64(s.SuccessUnknownCurrencyCount), 1},
		{"success_unknown_currency_money", s.SuccessUnknownCurrencyMoney, 20},
		{"unknown_currency_count", float64(s.UnknownCurrencyCount), 1},
		{"reviewing_count", float64(s.ReviewingCount), 1},
		{"reviewing_money_usd", s.ReviewingMoneyUSD, 10},
		{"pending_money_usd", s.PendingMoneyUSD, 10},
		{"unknown_status_count", float64(s.UnknownCount), 0},
		{"cny_per_usd", s.CNYPerUSD, 7},
	}
	for _, c := range checks {
		if !topUpNear(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestListTopUpRecords_CurrencyFilterAndRowFields(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	insertTopUpSeeds(t, db, mixedRailSeeds())

	unknown, err := ListTopUpRecords(ListTopUpParams{Page: 1, PageSize: 20, Currency: "unknown"})
	if err != nil {
		t.Fatalf("list unknown: %v", err)
	}
	if len(unknown.Items) != 1 || unknown.Items[0].ID != 4 {
		t.Fatalf("unknown-currency filter = %#v, want only id 4", unknown.Items)
	}
	if unknown.Items[0].PaymentCurrency != "" || unknown.Items[0].PaidUSD != nil {
		t.Fatalf("unknown row must carry no currency and no USD value: %#v", unknown.Items[0])
	}

	cny, err := ListTopUpRecords(ListTopUpParams{Page: 1, PageSize: 20, Currency: "CNY"})
	if err != nil {
		t.Fatalf("list cny: %v", err)
	}
	if len(cny.Items) != 2 {
		t.Fatalf("CNY filter returned %d rows, want 2", len(cny.Items))
	}
	for _, item := range cny.Items {
		if item.PaidUSD == nil || !topUpNear(*item.PaidUSD, 10) || !topUpNear(item.CreditedUSD, 10) {
			t.Fatalf("CNY row should pay $10 and credit $10: %#v", item)
		}
	}

	paypal, err := GetTopUpByID(3)
	if err != nil {
		t.Fatalf("get paypal: %v", err)
	}
	if !topUpNear(paypal.CreditedUSD, 10) {
		t.Fatalf("PayPal quota should credit $10, got %v", paypal.CreditedUSD)
	}
}

func TestTopUpStatusBucket_ReviewingIsItsOwnBucket(t *testing.T) {
	if got := topUpStatusBucket(" Reviewing "); got != "reviewing" {
		t.Fatalf("bucket = %q, want reviewing", got)
	}
	reasons := topUpAnomalyReasons(TopUpRecord{Amount: 10, Money: 10, TradeNo: "ref_1", Status: "reviewing", CreateTime: 1}, 1_000_000, 2)
	for _, r := range reasons {
		if r == "未知状态" || r == "超时待支付" {
			t.Fatalf("reviewing must not be flagged %q", r)
		}
	}
}

func TestTopUpAnomalies_SubscriptionRowsHaveNoQuota(t *testing.T) {
	clearTopUpAnalyticsCache(t)
	t.Cleanup(func() { clearTopUpAnalyticsCache(t) })
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	now := time.Now().Unix()
	insertTopUpSeeds(t, db, []topUpSeed{
		{id: 1, provider: "stripe", method: "stripe", tradeNo: "sub_ref_abc", amount: 0, money: 9.9, status: "success", createTime: now - 600, completeTime: now - 500},
		{id: 2, provider: "epay", method: "alipay", tradeNo: "SUBUSR1NOxyz", amount: 0, money: 69.3, status: "success", createTime: now - 600, completeTime: now - 500},
		{id: 3, provider: "epay", method: "alipay", tradeNo: "USR1NOabc", amount: 0, money: 70, status: "success", createTime: now - 600, completeTime: now - 500},
	})

	if reasons := topUpAnomalyReasons(TopUpRecord{Amount: 0, Money: 9.9, TradeNo: "sub_ref_abc", StatusBucket: "success"}, now, 2); len(reasons) != 0 {
		t.Fatalf("subscription row flagged: %v", reasons)
	}
	got, err := GetTopUpAnomalies(30, 2, 50)
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	if got.Summary.InvalidAmount != 1 || len(got.Items) != 1 || got.Items[0].ID != 3 {
		t.Fatalf("only the non-subscription zero-amount row is anomalous, got summary %+v items %#v", got.Summary, got.Items)
	}
}

func TestExportTopUpsToCSV_OriginalCurrencyAndUSDColumns(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	insertTopUpSeeds(t, db, mixedRailSeeds())

	var buf bytes.Buffer
	if err := ExportTopUpsToCSV(context.Background(), &buf, ListTopUpParams{}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records := readCSVRecords(t, buf.Bytes())
	header := records[0]
	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}
	for _, name := range []string{"原币种", "原币金额", "折合美元", "入账额度(美元)"} {
		if _, ok := col[name]; !ok {
			t.Fatalf("missing column %q in %v", name, header)
		}
	}
	if strings.Contains(strings.Join(header, ","), "实付金额(CNY)") {
		t.Fatalf("header must not claim every amount is CNY: %v", header)
	}
	want := map[string][4]string{
		"1": {"CNY", "70.00", "10.00", "10.00"},
		"3": {"USD", "10.00", "10.00", "10.00"},
		"4": {"未知", "20.00", "", "20.00"},
	}
	for _, rec := range records[1:] {
		exp, ok := want[rec[col["ID"]]]
		if !ok {
			continue
		}
		got := [4]string{rec[col["原币种"]], rec[col["原币金额"]], rec[col["折合美元"]], rec[col["入账额度(美元)"]]}
		if got != exp {
			t.Errorf("row %s = %v, want %v", rec[col["ID"]], got, exp)
		}
	}
}

func TestAffiliateStats_SumInUSD(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	db.MustExec(`INSERT INTO users (id, username, inviter_id, aff_count) VALUES (10, 'inviter', 0, 1), (11, 'invitee', 10, 0)`)
	insertTopUpSeeds(t, db, []topUpSeed{
		{id: 1, userID: 11, provider: "epay", method: "alipay", amount: 10, money: 70, status: "success", completeTime: 1_700_000_100},
		{id: 2, userID: 11, provider: "paypal", method: "paypal", amount: 5_000_000, money: 10, status: "success", completeTime: 1_700_000_200},
		{id: 3, userID: 11, provider: "beatapi", method: "crypto", amount: 20, money: 20, status: "success"},
		{id: 4, userID: 11, provider: "stripe", method: "stripe", amount: 10, money: 10, status: "pending"},
	})

	list, err := ListAffiliateStats(AffiliateStatsParams{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("rows = %d, want 1", len(list.Items))
	}
	row := list.Items[0]
	if row.SuccessTopUpCount != 3 || !topUpNear(row.SuccessPaidUSD, 20) || !topUpNear(row.SuccessCreditedUSD, 40) || row.UnknownCurrencyCount != 1 {
		t.Fatalf("affiliate row = %+v, want 3 orders, $20 paid, $40 credited, 1 unknown", row)
	}
	summary, err := GetAffiliateStatsSummary(AffiliateStatsParams{})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if !topUpNear(summary.TotalPaidUSD, 20) || !topUpNear(summary.TotalCreditedUSD, 40) || summary.TotalUnknownCurrencyCount != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

func readCSVRecords(t *testing.T, buf []byte) [][]string {
	t.Helper()
	buf = bytes.TrimPrefix(buf, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(buf))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("empty csv")
	}
	return records
}

// Revenue belongs to the moment it was paid, and the analytics tab and the
// growth panel must report the same figure for the same month.
func TestRevenueByCompletionTime_AnalyticsMatchesGrowth(t *testing.T) {
	clearTopUpAnalyticsCache(t)
	t.Cleanup(func() { clearTopUpAnalyticsCache(t) })
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	insertTopUpSeeds(t, db, []topUpSeed{
		// created last month, paid this month: this month's revenue
		{id: 1, method: "alipay", amount: 10, money: 70, status: "success", createTime: monthStart - 600, completeTime: monthStart + 600},
		// created and paid last month
		{id: 2, method: "stripe", amount: 5, money: 5, status: "success", createTime: monthStart - 7200, completeTime: monthStart - 7000},
		// paid this month in an unknown currency: an order, not revenue
		{id: 3, provider: "beatapi", method: "crypto", amount: 100, money: 100, status: "success", createTime: monthStart + 700, completeTime: monthStart + 800},
		// completed without a stamp: falls back to its creation time
		{id: 4, method: "stripe", amount: 3, money: 3, status: "success", createTime: monthStart + 900},
		// never paid
		{id: 5, method: "wxpay", amount: 10, money: 70, status: "pending", createTime: monthStart + 1000},
	})

	rt, err := GetTopUpRealtimeStats()
	if err != nil {
		t.Fatalf("realtime: %v", err)
	}
	if !topUpNear(rt.MonthMoney, 13) || !topUpNear(rt.LastMonthMoney, 5) || rt.MonthCount != 3 {
		t.Fatalf("realtime month = $%v (%d orders), last month = $%v; want $13 (3), $5", rt.MonthMoney, rt.MonthCount, rt.LastMonthMoney)
	}

	fin, err := GetTopUpFinancialSummary(2)
	if err != nil || len(fin) != 2 {
		t.Fatalf("financial summary = %#v, %v", fin, err)
	}
	if !topUpNear(fin[0].Revenue, 13) || fin[0].Count != 3 || fin[0].UnknownCurrencyCount != 1 || !topUpNear(fin[0].AvgOrder, 6.5) {
		t.Fatalf("this month = %+v, want $13 over 3 orders (1 unknown), avg $6.50", fin[0])
	}
	if !topUpNear(fin[1].Revenue, 5) {
		t.Fatalf("last month revenue = %v, want 5", fin[1].Revenue)
	}

	growth, err := NewDashboardService().GetGrowthMetrics(true)
	if err != nil {
		t.Fatalf("growth: %v", err)
	}
	if !topUpNear(toFloat64(growth["month_revenue"]), rt.MonthMoney) || !topUpNear(toFloat64(growth["total_revenue"]), 18) {
		t.Fatalf("growth month/total = %v/%v, analytics month = %v", growth["month_revenue"], growth["total_revenue"], rt.MonthMoney)
	}
}
