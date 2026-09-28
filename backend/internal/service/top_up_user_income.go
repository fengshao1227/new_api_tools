package service

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/util"
)

// UserQuotaIncomeSummary 单用户额度入账汇总：在线充值 vs 兑换码（可剔除）。
// 金额一律美元：实付只合计已确认币种、人民币按 CNYPerUSD 折算；币种未知的
// 成功单单独计数并给出原币金额，不进实付合计。
type UserQuotaIncomeSummary struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`

	// 在线充值（成功）
	PaidCount                int64   `json:"paid_count"`
	PaidMoneyUSD             float64 `json:"paid_money_usd"` // 实付折美元
	PaidCNYMoney             float64 `json:"paid_cny_money"` // 其中人民币原币
	PaidUSDMoney             float64 `json:"paid_usd_money"` // 其中美元原币
	PaidUnknownCurrencyCount int64   `json:"paid_unknown_currency_count"`
	PaidUnknownCurrencyMoney float64 `json:"paid_unknown_currency_money"` // 原币金额，不计入实付
	PaidAmount               float64 `json:"paid_amount"`                 // 入账额度（美元）

	// 未成功（待处理 + 审核中 + 已过期）
	UnsuccessCount    int64   `json:"unsuccess_count"`
	UnsuccessMoneyUSD float64 `json:"unsuccess_money_usd"`

	// 兑换码使用
	RedemptionCount    int64   `json:"redemption_count"`
	RedemptionQuotaRaw int64   `json:"redemption_quota_raw"`
	RedemptionQuotaUSD float64 `json:"redemption_quota_usd"` // 兑换获得额度 USD

	// 在线充值获得额度（= PaidAmount，不含兑换码）
	NetPaidAmountUSD float64 `json:"net_paid_amount_usd"`
	// 总入账额度（在线充值获得 + 兑换码获得）
	TotalIncomeUSD float64 `json:"total_income_usd"`
	CNYPerUSD      float64 `json:"cny_per_usd"`
}

// GetUserQuotaIncomeSummary 汇总指定用户的在线充值与兑换码入账。
// startDate/endDate 可选；充值按 create_time（与记录列表一致），兑换按 redeemed_time。
func GetUserQuotaIncomeSummary(userID int64, startDate, endDate string) (*UserQuotaIncomeSummary, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("user_id is required")
	}

	db := database.Get()
	out := &UserQuotaIncomeSummary{UserID: userID, CNYPerUSD: cnyPerUSD()}

	var uname string
	if err := db.DB.Get(&uname, fmt.Sprintf("SELECT username FROM users WHERE id = %s", db.Placeholder(1)), userID); err == nil {
		out.Username = uname
	}

	topUpWhere, topUpArgs := userIncomeWhere("user_id", "create_time", userID, startDate, endDate)
	buckets, err := queryTopUpBucketTotals(topUpWhere, topUpArgs)
	if err != nil {
		return nil, fmt.Errorf("top-up aggregate failed: %w", err)
	}
	out.applyTopUpBuckets(buckets)

	redWhere, redArgs := userIncomeWhere("used_user_id", "redeemed_time", userID, startDate, endDate,
		"redeemed_time IS NOT NULL", "redeemed_time > 0", "deleted_at IS NULL")
	type redAgg struct {
		Cnt   int64 `db:"cnt"`
		Quota int64 `db:"quota"`
	}
	var red redAgg
	redSQL := fmt.Sprintf(`SELECT COUNT(*) as cnt, COALESCE(SUM(quota), 0) as quota
		FROM redemptions WHERE %s`, redWhere)
	if err := db.DB.Get(&red, redSQL, redArgs...); err != nil {
		return nil, fmt.Errorf("redemption aggregate failed: %w", err)
	}
	out.RedemptionCount = red.Cnt
	out.RedemptionQuotaRaw = red.Quota
	out.RedemptionQuotaUSD = float64(red.Quota) / float64(util.TokensPerUSD)

	out.NetPaidAmountUSD = out.PaidAmount
	out.TotalIncomeUSD = out.PaidAmount + out.RedemptionQuotaUSD
	return out, nil
}

func (out *UserQuotaIncomeSummary) applyTopUpBuckets(buckets []topUpBucketTotals) {
	for _, b := range buckets {
		switch b.Bucket {
		case "success":
			out.PaidCount, out.PaidMoneyUSD, out.PaidAmount = b.Count, b.PaidUSD, b.CreditedUSD
			out.PaidCNYMoney, out.PaidUSDMoney = b.CNYMoney, b.USDMoney
			out.PaidUnknownCurrencyCount, out.PaidUnknownCurrencyMoney = b.UnknownCurrencyCount, b.UnknownCurrencyMoney
		case "pending", "reviewing", "expired":
			out.UnsuccessCount += b.Count
			out.UnsuccessMoneyUSD += b.PaidUSD
		}
	}
}

// userIncomeWhere builds "userCol = ? AND extra... [AND timeCol >= ?] [AND timeCol <= ?]"
// with dialect placeholders numbered from 1.
func userIncomeWhere(userCol, timeCol string, userID int64, startDate, endDate string, extra ...string) (string, []interface{}) {
	db := database.Get()
	where := append([]string{fmt.Sprintf("%s = %s", userCol, db.Placeholder(1))}, extra...)
	args := []interface{}{userID}
	if startDate != "" {
		if ts, err := util.ParseDateToTimestampPublic(startDate, false); err == nil {
			args = append(args, ts)
			where = append(where, fmt.Sprintf("%s >= %s", timeCol, db.Placeholder(len(args))))
		}
	}
	if endDate != "" {
		if ts, err := util.ParseDateToTimestampPublic(endDate, true); err == nil {
			args = append(args, ts)
			where = append(where, fmt.Sprintf("%s <= %s", timeCol, db.Placeholder(len(args))))
		}
	}
	return strings.Join(where, " AND "), args
}

// userIncomeCSVTotals accumulates the footer while the rows stream out.
type userIncomeCSVTotals struct {
	paidCount, unknownCount, redCount                                   int64
	paidUSD, cnyMoney, usdMoney, unknownMoney, creditedUSD, redQuotaUSD float64
}

func (t *userIncomeCSVTotals) addPaid(rec TopUpRecord) {
	t.paidCount++
	t.creditedUSD += rec.CreditedUSD
	switch rec.PaymentCurrency {
	case topUpCurrencyCNY:
		t.cnyMoney += rec.Money
	case topUpCurrencyUSD:
		t.usdMoney += rec.Money
	default:
		t.unknownCount++
		t.unknownMoney += rec.Money
	}
	if rec.PaidUSD != nil {
		t.paidUSD += *rec.PaidUSD
	}
}

// 统一表头：在线充值与兑换码共用，便于 Excel 筛选。
func userIncomeCSVHeader() []string {
	header := append([]string{"类型", "计入实付统计", "ID", "用户ID", "用户名"}, topUpCSVMoneyHeader...)
	return append(header, "交易号/兑换码", "名称/支付方式", "支付渠道", "状态", "归一状态",
		"完成耗时(秒)", "异常标记", "创建/兑换时间", "完成时间", "备注")
}

// exportUserIncomeCSV 导出单用户「充值 + 兑换码」明细，并在 CSV 中标注类型与是否计入实付。
// 底部附统计摘要：实付（折美元）/ 兑换 / 剔除兑换后净入账。
func exportUserIncomeCSV(ctx context.Context, w io.Writer, params ListTopUpParams) error {
	if params.UserID == nil || *params.UserID <= 0 {
		return fmt.Errorf("user_id is required for user income export")
	}
	userID := *params.UserID

	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	csvW := csv.NewWriter(w)
	defer csvW.Flush()
	if err := csvW.Write(userIncomeCSVHeader()); err != nil {
		return err
	}

	var totals userIncomeCSVTotals
	written, usernameHint, err := writeUserIncomeTopUpRows(ctx, csvW, params, &totals)
	if err != nil {
		return err
	}
	if written < TopUpExportLimit {
		hint, err := writeUserIncomeRedemptionRows(ctx, csvW, params, userID, TopUpExportLimit-written, &totals)
		if err != nil {
			return err
		}
		if usernameHint == "" {
			usernameHint = hint
		}
	}

	writeUserIncomeFooter(csvW, userID, usernameHint, totals, params)
	csvW.Flush()
	return csvW.Error()
}

func writeUserIncomeTopUpRows(ctx context.Context, csvW *csv.Writer, params ListTopUpParams, totals *userIncomeCSVTotals) (int64, string, error) {
	db := database.Get()
	whereSQL, args, _ := buildTopUpWhere(params)
	selectSQL := fmt.Sprintf(
		`SELECT %s FROM top_ups t LEFT JOIN users u ON t.user_id = u.id WHERE %s ORDER BY t.create_time DESC`,
		topUpSelectColumns(), whereSQL)
	rows, err := db.DB.QueryxContext(ctx, selectSQL, args...)
	if err != nil {
		return 0, "", fmt.Errorf("export top-ups query failed: %w", err)
	}
	defer rows.Close()

	var written int64
	usernameHint := ""
	now := time.Now().Unix()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return written, usernameHint, err
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
		if usernameHint == "" {
			usernameHint = username
		}
		if err := csvW.Write(userIncomeTopUpRow(rec, username, totals)); err != nil {
			return written, usernameHint, err
		}
		written++
		if written >= TopUpExportLimit {
			break
		}
		if written%500 == 0 {
			csvW.Flush()
			if err := csvW.Error(); err != nil {
				return written, usernameHint, err
			}
		}
	}
	return written, usernameHint, rows.Err()
}

func userIncomeTopUpRow(rec TopUpRecord, username string, totals *userIncomeCSVTotals) []string {
	countInPaid, note := "否", "非成功充值，不计入实付统计"
	if rec.StatusBucket == "success" {
		totals.addPaid(rec)
		countInPaid, note = "是", "在线充值成功，计入实付"
		if rec.PaidUSD == nil {
			countInPaid, note = "否", "在线充值成功但币种未知：计入笔数与入账额度，不计入实付合计"
		}
	}
	row := append([]string{"在线充值", countInPaid, strconv.FormatInt(rec.ID, 10), strconv.FormatInt(rec.UserID, 10), username},
		topUpCSVMoneyCells(rec)...)
	return append(row,
		rec.TradeNo,
		rec.PaymentMethod,
		rec.PaymentProvider,
		rec.Status,
		rec.StatusBucket,
		strconv.FormatInt(rec.CompletionSeconds, 10),
		strings.Join(rec.AnomalyReasons, "; "),
		formatUnixRFC3339(rec.CreateTime),
		formatUnixRFC3339(rec.CompleteTime),
		note,
	)
}

type userIncomeRedemptionRow struct {
	ID           int64  `db:"id"`
	Key          string `db:"key"`
	Name         string `db:"name"`
	Quota        int64  `db:"quota"`
	RedeemedTime int64  `db:"redeemed_time"`
	Username     string `db:"username"`
}

// writeUserIncomeRedemptionRows 兑换码行（不计入实付），最多 limit 行。
func writeUserIncomeRedemptionRows(ctx context.Context, csvW *csv.Writer, params ListTopUpParams, userID, limit int64, totals *userIncomeCSVTotals) (string, error) {
	db := database.Get()
	where, args := userIncomeWhere("r.used_user_id", "r.redeemed_time", userID, params.StartDate, params.EndDate,
		"r.redeemed_time IS NOT NULL", "r.redeemed_time > 0", "r.deleted_at IS NULL")
	args = append(args, limit)
	redSQL := fmt.Sprintf(`SELECT r.id, COALESCE(r.%s,'') as "key", COALESCE(r.name,'') as name,
		COALESCE(r.quota,0) as quota, COALESCE(r.redeemed_time,0) as redeemed_time,
		COALESCE(u.username,'') as username
		FROM redemptions r
		LEFT JOIN users u ON r.used_user_id = u.id
		WHERE %s
		ORDER BY r.redeemed_time DESC
		LIMIT %s`,
		keyCol(db.IsPG), where, db.Placeholder(len(args)))

	redRows, err := db.DB.QueryxContext(ctx, redSQL, args...)
	if err != nil {
		return "", fmt.Errorf("export redemptions query failed: %w", err)
	}
	defer redRows.Close()

	usernameHint := ""
	for redRows.Next() {
		if err := ctx.Err(); err != nil {
			return usernameHint, err
		}
		var rr userIncomeRedemptionRow
		if err := redRows.StructScan(&rr); err != nil {
			continue
		}
		if usernameHint == "" {
			usernameHint = rr.Username
		}
		quotaUSD := float64(rr.Quota) / float64(util.TokensPerUSD)
		totals.redCount++
		totals.redQuotaUSD += quotaUSD
		if err := csvW.Write([]string{
			"兑换码", "否", // 明确不计入实付
			strconv.FormatInt(rr.ID, 10), strconv.FormatInt(userID, 10), rr.Username,
			"", "", "", strconv.FormatFloat(quotaUSD, 'f', 4, 64),
			rr.Key, rr.Name, "", "used", "success", "", "",
			formatUnixRFC3339(rr.RedeemedTime), formatUnixRFC3339(rr.RedeemedTime),
			"兑换码兑换额度，已从实付统计中剔除",
		}); err != nil {
			return usernameHint, err
		}
	}
	return usernameHint, redRows.Err()
}

// writeUserIncomeFooter 统计摘要（空行分隔，Excel 可一眼看到）。金额一律美元。
func writeUserIncomeFooter(csvW *csv.Writer, userID int64, username string, t userIncomeCSVTotals, params ListTopUpParams) {
	// 未成功数不在导出行里累计（导出可能带状态筛选），按同一用户与日期二次汇总。
	var unsuccessCount int64
	var unsuccessUSD float64
	if summary, err := GetUserQuotaIncomeSummary(userID, params.StartDate, params.EndDate); err == nil {
		unsuccessCount, unsuccessUSD = summary.UnsuccessCount, summary.UnsuccessMoneyUSD
	}
	lines := [][]string{
		{},
		{"===== 统计摘要 ====="},
		{"用户ID", strconv.FormatInt(userID, 10)},
		{"用户名", username},
		{"成功充值笔数", strconv.FormatInt(t.paidCount, 10)},
		{"实付合计(折合美元)", formatMoney2(t.paidUSD)},
		{"其中人民币原币(CNY)", formatMoney2(t.cnyMoney)},
		{"其中美元原币(USD)", formatMoney2(t.usdMoney)},
		{"币种未知笔数(不计入实付合计)", strconv.FormatInt(t.unknownCount, 10)},
		{"币种未知原币金额", formatMoney2(t.unknownMoney)},
		{"在线充值入账额度(美元)", formatMoney2(t.creditedUSD)},
		{"未成功充值笔数", strconv.FormatInt(unsuccessCount, 10)},
		{"未成功金额(折合美元)", formatMoney2(unsuccessUSD)},
		{"兑换码使用笔数", strconv.FormatInt(t.redCount, 10)},
		{"兑换码入账额度(美元)", strconv.FormatFloat(t.redQuotaUSD, 'f', 4, 64)},
		// 兼容旧字段名：剔除兑换后仅含在线充值成功单的入账额度
		{"剔除兑换码后实付额度(USD)", formatMoney2(t.creditedUSD)},
		{"含兑换总入账额度(美元)", strconv.FormatFloat(t.creditedUSD+t.redQuotaUSD, 'f', 4, 64)},
		{"折算汇率", cnyRateNote()},
		{"说明", "原币金额=按订单原币种实际支付；折合美元=人民币按上行汇率折算；入账额度=入账的美元额度；未成功=待处理+审核中+已过期；兑换码行不计入实付；币种未知的成功单计入笔数与入账额度，不计入实付合计"},
	}
	for _, line := range lines {
		_ = csvW.Write(line)
	}
}

// cnyRateNote is the conversion caption shown next to every converted total.
func cnyRateNote() string {
	return fmt.Sprintf("人民币按 ¥%s=$1 折算", strconv.FormatFloat(cnyPerUSD(), 'f', -1, 64))
}

func formatUnixRFC3339(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format(time.RFC3339)
}
