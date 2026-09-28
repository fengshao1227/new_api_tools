package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
