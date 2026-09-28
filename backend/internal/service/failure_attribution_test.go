package service

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Every raw text is a failure the gateway stored in production (logs.content
// of an error line, or tasks.fail_reason). The first block is the gateway's own
// fixture table (service/customer_failure_test.go), whose verdicts this port
// must reproduce; the second is what drove the channel monitor red in 09-2026.
func TestClassifyFailureAgreesWithTheGateway(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		side     FailureSide
		category string
	}{
		// gateway fixtures
		{"cloudflare timeout json", `{"error":{"message":"The origin web server did not return a complete response within the 120-second Proxy Read Timeout window. The connection was established, but the origin took too long to respond.","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}`, FailureSideChannel, FailureCatTimeout},
		{"cloudflare problem document", `{"type":"https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-524/","title":"Error 524: A timeout occurred","status":524,"detail":"The origin web server did not return a complete response"}`, FailureSideChannel, FailureCatTimeout},
		{"deferred dispatch hang", "upstream dispatch did not complete", FailureSideChannel, FailureCatTimeout},
		{"supplier out of credit in a plugin stack", "status_code=502, plugin aistarslab@1.8.0 hook parseSubmitResponse failed: Error: 积分不足 at payloadOf (aistarslab.js:524:11(38))", FailureSideChannel, FailureCatUpstreamAccount},
		{"supplier token revoked", `{"error":{"code":"","message":"Invalid token (request id: 20260921abc)","type":"new_api_error"}}`, FailureSideChannel, FailureCatUpstreamAccount},
		{"supplier balance wrapped by a reseller", `status_code=403, {"code":"fail_to_fetch_task","message":"{\"code\":\"insufficient_user_quota\",\"message\":\"预扣费额度失败, 用户剩余额度: ＄0.05, 需要预扣费额度: ＄0.10\",\"data\":null}","data":null}`, FailureSideChannel, FailureCatUpstreamAccount},
		{"our own balance refusal", "status_code=403, 预扣费额度失败, 用户剩余额度: ¥1.20, 需要预扣费额度: ¥1.80 (request id 2026)", FailureSideUser, FailureCatInsufficientBalance},
		{"supplier has no generation account", "服务端未配置生成账号，当前无法接单，请联系管理员。", FailureSideChannel, FailureCatUpstreamAccount},
		{"line cannot take this aspect ratio", "status_code=502, plugin aistarslab@1.8.0 hook parseSubmitResponse failed: Error: 当前线路不支持该画幅比例 at payloadOf (aistarslab.js:524:11(38))", FailureSideUser, FailureCatInvalidRequest},
		{"real person in an input image", `status_code=400, {"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"The request failed because the input image 'content[1]' may contain real person. Request id: 0217899858","param":"","type":"BadRequest"}}`, FailureSideUser, FailureCatContentPolicy},
		{"public figure", "Request blocked: The input content was flagged for containing a prominent public figure.", FailureSideUser, FailureCatContentPolicy},
		{"copyright", `status_code=400, {"error":{"code":"InputImageSensitiveContentDetected.PolicyViolation","message":"The request failed because the input image 'content[2]' 'content[3]' may be related to copyright restrictions."}}`, FailureSideUser, FailureCatContentPolicy},
		{"chinese moderation", "您的请求无法用于生成图像。该请求可能因安全政策被拦截，或不适合进行图像生成。", FailureSideUser, FailureCatContentPolicy},
		{"output moderation code", "mt_output_sensitive", FailureSideUser, FailureCatContentPolicy},
		{"generated image unsafe", "plugin gemini-image@2.6.0 hook parseSubmitResponse failed: Error: generation failed: The generated images appear to be unsafe. Try modifying the prompts or the seeds.", FailureSideUser, FailureCatContentPolicy},
		{"input url unreachable", "content[1].image_url: cannot download media URL", FailureSideUser, FailureCatInputUnreachable},
		{"input url 404", `status_code=500, fetching "https://cdn.example.com/a.png" returned status 404`, FailureSideUser, FailureCatInputUnreachable},
		{"input asset broken", "content[2].image_url: asset processing failed", FailureSideUser, FailureCatInputUnreachable},
		{"parameter with field", "duration: Input should be greater than or equal to 5", FailureSideUser, FailureCatInvalidRequest},
		{"prompt too simple", "Your prompt is too simple for image generation. Please describe the image you want to create in detail.", FailureSideUser, FailureCatInvalidRequest},
		{"parameter in chinese json", `status_code=400, {"type":"error","error":{"type":"bad_request_error","message":"prompt 不能超过 2000 个字符 (req_8d7f)","http_code":"400"},"request_id":"8d7f"}`, FailureSideUser, FailureCatInvalidRequest},
		{"prompt byte limit in chinese", "status_code=400, grok-imagine-video-1.5 prompt 最长 4096 字节（UTF-8），当前 4112 字节", FailureSideUser, FailureCatInvalidRequest},
		{"size tier in chinese", "模型 gpt-image-2.5 属于 1k 档，size 必须是该档位允许尺寸", FailureSideUser, FailureCatInvalidRequest},
		{"references on text-to-video in chinese", "status_code=400, 文生视频模式不支持传入参考素材", FailureSideUser, FailureCatInvalidRequest},
		{"aspect ratio list in chinese", "status_code=400, aspect_ratio 必须是 1:1、16:9、9:16、4:3、3:4、3:2 或 2:3", FailureSideUser, FailureCatInvalidRequest},
		{"vague chinese retry under 400", "status_code=400, 模型请求失败，请稍后重试或更换模型。", FailureSideChannel, FailureCatUpstreamError},
		{"media constraint", "content[5].audio_url: media duration must be between 2 and 15 seconds", FailureSideUser, FailureCatInvalidRequest},
		{"pricing parameter missing", `status_code=400, expr run error: invalid operation: <nil> * float64 (1:94)`, FailureSideUser, FailureCatInvalidRequest},
		{"no channel for the model", "status_code=503, No available channel for model grok-imagine-image-quality under group 【GPT】生图分组 (distributor) (20260921)", FailureSideChannel, FailureCatModelUnavailable},
		{"concurrency limit", `status_code=429, {"type":"error","error":{"type":"rate_limit_error","message":"concurrent video task limit reached, please retry later (3)","http_code":"429"}}`, FailureSideChannel, FailureCatRateLimited},
		{"supplier asks to retry or switch lines", "任务创建失败，请重试，或切换其他线路。", FailureSideChannel, FailureCatUpstreamError},
		{"supplier network busy, switch lines", "服务器网络繁忙，请重新提交或切换线路", FailureSideChannel, FailureCatUpstreamError},
		{"http2 stream reset", "status_code=500, stream error: stream ID 1; INTERNAL_ERROR; received from peer", FailureSideChannel, FailureCatUpstreamError},
		{"supplier billing outage", "status_code=503, Billing service temporarily unavailable. Please retry later.", FailureSideChannel, FailureCatUpstreamError},
		{"empty completion", "status_code=502, upstream returned empty text output", FailureSideChannel, FailureCatEmptyResult},
		{"video without a result", "视频生成响应缺少有效结果", FailureSideChannel, FailureCatEmptyResult},
		{"result not stored", "failed to store the generated media", FailureSideChannel, FailureCatUpstreamError},
		{"routing 404", `status_code=404, {"error":{"message":"Not Found","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}`, FailureSideChannel, FailureCatOther},
		{"bare word", "status_code=400, error", FailureSideChannel, FailureCatOther},
		{"opaque supplier code", "InternalServiceFailure", FailureSideChannel, FailureCatUpstreamError},
		{"already presented", gatewayUnavailableMessage, FailureSideChannel, FailureCatModelUnavailable},
		{"presented before 2026-09-22", "The service is temporarily busy. Please try again later.", FailureSideChannel, FailureCatModelUnavailable},
		{"three resellers deep", `status_code=400, {"code":"fail_to_fetch_task","message":"{\"code\":\"fail_to_fetch_task\",\"message\":\"{\\\"error\\\":{\\\"code\\\":\\\"ERR-F3C08956F3\\\",\\\"message\\\":\\\"kling-v3 supports quality=720p only\\\",\\\"type\\\":\\\"invalid_request_error\\\"}} (request id: 202609101813477352169478268d9d6ib6JNGwQ)\",\"data\":null}","data":null}`, FailureSideUser, FailureCatInvalidRequest},
		{"python detail pointing at the supplier's support", `status_code=400, {"detail":{"code":400,"request_id":"d5bdda29","message":"Request failed. Please retry. Check the docs below to verify your parameters, test with the default demo parameters, and send the response JSON to support. You won't be charged for this request."}}`, FailureSideChannel, FailureCatUpstreamError},
		{"cloudflare 502 page", "status_code=502, The origin web server returned an invalid or incomplete response to Cloudflare. This typically indicates the origin is overloaded or misconfigured.", FailureSideChannel, FailureCatUpstreamError},
		{"input image unusable", "status_code=400, Unable to process input image. Please retry or report in https://support.google/x", FailureSideUser, FailureCatInputUnreachable},

		// one per category the monitor has to get right
		{"parameter error", `status_code=400, {"error":{"message":"invalid size \"4096x4096\": total pixels must be at most 8294400; received 16777216. Supported: auto, WxH, or W,H","type":"invalid_request_error","param":"","code":null}}`, FailureSideUser, FailureCatInvalidRequest},
		{"image prompt refused", `status_code=403, {"error":{"message":"The request was rejected by prompt moderation. Please check that your prompt is a clear, policy-compliant image-generation request, not a single word, greeting, chat message.","type":"upstream_error","param":"","code":403}}`, FailureSideUser, FailureCatContentPolicy},
		{"video prompt refused", `status_code=400, {"error":{"code":"InputTextSensitiveContentDetected","message":"The request failed because the input text 'content[0]' may contain sensitive information. Request id: 0217900001","param":"","type":"BadRequest"}}`, FailureSideUser, FailureCatContentPolicy},
		{"image in an unsupported format", `status_code=400, {"error":{"message":"Provider API error: You uploaded an unsupported image. Please make sure your image has of one the following formats: ['png', 'jpeg', 'gif', 'webp'].","type":"upstream_error"}}`, FailureSideUser, FailureCatInputUnreachable},
		{"customer hung up", "status_code=499, client closed the connection before the model responded", FailureSideUser, FailureCatClientClosed},
		{"supplier rate limit", "status_code=429, Too many pending requests, please retry later", FailureSideChannel, FailureCatRateLimited},
		{"upstream 5xx", "status_code=500, upstream error: do request failed", FailureSideChannel, FailureCatUpstreamError},
		{"cloudflare 524", "status_code=524, bad response status code 524", FailureSideChannel, FailureCatTimeout},
		{"supplier key without access", `status_code=403, {"error":{"message":"This API key does not have access to model gpt-6-sol","type":"invalid_request_error","code":"model_not_available"}}`, FailureSideChannel, FailureCatUpstreamAccount},
		{"supplier forbids us", "status_code=403, RBAC: access denied", FailureSideChannel, FailureCatOther},
		{"no reason at all", "", FailureSideChannel, FailureCatOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFailure(tc.raw)
			if got.Side != tc.side || got.Category != tc.category {
				t.Fatalf("ClassifyFailure(%q) = %s/%s, want %s/%s", tc.raw, got.Side, got.Category, tc.side, tc.category)
			}
		})
	}
}

func TestAttributeAttemptAppliesTheGatewaysTwoExceptions(t *testing.T) {
	cases := []struct {
		name              string
		class             FailureClass
		acceptedElsewhere bool
		capped            bool
		want              FailureClass
	}{
		{"parameter refused, another channel took the request", userFailure(FailureCatInvalidRequest), true, false, channelFailure(FailureCatParamRejected)},
		{"parameter refused everywhere", userFailure(FailureCatInvalidRequest), false, false, userFailure(FailureCatInvalidRequest)},
		{"moderation is never overruled", userFailure(FailureCatContentPolicy), true, false, userFailure(FailureCatContentPolicy)},
		{"capped channel full", channelFailure(FailureCatRateLimited), false, true, FailureClass{Side: FailureSideQueue, Category: FailureCatQueueFull}},
		{"uncapped supplier rate limit", channelFailure(FailureCatRateLimited), false, false, channelFailure(FailureCatRateLimited)},
		{"capped channel timing out", channelFailure(FailureCatTimeout), false, true, channelFailure(FailureCatTimeout)},
	}
	for _, tc := range cases {
		if got := AttributeAttempt(tc.class, tc.acceptedElsewhere, tc.capped); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestHealthLevelNeedsFiveAttemptsAndUsesTheGatewayLines(t *testing.T) {
	cases := []struct {
		success, errors int64
		want            string
	}{
		{0, 0, HealthIdle},
		{0, 4, HealthIdle}, // four failures out of four: too few to call
		{1, 4, HealthRed},  // 80 %
		{4, 1, HealthYellow},
		{5, 1, HealthGreen},
		{80, 20, HealthYellow},
		{100, 19, HealthGreen},
	}
	for _, tc := range cases {
		if got := healthLevel(tc.success, tc.errors); got != tc.want {
			t.Errorf("healthLevel(%d ok, %d failed) = %s, want %s", tc.success, tc.errors, got, tc.want)
		}
	}
}

// monitorFixture builds the gateway tables the monitors read, in SQLite.
type monitorFixture struct {
	statements []string
	nextID     int
}

func newMonitorFixture() *monitorFixture {
	return &monitorFixture{statements: []string{
		`CREATE TABLE logs (id INTEGER PRIMARY KEY, created_at INTEGER, type INTEGER, channel_id INTEGER, model_name TEXT,
			request_id TEXT, username TEXT, content TEXT, other TEXT, completion_tokens INTEGER, use_time INTEGER)`,
		`CREATE TABLE channels (id INTEGER PRIMARY KEY, setting TEXT)`,
		`CREATE TABLE tasks (id INTEGER PRIMARY KEY, created_at INTEGER, channel_id INTEGER, platform TEXT, status TEXT,
			fail_reason TEXT, submit_time INTEGER, finish_time INTEGER, properties TEXT)`,
	}}
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// served adds n served requests (consume lines) of model on channel.
func (f *monitorFixture) served(at int64, channel int, model string, n int, requestID string) {
	for range n {
		f.nextID++
		rid := requestID
		if rid == "" {
			rid = fmt.Sprintf("ok-%d", f.nextID)
		}
		f.statements = append(f.statements, fmt.Sprintf(`INSERT INTO logs VALUES (%d, %d, 2, %d, %s, %s, 'u', '', '{}', 20, 4)`,
			f.nextID, at, channel, sqlQuote(model), sqlQuote(rid)))
	}
}

// receipt adds a task's receipt: a consume line that only says it was accepted.
func (f *monitorFixture) receipt(at int64, channel int, model string) {
	f.nextID++
	f.statements = append(f.statements, fmt.Sprintf(`INSERT INTO logs VALUES (%d, %d, 2, %d, %s, 'task-%d', 'u', '', '{"is_task":true}', 0, 0)`,
		f.nextID, at, channel, sqlQuote(model), f.nextID))
}

// failed adds one error line.
func (f *monitorFixture) failed(at int64, channel int, model, requestID, content string) {
	f.nextID++
	if requestID == "" {
		requestID = fmt.Sprintf("err-%d", f.nextID)
	}
	f.statements = append(f.statements, fmt.Sprintf(`INSERT INTO logs VALUES (%d, %d, 5, %d, %s, %s, 'u', %s, '{}', 0, 0)`,
		f.nextID, at, channel, sqlQuote(model), sqlQuote(requestID), sqlQuote(content)))
}

func (f *monitorFixture) task(at int64, channel int, model, status, reason string) {
	f.nextID++
	f.statements = append(f.statements, fmt.Sprintf(`INSERT INTO tasks VALUES (%d, %d, %d, 'p', %s, %s, %d, %d, %s)`,
		f.nextID, at-120, channel, sqlQuote(status), sqlQuote(reason), at-120, at, sqlQuote(`{"origin_model_name":"`+model+`"}`)))
}

const (
	textImageModeration = `status_code=403, {"error":{"message":"The request was rejected by prompt moderation. Please check that your prompt is a clear, policy-compliant image-generation request.","type":"upstream_error","param":"","code":403}}`
	textVideoModeration = `status_code=400, {"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"The request failed because the input image 'content[1]' may contain real person. Request id: 0217899858","param":"","type":"BadRequest"}}`
	textBadParameter    = `status_code=400, {"error":{"message":"invalid size \"4096x4096\": total pixels must be at most 8294400","type":"invalid_request_error"}}`
	textOwnBalance      = "status_code=403, 预扣费额度失败, 用户剩余额度: ¥1.20, 需要预扣费额度: ¥1.80"
	textHungUp          = "status_code=499, client closed the connection before the model responded"
	textBadImage        = "status_code=400, Provider API error: You uploaded an unsupported image. Please make sure your image has of one the following formats: png, jpeg."
	textUnsafeOutput    = "status_code=502, plugin gemini-image@2.7.0 hook parseSubmitResponse failed: Error: generation failed: The generated images appear to be unsafe. Try modifying the prompts or the seeds."
	textQueueFull       = `status_code=429, {"type":"error","error":{"type":"rate_limit_error","message":"concurrent video task limit reached, please retry later (3)","http_code":"429"}}`
	textSupplierBusy    = "status_code=429, Too many pending requests, please retry later"
	text5xx             = "status_code=500, upstream error: do request failed"
	textTimeout         = "status_code=524, bad response status code 524"
	textNoCredit        = "status_code=502, plugin aistarslab@1.8.0 hook parseSubmitResponse failed: Error: 积分不足 at payloadOf (aistarslab.js:524:11(38))"
	textEmpty           = "status_code=502, upstream returned empty text output"
)

func TestChannelHealthCountsOnlyChannelSideFailures(t *testing.T) {
	now := time.Now().Unix()
	at := now - 600
	f := newMonitorFixture()
	f.statements = append(f.statements, `INSERT INTO channels VALUES (3, '{"max_concurrent_tasks":4}')`, `INSERT INTO channels VALUES (4, '{"proxy":""}')`)
	// ch1: healthy, and every customer mistake in the book.
	f.served(at, 1, "img", 6, "")
	for _, text := range []string{textImageModeration, textVideoModeration, textBadParameter, textOwnBalance, textHungUp, textBadImage, textUnsafeOutput} {
		f.failed(at, 1, "img", "", text)
	}
	// ch2: nothing served, only customer mistakes — still not red.
	for range 6 {
		f.failed(at, 2, "img", "", textImageModeration)
	}
	// ch3: capped at 4 concurrent tasks and full five times: the queue.
	f.served(at, 3, "video", 6, "")
	for range 5 {
		f.failed(at, 3, "video", "", textQueueFull)
	}
	// ch4: a supplier that keeps saying it is busy.
	f.served(at, 4, "chat", 1, "")
	for range 5 {
		f.failed(at, 4, "chat", "", textSupplierBusy)
	}
	// ch5: 5xx, timeout, our empty wallet, an empty answer.
	f.served(at, 5, "chat", 2, "")
	for _, text := range []string{text5xx, textTimeout, textNoCredit, textEmpty} {
		f.failed(at, 5, "chat", "", text)
	}
	// ch6 refused a size that ch7 then served for the same request.
	f.failed(at, 6, "img", "req-x", textBadParameter)
	f.served(at+5, 7, "img", 1, "req-x")
	f.failed(at, 6, "img", "req-y", textBadParameter)
	f.served(at, 6, "img", 5, "")
	// ch8 runs tasks: receipts are not successes, tasks are.
	for range 9 {
		f.receipt(at, 8, "seedance")
	}
	for range 5 {
		f.task(at, 8, "seedance", "SUCCESS", "")
	}
	f.task(at, 8, "seedance", "FAILURE", "The request failed because the input image 'content[1]' may contain real person.")
	f.task(at, 8, "seedance", "FAILURE", "The request was rejected by prompt moderation.")
	f.task(at, 8, "seedance", "FAILURE", "Provider API error: You uploaded an unsupported image.")
	f.task(at, 8, "seedance", "FAILURE", "upstream dispatch did not complete")
	// ch9: one failure is not a verdict.
	f.failed(at, 9, "chat", "", text5xx)

	biz := newBusinessTestService(t, f.statements...)
	svc := &ChannelMonitorService{db: biz.db, logDB: biz.logDB}
	stats, err := svc.channelHealthStats(now-3600, now+1)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]ChannelHealthStat{}
	for _, s := range stats {
		byID[s.ChannelID] = s
	}
	want := []struct {
		channel                               int64
		level                                 string
		success, errors, userErrors, queueFul int64
	}{
		{1, HealthGreen, 6, 0, 7, 0},
		{2, HealthIdle, 0, 0, 6, 0},
		{3, HealthGreen, 6, 0, 0, 5},
		{4, HealthRed, 1, 5, 0, 0},
		{5, HealthYellow, 2, 4, 0, 0},
		{6, HealthGreen, 5, 1, 1, 0},
		{7, HealthIdle, 1, 0, 0, 0},
		{8, HealthGreen, 5, 1, 3, 0},
		{9, HealthIdle, 0, 1, 0, 0},
	}
	for _, w := range want {
		got, ok := byID[w.channel]
		if !ok {
			t.Fatalf("channel %d missing from %+v", w.channel, stats)
		}
		if got.Level != w.level || got.Success != w.success || got.Errors != w.errors || got.UserErrors != w.userErrors || got.QueueFull != w.queueFul {
			t.Errorf("channel %d = level %s, %d ok / %d errors / %d user / %d queue; want %s, %d / %d / %d / %d",
				w.channel, got.Level, got.Success, got.Errors, got.UserErrors, got.QueueFull, w.level, w.success, w.errors, w.userErrors, w.queueFul)
		}
	}
	if byID[1].ErrorRate != 0 || byID[1].Attempts != 13 {
		t.Errorf("customer mistakes leaked into ch1's rate: %+v", byID[1])
	}
	if c := byID[5].ChannelCategories; c[FailureCatUpstreamError] != 1 || c[FailureCatTimeout] != 1 || c[FailureCatUpstreamAccount] != 1 || c[FailureCatEmptyResult] != 1 {
		t.Errorf("ch5 categories = %v", c)
	}
	if byID[6].ChannelCategories[FailureCatParamRejected] != 1 || byID[6].UserCategories[FailureCatInvalidRequest] != 1 {
		t.Errorf("ch6 = %+v, want the refusal another channel overruled counted, the other one not", byID[6])
	}
	if byID[8].TaskFinished != 9 || byID[8].AvgTaskSeconds == nil || *byID[8].AvgTaskSeconds != 120 {
		t.Errorf("ch8 task outcomes = %+v", byID[8])
	}

	models, err := svc.modelHealthStats(now-3600, now+1)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ModelName == "seedance" && (m.SyncSuccess != 0 || m.EmptyCount != 0 || m.Success != 5 || m.Level != HealthGreen) {
			t.Errorf("task receipts read as empty answers: %+v", m)
		}
		if m.ModelName == "img" && (m.Errors != 1 || m.UserErrors != 14 || m.Success != 12) {
			t.Errorf("img model = %+v", m)
		}
	}

	analysis, err := svc.errorAnalysis(now-3600, now+1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Total != 30 || analysis.ChannelErrors != 11 || analysis.UserErrors != 14 || analysis.QueueFull != 5 || analysis.Sampled != 5 {
		t.Errorf("error analysis = total %d, channel %d, user %d, queue %d, sampled %d",
			analysis.Total, analysis.ChannelErrors, analysis.UserErrors, analysis.QueueFull, analysis.Sampled)
	}
	if analysis.UserCategories[FailureCatContentPolicy] != 9 || analysis.ChannelCategories[FailureCatRateLimited] != 5 {
		t.Errorf("error analysis categories = channel %v, user %v", analysis.ChannelCategories, analysis.UserCategories)
	}
}
