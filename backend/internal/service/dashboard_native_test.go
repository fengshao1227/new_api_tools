package service

import (
	"testing"
	"time"
)

func TestSystemOverviewCounts24HourActivityFromLogs(t *testing.T) {
	now := time.Now().Unix()
	business := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, deleted_at INTEGER)`,
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY, deleted_at INTEGER, status INTEGER)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, created_at INTEGER, type INTEGER, user_id INTEGER, token_id INTEGER)`,
		`CREATE TABLE channels (id INTEGER PRIMARY KEY, status INTEGER)`,
		`CREATE TABLE abilities (channel_id INTEGER, model TEXT)`,
		`CREATE TABLE redemptions (id INTEGER PRIMARY KEY, deleted_at INTEGER, status INTEGER)`,
		`INSERT INTO users VALUES (1, NULL), (2, NULL), (3, NULL)`,
		`INSERT INTO tokens VALUES (10, NULL, 2), (11, NULL, 3), (12, NULL, 1)`,
		`INSERT INTO channels VALUES (1, 1), (2, 2)`,
		`INSERT INTO abilities VALUES (1, 'model-a')`,
		`INSERT INTO redemptions VALUES (1, NULL, 1)`,
	)

	// Insert timestamps separately so the 24h boundary is deterministic.
	for _, row := range []struct {
		id, created, typ, user, token int64
	}{
		{1, now - 3600, 2, 1, 10},
		{2, now - 7200, 5, 2, 11},
		{3, now - 86400 - 1, 2, 1, 10},
		{4, now - 1800, 2, 3, 12},
	} {
		if _, err := business.db.DB.Exec(`INSERT INTO logs VALUES (?, ?, ?, ?, ?)`, row.id, row.created, row.typ, row.user, row.token); err != nil {
			t.Fatal(err)
		}
	}

	result, err := (&DashboardService{db: business.db, logDB: business.logDB}).GetSystemOverview("24h", true)
	if err != nil {
		t.Fatal(err)
	}
	if toInt64(result["total_users"]) != 3 || toInt64(result["total_tokens"]) != 3 {
		t.Fatalf("resource totals = %+v", result)
	}
	if toInt64(result["active_users_24h"]) != 3 || toInt64(result["active_tokens_24h"]) != 3 {
		t.Fatalf("24h activity = users %v/tokens %v, want 3/3", result["active_users_24h"], result["active_tokens_24h"])
	}
}
