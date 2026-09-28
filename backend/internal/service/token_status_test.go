package service

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestTokenEffectiveStateFollowsGatewayChecks(t *testing.T) {
	const now = int64(1_000_000)
	for _, c := range []struct {
		name                    string
		status, expired, remain int64
		unlimited               bool
		want                    string
	}{
		{"enabled, never expires", 1, -1, 10, false, TokenStateActive},
		{"enabled, unlimited with nothing left", 1, -1, 0, true, TokenStateActive},
		{"enabled, expires later", 1, now + 1, 10, false, TokenStateActive},
		{"enabled but past expiry", 1, now - 1, 10, false, TokenStateExpired},
		{"expired_time 0 is past for the gateway", 1, 0, 10, false, TokenStateExpired},
		{"expiry wins over quota", 1, now - 1, 0, false, TokenStateExpired},
		{"enabled but out of quota", 1, -1, 0, false, TokenStateExhausted},
		{"disabled by hand", 2, -1, 10, false, TokenStateDisabled},
		{"stored expired", 3, now + 100, 10, false, TokenStateExpired},
		{"stored exhausted", 4, -1, 10, false, TokenStateExhausted},
		{"unknown status", 9, -1, 10, false, TokenStateUnknown},
	} {
		if got := tokenEffectiveState(c.status, c.expired, c.unlimited, c.remain, now); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func newTokenStatusFixture(t *testing.T) *TokenService {
	t.Helper()
	future, past := time.Now().Unix()+86400, time.Now().Unix()-86400
	m := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, role INTEGER, quota INTEGER, used_quota INTEGER, deleted_at INTEGER)`,
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY, "key" TEXT, name TEXT, user_id INTEGER, status INTEGER, remain_quota INTEGER,
			unlimited_quota BOOLEAN, used_quota INTEGER, model_limits TEXT, allow_ips TEXT, "group" TEXT, created_time INTEGER,
			expired_time INTEGER, deleted_at INTEGER)`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, created_at INTEGER, type INTEGER, token_id INTEGER, ip TEXT)`,
		`INSERT INTO users VALUES (1, 'alice', 1, 0, 0, NULL)`,
		`INSERT INTO users VALUES (9, 'root', 100, 0, 0, NULL)`,
		fmt.Sprintf(`INSERT INTO tokens VALUES (1, 'k1', 'active', 1, 1, 100, FALSE, 0, '', '', '', 0, %d, NULL)`, future),
		`INSERT INTO tokens VALUES (2, 'k2', 'unlimited', 1, 1, 0, TRUE, 0, '', '', '', 0, -1, NULL)`,
		`INSERT INTO tokens VALUES (3, 'k3', 'by hand', 1, 2, 100, FALSE, 0, '', '', '', 0, -1, NULL)`,
		`INSERT INTO tokens VALUES (4, 'k4', 'stored expired', 1, 3, 100, FALSE, 0, '', '', '', 0, -1, NULL)`,
		fmt.Sprintf(`INSERT INTO tokens VALUES (5, 'k5', 'lazily expired', 1, 1, 100, FALSE, 0, '', '', '', 0, %d, NULL)`, past),
		`INSERT INTO tokens VALUES (6, 'k6', 'stored exhausted', 1, 4, 0, FALSE, 0, '', '', '', 0, -1, NULL)`,
		`INSERT INTO tokens VALUES (7, 'k7', 'lazily exhausted', 1, 1, 0, FALSE, 0, '', '', '', 0, -1, NULL)`,
		`INSERT INTO tokens VALUES (8, 'k8', 'deleted', 1, 2, 0, FALSE, 0, '', '', '', 0, -1, 1)`,
		// An admin's token: the panel whitelist hides it from list and statistics alike.
		`INSERT INTO tokens VALUES (9, 'k9', 'admin', 9, 2, 100, FALSE, 0, '', '', '', 0, -1, NULL)`,
	).db
	return &TokenService{db: m, logDB: m}
}

func TestTokenStatisticsSplitsDisabledExpiredAndExhausted(t *testing.T) {
	stats, err := newTokenStatusFixture(t).GetTokenStatistics()
	if err != nil {
		t.Fatal(err)
	}
	want := TokenStatistics{Total: 7, Active: 2, Disabled: 1, Expired: 2, Exhausted: 2}
	if *stats != want {
		t.Fatalf("statistics = %+v, want %+v", *stats, want)
	}
}

func TestListTokensFiltersByEffectiveState(t *testing.T) {
	svc := newTokenStatusFixture(t)
	for state, want := range map[string][]int64{
		TokenStateActive:    {1, 2},
		TokenStateDisabled:  {3},
		TokenStateExpired:   {4, 5},
		TokenStateExhausted: {6, 7},
	} {
		result, err := svc.ListTokens(TokenListParams{Status: state})
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		items := result["items"].([]map[string]interface{})
		got := make([]int64, 0, len(items))
		for _, item := range items {
			got = append(got, toInt64(item["id"]))
			if item["state"] != state {
				t.Errorf("%s filter returned token %v in state %v", state, item["id"], item["state"])
			}
		}
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s filter = %v, want %v", state, got, want)
		}
	}
}

func TestToBoolReadsEveryDriverSpelling(t *testing.T) {
	for _, v := range []interface{}{true, int64(1), "1", "t", "true"} {
		if !toBool(v) {
			t.Errorf("toBool(%#v) = false", v)
		}
	}
	for _, v := range []interface{}{false, int64(0), "0", "f", nil} {
		if toBool(v) {
			t.Errorf("toBool(%#v) = true", v)
		}
	}
}
