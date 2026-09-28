package service

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"github.com/jmoiron/sqlx"
)

// topUpRuleCase is one row of the currency table the gateway's rails imply.
// wantPaid is ignored when wantCurrency is "" (unknown currency has no USD).
type topUpRuleCase struct {
	name         string
	provider     string
	method       string
	amount       int64
	money        float64
	wantCurrency string
	wantPaid     float64
	wantCredited float64
}

var topUpRuleCases = []topUpRuleCase{
	{"epay alipay pays CNY", "epay", "alipay", 10, 70, "CNY", 10, 10},
	{"epay wxpay pays CNY", "epay", "wxpay", 20, 140, "CNY", 20, 20},
	{"stripe credits money, not units", "stripe", "stripe", 12, 10, "USD", 10, 10},
	{"paypal amount is quota", "paypal", "paypal", 5_000_000, 10, "USD", 10, 10},
	{"dodo amount is quota", "dodo", "dodo", 5_000_000, 10, "USD", 10, 10},
	{"creem amount is quota", "creem", "creem", 2_500_000, 5, "USD", 5, 5},
	{"waffo amount is display units", "waffo", "waffo", 10, 10, "USD", 10, 10},
	{"waffo pancake", "waffo_pancake", "waffo_pancake", 30, 30, "USD", 30, 30},
	{"beatapi alipay pays CNY", "beatapi", "alipay", 10, 70, "CNY", 10, 10},
	{"beatapi wxpay pays CNY", "beatapi", "wxpay", 10, 70, "CNY", 10, 10},
	{"beatapi stripe pays USD, credits amount", "beatapi", "stripe", 10, 12, "USD", 12, 10},
	{"beatapi paypal pays USD", "beatapi", "paypal", 25, 25, "USD", 25, 25},
	{"beatapi dodo pays USD", "beatapi", "dodo", 15, 15, "USD", 15, 15},
	{"beatapi unknown method is unknown", "beatapi", "crypto", 20, 20, "", 0, 20},
	{"legacy method-only alipay", "", "alipay", 10, 70, "CNY", 10, 10},
	{"legacy method-only wxpay", "", "wxpay", 10, 70, "CNY", 10, 10},
	{"legacy method-only stripe", "", "stripe", 10, 10, "USD", 10, 10},
	{"case and spaces are ignored", " EPAY ", " AliPay ", 10, 70, "CNY", 10, 10},
	{"balance is unknown", "balance", "balance", 0, 9, "", 0, 0},
	{"unrecognised rail is unknown", "mystery", "mystery", 10, 10, "", 0, 10},
}

func topUpNear(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestTopUpCurrencyRules_Go(t *testing.T) {
	for _, c := range topUpRuleCases {
		t.Run(c.name, func(t *testing.T) {
			currency := topUpCurrencyOf(c.provider, c.method)
			if currency != c.wantCurrency {
				t.Fatalf("currency = %q, want %q", currency, c.wantCurrency)
			}
			paid, ok := topUpPaidUSDOf(currency, c.money)
			if ok != (c.wantCurrency != "") {
				t.Fatalf("paid ok = %v for currency %q", ok, currency)
			}
			if ok && !topUpNear(paid, c.wantPaid) {
				t.Fatalf("paid USD = %v, want %v", paid, c.wantPaid)
			}
			if got := topUpCreditedUSDOf(c.provider, c.method, c.amount, c.money); !topUpNear(got, c.wantCredited) {
				t.Fatalf("credited USD = %v, want %v", got, c.wantCredited)
			}
		})
	}
}

// The SQL expressions must reach the same verdict as the Go mirror on every
// row, or a card and the record list would disagree about one order.
func TestTopUpCurrencyRules_SQLMatchesGo(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	seeds := make([]topUpSeed, len(topUpRuleCases))
	for i, c := range topUpRuleCases {
		seeds[i] = topUpSeed{id: int64(i + 1), provider: c.provider, method: c.method, amount: c.amount, money: c.money, status: "success"}
	}
	insertTopUpSeeds(t, db, seeds)

	// SQLite has no information_schema, so the column probe reports
	// payment_provider missing; name the column explicitly to exercise the
	// provider rules the production databases run.
	cols := topUpSQLColsWithProvider("", "payment_provider")
	query := fmt.Sprintf(`SELECT id, %s AS currency, %s AS paid_usd, %s AS credited_usd FROM top_ups ORDER BY id`,
		cols.currency(), cols.paidUSD(), cols.creditedUSD())
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var (
			id       int
			currency sql.NullString
			paid     sql.NullFloat64
			credited float64
		)
		if err := rows.Scan(&id, &currency, &paid, &credited); err != nil {
			t.Fatalf("scan: %v", err)
		}
		c := topUpRuleCases[id-1]
		seen++
		if currency.String != c.wantCurrency || currency.Valid != (c.wantCurrency != "") {
			t.Errorf("%s: SQL currency = %v, want %q", c.name, currency, c.wantCurrency)
		}
		if paid.Valid != (c.wantCurrency != "") || (paid.Valid && !topUpNear(paid.Float64, c.wantPaid)) {
			t.Errorf("%s: SQL paid USD = %v, want %v", c.name, paid, c.wantPaid)
		}
		if !topUpNear(credited, c.wantCredited) {
			t.Errorf("%s: SQL credited USD = %v, want %v", c.name, credited, c.wantCredited)
		}
	}
	if seen != len(topUpRuleCases) {
		t.Fatalf("rows = %d, want %d", seen, len(topUpRuleCases))
	}
}

// A deployment whose top_ups predates payment_provider reads every rule off
// the method, which is what the old gateway recorded there.
func TestTopUpCurrencySQL_WithoutProviderColumnUsesMethod(t *testing.T) {
	db := installSQLiteForTests(t)
	createCurrencyTestTables(t, db)
	insertTopUpSeeds(t, db, []topUpSeed{
		{id: 1, method: "alipay", amount: 10, money: 70, status: "success"},
		{id: 2, method: "paypal", amount: 5_000_000, money: 10, status: "success"},
		{id: 3, method: "balance", amount: 5, money: 5, status: "success"},
	})
	if got := topUpPaymentProviderExpr(""); got != "''" {
		t.Fatalf("SQLite should report payment_provider missing, got %q", got)
	}
	query := fmt.Sprintf(`SELECT %s AS currency, %s AS paid_usd, %s AS credited_usd FROM top_ups ORDER BY id`,
		topUpCurrencySQL(""), topUpPaidUSDSQL(""), topUpCreditedUSDSQL(""))
	type row struct {
		Currency sql.NullString  `db:"currency"`
		Paid     sql.NullFloat64 `db:"paid_usd"`
		Credited float64         `db:"credited_usd"`
	}
	var rows []row
	if err := db.Select(&rows, query); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Currency.String != "CNY" || !topUpNear(rows[0].Paid.Float64, 10) || !topUpNear(rows[0].Credited, 10) {
		t.Errorf("alipay row = %+v", rows[0])
	}
	if rows[1].Currency.String != "USD" || !topUpNear(rows[1].Paid.Float64, 10) || !topUpNear(rows[1].Credited, 10) {
		t.Errorf("paypal row = %+v", rows[1])
	}
	if rows[2].Currency.Valid || rows[2].Paid.Valid {
		t.Errorf("balance row must stay unknown: %+v", rows[2])
	}
}

func TestTopUpPaidAtSQL_MatchesGrowthPanel(t *testing.T) {
	if topUpPaidAtSQL("") != paidAtExpr {
		t.Fatalf("analytics and growth must share one paid-at expression:\n%s\n%s", topUpPaidAtSQL(""), paidAtExpr)
	}
	if revenueUSDExpr() != topUpPaidUSDSQL("") {
		t.Fatal("growth revenue must use the shared paid-USD expression")
	}
}

func TestSQLFloatLiteral_AlwaysHasDecimalPoint(t *testing.T) {
	for in, want := range map[float64]string{7: "7.0", 7.25: "7.25", 0.5: "0.5"} {
		if got := sqlFloatLiteral(in); got != want {
			t.Errorf("sqlFloatLiteral(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTopUpSubscriptionTradeNo(t *testing.T) {
	for tradeNo, want := range map[string]bool{
		"SUBUSR12NOabc123":          true,
		"sub_ref_0123abcd":          true,
		"WAFFO_PANCAKE_SUB-1-2-abc": true,
		"USR12NOabc123":             false,
		"ref_0123abcd":              false,
		"WAFFO_PANCAKE-1-2-abc":     false,
		"":                          false,
	} {
		if got := isTopUpSubscriptionTradeNo(tradeNo); got != want {
			t.Errorf("isTopUpSubscriptionTradeNo(%q) = %v, want %v", tradeNo, got, want)
		}
	}
}

// ---------- fixtures shared by the currency regression tests ----------

type topUpSeed struct {
	id           int64
	userID       int64
	provider     string
	method       string
	tradeNo      string
	status       string
	amount       int64
	money        float64
	createTime   int64
	completeTime int64
}

func createCurrencyTestTables(t *testing.T, db *sqlx.DB) {
	t.Helper()
	db.MustExec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			display_name TEXT,
			email TEXT,
			inviter_id INTEGER,
			aff_count INTEGER,
			created_at INTEGER,
			deleted_at TEXT
		);
		CREATE TABLE top_ups (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			amount INTEGER,
			money REAL,
			trade_no TEXT,
			payment_method TEXT,
			payment_provider TEXT,
			create_time INTEGER,
			complete_time INTEGER,
			status TEXT
		);
	`)
}

func insertTopUpSeeds(t *testing.T, db *sqlx.DB, seeds []topUpSeed) {
	t.Helper()
	for _, s := range seeds {
		tradeNo := s.tradeNo
		if tradeNo == "" {
			tradeNo = fmt.Sprintf("T%d", s.id)
		}
		userID := s.userID
		if userID == 0 {
			userID = 1
		}
		createTime := s.createTime
		if createTime == 0 {
			createTime = 1_700_000_000
		}
		db.MustExec(`INSERT INTO top_ups (id, user_id, amount, money, trade_no, payment_method, payment_provider, create_time, complete_time, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.id, userID, s.amount, s.money, tradeNo, s.method, s.provider, createTime, s.completeTime, s.status)
	}
}
