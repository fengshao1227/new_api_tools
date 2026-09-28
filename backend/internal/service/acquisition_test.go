package service

import (
	"testing"
	"time"
)

func TestAcquisitionLabelFallsBackToReferralOwner(t *testing.T) {
	source, detail, kind := acquisitionLabel("", "", 42, true)
	if source != "历史邀请" || detail != "历史邀请码归因" || kind != "legacy_referral" {
		t.Fatalf("referral fallback = (%q, %q)", source, detail)
	}

	source, detail, kind = acquisitionLabel("", "", 0, true)
	if source != "未采集" || detail != "注册时未采集" || kind != "unattributed" {
		t.Fatalf("unknown fallback = (%q, %q)", source, detail)
	}
}

func TestAcquisitionBucketComputesPaidAndUnpaidRate(t *testing.T) {
	bucket := acquisitionBucket("google", "post-42", "captured", 10, 3)
	if bucket.Users != 10 || bucket.PaidUsers != 3 || bucket.UnpaidUsers != 7 {
		t.Fatalf("bucket counts = %+v", bucket)
	}
	if bucket.PaidRate < 0.2999 || bucket.PaidRate > 0.3001 {
		t.Fatalf("bucket paid rate = %v", bucket.PaidRate)
	}
}

func TestAcquisitionOverviewGroupsSourcesAndSuccessfulTopUps(t *testing.T) {
	m := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, role INTEGER, acquisition_source TEXT, acquisition_detail TEXT, inviter_id INTEGER,
			signup_country TEXT, grant_region TEXT, granted_quota INTEGER, deleted_at INTEGER, created_at INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`INSERT INTO users VALUES (1, 1, 'Google', 'Launch', 0, 'us', 'US', 500000, NULL, 100)`,
		`INSERT INTO users VALUES (2, 1, 'Google', 'Launch', 0, 'US', 'US', 500000, NULL, 100)`,
		`INSERT INTO users VALUES (3, 1, '', '', 42, '', '', 0, NULL, 100)`,
		`INSERT INTO top_ups VALUES (1, 2, 'success')`,
		`INSERT INTO top_ups VALUES (2, 3, 'pending')`,
	).db

	overview, err := (&AcquisitionSourceService{db: m}).GetOverview(0)
	if err != nil {
		t.Fatal(err)
	}
	if overview.TotalUsers != 3 || overview.PaidUsers != 1 || overview.UnpaidUsers != 2 {
		t.Fatalf("unexpected overview totals: %+v", overview)
	}
	if len(overview.BySource) != 2 || overview.BySource[0].Source != "google" || overview.BySource[0].PaidUsers != 1 {
		t.Fatalf("unexpected source buckets: %+v", overview.BySource)
	}
	if len(overview.ByDetail) != 2 || overview.ByDetail[0].Detail != "launch" {
		t.Fatalf("unexpected detail buckets: %+v", overview.ByDetail)
	}
}

func TestAcquisitionOverviewSplitsCountryAndGrantRegion(t *testing.T) {
	m := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, role INTEGER, acquisition_source TEXT, acquisition_detail TEXT, inviter_id INTEGER,
			signup_country TEXT, grant_region TEXT, granted_quota INTEGER, deleted_at INTEGER, created_at INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		// Two US signups on the US rung, one held down to $0.20 by risk.
		`INSERT INTO users VALUES (1, 1, 'google', '', 0, 'us', 'US', 1000000, NULL, 100)`,
		`INSERT INTO users VALUES (2, 1, 'google', '', 0, 'US', 'US', 100000, NULL, 100)`,
		// A CN signup priced by the wildcard rung, who paid.
		`INSERT INTO users VALUES (3, 1, 'direct', '', 0, 'CN', '*', 250000, NULL, 100)`,
		// An account made before the gateway recorded either field.
		`INSERT INTO users VALUES (4, 1, '', '', 0, NULL, NULL, 0, NULL, 100)`,
		// Internal accounts: an admin, and an ID on the panel whitelist.
		`INSERT INTO users VALUES (5, 1, 'google', '', 0, 'US', 'US', 1000000, NULL, 100)`,
		`INSERT INTO users VALUES (6, 100, 'google', '', 0, 'US', 'US', 1000000, NULL, 100)`,
		`INSERT INTO top_ups VALUES (1, 3, 'success')`,
		`INSERT INTO top_ups VALUES (2, 5, 'success')`,
		`INSERT INTO top_ups VALUES (3, 6, 'success')`,
	).db
	setPanelWhitelistForTest(PanelWhitelistConfig{UserIDs: []int64{5}, ExcludeAdmins: true})

	overview, err := (&AcquisitionSourceService{db: m}).GetOverview(0)
	if err != nil {
		t.Fatal(err)
	}
	if overview.TotalUsers != 4 || overview.PaidUsers != 1 {
		t.Fatalf("internal accounts must be excluded: %+v", overview)
	}
	if overview.RegionStatus != "ready" {
		t.Fatalf("region status = %q", overview.RegionStatus)
	}
	wantCountries := map[string][2]int64{"US": {2, 0}, "CN": {1, 1}, acquisitionUnrecorded: {1, 0}}
	if len(overview.ByCountry) != len(wantCountries) || overview.ByCountry[0].Source != "US" {
		t.Fatalf("country buckets = %+v", overview.ByCountry)
	}
	for _, b := range overview.ByCountry {
		if want := wantCountries[b.Source]; b.Users != want[0] || b.PaidUsers != want[1] {
			t.Fatalf("country %s = %+v, want %v", b.Source, b, want)
		}
	}
	regions := map[string]AcquisitionBucket{}
	for _, b := range overview.ByGrantRegion {
		regions[b.Source] = b
	}
	us := regions["US"]
	if us.Users != 2 || !approx(us.GrantedUSD, 2.2) || !approx(us.GrantedMaxUSD, 2) {
		t.Fatalf("US rung = %+v", us)
	}
	if wild := regions["*"]; wild.Users != 1 || wild.PaidUsers != 1 || !approx(wild.GrantedUSD, 0.5) {
		t.Fatalf("wildcard rung = %+v", wild)
	}
	if none := regions[acquisitionUnrecorded]; none.Users != 1 || none.GrantedUSD != 0 {
		t.Fatalf("unrecorded rung = %+v", none)
	}
}

func TestAcquisitionOverviewMarksMissingGatewayColumns(t *testing.T) {
	m := newBusinessTestService(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, inviter_id INTEGER, deleted_at INTEGER, created_at INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`INSERT INTO users VALUES (1, 42, NULL, 100)`,
		`INSERT INTO users VALUES (2, 0, NULL, 100)`,
		`INSERT INTO top_ups VALUES (1, 1, 'success')`,
	).db
	overview, err := (&AcquisitionSourceService{db: m}).GetOverview(0)
	if err != nil {
		t.Fatal(err)
	}
	if overview.AttributionStatus != "schema_missing" || overview.SourceUsers != 0 || overview.HistoricalReferralUsers != 1 || overview.UnattributedUsers != 1 {
		t.Fatalf("unexpected legacy coverage: %+v", overview)
	}
	if len(overview.BySource) != 2 || overview.BySource[0].Source == overview.BySource[1].Source || (overview.BySource[0].Source != "历史邀请" && overview.BySource[0].Source != "未采集") {
		t.Fatalf("unexpected legacy source buckets: %+v", overview.BySource)
	}
	if overview.RegionStatus != "schema_missing" || len(overview.ByCountry) != 0 || len(overview.ByGrantRegion) != 0 {
		t.Fatalf("region split must degrade without the columns: %+v", overview)
	}
}

// setPanelWhitelistForTest replaces the whitelist for the rest of the test;
// newBusinessTestService's cleanup restores the saved one.
func setPanelWhitelistForTest(cfg PanelWhitelistConfig) {
	globalPanelWL.mu.Lock()
	defer globalPanelWL.mu.Unlock()
	globalPanelWL.cfg = cfg
	globalPanelWL.resolved = nil
	globalPanelWL.resolvedAt = time.Time{}
}
