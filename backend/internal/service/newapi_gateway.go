package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/new-api-tools/backend/internal/config"
)

// Actions the Tool may send to new-api's POST /api/user/manage.
const (
	GatewayUserDisable = "disable"
	GatewayUserEnable  = "enable"
)

const gatewayManageTimeout = 15 * time.Second

// newAPIAdminTarget is the gateway's base URL plus the admin access token the
// Tool uses for new-api's management API (NEWAPI_BASEURL / NEWAPI_API_KEY).
type newAPIAdminTarget struct {
	baseURL string
	apiKey  string
}

func loadNewAPIAdminTarget() (newAPIAdminTarget, error) {
	cfg := config.Get()
	key := strings.TrimSpace(cfg.NewAPIKey)
	if key == "" {
		return newAPIAdminTarget{}, fmt.Errorf("NEWAPI_API_KEY 未配置")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.NewAPIBaseURL), "/")
	if base == "" {
		return newAPIAdminTarget{}, fmt.Errorf("NEWAPI_BASEURL 未配置")
	}
	return newAPIAdminTarget{baseURL: base, apiKey: key}, nil
}

// setAdminHeaders authenticates a request to new-api's admin API. The access
// token in Authorization identifies the admin. New-Api-User: 1 (the root
// admin's id) rides along because upstream new-api builds reject admin calls
// that lack it.
func (t newAPIAdminTarget) setAdminHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("New-Api-User", "1")
}

// GatewayError is a refusal new-api reported itself ({"success":false,
// "message":...}). Message is new-api's text, passed on unchanged.
type GatewayError struct {
	Message string
}

func (e *GatewayError) Error() string { return e.Message }

// ManageGatewayUser bans ("disable") or unbans ("enable") a user through
// new-api's admin API instead of writing users.status directly, so the gateway
// also stores the ban reason shown to the user, revokes the user's sessions and
// clears its caches. reason is sent only with "disable".
func ManageGatewayUser(ctx context.Context, userID int64, action, reason string) error {
	target, err := loadNewAPIAdminTarget()
	if err != nil {
		return fmt.Errorf("%w，无法调用网关封禁接口", err)
	}
	return manageGatewayUserAt(ctx, target, userID, action, reason)
}

func manageGatewayUserAt(ctx context.Context, target newAPIAdminTarget, userID int64, action, reason string) error {
	if action != GatewayUserDisable && action != GatewayUserEnable {
		return fmt.Errorf("unsupported user action: %s", action)
	}

	payload := map[string]interface{}{"id": userID, "action": action}
	if action == GatewayUserDisable {
		payload["reason"] = reason
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, gatewayManageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.baseURL+"/api/user/manage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	target.setAdminHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求网关用户管理接口失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var envelope struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return fmt.Errorf("网关用户管理接口返回 HTTP %d，响应无法解析: %s", resp.StatusCode, snippet)
	}
	if !envelope.Success {
		if envelope.Message == "" {
			return &GatewayError{Message: fmt.Sprintf("网关拒绝了该操作 (HTTP %d)", resp.StatusCode)}
		}
		return &GatewayError{Message: envelope.Message}
	}
	return nil
}
