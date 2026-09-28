package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/service"
)

// RegisterDashboardRoutes registers /api/dashboard endpoints
func RegisterDashboardRoutes(r *gin.RouterGroup) {
	g := r.Group("/dashboard")
	{
		g.GET("/growth", GetGrowthMetrics)
		g.GET("/growth/trend", GetGrowthTrend)
		g.GET("/business/finance", GetBusinessFinance)
		g.GET("/business/conversion", GetBusinessConversion)
		g.GET("/business/gifts-risk", GetBusinessGiftsRisk)
		g.GET("/business/tasks", GetBusinessTasks)
		g.GET("/business/supply", GetBusinessSupply)
		g.GET("/business/pricing-gaps", GetBusinessPricingGaps)
		g.POST("/cache/invalidate", InvalidateDashboardCache)
		g.GET("/ip-distribution", GetIPDistribution)
	}
}

// GET /api/dashboard/growth
func GetGrowthMetrics(c *gin.Context) {
	noCache := c.Query("no_cache") == "true"
	svc := service.NewDashboardService()

	data, err := svc.GetGrowthMetrics(noCache)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GET /api/dashboard/growth/trend
func GetGrowthTrend(c *gin.Context) {
	// Anything that is not "monthly" is the 30-day series. An unrecognised
	// value returning the default beats returning an error for a chart that
	// only ever has two states.
	granularity := c.DefaultQuery("granularity", "daily")
	if granularity != "monthly" {
		granularity = "daily"
	}
	noCache := c.Query("no_cache") == "true"
	svc := service.NewDashboardService()

	data, err := svc.GetGrowthTrend(granularity, noCache)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// POST /api/dashboard/cache/invalidate
func InvalidateDashboardCache(c *gin.Context) {
	svc := service.NewDashboardService()
	svc.InvalidateDashboardCache()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Dashboard cache invalidated",
	})
}

// GET /api/dashboard/ip-distribution
func GetIPDistribution(c *gin.Context) {
	window := c.DefaultQuery("window", "24h")
	if !validWindow(window) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": gin.H{"message": "Invalid window value"}})
		return
	}
	noCache := c.Query("no_cache") == "true"

	svc := service.NewDashboardService()
	data, err := svc.GetIPDistribution(window, noCache)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
