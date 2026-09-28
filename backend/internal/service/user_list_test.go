package service

import (
	"reflect"
	"sort"
	"testing"
)

const userListUsersTable = `CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, display_name TEXT, email TEXT,
	role INTEGER, status INTEGER, quota INTEGER, used_quota INTEGER, request_count INTEGER, "group" TEXT,
	aff_code TEXT, remark TEXT, github_id TEXT, signup_country TEXT, grant_region TEXT, granted_quota INTEGER,
	topup_quota INTEGER, deleted_at INTEGER)`

func newUserListFixture(t *testing.T) *UserManagementService {
	t.Helper()
	m := newBusinessTestService(t,
		userListUsersTable,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`CREATE TABLE risk_events (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT, held_quota INTEGER, resolved_at INTEGER)`,
		`CREATE TABLE custom_oauth_providers (id INTEGER PRIMARY KEY, slug TEXT, name TEXT)`,
		`CREATE TABLE user_oauth_bindings (id INTEGER PRIMARY KEY, user_id INTEGER, provider_id INTEGER, provider_user_id TEXT)`,
		// 1: unpaid, spent $0.40 of a $2 grant, GitHub (built-in) + Google (console), under review.
		`INSERT INTO users VALUES (1, 'alice', '', '', 1, 1, 800000, 200000, 3, 'default', 'a1', '', 'gh-1', 'US', 'US', 1000000, 0, NULL)`,
		// 2: paid by card; decided twice, released last.
		`INSERT INTO users VALUES (2, 'bob', '', '', 1, 1, 9000000, 0, 9, 'default', 'b2', '', '', 'CN', '*', 500000, 5000000, NULL)`,
		// 3: a ToB account credited by hand, no top-up row; GitHub through the console provider.
		`INSERT INTO users VALUES (3, 'carol', '', '', 1, 1, 4000000, 0, 0, 'vip', 'c3', '', NULL, '', '', 0, 5000000, NULL)`,
		// 4: password only, registered before grants were recorded.
		`INSERT INTO users VALUES (4, 'dave', '', '', 1, 1, 300000, 0, 0, 'default', 'd4', '', '', NULL, NULL, 0, 0, NULL)`,
		// 5: an admin, hidden by the panel whitelist.
		`INSERT INTO users VALUES (5, 'root', '', '', 100, 1, 0, 0, 0, 'default', 'r5', '', '', '', '', 0, 0, NULL)`,
		`INSERT INTO top_ups VALUES (1, 2, 'success')`,
		`INSERT INTO top_ups VALUES (2, 4, 'pending')`,
		`INSERT INTO risk_events VALUES (1, 1, 'open', 500000, 0)`,
		`INSERT INTO risk_events VALUES (2, 1, 'dismissed', 0, 50)`,
		`INSERT INTO risk_events VALUES (3, 2, 'confirmed', 0, 100)`,
		`INSERT INTO risk_events VALUES (4, 2, 'released', 0, 200)`,
		`INSERT INTO risk_events VALUES (5, 4, 'none', 0, 0)`,
		`INSERT INTO custom_oauth_providers VALUES (1, 'github', 'GitHub')`,
		`INSERT INTO custom_oauth_providers VALUES (2, 'Google', 'Google')`,
		`INSERT INTO user_oauth_bindings VALUES (1, 1, 2, 'g-1')`,
		`INSERT INTO user_oauth_bindings VALUES (2, 3, 1, 'gh-3')`,
	).db
	return &UserManagementService{db: m, logDB: m}
}

func usersByID(t *testing.T, result map[string]interface{}) map[int64]map[string]interface{} {
	t.Helper()
	items, ok := result["items"].([]map[string]interface{})
	if !ok {
		t.Fatalf("items = %#v", result["items"])
	}
	out := make(map[int64]map[string]interface{}, len(items))
	for _, item := range items {
		out[toInt64(item["id"])] = item
	}
	return out
}

func sortedIDs(users map[int64]map[string]interface{}) []int64 {
	ids := make([]int64, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestGetUsersAddsPaidCreditRiskAndLoginSources(t *testing.T) {
	svc := newUserListFixture(t)
	result, err := svc.GetUsers(ListUsersParams{OrderBy: "id", OrderDir: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	users := usersByID(t, result)
	if got := sortedIDs(users); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("admin must be hidden, got ids %v", got)
	}
	if result["risk_available"] != true || result["oauth_bindings_available"] != true {
		t.Fatalf("availability flags = %v / %v", result["risk_available"], result["oauth_bindings_available"])
	}

	alice := users[1]
	if alice["paid"] != false || !approx(alice["free_credit_usd"].(float64), 1.6) || alice["signup_country"] != "US" || alice["grant_region"] != "US" {
		t.Fatalf("alice = %#v", alice)
	}
	if !reflect.DeepEqual(alice["login_sources"], []string{"github", "google"}) || alice["source"] != "github" {
		t.Fatalf("alice sources = %v", alice["login_sources"])
	}
	if _, raw := alice["github_id"]; raw {
		t.Fatal("raw OAuth columns must not be returned")
	}
	if risk := alice["risk"].(*userRiskBrief); risk.Status != "open" || risk.OpenCases != 1 || !approx(risk.HeldUSD, 1) {
		t.Fatalf("alice risk = %+v", risk)
	}

	bob := users[2]
	if bob["paid"] != true || bob["paid_via"] != "top_up" || bob["free_credit_usd"].(float64) != 0 {
		t.Fatalf("bob = %#v", bob)
	}
	if risk := bob["risk"].(*userRiskBrief); risk.Status != "released" || risk.ResolvedAt != 200 {
		t.Fatalf("bob risk = %+v", risk)
	}

	carol := users[3]
	if carol["paid"] != true || carol["paid_via"] != "credited" || carol["free_credit_usd"].(float64) != 0 {
		t.Fatalf("carol = %#v", carol)
	}
	if !reflect.DeepEqual(carol["login_sources"], []string{"github"}) || carol["risk"].(*userRiskBrief).Status != "none" {
		t.Fatalf("carol sources/risk = %v / %+v", carol["login_sources"], carol["risk"])
	}

	dave := users[4]
	if dave["paid"] != false || !approx(dave["free_credit_usd"].(float64), 0.6) || dave["source"] != "password" {
		t.Fatalf("dave = %#v", dave)
	}
	if dave["risk"].(*userRiskBrief).Status != "none" {
		t.Fatalf("a 'none' event is not a verdict: %+v", dave["risk"])
	}
}

func TestGetUsersFiltersByLoginSource(t *testing.T) {
	svc := newUserListFixture(t)
	for filter, want := range map[string][]int64{
		"github":   {1, 3}, // built-in column or console provider
		"google":   {1},
		"password": {2, 4},
		"discord":  {}, // no such column here and no such provider
	} {
		result, err := svc.GetUsers(ListUsersParams{SourceFilter: filter})
		if err != nil {
			t.Fatalf("%s: %v", filter, err)
		}
		if got := sortedIDs(usersByID(t, result)); !reflect.DeepEqual(got, want) {
			t.Fatalf("filter %s = %v, want %v", filter, got, want)
		}
	}
}

func TestGetUsersSearchFindsWhitelistedAccounts(t *testing.T) {
	svc := newUserListFixture(t)
	result, err := svc.GetUsers(ListUsersParams{Search: "roo"})
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedIDs(usersByID(t, result)); !reflect.DeepEqual(got, []int64{5}) {
		t.Fatalf("search = %v", got)
	}
}

func TestGetUsersDegradesOnOlderGateways(t *testing.T) {
	m := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, display_name TEXT, email TEXT, role INTEGER,
			status INTEGER, quota INTEGER, used_quota INTEGER, request_count INTEGER, "group" TEXT, aff_code TEXT,
			remark TEXT, deleted_at INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`INSERT INTO users VALUES (1, 'old', '', '', 1, 1, 250000, 0, 0, 'default', 'o1', '', NULL)`,
	).db
	svc := &UserManagementService{db: m, logDB: m}

	result, err := svc.GetUsers(ListUsersParams{SourceFilter: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if result["risk_available"] != false || result["oauth_bindings_available"] != false {
		t.Fatalf("availability flags = %v / %v", result["risk_available"], result["oauth_bindings_available"])
	}
	old := usersByID(t, result)[1]
	if old == nil || old["risk"] != nil || old["source"] != "password" || !approx(old["free_credit_usd"].(float64), 0.5) || old["signup_country"] != "" {
		t.Fatalf("old = %#v", old)
	}
	if sources := svc.GetLoginSources(); len(sources) != 1 || sources[0].Key != "password" {
		t.Fatalf("login sources = %+v", sources)
	}
}

func TestGetLoginSourcesMergesBuiltinAndConsoleProviders(t *testing.T) {
	svc := newUserListFixture(t)
	got := svc.GetLoginSources()
	keys := make([]string, len(got))
	for i, option := range got {
		keys[i] = option.Key + ":" + option.Kind
	}
	if want := []string{"github:builtin", "google:custom", "password:password"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("login sources = %v, want %v", keys, want)
	}
}

func TestFreeCreditQuotaMatchesGatewayRule(t *testing.T) {
	for _, c := range []struct{ quota, granted, want int64 }{
		{0, 500000, 0}, {-5, 0, 0}, {300000, 0, 300000}, {300000, 500000, 300000}, {900000, 500000, 500000},
	} {
		if got := freeCreditQuota(c.quota, c.granted); got != c.want {
			t.Fatalf("freeCreditQuota(%d, %d) = %d, want %d", c.quota, c.granted, got, c.want)
		}
	}
}
