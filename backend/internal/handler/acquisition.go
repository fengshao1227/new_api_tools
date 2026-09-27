package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/service"
)

// RegisterAcquisitionRoutes mounts the two-level BeatAPI source analytics.
func RegisterAcquisitionRoutes(r *gin.RouterGroup) {
	r.GET("/acquisition/overview", GetAcquisitionOverview)
}

// GET /api/acquisition/overview?days=30 (days=0 means all time)
func GetAcquisitionOverview(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	data, err := service.NewAcquisitionSourceService().GetOverview(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to get acquisition analytics: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
