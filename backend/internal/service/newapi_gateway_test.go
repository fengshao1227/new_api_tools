package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capturedManageRequest struct {
	method string
	path   string
	auth   string
	user   string
	body   map[string]interface{}
}

func gatewayStub(t *testing.T, status int, reply string) (newAPIAdminTarget, *capturedManageRequest) {
	t.Helper()
	captured := &capturedManageRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.auth = r.Header.Get("Authorization")
		captured.user = r.Header.Get("New-Api-User")
		if err := json.NewDecoder(r.Body).Decode(&captured.body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return newAPIAdminTarget{baseURL: srv.URL, apiKey: "test-admin-token"}, captured
}

func TestManageGatewayUserDisableSendsReasonWithAdminHeaders(t *testing.T) {
	target, got := gatewayStub(t, http.StatusOK, `{"success":true,"message":""}`)

	if err := manageGatewayUserAt(context.Background(), target, 42, GatewayUserDisable, "Chargeback on a stolen card"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got.method != http.MethodPost || got.path != "/api/user/manage" {
		t.Fatalf("request = %s %s, want POST /api/user/manage", got.method, got.path)
	}
	if got.auth != "Bearer test-admin-token" || got.user != "1" {
		t.Fatalf("admin headers = %q / %q", got.auth, got.user)
	}
	if got.body["id"] != float64(42) || got.body["action"] != "disable" || got.body["reason"] != "Chargeback on a stolen card" {
		t.Fatalf("unexpected body: %#v", got.body)
	}
}

func TestManageGatewayUserEnableOmitsReason(t *testing.T) {
	target, got := gatewayStub(t, http.StatusOK, `{"success":true}`)

	if err := manageGatewayUserAt(context.Background(), target, 7, GatewayUserEnable, "ignored"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got.body["action"] != "enable" {
		t.Fatalf("action = %#v", got.body["action"])
	}
	if _, ok := got.body["reason"]; ok {
		t.Fatalf("enable must not send a reason: %#v", got.body)
	}
}

func TestManageGatewayUserPassesGatewayMessageThrough(t *testing.T) {
	target, _ := gatewayStub(t, http.StatusOK, `{"success":false,"message":"无权更改同权限等级或更高权限等级的用户信息"}`)

	err := manageGatewayUserAt(context.Background(), target, 1, GatewayUserDisable, "reason")
	var gatewayErr *GatewayError
	if !errors.As(err, &gatewayErr) {
		t.Fatalf("want *GatewayError, got %T %v", err, err)
	}
	if gatewayErr.Message != "无权更改同权限等级或更高权限等级的用户信息" {
		t.Fatalf("message = %q", gatewayErr.Message)
	}
}

func TestManageGatewayUserReportsUnreadableReply(t *testing.T) {
	target, _ := gatewayStub(t, http.StatusBadGateway, `<html>bad gateway</html>`)

	err := manageGatewayUserAt(context.Background(), target, 1, GatewayUserEnable, "")
	if err == nil {
		t.Fatal("want an error for a non-JSON reply")
	}
	var gatewayErr *GatewayError
	if errors.As(err, &gatewayErr) {
		t.Fatalf("a non-JSON reply is not a gateway refusal: %v", err)
	}
}

func TestManageGatewayUserRejectsUnknownAction(t *testing.T) {
	target := newAPIAdminTarget{baseURL: "http://127.0.0.1:1", apiKey: "k"}
	if err := manageGatewayUserAt(context.Background(), target, 1, "delete", ""); err == nil {
		t.Fatal("delete must not be sendable through this helper")
	}
}
