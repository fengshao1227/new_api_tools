package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/service"
)

// The business view's sections. Each is its own endpoint so the page can load
// them in parallel and one slow or failing section costs one card.

// businessWindow reads ?window=today|7d|30d (default 7d).
func businessWindow(c *gin.Context) (string, bool) {
	window := c.DefaultQuery("window", service.DashboardWindow7d)
	if !service.IsDashboardWindow(window) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": gin.H{"message": "Invalid window value"}})
		return "", false
	}
	return window, true
}

func respondBusiness[T any](c *gin.Context, data T, err error) {
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GET /api/dashboard/business/finance
func GetBusinessFinance(c *gin.Context) {
	window, ok := businessWindow(c)
	if !ok {
		return
	}
	data, err := service.NewBusinessDashboardService().GetFinance(window, c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}

// GET /api/dashboard/business/conversion
func GetBusinessConversion(c *gin.Context) {
	window, ok := businessWindow(c)
	if !ok {
		return
	}
	data, err := service.NewBusinessDashboardService().GetConversion(window, c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}

// GET /api/dashboard/business/gifts-risk
func GetBusinessGiftsRisk(c *gin.Context) {
	window, ok := businessWindow(c)
	if !ok {
		return
	}
	data, err := service.NewBusinessDashboardService().GetGiftsRisk(window, c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}

// GET /api/dashboard/business/tasks
func GetBusinessTasks(c *gin.Context) {
	window, ok := businessWindow(c)
	if !ok {
		return
	}
	data, err := service.NewBusinessDashboardService().GetTasks(window, c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}

// GET /api/dashboard/business/supply — not windowed: current state.
func GetBusinessSupply(c *gin.Context) {
	data, err := service.NewBusinessDashboardService().GetSupply(c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}

// GET /api/dashboard/business/pricing-gaps
func GetBusinessPricingGaps(c *gin.Context) {
	window, ok := businessWindow(c)
	if !ok {
		return
	}
	data, err := service.NewBusinessDashboardService().GetPricingGaps(window, c.Query("no_cache") == "true")
	respondBusiness(c, data, err)
}
