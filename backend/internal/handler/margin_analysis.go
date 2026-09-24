package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/models"
	"github.com/new-api-tools/backend/internal/service"
)

// RegisterMarginAnalysisRoutes registers the read-only gross-margin report.
func RegisterMarginAnalysisRoutes(r *gin.RouterGroup) {
	r.GET("/margin-analysis", GetMarginAnalysis)
	r.GET("/margin-analysis/pricing", GetMarginPricingAnalysis)
}

// GET /api/margin-analysis?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD
func GetMarginAnalysis(c *gin.Context) {
	loc := time.Local
	now := time.Now().In(loc)
	startDate := strings.TrimSpace(c.Query("start_date"))
	endDate := strings.TrimSpace(c.Query("end_date"))
	if startDate == "" {
		startDate = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).Format("2006-01-02")
	}
	if endDate == "" {
		endDate = now.Format("2006-01-02")
	}
	start, startErr := time.ParseInLocation("2006-01-02", startDate, loc)
	endDay, endErr := time.ParseInLocation("2006-01-02", endDate, loc)
	if startErr != nil || endErr != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_RANGE", "日期必须是 YYYY-MM-DD", ""))
		return
	}
	end := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 23, 59, 59, 0, loc)
	if start.After(end) {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_RANGE", "开始日期不能晚于结束日期", ""))
		return
	}
	if end.Sub(start) > 366*24*time.Hour {
		c.JSON(http.StatusBadRequest, models.ErrorResp("RANGE_TOO_LARGE", "一次最多查询 366 天", ""))
		return
	}

	limit := 100
	params := service.MarginAnalysisParams{
		StartTime: start.Unix(),
		EndTime:   end.Unix(),
		Limit:     limit,
		NoCache:   c.Query("no_cache") == "true",
	}
	data, err := service.NewMarginAnalysisService().GetMarginAnalysis(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GET /api/margin-analysis/pricing
func GetMarginPricingAnalysis(c *gin.Context) {
	data, err := service.GetPricingAnalysis()
	if err != nil {
		c.JSON(http.StatusBadGateway, models.ErrorResp("PRICING_SOURCE_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
