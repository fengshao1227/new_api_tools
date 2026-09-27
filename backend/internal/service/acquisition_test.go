package service

import (
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/new-api-tools/backend/internal/database"
	_ "modernc.org/sqlite"
)

func TestAcquisitionLabelFallsBackToReferralOwner(t *testing.T) {
	source, detail := acquisitionLabel("", "", 42)
	if source != "referral" || detail != "inviter:42" {
		t.Fatalf("referral fallback = (%q, %q)", source, detail)
	}

	source, detail = acquisitionLabel("", "", 0)
	if source != "unknown" || detail != "unattributed" {
		t.Fatalf("unknown fallback = (%q, %q)", source, detail)
	}
}

func TestAcquisitionBucketComputesPaidAndUnpaidRate(t *testing.T) {
	bucket := acquisitionBucket("linuxdo", "post-42", 10, 3)
	if bucket.Users != 10 || bucket.PaidUsers != 3 || bucket.UnpaidUsers != 7 {
		t.Fatalf("bucket counts = %+v", bucket)
	}
	if bucket.PaidRate < 0.2999 || bucket.PaidRate > 0.3001 {
		t.Fatalf("bucket paid rate = %v", bucket.PaidRate)
	}
}

func TestAcquisitionOverviewGroupsSourcesAndSuccessfulTopUps(t *testing.T) {
	db, err := sqlx.Open("sqlite", "file:acquisition-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := &database.Manager{DB: db}
	for _, statement := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, acquisition_source TEXT, acquisition_detail TEXT, inviter_id INTEGER, deleted_at INTEGER, created_at INTEGER)`,
		`CREATE TABLE top_ups (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT)`,
		`INSERT INTO users VALUES (1, 'Linux.DO', 'Launch', 0, NULL, 100)`,
		`INSERT INTO users VALUES (2, 'Linux.DO', 'Launch', 0, NULL, 100)`,
		`INSERT INTO users VALUES (3, '', '', 42, NULL, 100)`,
		`INSERT INTO top_ups VALUES (1, 2, 'success')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	overview, err := (&AcquisitionSourceService{db: manager}).GetOverview(0)
	if err != nil {
		t.Fatal(err)
	}
	if overview.TotalUsers != 3 || overview.PaidUsers != 1 || overview.UnpaidUsers != 2 {
		t.Fatalf("unexpected overview totals: %+v", overview)
	}
	if len(overview.BySource) != 2 || overview.BySource[0].Source != "linux.do" || overview.BySource[0].PaidUsers != 1 {
		t.Fatalf("unexpected source buckets: %+v", overview.BySource)
	}
	if len(overview.ByDetail) != 2 || overview.ByDetail[0].Detail != "launch" {
		t.Fatalf("unexpected detail buckets: %+v", overview.ByDetail)
	}
}
