package service

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

// TopUpRecord represents a top-up record
type TopUpRecord struct {
	ID        int64   `json:"id" db:"id"`
	UserID    int64   `json:"user_id" db:"user_id"`
	Username  *string `json:"username" db:"username"`
	UserEmail *string `json:"user_email" db:"user_email"`
	// Amount 原始 amount 列：易支付 / Waffo / BeatAPI 为展示单位，Stripe 为购买单位，
	// Dodo / PayPal / Creem 为 raw quota。展示一律用 CreditedUSD。
	Amount int64 `json:"amount" db:"amount"`
	// Money 原币金额，币种见 PaymentCurrency。
	Money float64 `json:"money" db:"money"`
	// PaymentCurrency 为 "CNY" / "USD"；空串 = 币种未知，不计入任何收入合计。
	PaymentCurrency string `json:"payment_currency" db:"-"`
	// PaidUSD 实付折美元（人民币按 CNY_PER_USD 折算）；币种未知时为 null。
	PaidUSD *float64 `json:"paid_usd" db:"-"`
	// CreditedUSD 入账额度（美元）。
	CreditedUSD       float64  `json:"credited_usd" db:"-"`
	IsSubscription    bool     `json:"is_subscription" db:"-"`
	TradeNo           string   `json:"trade_no" db:"trade_no"`
	PaymentMethod     string   `json:"payment_method" db:"payment_method"`
	PaymentProvider   string   `json:"payment_provider" db:"payment_provider"`
	CreateTime        int64    `json:"create_time" db:"create_time"`
	CompleteTime      int64    `json:"complete_time" db:"complete_time"`
	Status            string   `json:"status" db:"status"`
	StatusBucket      string   `json:"status_bucket" db:"status_bucket"`
	CompletionSeconds int64    `json:"completion_seconds" db:"completion_seconds"`
	AnomalyReasons    []string `json:"anomaly_reasons,omitempty"`
}

// TopUpStatistics 充值记录页的汇总卡片。金额只合计已确认币种的订单并折成美元
// （人民币按 CNYPerUSD 折算）；币种未知的订单单独计数、给出原币金额，不进任何合计。
type TopUpStatistics struct {
	TotalCount     int64 `json:"total_count"`
	SuccessCount   int64 `json:"success_count"`
	PendingCount   int64 `json:"pending_count"`
	ReviewingCount int64 `json:"reviewing_count"`
	FailedCount    int64 `json:"failed_count"`
	ExpiredCount   int64 `json:"expired_count"`
	UnknownCount   int64 `json:"unknown_count"` // 状态未知

	SuccessMoneyUSD   float64 `json:"success_money_usd"`
	PendingMoneyUSD   float64 `json:"pending_money_usd"`
	ReviewingMoneyUSD float64 `json:"reviewing_money_usd"`
	FailedMoneyUSD    float64 `json:"failed_money_usd"`
	ExpiredMoneyUSD   float64 `json:"expired_money_usd"`
	// SuccessAmountUSD 成功订单的入账额度（美元）。
	SuccessAmountUSD float64 `json:"success_amount_usd"`

	// 成功订单按原币拆分，便于核对折算。
	SuccessCNYMoney float64 `json:"success_cny_money"`
	SuccessUSDMoney float64 `json:"success_usd_money"`

	UnknownCurrencyCount        int64   `json:"unknown_currency_count"` // 全部状态
	SuccessUnknownCurrencyCount int64   `json:"success_unknown_currency_count"`
	SuccessUnknownCurrencyMoney float64 `json:"success_unknown_currency_money"` // 原币金额

	CNYPerUSD float64 `json:"cny_per_usd"`
}

// ListTopUpParams holds list query parameters
type ListTopUpParams struct {
	Page            int    `json:"page"`
	PageSize        int    `json:"page_size"`
	UserID          *int64 `json:"user_id"`
	Username        string `json:"username"`
	InviterID       *int64 `json:"inviter_id"`
	Status          string `json:"status"`
	PaymentMethod   string `json:"payment_method"`
	PaymentProvider string `json:"payment_provider"`
	TradeNo         string `json:"trade_no"`
	StartDate       string `json:"start_date"`
	EndDate         string `json:"end_date"`
	// Currency 过滤原币种：CNY / USD / unknown（币种未知）。
	Currency string `json:"currency"`
}

// PaginatedTopUps holds paginated top-up results
type PaginatedTopUps struct {
	Items      []TopUpRecord `json:"items"`
	Total      int64         `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

const defaultPendingAnomalyHours = 2

// topUpStatusBucketSQL normalises the free-text status column. "reviewing" is
// the gateway's Stripe fraud-review hold: the money arrived but the quota has
// not been credited, so it is neither success nor an unrecognised state.
func topUpStatusBucketSQL(column string) string {
	trimmed := fmt.Sprintf("TRIM(COALESCE(%s, ''))", column)
	lower := fmt.Sprintf("LOWER(%s)", trimmed)
	return fmt.Sprintf(`CASE
		WHEN %[1]s = '' THEN 'pending'
		WHEN %[2]s IN ('success', 'completed') OR %[1]s = '1' THEN 'success'
		WHEN %[2]s IN ('failed', 'error') OR %[1]s = '-1' THEN 'failed'
		WHEN %[2]s = 'expired' THEN 'expired'
		WHEN %[2]s = 'reviewing' THEN 'reviewing'
		WHEN %[2]s IN ('pending', 'processing', 'created', 'waiting', 'unpaid') OR %[1]s = '0' THEN 'pending'
		ELSE 'unknown'
	END`, trimmed, lower)
}

func topUpStatusBucket(status string) string {
	trimmed := strings.TrimSpace(status)
	lower := strings.ToLower(trimmed)
	switch {
	case trimmed == "":
		return "pending"
	case lower == "success" || lower == "completed" || trimmed == "1":
		return "success"
	case lower == "failed" || lower == "error" || trimmed == "-1":
		return "failed"
	case lower == "expired":
		return "expired"
	case lower == "reviewing":
		return "reviewing"
	case lower == "pending" || lower == "processing" || lower == "created" || lower == "waiting" || lower == "unpaid" || trimmed == "0":
		return "pending"
	default:
		return "unknown"
	}
}

func topUpCompletionSeconds(createTime, completeTime int64) int64 {
	if createTime <= 0 || completeTime <= 0 || completeTime < createTime {
		return 0
	}
	return completeTime - createTime
}

// isCompleteTradeNo reports whether the input looks like a full trade number
// rather than a search fragment. A complete trade_no has no LIKE wildcards
// (% / _) and no internal whitespace — in that case we match it with an exact
// equality against the unique top_ups_trade_no_key index. Anything else falls
// back to a substring LIKE.
func isCompleteTradeNo(s string) bool {
	return !strings.ContainsAny(s, "%_ \t")
}

func enrichTopUpRecord(rec *TopUpRecord, now int64, pendingHours int) {
	if rec.StatusBucket == "" {
		rec.StatusBucket = topUpStatusBucket(rec.Status)
	}
	if rec.CompletionSeconds == 0 {
		rec.CompletionSeconds = topUpCompletionSeconds(rec.CreateTime, rec.CompleteTime)
	}
	rec.AnomalyReasons = topUpAnomalyReasons(*rec, now, pendingHours)
	rec.IsSubscription = isTopUpSubscriptionTradeNo(rec.TradeNo)
	rec.PaymentCurrency = topUpCurrencyOf(rec.PaymentProvider, rec.PaymentMethod)
	rec.CreditedUSD = topUpCreditedUSDOf(rec.PaymentProvider, rec.PaymentMethod, rec.Amount, rec.Money)
	rec.PaidUSD = nil
	if usd, ok := topUpPaidUSDOf(rec.PaymentCurrency, rec.Money); ok {
		rec.PaidUSD = &usd
	}
}

func topUpAnomalyReasons(rec TopUpRecord, now int64, pendingHours int) []string {
	if pendingHours < 1 {
		pendingHours = defaultPendingAnomalyHours
	}

	bucket := rec.StatusBucket
	if bucket == "" {
		bucket = topUpStatusBucket(rec.Status)
	}

	reasons := make([]string, 0, 4)
	if strings.TrimSpace(rec.TradeNo) == "" {
		reasons = append(reasons, "空交易号")
	}
	if rec.Money <= 0 {
		reasons = append(reasons, "金额异常")
	}
	// 订阅购买镜像进 top_ups 时 amount 恒为 0（买的是套餐不是额度），不算异常。
	if rec.Amount <= 0 && !isTopUpSubscriptionTradeNo(rec.TradeNo) {
		reasons = append(reasons, "额度异常")
	}
	if rec.CreateTime > 0 && rec.CompleteTime > 0 && rec.CompleteTime < rec.CreateTime {
		reasons = append(reasons, "完成早于创建")
	}
	if bucket == "pending" && rec.CreateTime > 0 && now-rec.CreateTime >= int64(pendingHours)*3600 {
		reasons = append(reasons, "超时待支付")
	}
	if bucket == "unknown" {
		reasons = append(reasons, "未知状态")
	}
	return reasons
}

func topUpPaymentProviderExpr(alias string) string {
	db := database.Get()
	if db.ColumnExists("top_ups", "payment_provider") {
		if alias != "" {
			return alias + ".payment_provider"
		}
		return "payment_provider"
	}
	return "''"
}

func topUpSelectColumns() string {
	email := "NULL"
	if database.Get().ColumnExists("users", "email") {
		email = "u.email"
	}
	return fmt.Sprintf(`t.id, t.user_id, u.username, %s AS user_email, t.amount, t.money,
		COALESCE(t.trade_no,'') as trade_no,
		COALESCE(t.payment_method,'') as payment_method,
		COALESCE(%s,'') as payment_provider,
		COALESCE(t.create_time,0) as create_time,
		COALESCE(t.complete_time,0) as complete_time,
		COALESCE(t.status,'') as status,
		%s as status_bucket,
		CASE
			WHEN t.create_time > 0 AND t.complete_time > 0 AND t.complete_time >= t.create_time THEN t.complete_time - t.create_time
			ELSE 0
		END as completion_seconds`, email, topUpPaymentProviderExpr("t"), topUpStatusBucketSQL("t.status"))
}

// topUpCurrencyFilter turns the currency filter into a condition on the
// aliased list query: CNY / USD match the rule, unknown matches rows no rule
// recognises. Anything else adds nothing.
func topUpCurrencyFilter(currency, placeholder string) (string, []interface{}) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case topUpCurrencyCNY, topUpCurrencyUSD:
		return fmt.Sprintf("%s = %s", topUpCurrencySQL("t"), placeholder), []interface{}{strings.ToUpper(strings.TrimSpace(currency))}
	case "UNKNOWN":
		return fmt.Sprintf("%s IS NULL", topUpCurrencySQL("t")), nil
	default:
		return "", nil
	}
}

// buildTopUpWhere translates filter params into a parameterised WHERE clause.
// Returns the WHERE body (without the leading "WHERE"), the corresponding args,
// and the next placeholder index that the caller should use for additional args
// (e.g. LIMIT/OFFSET when paginating).
func buildTopUpWhere(params ListTopUpParams) (string, []interface{}, int) {
	db := database.Get()

	where := []string{}
	args := []interface{}{}
	argIdx := 1

	if params.UserID != nil {
		where = append(where, fmt.Sprintf("t.user_id = %s", db.Placeholder(argIdx)))
		args = append(args, *params.UserID)
		argIdx++
	} else {
		// 全局面板白名单：列表默认排除；显式按 user_id 查询时仍可审计该用户
		if cond, wlArgs, next := PanelWhitelistNotInSQL("t.user_id", argIdx); cond != "" {
			// cond 自带 AND 前缀，改成 where 片段
			where = append(where, strings.TrimPrefix(strings.TrimSpace(cond), "AND "))
			args = append(args, wlArgs...)
			argIdx = next
		}
	}

	if params.InviterID != nil {
		where = append(where, fmt.Sprintf("u.inviter_id = %s", db.Placeholder(argIdx)))
		args = append(args, *params.InviterID)
		argIdx++
	}

	if uname := strings.TrimSpace(params.Username); uname != "" {
		where = append(where, fmt.Sprintf("u.username LIKE %s", db.Placeholder(argIdx)))
		args = append(args, "%"+uname+"%")
		argIdx++
	}

	if params.Status != "" {
		switch params.Status {
		case "success", "failed", "pending", "reviewing", "expired", "unknown":
			where = append(where, fmt.Sprintf("(%s) = %s", topUpStatusBucketSQL("t.status"), db.Placeholder(argIdx)))
			args = append(args, params.Status)
			argIdx++
		}
	}

	if cond, currencyArgs := topUpCurrencyFilter(params.Currency, db.Placeholder(argIdx)); cond != "" {
		where = append(where, cond)
		args = append(args, currencyArgs...)
		argIdx += len(currencyArgs)
	}

	if params.PaymentMethod != "" {
		where = append(where, fmt.Sprintf("t.payment_method = %s", db.Placeholder(argIdx)))
		args = append(args, params.PaymentMethod)
		argIdx++
	}

	if params.PaymentProvider != "" {
		if db.ColumnExists("top_ups", "payment_provider") {
			where = append(where, fmt.Sprintf("t.payment_provider = %s", db.Placeholder(argIdx)))
			args = append(args, params.PaymentProvider)
			argIdx++
		} else {
			where = append(where, "1=0")
		}
	}

	if tradeNo := strings.TrimSpace(params.TradeNo); tradeNo != "" {
		// 账单号智能匹配：粘贴完整交易号（无空格、无 LIKE 通配符）时走精确等值，
		// 命中唯一索引 top_ups_trade_no_key 做秒查；否则按片段 LIKE 模糊匹配。
		if isCompleteTradeNo(tradeNo) {
			where = append(where, fmt.Sprintf("t.trade_no = %s", db.Placeholder(argIdx)))
			args = append(args, tradeNo)
		} else {
			where = append(where, fmt.Sprintf("t.trade_no LIKE %s", db.Placeholder(argIdx)))
			args = append(args, "%"+tradeNo+"%")
		}
		argIdx++
	}

	if params.StartDate != "" {
		ts, err := util.ParseDateToTimestampPublic(params.StartDate, false)
		if err == nil {
			where = append(where, fmt.Sprintf("t.create_time >= %s", db.Placeholder(argIdx)))
			args = append(args, ts)
			argIdx++
		}
	}

	if params.EndDate != "" {
		ts, err := util.ParseDateToTimestampPublic(params.EndDate, true)
		if err == nil {
			where = append(where, fmt.Sprintf("t.create_time <= %s", db.Placeholder(argIdx)))
			args = append(args, ts)
			argIdx++
		}
	}

	whereSQL := "1=1"
	if len(where) > 0 {
		whereSQL = strings.Join(where, " AND ")
	}
	return whereSQL, args, argIdx
}

// ListTopUpRecords lists top-up records with pagination and filtering
func ListTopUpRecords(params ListTopUpParams) (*PaginatedTopUps, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 100 {
		params.PageSize = 20
	}

	db := database.Get()

	whereSQL, args, argIdx := buildTopUpWhere(params)

	// Count
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE %s", whereSQL)
	var total int64
	if err := db.DB.Get(&total, countSQL, args...); err != nil {
		return nil, fmt.Errorf("count query failed: %w", err)
	}

	totalPages := int((total + int64(params.PageSize) - 1) / int64(params.PageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	offset := (params.Page - 1) * params.PageSize

	// Select with user join
	selectSQL := fmt.Sprintf(`SELECT %s FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE %s ORDER BY t.create_time DESC LIMIT %s OFFSET %s`,
		topUpSelectColumns(), whereSQL, db.Placeholder(argIdx), db.Placeholder(argIdx+1))
	args = append(args, params.PageSize, offset)

	rows, err := db.DB.Queryx(selectSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("select query failed: %w", err)
	}
	defer rows.Close()

	var items []TopUpRecord
	now := time.Now().Unix()
	for rows.Next() {
		var rec TopUpRecord
		if err := rows.StructScan(&rec); err != nil {
			continue
		}
		enrichTopUpRecord(&rec, now, defaultPendingAnomalyHours)
		items = append(items, rec)
	}

	if items == nil {
		items = []TopUpRecord{}
	}

	return &PaginatedTopUps{
		Items:      items,
		Total:      total,
		Page:       params.Page,
		PageSize:   params.PageSize,
		TotalPages: totalPages,
	}, nil
}

// CountTopUps returns the total number of top-ups matching the filter.
// Used by ExportTopUpsToCSV to enforce the export size cap before streaming.
func CountTopUps(params ListTopUpParams) (int64, error) {
	db := database.Get()
	whereSQL, args, _ := buildTopUpWhere(params)
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE %s", whereSQL)
	var total int64
	if err := db.DB.Get(&total, countSQL, args...); err != nil {
		return 0, fmt.Errorf("count query failed: %w", err)
	}
	return total, nil
}

// ErrExportTooLarge is returned when an export request exceeds the row cap.
var ErrExportTooLarge = errors.New("export exceeds row limit")

// TopUpExportLimit caps how many rows a single CSV export may contain.
// Streaming the table is fine, but the user-side cost (download size, Excel
// load time) makes a hard ceiling kinder than letting them request millions.
// Declared as var (not const) so tests can shrink it temporarily and verify
// the streaming break — production code should treat it as immutable.
var TopUpExportLimit int64 = 100000

// topUpCSVMoneyHeader is the money block both CSV exports share: the order's
// own currency and amount, its USD value (blank when the currency is unknown)
// and the quota it credited in USD.
var topUpCSVMoneyHeader = []string{"原币种", "原币金额", "折合美元", "入账额度(美元)"}

func topUpCurrencyLabel(currency string) string {
	if currency == "" {
		return "未知"
	}
	return currency
}

func formatMoney2(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func topUpCSVMoneyCells(rec TopUpRecord) []string {
	paid := ""
	if rec.PaidUSD != nil {
		paid = formatMoney2(*rec.PaidUSD)
	}
	return []string{topUpCurrencyLabel(rec.PaymentCurrency), formatMoney2(rec.Money), paid, formatMoney2(rec.CreditedUSD)}
}

// ExportTopUpsToCSV streams top-up records as CSV to the writer. The caller is
// responsible for setting response headers and (recommended) running CountTopUps
// first to short-circuit oversized exports — this function only flips on the
// limit if the count exceeds it mid-stream.
//
// 当 params.UserID 已指定时，走「单用户额度入账」导出：在线充值 + 兑换码明细，
// CSV 标注类型/是否计入实付，并在末尾附剔除兑换码后的统计摘要。
func ExportTopUpsToCSV(ctx context.Context, w io.Writer, params ListTopUpParams) error {
	if params.UserID != nil && *params.UserID > 0 {
		return exportUserIncomeCSV(ctx, w, params)
	}

	db := database.Get()
	whereSQL, args, _ := buildTopUpWhere(params)

	// UTF-8 BOM so Excel (especially zh-CN locale) auto-detects encoding.
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	csvW := csv.NewWriter(w)
	defer csvW.Flush()

	header := append([]string{"ID", "用户ID", "用户名"}, topUpCSVMoneyHeader...)
	header = append(header, "交易号", "支付方式", "支付渠道", "状态", "归一状态", "完成耗时(秒)", "异常标记", "创建时间", "完成时间")
	if err := csvW.Write(header); err != nil {
		return err
	}

	selectSQL := fmt.Sprintf(`SELECT %s FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE %s ORDER BY t.create_time DESC`, topUpSelectColumns(), whereSQL)

	rows, err := db.DB.QueryxContext(ctx, selectSQL, args...)
	if err != nil {
		return fmt.Errorf("export query failed: %w", err)
	}
	defer rows.Close()

	var written int64
	now := time.Now().Unix()
	for rows.Next() {
		// Surface ctx cancellation (timeout / client disconnect) without finishing the loop.
		if err := ctx.Err(); err != nil {
			return err
		}

		var rec TopUpRecord
		if err := rows.StructScan(&rec); err != nil {
			continue
		}
		enrichTopUpRecord(&rec, now, defaultPendingAnomalyHours)

		username := ""
		if rec.Username != nil {
			username = *rec.Username
		}
		createTimeStr := ""
		if rec.CreateTime > 0 {
			createTimeStr = time.Unix(rec.CreateTime, 0).Format(time.RFC3339)
		}
		completeTimeStr := ""
		if rec.CompleteTime > 0 {
			completeTimeStr = time.Unix(rec.CompleteTime, 0).Format(time.RFC3339)
		}

		row := append([]string{strconv.FormatInt(rec.ID, 10), strconv.FormatInt(rec.UserID, 10), username}, topUpCSVMoneyCells(rec)...)
		row = append(row,
			rec.TradeNo,
			rec.PaymentMethod,
			rec.PaymentProvider,
			rec.Status,
			rec.StatusBucket,
			strconv.FormatInt(rec.CompletionSeconds, 10),
			strings.Join(rec.AnomalyReasons, "; "),
			createTimeStr,
			completeTimeStr,
		)
		if err := csvW.Write(row); err != nil {
			return err
		}

		written++
		if written >= TopUpExportLimit {
			// 写满上限就停手，不再吐第 100001 行 —— handler 的 CountTopUps 预检通常已经
			// 把超限请求挡在 400 上，这里只是兜底 race（count 之后又有新插入）。
			break
		}
		// Periodic flush so the browser begins receiving bytes promptly.
		if written%500 == 0 {
			csvW.Flush()
			if err := csvW.Error(); err != nil {
				return err
			}
		}
	}

	return rows.Err()
}

// GetTopUpStatistics returns the record page's summary cards. Like the record
// list it filters on creation date, so a card and the rows it filters to agree.
func GetTopUpStatistics(startDate, endDate string) (*TopUpStatistics, error) {
	whereSQL, args := topUpStatisticsWhere(startDate, endDate)
	rows, err := queryTopUpBucketTotals(whereSQL, args)
	if err != nil {
		return nil, fmt.Errorf("statistics query failed: %w", err)
	}
	return foldTopUpStatistics(rows), nil
}

func topUpStatisticsWhere(startDate, endDate string) (string, []interface{}) {
	db := database.Get()
	where := []string{}
	args := []interface{}{}
	argIdx := 1

	if startDate != "" {
		if ts, err := util.ParseDateToTimestampPublic(startDate, false); err == nil {
			where = append(where, fmt.Sprintf("create_time >= %s", db.Placeholder(argIdx)))
			args = append(args, ts)
			argIdx++
		}
	}
	if endDate != "" {
		if ts, err := util.ParseDateToTimestampPublic(endDate, true); err == nil {
			where = append(where, fmt.Sprintf("create_time <= %s", db.Placeholder(argIdx)))
			args = append(args, ts)
			argIdx++
		}
	}
	if cond, wlArgs, _ := PanelWhitelistNotInSQL("user_id", argIdx); cond != "" {
		where = append(where, strings.TrimPrefix(strings.TrimSpace(cond), "AND "))
		args = append(args, wlArgs...)
	}
	if len(where) == 0 {
		return "1=1", args
	}
	return strings.Join(where, " AND "), args
}

func foldTopUpStatistics(rows []topUpBucketTotals) *TopUpStatistics {
	s := &TopUpStatistics{CNYPerUSD: cnyPerUSD()}
	for _, r := range rows {
		s.TotalCount += r.Count
		s.UnknownCurrencyCount += r.UnknownCurrencyCount
		switch r.Bucket {
		case "success":
			s.SuccessCount, s.SuccessMoneyUSD, s.SuccessAmountUSD = r.Count, r.PaidUSD, r.CreditedUSD
			s.SuccessCNYMoney, s.SuccessUSDMoney = r.CNYMoney, r.USDMoney
			s.SuccessUnknownCurrencyCount, s.SuccessUnknownCurrencyMoney = r.UnknownCurrencyCount, r.UnknownCurrencyMoney
		case "pending":
			s.PendingCount, s.PendingMoneyUSD = r.Count, r.PaidUSD
		case "reviewing":
			s.ReviewingCount, s.ReviewingMoneyUSD = r.Count, r.PaidUSD
		case "failed":
			s.FailedCount, s.FailedMoneyUSD = r.Count, r.PaidUSD
		case "expired":
			s.ExpiredCount, s.ExpiredMoneyUSD = r.Count, r.PaidUSD
		default:
			s.UnknownCount += r.Count
		}
	}
	return s
}

// GetPaymentMethods returns distinct payment methods
func GetPaymentMethods() ([]string, error) {
	db := database.Get()
	var methods []string
	err := db.DB.Select(&methods, "SELECT DISTINCT payment_method FROM top_ups WHERE payment_method IS NOT NULL AND payment_method != '' ORDER BY payment_method")
	if err != nil {
		return nil, err
	}
	if methods == nil {
		methods = []string{}
	}
	return methods, nil
}

// GetPaymentProviders returns distinct payment providers.
func GetPaymentProviders() ([]string, error) {
	db := database.Get()
	if !db.ColumnExists("top_ups", "payment_provider") {
		return []string{}, nil
	}
	var providers []string
	err := db.DB.Select(&providers, "SELECT DISTINCT payment_provider FROM top_ups WHERE payment_provider IS NOT NULL AND payment_provider != '' ORDER BY payment_provider")
	if err != nil {
		return nil, err
	}
	if providers == nil {
		providers = []string{}
	}
	return providers, nil
}

// GetTopUpByID returns a single top-up record
func GetTopUpByID(id int64) (*TopUpRecord, error) {
	db := database.Get()
	sql := fmt.Sprintf(`SELECT %s FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE t.id = %s`, topUpSelectColumns(), db.Placeholder(1))

	var rec TopUpRecord
	if err := db.DB.Get(&rec, sql, id); err != nil {
		return nil, err
	}
	enrichTopUpRecord(&rec, time.Now().Unix(), defaultPendingAnomalyHours)
	return &rec, nil
}
