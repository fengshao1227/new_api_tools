package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/new-api-tools/backend/internal/database"
)

func insightsTestDB(t *testing.T, driver, dsn string) *database.Manager {
	t.Helper()
	db, err := sqlx.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("open fixture %s: %v", driver, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &database.Manager{DB: db, IsPG: driver == "pgx", IsCH: driver == "clickhouse"}
}

func insightsFixtureTable(t *testing.T, db *database.Manager, name, columns string) {
	t.Helper()
	query := "CREATE TABLE " + name + " (" + columns + ")"
	if db.IsCH {
		query += " ENGINE = MergeTree ORDER BY id"
	}
	if _, err := db.DB.Exec(query); err != nil {
		t.Fatalf("create dedicated fixture %s: %v", name, err)
	}
	// A table is eligible for cleanup only after this test created it. Never
	// drop or overwrite an existing table to make an externally supplied DSN fit.
	t.Cleanup(func() { _, _ = db.DB.Exec("DROP TABLE " + name) })
}

func insightsFixture(t *testing.T, main, logs *database.Manager) *UserInsightsService {
	t.Helper()
	group := main.QuoteIdentifier("group")
	insightsFixtureTable(t, main, "users", `id BIGINT PRIMARY KEY,username VARCHAR(50),display_name VARCHAR(50),email VARCHAR(100),status BIGINT,role BIGINT,`+group+` VARCHAR(30),remark TEXT,quota BIGINT,used_quota BIGINT,topup_quota BIGINT,granted_quota BIGINT,created_at BIGINT,last_login_at BIGINT,signup_country VARCHAR(8),signup_language VARCHAR(16),acquisition_source VARCHAR(64),acquisition_detail VARCHAR(128),inviter_id BIGINT,deleted_at BIGINT`)
	insightsFixtureTable(t, main, "top_ups", `id BIGINT PRIMARY KEY,user_id BIGINT,money DECIMAL(18,6),amount BIGINT,payment_method VARCHAR(30),payment_provider VARCHAR(30),status VARCHAR(30),create_time BIGINT,complete_time BIGINT,trade_no VARCHAR(80)`)
	insightsFixtureTable(t, main, "auto_top_up_attempts", `id BIGINT PRIMARY KEY,trade_no VARCHAR(80),amount_quota BIGINT`)
	properties := "TEXT"
	if main.IsPG {
		properties = "JSONB"
	}
	insightsFixtureTable(t, main, "tasks", `id BIGINT PRIMARY KEY,task_id VARCHAR(100),user_id BIGINT,platform VARCHAR(30),action VARCHAR(30),status VARCHAR(30),progress VARCHAR(30),submit_time BIGINT,start_time BIGINT,finish_time BIGINT,quota BIGINT,fail_reason TEXT,properties `+properties)
	insightsFixtureTable(t, logs, "logs", `id BIGINT,user_id BIGINT,created_at BIGINT,type BIGINT,model_name VARCHAR(100),quota BIGINT,cost BIGINT,prompt_tokens BIGINT,completion_tokens BIGINT,token_id BIGINT,token_name VARCHAR(100),channel_id BIGINT,use_time BIGINT,is_stream BIGINT,ip VARCHAR(64),content TEXT,other TEXT,request_id VARCHAR(100)`)
	exec := func(db *database.Manager, q string, args ...any) {
		t.Helper()
		if _, err := db.DB.Exec(db.RebindQuery(q), args...); err != nil {
			t.Fatalf("fixture insert: %v", err)
		}
	}
	exec(main, "INSERT INTO users VALUES (1,'alice','Alice','alice@example.invalid',1,1,'default','ops note',2000000,750000,5000000,500000,100,110,'US','en','search','google',0,NULL)")
	exec(main, "INSERT INTO users VALUES (2,'other','','other@example.invalid',1,1,'default','',0,0,0,0,100,0,'','','','',0,NULL)")
	exec(main, "INSERT INTO top_ups VALUES (1,1,10,10,'stripe','stripe','success',100,200,'manual-1')")
	exec(main, "INSERT INTO top_ups VALUES (2,1,10.01,0,'stripe','stripe','success',100000,100005,'auto_case')")
	exec(main, "INSERT INTO auto_top_up_attempts VALUES (1,'auto_case',6006000)")
	exec(main, "INSERT INTO top_ups VALUES (3,1,70,10,'alipay','epay','success',100000,100006,'cny-1')")
	exec(main, "INSERT INTO top_ups VALUES (4,1,99,99,'unknown','unknown','success',100000,100007,'unknown-1')")
	exec(main, "INSERT INTO top_ups VALUES (5,1,100,100,'stripe','stripe','pending',100000,0,'pending-1')")
	for i, status := range []string{"SUCCESS", "FAILURE", "IN_PROGRESS"} {
		exec(main, "INSERT INTO tasks VALUES (?,?,1,'image','generate',?,'50%',100010,100011,100020,50000,?,?)", i+1, fmt.Sprintf("task-%d", i), status, "upstream 500 secret never returned", `{"origin_model_name":"image-a","input":"private-prompt"}`)
	}
	entries := []struct {
		kind, prompt, output, quota int64
		model, other, request       string
	}{
		{2, 100, 20, 500000, "text-a", `{"cache_tokens":30,"cache_write_tokens":5}`, "req-a"},
		{5, 0, 0, 0, "text-a", `{}`, "req-a"},
		{2, 10, 20, 50000, "text-a", `{"claude":true,"cache_tokens":30,"cache_creation_tokens":40,"cache_creation_tokens_5m":5,"cache_creation_tokens_1h":10}`, "req-b"},
		{2, 0, 0, 0, "image-a", `{"is_task":true,"task_id":"task-1","admin_info":{"voided_quota":500000,"private":"secret"}}`, "req-task"},
		{6, 0, 0, 500000, "image-a", `{"task_id":"task-1"}`, "refund-1"},
		{2, 0, 0, 100000, "image-a", `{"task_id":"task-0","actual_quota":600000,"pre_consumed_quota":500000}`, "adjustment-1"},
		{6, 0, 0, 20000, "image-a", `{"task_id":"task-0"}`, "refund-2"},
		{2, 50, 10, 10000, "text-b", `{}`, "req-c"},
		{2, 3, 1, 1000, "text-a", `{malformed`, "req-d"},
		{2, 70, 1, 1000, "text-a", `{"input_tokens_total":90,"cache_tokens":0,"cache_write_tokens":0}`, "req-e"},
		{2, 1, 1, 1000, "text-a", `{"cache_tokens":-1,"cache_write_tokens":"2"}`, "req-f"},
	}
	for i, row := range entries {
		exec(logs, "INSERT INTO logs VALUES (?,?,?, ?,?,?,0,?,?,1,'key-name',1,2,1,'','Bearer secret prompt',?,?)", i+1, 1, 100000+i, row.kind, row.model, row.quota, row.prompt, row.output, row.other, row.request)
	}
	exec(logs, "INSERT INTO logs VALUES (100,1,101000,2,'text-a',999999,0,999999,0,1,'key',1,0,0,'','','{}','end-excluded')")
	exec(logs, "INSERT INTO logs VALUES (101,2,100001,2,'text-a',999999,0,999999,0,1,'key',1,0,0,'','','{}','other-user')")
	return &UserInsightsService{db: main, logDB: logs}
}

func insightsContract(t *testing.T, s *UserInsightsService) {
	t.Helper()
	p := UserInsightsParams{UserID: 1, Window: UserInsightsWindow{StartTime: 100000, EndTime: 101000}, Page: 1, PageSize: 3}
	out, err := s.Report(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	m := out.Summary
	if m == nil || m.BillingRecords != 8 || m.ErrorRecords != 1 || m.RefundRecords != 2 || m.ChargedQuota != 663000 || m.RefundQuota != 520000 {
		t.Fatalf("ledger counts/money: %+v", m)
	}
	if m.RawPromptTokens != 234 || m.InputTokens != 324 || m.OutputTokens != 53 {
		t.Fatalf("token totals: %+v", m)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 60 || m.CacheReadRecords != 3 || m.CacheWriteTokens == nil || *m.CacheWriteTokens != 45 || m.CacheWriteRecords != 3 || m.ReasoningTokens != nil {
		t.Fatalf("token coverage: %+v", m)
	}
	if out.Balances.LifetimeUsedUSD != 1.5 || out.Profile.Email != "alice@example.invalid" || out.Profile.LastLoginAt == nil || *out.Profile.LastLoginAt != 110 {
		t.Fatalf("profile/balances: %+v", out.Profile)
	}
	if out.Tasks == nil || out.Tasks.Total != 3 || out.Tasks.Success != 1 || out.Tasks.Failed != 1 || out.Tasks.InProgress != 1 {
		t.Fatalf("tasks: %+v", out.Tasks)
	}
	if out.Payments == nil || out.Payments.Window.PaidCount != 3 || out.Payments.Lifetime.PaidCount != 4 || out.Payments.Window.UnknownCurrencyCount != 1 || out.Payments.Window.CreditedUSD != nil {
		t.Fatalf("payments: %+v", out.Payments)
	}
	if !approx(out.Payments.Window.PaidUSD, 10.01+70/cnyPerUSD()) {
		t.Fatalf("currency conversion: %+v", out.Payments.Window)
	}
	if len(out.Models) != 3 || len(out.Daily) != 1 || out.Daily[0].Date != "1970-01-02" {
		t.Fatalf("groups: %+v %+v", out.Models, out.Daily)
	}
	page, err := s.Logs(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 11 || len(page.Items) != 3 || page.Items[0].ID != 11 {
		t.Fatalf("pagination: %+v", page)
	}
	if len(page.Warnings) != 1 || page.Warnings[0] != "log_token_details_unavailable" {
		t.Fatalf("invalid or truncated metadata must be visible to the caller: %+v", page.Warnings)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "Bearer") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("raw diagnostics escaped projection: %s", encoded)
	}
	if page.Items[0].CacheReadTokens != nil || page.Items[0].CacheWriteTokens != nil || page.Items[1].InputTokens != 90 {
		t.Fatalf("detail normalization: %+v", page.Items)
	}
	p.Window.Model = "text-a"
	filtered, err := s.Report(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Summary.BillingRecords != 5 || filtered.Summary.InputTokens != 274 || filtered.Tasks.Total != 0 || filtered.Payments.Window.PaidCount != 3 {
		t.Fatalf("model scope: %+v", filtered)
	}
	p.Window.Model = "image-a"
	p.Status = "active"
	tasks, err := s.Tasks(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if tasks.Total != 1 || len(tasks.Items) != 1 || tasks.Items[0].Status != "IN_PROGRESS" {
		t.Fatalf("task filter: %+v", tasks)
	}
	p.UserID = 999
	if _, err := s.Report(context.Background(), p); !errors.Is(err, ErrInsightsUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	p.UserID = 1
	if _, err := s.Report(cancelled, p); err == nil {
		t.Fatal("cancelled request reported as zero data")
	}
}

func TestUserInsightsSeparateDatabases(t *testing.T) {
	main := insightsTestDB(t, "sqlite", ":memory:")
	logs := insightsTestDB(t, "sqlite", ":memory:")
	insightsContract(t, insightsFixture(t, main, logs))
}

// External integration is opt-in and must target empty disposable test DBs.
// It creates only fixture tables and never drops preexisting tables.
func TestUserInsightsExternalDatabases(t *testing.T) {
	mainDSN, logDSN := os.Getenv("TOOL_INSIGHTS_TEST_MAIN_DSN"), os.Getenv("TOOL_INSIGHTS_TEST_LOG_DSN")
	if mainDSN == "" || logDSN == "" || os.Getenv("TOOL_INSIGHTS_TEST_ALLOW_FIXTURE_WRITES") != "1" {
		t.Skip("explicit disposable test databases not supplied")
	}
	mainDriver, logDriver := os.Getenv("TOOL_INSIGHTS_TEST_MAIN_DRIVER"), os.Getenv("TOOL_INSIGHTS_TEST_LOG_DRIVER")
	if mainDriver != "pgx" && mainDriver != "mysql" {
		t.Fatal("main test driver must be pgx or mysql")
	}
	if logDriver != "pgx" && logDriver != "mysql" && logDriver != "clickhouse" {
		t.Fatal("log test driver must be pgx/mysql/clickhouse")
	}
	main := insightsTestDB(t, mainDriver, mainDSN)
	logs := insightsTestDB(t, logDriver, logDSN)
	insightsContract(t, insightsFixture(t, main, logs))
}

func TestUserInsightsMissingSchemaAndFailures(t *testing.T) {
	main := insightsTestDB(t, "sqlite", ":memory:")
	logs := insightsTestDB(t, "sqlite", ":memory:")
	insightsFixtureTable(t, main, "users", `id INTEGER,username TEXT,display_name TEXT,email TEXT,status INTEGER,role INTEGER,"group" TEXT,remark TEXT,quota INTEGER,used_quota INTEGER,deleted_at INTEGER`)
	_, err := main.DB.Exec(`INSERT INTO users VALUES (1,'legacy','','',1,1,'default','',100,20,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	s := &UserInsightsService{db: main, logDB: logs}
	p := UserInsightsParams{UserID: 1, Window: UserInsightsWindow{StartTime: 0, EndTime: 1000, AllTime: true}, Page: 1, PageSize: 20}
	out, err := s.Report(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != nil || out.Payments != nil || out.Tasks != nil || out.Profile.LastLoginAt != nil || out.Availability.Logs || out.Availability.Acquisition {
		t.Fatalf("missing values fabricated: %+v", out)
	}
	_ = logs.DB.Close()
	if _, err := s.Report(context.Background(), p); err == nil {
		t.Fatal("unavailable database was mistaken for missing schema")
	}
}

func TestUserInsightsAutoRechargePrincipal(t *testing.T) {
	main := insightsTestDB(t, "sqlite", ":memory:")
	logs := insightsTestDB(t, "sqlite", ":memory:")
	s := insightsFixture(t, main, logs)
	_, err := main.DB.Exec("DELETE FROM top_ups WHERE id = 4")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Report(context.Background(), UserInsightsParams{UserID: 1, Window: UserInsightsWindow{StartTime: 100000, EndTime: 101000}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Payments.Window.CreditedUSD == nil || !approx(*out.Payments.Window.CreditedUSD, 22.012) {
		t.Fatalf("cash charged was mistaken for credited principal: %+v", out.Payments.Window)
	}
}

func TestUserInsightsLogMetadataRejectsPartialJSON(t *testing.T) {
	for _, raw := range []string{
		`{"cache_tokens":4,"input_tokens_total":99} trailing`,
		`{"cache_tokens":4,"input_tokens_total":99} {"cache_tokens":0}`,
		`{"cache_tokens":4,"input_tokens_total":99,"unfinished":"`,
	} {
		row := insightsLog(map[string]any{"prompt_tokens": int64(7), "other": raw})
		if row.InputTokens != 7 || row.CacheReadTokens != nil || row.CacheWriteTokens != nil {
			t.Fatalf("invalid metadata must preserve only raw prompt count: %+v", row)
		}
	}
	zero := insightsLog(map[string]any{"prompt_tokens": int64(7), "other": `{"cache_tokens":0,"cache_write_tokens":0,"input_tokens_total":0}`})
	if zero.InputTokens != 0 || zero.CacheReadTokens == nil || *zero.CacheReadTokens != 0 || zero.CacheWriteTokens == nil || *zero.CacheWriteTokens != 0 {
		t.Fatalf("valid recorded zero was lost: %+v", zero)
	}
}
