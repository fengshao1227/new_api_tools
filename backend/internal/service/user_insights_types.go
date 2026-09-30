package service

// UserInsightsWindow bounds recorded activity to [StartTime, EndTime), in UTC.
// AllTime means all retained records, not a promise that no logs were deleted.
type UserInsightsWindow struct {
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
	Model     string `json:"model"`
	AllTime   bool   `json:"all_time"`
}

type UserInsightsParams struct {
	UserID   int64
	Window   UserInsightsWindow
	Page     int
	PageSize int
	Type     string
	Status   string
}

type UserInsightsProfile struct {
	ID                int64          `json:"id"`
	Username          string         `json:"username"`
	DisplayName       string         `json:"display_name"`
	Email             string         `json:"email"`
	Status            int64          `json:"status"`
	Role              int64          `json:"role"`
	Group             string         `json:"group"`
	Remark            string         `json:"remark"`
	CreatedAt         *int64         `json:"created_at"`
	LastLoginAt       *int64         `json:"last_login_at"`
	SignupCountry     *string        `json:"signup_country"`
	SignupLanguage    *string        `json:"signup_language"`
	AcquisitionSource *string        `json:"acquisition_source"`
	AcquisitionDetail *string        `json:"acquisition_detail"`
	LoginSources      []string       `json:"login_sources"`
	InviterID         *int64         `json:"inviter_id"`
	Paid              *bool          `json:"paid"`
	PaidVia           *string        `json:"paid_via"`
	Risk              *userRiskBrief `json:"risk"`
}

type UserInsightsBalances struct {
	Quota           int64   `json:"quota"`
	UsedQuota       int64   `json:"used_quota"`
	TopupQuota      *int64  `json:"topup_quota"`
	GrantedQuota    *int64  `json:"granted_quota"`
	BalanceUSD      float64 `json:"balance_usd"`
	LifetimeUsedUSD float64 `json:"lifetime_used_usd"`
}

type UserInsightsCurrency struct {
	Currency string  `json:"currency"` // unknown remains separate, never added to USD.
	Count    int64   `json:"count"`
	Amount   float64 `json:"amount"`
}

type UserInsightsPaymentTotals struct {
	PaidCount            int64                  `json:"paid_count"`
	PaidUSD              float64                `json:"paid_usd"` // Known currencies only; original amounts below.
	CreditedUSD          *float64               `json:"credited_usd"`
	UnknownCurrencyCount int64                  `json:"unknown_currency_count"`
	ByCurrency           []UserInsightsCurrency `json:"by_currency"`
}

type UserInsightsPayments struct {
	Window      UserInsightsPaymentTotals `json:"window"`
	Lifetime    UserInsightsPaymentTotals `json:"lifetime"`
	FirstPaidAt *int64                    `json:"first_paid_at"`
	LastPaidAt  *int64                    `json:"last_paid_at"`
	CNYPerUSD   float64                   `json:"cny_per_usd"`
}

type UserInsightsActivity struct {
	FirstRecordAt *int64  `json:"first_record_at"`
	LastRecordAt  *int64  `json:"last_record_at"`
	ActiveDays    int64   `json:"active_days"`
	LastModel     *string `json:"last_model"`
}

// Records are ledger entries, not deduplicated customer calls. A retry writes
// an error per attempt and a task adjustment can write another consume entry.
type UserInsightsMetrics struct {
	BillingRecords       int64   `json:"billing_records"`
	ErrorRecords         int64   `json:"error_records"`
	RefundRecords        int64   `json:"refund_records"`
	ChargedQuota         int64   `json:"charged_quota"`
	ChargedUSD           float64 `json:"charged_usd"`
	RefundQuota          int64   `json:"refund_quota"`
	RefundUSD            float64 `json:"refund_usd"`
	RawPromptTokens      int64   `json:"raw_prompt_tokens"`
	InputTokens          int64   `json:"input_tokens"`
	OutputTokens         int64   `json:"output_tokens"`
	CacheReadTokens      *int64  `json:"cache_read_tokens"`
	CacheWriteTokens     *int64  `json:"cache_write_tokens"`
	CacheReadRecords     int64   `json:"cache_read_records"`
	CacheWriteRecords    int64   `json:"cache_write_records"`
	ReasoningTokens      *int64  `json:"reasoning_tokens"`
	ModelsCount          int64   `json:"models_count"`
	TokenDetailsRecorded int64   `json:"token_details_recorded"`
}

type UserInsightsModel struct {
	ModelName string `json:"model_name"`
	UserInsightsMetrics
	LastRecordAt *int64 `json:"last_record_at"`
}

type UserInsightsDay struct {
	Date string `json:"date"` // UTC YYYY-MM-DD.
	UserInsightsMetrics
}

type UserInsightsTaskTotals struct {
	Total      int64 `json:"total"`
	Success    int64 `json:"success"`
	Failed     int64 `json:"failed"`
	InProgress int64 `json:"in_progress"`
}

type UserInsightsAvailability struct {
	Profile         bool     `json:"profile"`
	Logs            bool     `json:"logs"`
	Tasks           bool     `json:"tasks"`
	Payments        bool     `json:"payments"`
	Risk            bool     `json:"risk"`
	Acquisition     bool     `json:"acquisition"`
	TokenDetails    bool     `json:"token_details"`
	ReasoningTokens bool     `json:"reasoning_tokens"`
	Warnings        []string `json:"warnings"`
}

type UserInsightsReport struct {
	Window       UserInsightsWindow       `json:"window"`
	Profile      UserInsightsProfile      `json:"profile"`
	Balances     UserInsightsBalances     `json:"balances"`
	Payments     *UserInsightsPayments    `json:"payments"`
	Activity     *UserInsightsActivity    `json:"activity"`
	Summary      *UserInsightsMetrics     `json:"summary"`
	Models       []UserInsightsModel      `json:"models"`
	Daily        []UserInsightsDay        `json:"daily"`
	Tasks        *UserInsightsTaskTotals  `json:"tasks"`
	Availability UserInsightsAvailability `json:"availability"`
}

type UserInsightsLog struct {
	ID               int64    `json:"id"`
	CreatedAt        int64    `json:"created_at"`
	Type             int64    `json:"type"`
	ModelName        string   `json:"model_name"`
	RequestID        *string  `json:"request_id"`
	TokenID          int64    `json:"token_id"`
	TokenName        string   `json:"token_name"`
	ChannelID        int64    `json:"channel_id"`
	PromptTokens     int64    `json:"prompt_tokens"`
	InputTokens      int64    `json:"input_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CacheReadTokens  *int64   `json:"cache_read_tokens"`
	CacheWriteTokens *int64   `json:"cache_write_tokens"`
	ReasoningTokens  *int64   `json:"reasoning_tokens"`
	Quota            int64    `json:"quota"`
	QuotaUSD         float64  `json:"quota_usd"`
	Cost             *int64   `json:"cost"`
	CostUSD          *float64 `json:"cost_usd"`
	UseTime          int64    `json:"use_time"`
	IsStream         bool     `json:"is_stream"`
	IP               string   `json:"ip"`
	Content          string   `json:"content"` // Safe diagnostic category; never raw provider text.
	TaskID           *string  `json:"task_id"`
	IsTask           bool     `json:"is_task"`
	VoidedQuota      *int64   `json:"voided_quota"`
}

type UserInsightsTask struct {
	ID         int64   `json:"id"`
	TaskID     string  `json:"task_id"`
	Platform   string  `json:"platform"`
	ModelName  *string `json:"model_name"`
	Action     string  `json:"action"`
	Status     string  `json:"status"`
	Progress   string  `json:"progress"`
	SubmitTime int64   `json:"submit_time"`
	StartTime  int64   `json:"start_time"`
	FinishTime int64   `json:"finish_time"`
	Quota      int64   `json:"quota"`
	QuotaUSD   float64 `json:"quota_usd"`
	FailReason string  `json:"fail_reason"` // Safe failure category only.
}

type UserInsightsPage[T any] struct {
	Items     []T      `json:"items"`
	Page      int      `json:"page"`
	PageSize  int      `json:"page_size"`
	Total     int64    `json:"total"`
	Available bool     `json:"available"`
	Warnings  []string `json:"warnings"`
}
