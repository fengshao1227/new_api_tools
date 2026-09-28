package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/logger"
	"github.com/new-api-tools/backend/internal/models"
	"github.com/new-api-tools/backend/internal/service"
)

// maxBanReasonRunes matches new-api's ban_reason column (255 characters).
const maxBanReasonRunes = 255

// manageGatewayUser is the gateway call behind ban/unban; tests replace it.
var manageGatewayUser = service.ManageGatewayUser

func RegisterUserManagementRoutes(r *gin.RouterGroup) {
	g := r.Group("/users")
	{
		g.GET("/activity-stats", GetActivityStats)
		g.GET("/stats", GetActivityStats)
		g.GET("/groups", GetUserGroups)
		g.GET("", GetUsers)
		g.POST("/:user_id/ban", BanUser)
		g.POST("/:user_id/unban", UnbanUser)
		g.GET("/:user_id/invited", GetInvitedUsers)
	}
}

// GET /api/users/activity-stats
func GetActivityStats(c *gin.Context) {
	quick := c.DefaultQuery("quick", "false") == "true"
	svc := service.NewUserManagementService()

	stats, err := svc.GetActivityStats(quick)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// GET /api/users/groups
func GetUserGroups(c *gin.Context) {
	svc := service.NewUserManagementService()
	groups, err := svc.GetUserGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": groups,
			"total": len(groups),
		},
	})
}

// GET /api/users
func GetUsers(c *gin.Context) {
	page := parsePage(c)
	pageSize := parsePageSize(c, 20, 200)

	params := service.ListUsersParams{
		Page:           page,
		PageSize:       pageSize,
		ActivityFilter: c.Query("activity"),
		GroupFilter:    c.Query("group"),
		SourceFilter:   c.Query("source"),
		Search:         c.Query("search"),
		OrderBy:        c.DefaultQuery("order_by", "request_count"),
		OrderDir:       c.DefaultQuery("order_dir", "DESC"),
	}

	svc := service.NewUserManagementService()
	result, err := svc.GetUsers(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// userManageError answers with the message both at the top level, where the
// frontend's toast reads it, and in the standard error object.
func userManageError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"message": message,
		"error":   models.ErrorDetail{Code: code, Message: message},
	})
}

// respondGatewayFailure passes new-api's own refusal text through unchanged;
// anything else means the gateway could not be reached or understood.
func respondGatewayFailure(c *gin.Context, code string, err error) {
	var gatewayErr *service.GatewayError
	if errors.As(err, &gatewayErr) {
		userManageError(c, http.StatusBadRequest, code, gatewayErr.Message)
		return
	}
	userManageError(c, http.StatusBadGateway, code, err.Error())
}

// POST /api/users/:user_id/ban
//
// Bans through new-api's POST /api/user/manage so the gateway stores the reason
// the user sees, revokes the user's sessions and clears its caches. A banned
// user's requests are all refused by the gateway, so tokens are left alone.
func BanUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		userManageError(c, http.StatusBadRequest, "INVALID_PARAMS", "Invalid user ID")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		userManageError(c, http.StatusBadRequest, "INVALID_PARAMS", "Invalid request body")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		userManageError(c, http.StatusBadRequest, "BAN_REASON_REQUIRED", "封禁理由必填，理由会展示给被封用户")
		return
	}
	if utf8.RuneCountInString(reason) > maxBanReasonRunes {
		userManageError(c, http.StatusBadRequest, "BAN_REASON_TOO_LONG", "封禁理由不能超过 255 个字符")
		return
	}

	if err := manageGatewayUser(c.Request.Context(), userID, service.GatewayUserDisable, reason); err != nil {
		respondGatewayFailure(c, "BAN_ERROR", err)
		return
	}
	logger.L.Security("经网关封禁用户 " + strconv.FormatInt(userID, 10))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "用户已封禁",
	})
}

// POST /api/users/:user_id/unban
//
// Unbans through the gateway, which also clears the stored ban reason. Tokens
// are not re-enabled: that would revive expired or exhausted ones too.
func UnbanUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		userManageError(c, http.StatusBadRequest, "INVALID_PARAMS", "Invalid user ID")
		return
	}

	if err := manageGatewayUser(c.Request.Context(), userID, service.GatewayUserEnable, ""); err != nil {
		respondGatewayFailure(c, "UNBAN_ERROR", err)
		return
	}
	logger.L.Security("经网关解封用户 " + strconv.FormatInt(userID, 10))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "用户已解封",
	})
}

// GET /api/users/:user_id/invited
func GetInvitedUsers(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_PARAMS", "Invalid user ID", ""))
		return
	}

	page := parsePage(c)
	pageSize := parsePageSize(c, 20, 200)

	svc := service.NewUserManagementService()
	data, err := svc.GetInvitedUsers(userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
