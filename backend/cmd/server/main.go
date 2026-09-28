package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/new-api-tools/backend/internal/auth"
	"github.com/new-api-tools/backend/internal/cache"
	"github.com/new-api-tools/backend/internal/config"
	"github.com/new-api-tools/backend/internal/database"
	"github.com/new-api-tools/backend/internal/handler"
	"github.com/new-api-tools/backend/internal/logger"
	"github.com/new-api-tools/backend/internal/middleware"
	"github.com/new-api-tools/backend/internal/service"
)

func main() {
	// ========== 1. Load configuration ==========
	cfg := config.Load()

	// ========== 2. Initialize logger ==========
	logger.Init(cfg.LogLevel, cfg.LogFile)
	logger.L.Banner("🚀 NewAPI Middleware Tool - Go Backend")
	logger.L.System(fmt.Sprintf("服务器地址: %s", cfg.ServerAddr()))
	logger.L.System(fmt.Sprintf("数据库引擎: %s", cfg.DatabaseEngine))
	logger.L.System(fmt.Sprintf("时区: %s", cfg.TimeZone))

	// ========== 3. Initialize database ==========
	_, err := database.Init(cfg)
	if err != nil {
		logger.L.Fatal("数据库初始化失败: " + err.Error())
	}
	defer database.Close()

	// No index DDL here: the Tool connects to the gateway's production database,
	// so indexes are created by hand after an EXPLAIN review, never at startup.

	// ========== 4. Initialize Redis cache ==========
	if cfg.RedisConnString != "" {
		_, err := cache.Init(cfg.RedisConnString)
		if err != nil {
			logger.L.Warn("Redis 连接失败，将使用无缓存模式: " + err.Error())
		}
	} else {
		logger.L.Warn("REDIS_CONN_STRING 未配置，缓存功能不可用")
	}
	defer cache.Close()

	// ========== 5. Setup Gin router ==========
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Global middleware
	r.Use(middleware.ErrorHandlerMiddleware())  // Panic recovery
	r.Use(middleware.CORSMiddleware())          // CORS
	r.Use(middleware.RequestLoggerMiddleware()) // Request logging

	// ========== 6. Register routes ==========

	// Health check (no auth required)
	handler.RegisterHealthRoutes(r)

	// API group with authentication
	api := r.Group("/api")
	api.Use(auth.AuthMiddleware())
	{
		// Auth routes (login/logout are whitelisted in middleware)
		handler.RegisterAuthRoutes(api)

		// Phase 2.1: Basic modules
		handler.RegisterTopUpRoutes(api)
		handler.RegisterTopUpAnalyticsRoutes(api)
		handler.RegisterStorageRoutes(api)
		handler.RegisterSystemRoutes(api)

		// Phase 2.2: Dashboard, UserManagement
		handler.RegisterDashboardRoutes(api)
		handler.RegisterMarginAnalysisRoutes(api)
		handler.RegisterUserManagementRoutes(api)
		handler.RegisterPanelWhitelistRoutes(api)
		handler.RegisterAffiliateStatsRoutes(api)
		handler.RegisterAcquisitionRoutes(api)

		// Phase 2.3: IP Monitoring, Risk Monitoring, Model Status
		handler.RegisterIPMonitoringRoutes(api)
		handler.RegisterRiskMonitoringRoutes(api)
		handler.RegisterModelStatusRoutes(api)

		// Phase 2.4: Token Management
		handler.RegisterTokenRoutes(api)

		// Phase 2.5: Channel Monitor
		handler.RegisterChannelMonitorRoutes(api)

		// Phase 2.6: Task Logs (任务日志 + 使用日志关联)
		handler.RegisterTaskLogRoutes(api)
	}

	// ========== 7. Background tasks ==========

	// IP recording enforcement: check every 10 minutes, enable if any user disabled it.
	// 保留：网关风控依赖 logs.ip，删除前须先让网关默认开启记录 IP。
	stopIPEnforce := make(chan struct{})
	go backgroundEnforceIPRecording(stopIPEnforce)

	// ========== 8. Start server with graceful shutdown ==========
	srv := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.L.Success(fmt.Sprintf("服务已启动: http://%s", cfg.ServerAddr()))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L.Fatal("服务启动失败: " + err.Error())
		}
	}()

	// ========== 9. Wait for interrupt signal ==========
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.L.System("正在优雅关闭服务...")

	// Stop background tasks
	close(stopIPEnforce)

	// Give the server 10 seconds to finish processing requests
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.L.Error("服务关闭异常: " + err.Error())
	}

	logger.L.Success("服务已关闭")
}

// backgroundEnforceIPRecording periodically checks and enforces IP recording for all users.
func backgroundEnforceIPRecording(stop <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			logger.L.Error(fmt.Sprintf("[IP记录] 后台任务 panic: %v", r))
		}
	}()

	// Wait 30 seconds after startup before first check
	select {
	case <-time.After(30 * time.Second):
	case <-stop:
		return
	}

	logger.L.System("[IP记录] 强制开启定时任务已启动 (间隔: 10分钟)")

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	// Run immediately on first tick, then every 10 minutes
	for {
		enforceIPRecordingOnce()

		select {
		case <-ticker.C:
		case <-stop:
			logger.L.System("[IP记录] 强制开启定时任务已停止")
			return
		}
	}
}

func enforceIPRecordingOnce() {
	defer func() {
		if r := recover(); r != nil {
			logger.L.Error(fmt.Sprintf("[IP记录] 检查执行 panic: %v", r))
		}
	}()

	svc := service.NewIPMonitoringService()

	stats, err := svc.GetIPStats()
	if err != nil {
		logger.L.Warn("[IP记录] 获取状态失败: " + err.Error())
		return
	}

	disabledCount := toInt64(stats["disabled_count"])
	totalUsers := toInt64(stats["total_users"])

	if disabledCount == 0 {
		logger.L.Debug(fmt.Sprintf("[IP记录] 所有用户 (%d) 已开启 IP 记录，无需操作", totalUsers))
		return
	}

	logger.L.System(fmt.Sprintf("[IP记录] 检测到 %d 个用户关闭了 IP 记录，正在强制开启...", disabledCount))

	result, err := svc.EnableAllIPRecording()
	if err != nil {
		logger.L.Warn("[IP记录] 强制开启失败: " + err.Error())
		return
	}

	logger.L.Success(fmt.Sprintf("[IP记录] %s", result["message"]))
}

func toInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	default:
		return 0
	}
}
