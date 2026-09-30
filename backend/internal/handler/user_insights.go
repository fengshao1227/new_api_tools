package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/logger"
	"github.com/new-api-tools/backend/internal/service"
)

func GetUserInsights(c *gin.Context) {
	serveUserInsights(c, service.NewUserInsightsService().Report)
}

func GetUserInsightsLogs(c *gin.Context) {
	serveUserInsights(c, service.NewUserInsightsService().Logs)
}

func GetUserInsightsTasks(c *gin.Context) {
	serveUserInsights(c, service.NewUserInsightsService().Tasks)
}

func serveUserInsights[T any](c *gin.Context, query func(context.Context, service.UserInsightsParams) (T, error)) {
	c.Header("Cache-Control", "no-store")
	params, err := parseUserInsightsParams(c, time.Now().Unix())
	if err != nil {
		userManageError(c, http.StatusBadRequest, "INVALID_PARAMS", err.Error())
		return
	}
	data, err := query(c.Request.Context(), params)
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	switch {
	case errors.Is(err, service.ErrInsightsUserNotFound):
		userManageError(c, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在或已删除")
	case errors.Is(err, context.DeadlineExceeded):
		userManageError(c, http.StatusGatewayTimeout, "QUERY_TIMEOUT", "查询超时，请缩小时间范围或选择模型后重试")
	case errors.Is(err, context.Canceled):
		userManageError(c, http.StatusRequestTimeout, "QUERY_CANCELLED", "查询已取消")
	default:
		if logger.L != nil {
			logger.L.Error(fmt.Sprintf("User insights query failed for user %d", params.UserID))
		}
		userManageError(c, http.StatusServiceUnavailable, "QUERY_UNAVAILABLE", "暂时无法读取用户数据，请稍后重试")
	}
}

// A single filter contract keeps the report and both detail lists on the same
// half-open UTC interval. An explicit start_time=0 selects retained history.
func parseUserInsightsParams(c *gin.Context, now int64) (service.UserInsightsParams, error) {
	p := service.UserInsightsParams{Page: 1, PageSize: 20, Type: "all", Status: "all"}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		return p, errors.New("请选择有效用户")
	}
	p.UserID = userID
	p.Window.EndTime = now + 1
	query := c.Request.URL.Query()
	for _, key := range []string{"start_time", "end_time", "model", "page", "page_size", "type", "status"} {
		if len(query[key]) > 1 {
			return p, fmt.Errorf("查询参数 %s 不能重复", key)
		}
	}
	if value, present := query["end_time"]; present {
		p.Window.EndTime, err = strconv.ParseInt(value[0], 10, 64)
		if err != nil || p.Window.EndTime <= 0 || p.Window.EndTime > 253402300799 {
			return p, errors.New("结束时间必须是有效的 Unix 秒时间戳")
		}
	}
	p.Window.StartTime = max(0, p.Window.EndTime-30*24*3600)
	if value, present := query["start_time"]; present {
		p.Window.StartTime, err = strconv.ParseInt(value[0], 10, 64)
		if err != nil || p.Window.StartTime < 0 {
			return p, errors.New("开始时间必须是有效的 Unix 秒时间戳")
		}
		p.Window.AllTime = p.Window.StartTime == 0
	}
	if p.Window.StartTime >= p.Window.EndTime {
		return p, errors.New("开始时间必须早于结束时间")
	}
	p.Window.Model = strings.TrimSpace(query.Get("model"))
	if !utf8.ValidString(p.Window.Model) || utf8.RuneCountInString(p.Window.Model) > 256 || strings.ContainsFunc(p.Window.Model, unicode.IsControl) {
		return p, errors.New("模型名称无效或超过 256 个字符")
	}
	for _, pagination := range []struct {
		key    string
		max    int
		target *int
	}{{"page", 1_000_000, &p.Page}, {"page_size", 100, &p.PageSize}} {
		if value, present := query[pagination.key]; present {
			n, err := strconv.Atoi(value[0])
			if err != nil || n < 1 || n > pagination.max {
				return p, fmt.Errorf("%s 必须介于 1 和 %d 之间", pagination.key, pagination.max)
			}
			*pagination.target = n
		}
	}
	if value := query.Get("type"); value != "" {
		p.Type = value
	}
	if value := query.Get("status"); value != "" {
		p.Status = value
	}
	switch p.Type {
	case "all", "consume", "error", "refund":
	default:
		return p, errors.New("日志类型无效")
	}
	switch p.Status {
	case "all", "active", "SUCCESS", "FAILURE", "IN_PROGRESS", "NOT_START", "SUBMITTED", "QUEUED":
	default:
		return p, errors.New("任务状态无效")
	}
	return p, nil
}
