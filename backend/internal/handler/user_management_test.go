package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/auth"
	"github.com/new-api-tools/backend/internal/logger"
	"github.com/new-api-tools/backend/internal/service"
)

type gatewayCall struct {
	called bool
	userID int64
	action string
	reason string
}

func stubGatewayUser(t *testing.T, result error) *gatewayCall {
	t.Helper()
	call := &gatewayCall{}
	original := manageGatewayUser
	manageGatewayUser = func(_ context.Context, userID int64, action, reason string) error {
		call.called = true
		call.userID, call.action, call.reason = userID, action, reason
		return result
	}
	t.Cleanup(func() { manageGatewayUser = original })
	return call
}

func serveUserAction(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterUserManagementRoutes(r.Group("/api"))
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func responseMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	return body.Message
}

func TestBanUserRequiresReasonBeforeCallingGateway(t *testing.T) {
	call := stubGatewayUser(t, nil)

	rec := serveUserAction(t, "/api/users/5/ban", `{"reason":"   "}`)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "BAN_REASON_REQUIRED") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if call.called {
		t.Fatal("gateway must not be called without a reason")
	}
}

func TestBanUserRejectsOverlongReason(t *testing.T) {
	call := stubGatewayUser(t, nil)

	rec := serveUserAction(t, "/api/users/5/ban", `{"reason":"`+strings.Repeat("盗", maxBanReasonRunes+1)+`"}`)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "BAN_REASON_TOO_LONG") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if call.called {
		t.Fatal("gateway must not be called with an overlong reason")
	}
}

func TestBanUserSendsDisableWithTrimmedReason(t *testing.T) {
	call := stubGatewayUser(t, nil)

	rec := serveUserAction(t, "/api/users/5/ban", `{"reason":"  Chargeback on a stolen card  ","disable_tokens":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if call.userID != 5 || call.action != service.GatewayUserDisable || call.reason != "Chargeback on a stolen card" {
		t.Fatalf("unexpected gateway call: %#v", call)
	}
}

func TestBanUserPassesGatewayRefusalThrough(t *testing.T) {
	stubGatewayUser(t, &service.GatewayError{Message: "无法禁用超级管理员用户"})

	rec := serveUserAction(t, "/api/users/1/ban", `{"reason":"test"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if msg := responseMessage(t, rec); msg != "无法禁用超级管理员用户" {
		t.Fatalf("message = %q", msg)
	}
}

func TestBanUserReportsUnreachableGatewayAsBadGateway(t *testing.T) {
	stubGatewayUser(t, errors.New("请求网关用户管理接口失败: connection refused"))

	rec := serveUserAction(t, "/api/users/5/ban", `{"reason":"test"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestUnbanUserSendsEnable(t *testing.T) {
	call := stubGatewayUser(t, nil)

	rec := serveUserAction(t, "/api/users/9/unban", `{"enable_tokens":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if call.userID != 9 || call.action != service.GatewayUserEnable || call.reason != "" {
		t.Fatalf("unexpected gateway call: %#v", call)
	}
}

func TestRemovedUserDeletionRoutesAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterUserManagementRoutes(r.Group("/api"))

	for _, route := range []struct{ method, path string }{
		{http.MethodDelete, "/api/users/5"},
		{http.MethodPost, "/api/users/batch-delete"},
		{http.MethodGet, "/api/users/soft-deleted/count"},
		{http.MethodPost, "/api/users/soft-deleted/purge"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s should no longer be routed, got %d", route.method, route.path, rec.Code)
		}
	}
}

func TestUserInsightsRejectsInvalidFiltersBeforeQuery(t *testing.T) {
	for _, path := range []string{
		"/api/users/0/insights", "/api/users/not-a-user/insights",
		"/api/users/2/insights?start_time=-1", "/api/users/2/insights?end_time=abc",
		"/api/users/2/insights?start_time=100&end_time=100",
		"/api/users/2/insights?end_time=253402300800",
		"/api/users/2/insights?model=" + url.QueryEscape(strings.Repeat("模", 257)),
		"/api/users/2/insights?model=a%00b", "/api/users/2/insights?page=-1",
		"/api/users/2/insights?page=1000001", "/api/users/2/insights?page_size=101",
		"/api/users/2/insights?type=other", "/api/users/2/insights?status=unknown",
		"/api/users/2/insights?model=a&model=b", "/api/users/2/insights?start_time=",
	} {
		t.Run(path, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			called := false
			r.GET("/api/users/:user_id/insights", func(c *gin.Context) {
				serveUserInsights(c, func(context.Context, service.UserInsightsParams) (string, error) {
					called = true
					return "unexpected", nil
				})
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusBadRequest || called {
				t.Fatalf("invalid filter reached query: status=%d called=%v", rec.Code, called)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private query must not be cached")
			}
		})
	}
}

func TestUserInsightsKeepsPreciseWindowModelAndPagination(t *testing.T) {
	const now int64 = 1_800_000_000
	for _, tc := range []struct {
		query      string
		start, end int64
		all        bool
		model      string
		page, size int
	}{
		{"", now + 1 - 30*24*3600, now + 1, false, "", 1, 20},
		{"?start_time=0&end_time=500&model=claude-model&page=2&page_size=100", 0, 500, true, "claude-model", 2, 100},
		{"?start_time=100&end_time=200&model=model%2Fpreview", 100, 200, false, "model/preview", 1, 20},
	} {
		t.Run(tc.query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Params = gin.Params{{Key: "user_id", Value: "42"}}
			c.Request = httptest.NewRequest(http.MethodGet, "/insights"+tc.query, nil)
			p, err := parseUserInsightsParams(c, now)
			if err != nil {
				t.Fatal(err)
			}
			if p.UserID != 42 || p.Window.StartTime != tc.start || p.Window.EndTime != tc.end || p.Window.AllTime != tc.all || p.Window.Model != tc.model || p.Page != tc.page || p.PageSize != tc.size {
				t.Fatalf("wrong query projection: %+v", p)
			}
		})
	}
}

func TestUserInsightsErrorsDoNotLeakDatabaseDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrInsightsUserNotFound, http.StatusNotFound, "USER_NOT_FOUND"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "QUERY_TIMEOUT"},
		{context.Canceled, http.StatusRequestTimeout, "QUERY_CANCELLED"},
		{errors.New("private database diagnostic"), http.StatusServiceUnavailable, "QUERY_UNAVAILABLE"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := gin.New()
			r.GET("/api/users/:user_id/insights", func(c *gin.Context) {
				serveUserInsights(c, func(ctx context.Context, _ service.UserInsightsParams) (string, error) {
					if ctx != c.Request.Context() {
						t.Error("request cancellation context lost")
					}
					return "", tc.err
				})
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/2/insights", nil))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) || strings.Contains(rec.Body.String(), "private database") {
				t.Fatalf("incorrect error response: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUserInsightsRoutesRequireExistingAdminAuthentication(t *testing.T) {
	if logger.L == nil {
		logger.Init("error", "")
	}
	r := gin.New()
	api := r.Group("/api")
	api.Use(auth.AuthMiddleware())
	RegisterUserManagementRoutes(api)
	for _, path := range []string{"/api/users/2/insights", "/api/users/2/insights/logs", "/api/users/2/insights/tasks"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s returned %d", path, rec.Code)
		}
	}
}
