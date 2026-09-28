package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Whose failure is it? Every error rate in this tool — channel health, model
// status, async task health — answers that question the way the gateway does,
// so a channel the gateway considers healthy is not painted red here.
//
// The rules are ported from the gateway (new-api, Beat fork):
//
//   - service/customer_failure.go  PresentFailure / IsCustomerSideFailure:
//     which failures are the caller's own doing (moderation, a parameter the
//     model refuses, an input URL that cannot be fetched, their own balance,
//     hanging up before the answer). Success rates leave these out of both
//     the numerator and the denominator.
//   - service/upstream_fault.go    IsUpstreamAccountFault: our account with a
//     supplier (no credit, revoked key) — a supplier-side failure.
//   - service/ops_alert_patrol.go  opsChannelTallies: the two per-attempt
//     exceptions — a parameter refusal counts against the channel when another
//     channel accepted the same request, and a capped channel answering 429 is
//     the task queue working, not a failure.
//
// Only the classification is ported, not the customer-facing sentences. When
// the gateway's word lists change, change these with them; the test table is
// the gateway's own production samples.

// FailureSide says who a failure is counted against.
type FailureSide string

const (
	// FailureSideChannel counts against the channel / upstream: 5xx, timeouts,
	// empty answers, supplier rate limits, our account with the supplier.
	FailureSideChannel FailureSide = "channel"
	// FailureSideUser is the caller's own doing. Shown on its own, never in
	// an error rate.
	FailureSideUser FailureSide = "user"
	// FailureSideQueue is a capped channel saying it is full: the gateway's
	// task queue working as designed. Shown, not counted.
	FailureSideQueue FailureSide = "queue"
)

// Failure categories. User side first, then channel side, then the queue.
const (
	FailureCatContentPolicy       = "content_policy"
	FailureCatInvalidRequest      = "invalid_request"
	FailureCatInputUnreachable    = "input_unreachable"
	FailureCatInsufficientBalance = "insufficient_balance"
	FailureCatClientClosed        = "client_closed"

	FailureCatUpstreamAccount  = "upstream_account"
	FailureCatModelUnavailable = "model_unavailable"
	FailureCatRateLimited      = "rate_limited"
	FailureCatTimeout          = "timeout"
	FailureCatUpstreamError    = "upstream_error"
	FailureCatEmptyResult      = "empty_result"
	FailureCatParamRejected    = "param_rejected"
	FailureCatOther            = "other"

	FailureCatQueueFull = "queue_full"
)

// FailureClass is one stored failure, attributed.
type FailureClass struct {
	Side     FailureSide `json:"side"`
	Category string      `json:"category"`
}

// Counted reports whether the failure belongs in a channel's error rate.
func (c FailureClass) Counted() bool { return c.Side == FailureSideChannel }

func channelFailure(category string) FailureClass {
	return FailureClass{Side: FailureSideChannel, Category: category}
}

func userFailure(category string) FailureClass {
	return FailureClass{Side: FailureSideUser, Category: category}
}

// Sentences the gateway writes itself into logs and task rows.
const (
	gatewayUnavailableMessage       = "This model is temporarily unavailable on our side. Retry in a few minutes or use another model; this request was not charged."
	gatewayLegacyBusyMessage        = "The service is temporarily busy. Please try again later."
	gatewayLegacyUnavailableMessage = "This model is temporarily unavailable. Please try again later."
	gatewayClientClosedText         = "client closed the connection before the model responded"
	gatewayLocalBalancePrefix       = "预扣费额度失败, 用户剩余额度"
)

var (
	failStatusPrefix = regexp.MustCompile(`^status_code=(\d{3}),\s*`)
	failPluginPrefix = regexp.MustCompile(`(?i)^plugin \S+ hook \S+ failed:\s*(?:error:\s*)?`)
	failStackTrace   = regexp.MustCompile(`\s+at\s+\S+\s*\(.*$`)
	failPhasePrefix  = regexp.MustCompile(`(?i)^(?:(?:submit|generation|analysis|request) failed:\s*)+`)
	failURL          = regexp.MustCompile(`(?i)https?://\S+`)
	failTrailingID   = regexp.MustCompile(`(?i)\s*\((?:req[_-]?)?[0-9a-f_-]{4,}\)$`)
	failTraceID      = regexp.MustCompile(`(?i)\s*[(\[]?request[ _-]?id[:=]?\s*[0-9a-z_.-]+[)\]]?[.\s]*$`)
	failHan          = regexp.MustCompile(`\p{Han}`)

	// Matched against lower-cased text, as in the gateway.
	failAccountFault  = regexp.MustCompile(`积分不足|未配置生成账号|无法接单|invalid token|all credentials|cooling down|configured account|account .*(?:suspend|disabled|banned)|cannot authenticate|authentication_error|incorrect api key|invalid api key`)
	failContentPolicy = regexp.MustCompile(`content[ _-]?(?:polic|filter|moderat)|safety|moderation|risk.?control|nsfw|sensitive|censor|privacy|prohibited|安全审核|内容审核|内容政策|违规内容|敏感`)
	failContent       = regexp.MustCompile(`unsafe|inappropriate|flagged|safety|安全政策|审核|real person|copyright|policyviolation|privacyinformation|violat|content-moderated|content review|nsfw`)
	failUnreachable   = regexp.MustCompile(`cannot download|could not be fetched|failed to download|refusing to fetch|dns resolution|no such host|returned status 4\d\d|无法下载|下载失败`)
	failUnusable      = regexp.MustCompile(`asset processing failed|unable to process input|unsupported (?:image|media|file)|cannot decode|解码失败`)
	failCloudflare    = regexp.MustCompile(`cloudflare|origin web server`)
	failTimeout       = regexp.MustCompile(`timeout|timed out|time-out|did not complete|still in progress|deadline|超时`)
	failNoModel       = regexp.MustCompile(`no available channel|model_not_found|not supported by any configured`)
	failRateLimit     = regexp.MustCompile(`rate.?limit|too many|concurrent .*limit|并发`)
	failNotStarted    = regexp.MustCompile(`任务创建失败|切换其他线路|切换线路|重新提交|upstream_create_failed`)
	failEmptyResult   = regexp.MustCompile(`empty (?:text )?(?:output|result|response)|without content|no text completion|returned no (?:image|video|result|output)|缺少有效结果`)
	failNotSaved      = regexp.MustCompile(`failed to store the generated|could not store the generated`)
	failBusy          = regexp.MustCompile(`temporarily|try again later|retry later|please retry|busy|繁忙|请重试|稍后|切换其他线路|unavailable|internal.?(?:server|service)?.?(?:error|failure)`)
	failPricing       = regexp.MustCompile(`expr run error|invalid operation: <nil>`)
	failParam         = regexp.MustCompile(`must be|should be|must not|is required|field required|this field|not valid|invalid|out of range|within the specified range|greater than|less than|at least|at most|does not accept|does not support|do not support|not supported|unsupported|exceed|too long|too short|too simple|in detail|不支持|不能超过|不能为空|必须|格式|范围|档位|尺寸|参数|temporarily blocked|modify your prompt`)
	failSupplierWords = regexp.MustCompile(`(?i)\b(?:channel|distributor|credential|upstream|provider)\b|\b(?:another|other|current|this) (?:line|route)\b|渠道|上游|to support\b|report in\b`)
	// Our standing with a supplier inside a 4xx sentence ("This API key does
	// not have access…", "Your organization must be verified…").
	failSupplierAccount = regexp.MustCompile(`\baccounts?\b|organi[sz]ations?\b|\borg[-_][a-z0-9]{2,}|api[ _-]?keys?\b|\b(?:secret|access|bearer)[ _-]?(?:key|token)s?\b|\bsk-[a-z0-9_-]{6,}|(?:\b|_)quotas?(?:\b|_)|plans? &(?:amp;)? billing|billing (?:details|hard limit|account|issue|problem)|billing_not_active|credit balance|balance is too low|insufficient (?:funds|credits?|quota)|purchase (?:more )?credits|out of credits|top up your|\bsubscriptions?\b|payment required|past due|\bunpaid\b|account_deactivated|invalid_api_key|organization_(?:restricted|deactivated)|access_terminated|账号|账户|密钥|配额|欠费|套餐|订阅|组织`)
	failListWords       = strings.NewReplacer("、", ", ", " 或 ", ", or ")
)

// Chinese sentences the gateway translates and passes on as parameter advice.
var failTranslations = []struct {
	pattern *regexp.Regexp
	english string
}{
	{regexp.MustCompile(`^当前(?:线路|模型)不支持该画幅比例$`), "This model does not support the requested aspect ratio."},
	{regexp.MustCompile(`^prompt 不能超过 (\d+) 个字符$`), "prompt must be at most $1 characters."},
	{regexp.MustCompile(`^(\S+) prompt 最长 (\d+) 字节（UTF-8），当前 (\d+) 字节$`), "$1 prompt must be at most $2 bytes (UTF-8); this one is $3 bytes."},
	{regexp.MustCompile(`^模型 (\S+) 属于 (\S+) 档，size 必须是该档位允许尺寸$`), "$1 is a $2-tier model: size must be one of the sizes allowed for that tier."},
	{regexp.MustCompile(`^文生视频模式不支持传入参考素材$`), "Text-to-video does not accept reference media. Remove the reference inputs, or use image-to-video."},
	{regexp.MustCompile(`^aspect_ratio 必须是 (.+)$`), "aspect_ratio must be one of $1."},
}

// upstreamAccountKeywords is the gateway's upstreamAccountFaultKeywords plus
// its automatic-disable list as configured in production (options row
// AutomaticDisableKeywords, 2026-09-28), lower-cased.
var upstreamAccountKeywords = []string{
	"预扣费额度失败", "额度不足", "余额不足", "积分不足",
	"insufficient_user_quota", "insufficient user quota", "insufficient quota",
	"insufficient credits", "insufficient balance", "insufficient funds",
	"credit balance is too low", "exceeded your current quota", "quota exceeded",
	"billing details", "billing hard limit", "billing_not_active", "payment required",
	"creditinsufficient", "account is not authorized", "organization has been disabled",
	// AutomaticDisableKeywords
	"your credit balance is too low", "this organization has been disabled.",
	"you exceeded your current quota", "permission denied",
	"the security token included in the request is invalid", "operation not allowed",
	"your account is not authorized",
}

// ClassifyFailure attributes one stored failure: an error log's content
// ("status_code=NNN, <upstream text>") or a task's fail_reason. It is the
// gateway's PresentFailure reduced to its verdict. An empty text is an
// unexplained failure and counts against the channel.
func ClassifyFailure(raw string) FailureClass {
	text := strings.TrimSpace(raw)
	status := 0
	if match := failStatusPrefix.FindStringSubmatch(text); match != nil {
		status, _ = strconv.Atoi(match[1])
		text = strings.TrimSpace(text[len(match[0]):])
	}
	switch text {
	case gatewayUnavailableMessage, gatewayLegacyBusyMessage, gatewayLegacyUnavailableMessage:
		return channelFailure(FailureCatModelUnavailable)
	case gatewayClientClosedText:
		return userFailure(FailureCatClientClosed)
	}
	wrapped := strings.HasPrefix(text, "{")
	message, upstreamCode := unwrapFailureText(text)
	message = cleanFailureText(message)
	lower := strings.ToLower(message + " " + upstreamCode + " " + text)
	localBalance := strings.HasPrefix(message, gatewayLocalBalancePrefix)

	switch {
	case failAccountFault.MatchString(lower) || isUpstreamAccountFault(status, lower) && (wrapped || !localBalance):
		// A supplier's empty wallet reads like the caller's own; only an
		// unwrapped gateway sentence is the caller's.
		return channelFailure(FailureCatUpstreamAccount)
	case localBalance:
		return userFailure(FailureCatInsufficientBalance)
	case failContentPolicy.MatchString(lower) || failContent.MatchString(lower):
		return userFailure(FailureCatContentPolicy)
	case failUnreachable.MatchString(lower) || failUnusable.MatchString(lower):
		return userFailure(FailureCatInputUnreachable)
	case failPricing.MatchString(lower):
		return userFailure(FailureCatInvalidRequest)
	case failNoModel.MatchString(lower):
		return channelFailure(FailureCatModelUnavailable)
	case status == 408 || status == 504 || status == 524 || failTimeout.MatchString(lower):
		return channelFailure(FailureCatTimeout)
	case failNotStarted.MatchString(lower) || failCloudflare.MatchString(lower):
		return channelFailure(FailureCatUpstreamError)
	case status == 429 || failRateLimit.MatchString(lower):
		return channelFailure(FailureCatRateLimited)
	case failEmptyResult.MatchString(lower):
		return channelFailure(FailureCatEmptyResult)
	case failNotSaved.MatchString(lower):
		return channelFailure(FailureCatUpstreamError)
	case status < 500 && failSupplierAccount.MatchString(strings.ToLower(message+" "+upstreamCode)):
		return channelFailure(FailureCatUpstreamAccount)
	case passableParamText(status, message):
		return passedOnParamFailure(message)
	case status >= 500 || failBusy.MatchString(lower):
		return channelFailure(FailureCatUpstreamError)
	default:
		return channelFailure(FailureCatOther)
	}
}

// AttributeAttempt applies the gateway's two per-attempt exceptions to one
// attempt on one channel (ops_alert_patrol.go opsChannelTallies):
//
//   - a parameter refusal for a request another channel accepted proves the
//     parameters were fine: the refusing channel is set up wrong. Moderation
//     is never overruled — suppliers moderate with different strictness.
//   - a rate limit from a channel with max_concurrent_tasks is the queue.
func AttributeAttempt(class FailureClass, acceptedElsewhere, cappedChannel bool) FailureClass {
	switch {
	case class.Category == FailureCatInvalidRequest && acceptedElsewhere:
		return channelFailure(FailureCatParamRejected)
	case class.Category == FailureCatRateLimited && cappedChannel:
		return FailureClass{Side: FailureSideQueue, Category: FailureCatQueueFull}
	}
	return class
}

func isUpstreamAccountFault(status int, text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || status == 402 {
		return status == 402
	}
	for _, keyword := range upstreamAccountKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// passableParamText is the gateway's passableParamMessage: a sentence about
// the request itself, under a status that describes the request, or one that
// says what to change.
func passableParamText(status int, message string) bool {
	if len([]rune(message)) < 8 || failSupplierWords.MatchString(message) {
		return false
	}
	switch status {
	case 400, 413, 415, 422:
		return true
	case 0:
		return failParam.MatchString(strings.ToLower(message))
	default:
		return status >= 500 && failParam.MatchString(strings.ToLower(message))
	}
}

// passedOnParamFailure is the gateway's passedOnFailure verdict: a Chinese
// sentence it has no translation for and that only says "retry later" is a
// temporary error; anything else is a parameter the model refused.
func passedOnParamFailure(message string) FailureClass {
	if !failHan.MatchString(message) {
		return userFailure(FailureCatInvalidRequest)
	}
	sentence := strings.TrimSuffix(strings.TrimSpace(message), "。")
	for _, translation := range failTranslations {
		if !translation.pattern.MatchString(sentence) {
			continue
		}
		english := failListWords.Replace(translation.pattern.ReplaceAllString(sentence, translation.english))
		if !failHan.MatchString(english) {
			return userFailure(FailureCatInvalidRequest)
		}
	}
	if failBusy.MatchString(sentence) {
		return channelFailure(FailureCatUpstreamError)
	}
	return userFailure(FailureCatInvalidRequest)
}

// cleanFailureText strips what describes machinery rather than the problem.
func cleanFailureText(message string) string {
	message = strings.TrimSpace(message)
	message = failPluginPrefix.ReplaceAllString(message, "")
	message = failStackTrace.ReplaceAllString(message, "")
	message = failPhasePrefix.ReplaceAllString(message, "")
	message = failTraceID.ReplaceAllString(message, "")
	message = failTrailingID.ReplaceAllString(message, "")
	message = failURL.ReplaceAllString(message, "")
	message = strings.Join(strings.Fields(message), " ")
	if r := []rune(message); len(r) > 300 {
		message = string(r[:300]) + "…"
	}
	return strings.TrimSpace(message)
}

// unwrapFailureText digs the sentence and the upstream's own code out of an
// error body, through up to three layers of resellers wrapping each other.
func unwrapFailureText(text string) (message, upstreamCode string) {
	message, upstreamCode = rejectionMessage(text)
	for range 3 {
		inner := strings.TrimSpace(failTraceID.ReplaceAllString(strings.TrimSpace(message), ""))
		if !strings.HasPrefix(inner, "{") {
			return message, upstreamCode
		}
		next, nextCode := rejectionFields(inner)
		if detail := detailText(inner); detail != "" {
			next = detail
		}
		if strings.TrimSpace(next) == "" || strings.TrimSpace(next) == inner {
			break
		}
		message = next
		if nextCode != "" {
			upstreamCode = nextCode
		}
	}
	if strings.HasPrefix(strings.TrimSpace(message), "{") {
		return "", upstreamCode
	}
	return message, upstreamCode
}

func rejectionMessage(body string) (message, upstreamCode string) {
	message, upstreamCode = rejectionFields(body)
	if !strings.HasPrefix(strings.TrimSpace(message), "{") {
		return message, upstreamCode
	}
	inner, innerCode := rejectionFields(message)
	if strings.TrimSpace(inner) == "" {
		return message, upstreamCode
	}
	if innerCode != "" {
		upstreamCode = innerCode
	}
	return inner, upstreamCode
}

// generalErrorBody is the gateway's dto.GeneralErrorResponse.
type generalErrorBody struct {
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
	Msg      string          `json:"msg"`
	Err      string          `json:"err"`
	ErrorMsg string          `json:"error_msg"`
	Detail   string          `json:"detail,omitempty"`
	Header   struct {
		Message string `json:"message"`
	} `json:"header"`
	Response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
}

// openAIErrorBody is the gateway's types.OpenAIError. Type and Param are
// read only so a body with a wrongly typed one is rejected the same way.
type openAIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    any    `json:"code"`
}

func rejectionFields(body string) (message, upstreamCode string) {
	text := strings.TrimSpace(body)
	if text == "" {
		return "", ""
	}
	var parsed generalErrorBody
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		if strings.HasPrefix(text, "<") {
			return "", ""
		}
		return text, ""
	}
	if len(parsed.Error) > 0 {
		var oai openAIErrorBody
		if json.Unmarshal(parsed.Error, &oai) == nil && oai.Message != "" {
			if oai.Code != nil {
				upstreamCode = fmt.Sprintf("%v", oai.Code)
			}
			return oai.Message, upstreamCode
		}
	}
	return parsed.message(), ""
}

func (e generalErrorBody) message() string {
	if raw := strings.TrimSpace(string(e.Error)); raw != "" {
		switch raw[0] {
		case '{':
			var oai openAIErrorBody
			if json.Unmarshal(e.Error, &oai) == nil && oai.Message != "" {
				return oai.Message
			}
		case '"':
			var msg string
			if json.Unmarshal(e.Error, &msg) == nil && msg != "" {
				return msg
			}
		default:
			return string(e.Error)
		}
	}
	for _, candidate := range []string{e.Message, e.Msg, e.Err, e.ErrorMsg, e.Detail, e.Header.Message, e.Response.Error.Message} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// detailText reads the {"detail": …} shape some Python upstreams answer in.
func detailText(text string) string {
	var body struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		return ""
	}
	switch detail := body.Detail.(type) {
	case string:
		return detail
	case map[string]any:
		message, _ := detail["message"].(string)
		return message
	}
	return ""
}
