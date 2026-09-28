package service

import (
	"testing"
	"time"
)

// TestBuildMarginResultCountsOfflineCustomersAsPaid runs the classification
// against real tables: an account credited by an admin (topup_quota > 0, no
// top-up row) is a paying customer, while admins and whitelisted test
// accounts with the same kind of credit stay internal cost.
func TestBuildMarginResultCountsOfflineCustomersAsPaid(t *testing.T) {
	now := time.Now().Unix()
	business := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, role INTEGER, topup_quota INTEGER, granted_quota INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, user_id INTEGER, type INTEGER, quota INTEGER, created_at INTEGER, content TEXT)`,
		`INSERT INTO users VALUES (1, 'online', 1, 500000, 0)`,
		`INSERT INTO users VALUES (2, 'b2b-offline', 1, 1000000, 0)`,
		`INSERT INTO users VALUES (3, 'free', 1, 0, 0)`,
		`INSERT INTO users VALUES (4, 'admin', 100, 1000000, 0)`,
		`INSERT INTO users VALUES (5, 'whitelisted-test', 1, 1000000, 0)`,
		`INSERT INTO top_ups VALUES (1, 1, 'success')`,
		`INSERT INTO top_ups VALUES (2, 3, 'pending')`,
	)
	globalPanelWL.mu.Lock()
	globalPanelWL.cfg.UserIDs = []int64{5}
	globalPanelWL.mu.Unlock()

	day := (now + int64(localTZOffset())) / 86400
	rows := make([]marginGroupRow, 0, 5)
	for id := int64(1); id <= 5; id++ {
		// $1 billed, $0.40 supplier cost per user (500,000 quota = $1).
		rows = append(rows, marginGroupRow{UserID: id, DayGroup: day, ModelName: "m", ChannelID: 1, ChannelName: "c", Requests: 1, Quota: 500000, Cost: 200000})
	}
	svc := &MarginAnalysisService{db: business.db, logDB: business.logDB}
	result, err := svc.buildMarginResult(MarginAnalysisParams{StartTime: now - 3600, EndTime: now, Limit: 100}, rows)
	if err != nil {
		t.Fatal(err)
	}

	s := result.Summary
	if !approx(s.RealizedRevenueUSD, 2) || !approx(s.PaidTrafficCostUSD, 0.8) {
		t.Fatalf("revenue/paid cost = %v/%v, want 2/0.8 (online + offline customer)", s.RealizedRevenueUSD, s.PaidTrafficCostUSD)
	}
	if !approx(s.InternalCostUSD, 0.8) || !approx(s.GiftAndFreeCostUSD, 0.4) {
		t.Fatalf("internal/free cost = %v/%v, want 0.8/0.4", s.InternalCostUSD, s.GiftAndFreeCostUSD)
	}
	if s.PaidCustomerCount != 2 || s.OfflinePaidCustomerCount != 1 || s.FreeCustomerCount != 1 || s.InternalUserCount != 2 {
		t.Fatalf("counts = paid %d/offline %d/free %d/internal %d, want 2/1/1/2",
			s.PaidCustomerCount, s.OfflinePaidCustomerCount, s.FreeCustomerCount, s.InternalUserCount)
	}
	buckets := map[int64]string{}
	for _, u := range result.Users {
		buckets[u.UserID] = u.Bucket
	}
	want := map[int64]string{1: "customer_paid", 2: "customer_offline", 3: "customer_free", 4: "staff_or_root", 5: "internal_whitelist"}
	for id, bucket := range want {
		if buckets[id] != bucket {
			t.Fatalf("user %d bucket = %q, want %q (all: %v)", id, buckets[id], bucket, buckets)
		}
	}
}
