package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/models"
	"github.com/new-api-tools/backend/internal/service"
)

// RegisterRiskMonitoringRoutes registers the read-only /api/risk endpoints.
// Risk control itself lives in the new-api gateway; the Tool only keeps the
// per-user analysis that the user/token pages and the IP lookup open.
func RegisterRiskMonitoringRoutes(r *gin.RouterGroup) {
	g := r.Group("/risk")
	{
		g.GET("/users/:user_id/analysis", GetUserRiskAnalysis)
	}
}

// GET /api/risk/users/:user_id/analysis
func GetUserRiskAnalysis(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_PARAMS", "Invalid user ID", ""))
		return
	}
	window := c.DefaultQuery("window", "24h")
	seconds, ok := service.WindowSeconds[window]
	if !ok {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_PARAMS", "Invalid window: "+window, ""))
		return
	}

	var endTime *int64
	if et := c.Query("end_time"); et != "" {
		v, err := strconv.ParseInt(et, 10, 64)
		if err == nil {
			endTime = &v
		}
	}

	svc := service.NewRiskMonitoringService()
	data, err := svc.GetUserAnalysis(userID, seconds, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
