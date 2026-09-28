package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/models"
	"github.com/new-api-tools/backend/internal/service"
)

const maxIPLimit = 500

// RegisterIPMonitoringRoutes registers the read-only /api/ip endpoints.
func RegisterIPMonitoringRoutes(r *gin.RouterGroup) {
	g := r.Group("/ip")
	{
		g.GET("/lookup/:ip", LookupIPUsers)
		g.GET("/indexes", GetIPIndexStatus)
		g.GET("/geo/:ip", GetIPGeo)
		g.POST("/geo/batch", GetIPGeoBatch)
	}
}

// GET /api/ip/lookup/:ip
func LookupIPUsers(c *gin.Context) {
	ip := c.Param("ip")
	window := c.DefaultQuery("window", "24h")
	if !validWindow(window) {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_PARAMS", "Invalid window value", ""))
		return
	}
	limit := parseLimit(c, 100, maxIPLimit)
	includeGeo := c.Query("include_geo") == "true"

	svc := service.NewIPMonitoringService()
	data, err := svc.LookupIPUsers(ip, window, limit, includeGeo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// GET /api/ip/indexes
func GetIPIndexStatus(c *gin.Context) {
	svc := service.NewIPMonitoringService()
	data, err := svc.GetIPIndexStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResp("QUERY_ERROR", err.Error(), ""))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// GET /api/ip/geo/:ip
func GetIPGeo(c *gin.Context) {
	ip := c.Param("ip")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    service.FormatIPGeoInfo(service.LookupIPGeo(ip)),
	})
}

// POST /api/ip/geo/batch
func GetIPGeoBatch(c *gin.Context) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResp("INVALID_PARAMS", "Invalid JSON body", ""))
		return
	}

	seen := map[string]bool{}
	ips := make([]string, 0, len(req.IPs))
	for _, raw := range req.IPs {
		ip := strings.TrimSpace(raw)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		ips = append(ips, ip)
		if len(ips) >= maxIPLimit {
			break
		}
	}

	geoMap := service.LookupIPGeoBatch(ips)
	results := make([]map[string]interface{}, 0, len(ips))
	for _, ip := range ips {
		results = append(results, service.FormatIPGeoInfo(geoMap[ip]))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": results})
}
