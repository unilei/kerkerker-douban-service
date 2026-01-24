package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kerkerker-douban-service/internal/config"
	"kerkerker-douban-service/internal/handler"
	"kerkerker-douban-service/internal/middleware"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Setup logging
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Load configuration
	cfg := config.Load()
	log.Info().
		Str("port", cfg.Port).
		Str("mode", cfg.GinMode).
		Int("proxies", len(cfg.DoubanProxies)).
		Msg("🚀 Starting kerkerker-douban-service")

	// Set Gin mode
	gin.SetMode(cfg.GinMode)

	// Initialize Redis cache
	cache, err := repository.NewCache(cfg.RedisURL, 1*time.Hour)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to Redis")
	}
	defer cache.Close()

	// Initialize metrics
	metrics, err := repository.NewMetrics(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize metrics")
	}
	defer metrics.Close()
	metrics.RecordServerStart(context.Background())
	log.Info().Msg("📊 Metrics enabled")

	// Initialize HTTP client with proxy support
	httpClient := httpclient.NewClient(cfg.DoubanProxies)
	if httpClient.HasProxy() {
		log.Info().Int("count", httpClient.ProxyCount()).Msg("🔀 Proxy enabled")
	}

	// Initialize services
	doubanService := service.NewDoubanService(httpClient)
	tmdbService := service.NewTMDBService(cfg.TMDBAPIKeys, cfg.TMDBBaseURL, cfg.TMDBImageBase)
	if tmdbService.IsConfigured() {
		log.Info().Int("keys", tmdbService.KeyCount()).Msg("🎬 TMDB service enabled (轮询模式)")
	}

	// Initialize handlers with configured cache TTL
	heroHandler := handler.NewHeroHandler(doubanService, tmdbService, cache, cfg.CacheTTLHero)
	categoryHandler := handler.NewCategoryHandler(doubanService, cache)
	detailHandler := handler.NewDetailHandler(doubanService, cache)
	latestHandler := handler.NewLatestHandler(doubanService, cache)
	moviesHandler := handler.NewMoviesHandler(doubanService, cache)
	tvHandler := handler.NewTVHandler(doubanService, cache)
	newHandler := handler.NewNewHandler(doubanService, cache)
	searchHandler := handler.NewSearchHandler(doubanService, cache)
	adminHandler := handler.NewAdminHandler(doubanService, tmdbService, metrics)
	calendarHandler := handler.NewCalendarHandler(tmdbService, doubanService, cache, cfg.CacheTTLCategory)

	// Setup router
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logging())
	r.Use(middleware.Metrics(metrics)) // Add metrics middleware
	r.Use(middleware.CORS())

	// Serve admin dashboard from filesystem
	r.StaticFile("/", "web/static/index.html")
	r.StaticFile("/admin", "web/static/index.html")
	r.Static("/static", "web/static")

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status": "ok",
			"time":   time.Now().Unix(),
		})
	})

	// API routes - 公开访问
	api := r.Group("/api/v1")
	{
		// 公开 GET 接口
		api.GET("/status", adminHandler.GetStatus)
		api.GET("/hero", heroHandler.GetHero)
		api.GET("/category", categoryHandler.GetCategory)
		api.GET("/detail/:id", detailHandler.GetDetail)
		api.GET("/latest", latestHandler.GetLatest)
		api.GET("/movies", moviesHandler.GetMovies)
		api.GET("/tv", tvHandler.GetTV)
		api.GET("/new", newHandler.GetNew)
		api.GET("/search", searchHandler.Search)
		api.POST("/search", searchHandler.GetSearchTags)

		// 日历接口
		api.GET("/calendar", calendarHandler.GetCalendar)
		api.GET("/calendar/airing", calendarHandler.GetAiring)
	}

	// Admin routes - 需要认证（如果配置了 ADMIN_API_KEY）
	admin := r.Group("/api/v1")
	admin.Use(middleware.AdminAuth(cfg.AdminAPIKey))
	{
		// Analytics（查询也需要认证保护）
		admin.GET("/analytics", adminHandler.GetAnalytics)
		admin.GET("/analytics/endpoint", adminHandler.GetEndpointStats)
		admin.DELETE("/analytics", adminHandler.ResetAnalytics)

		// 缓存管理
		admin.DELETE("/hero", heroHandler.DeleteHeroCache)
		admin.DELETE("/category", categoryHandler.DeleteCategoryCache)
		admin.DELETE("/detail/:id", detailHandler.DeleteDetailCache)
		admin.DELETE("/detail", detailHandler.DeleteAllDetailCache)
		admin.DELETE("/latest", latestHandler.DeleteLatestCache)
		admin.DELETE("/movies", moviesHandler.DeleteMoviesCache)
		admin.DELETE("/tv", tvHandler.DeleteTVCache)
		admin.DELETE("/new", newHandler.DeleteNewCache)
		admin.DELETE("/search", searchHandler.DeleteSearchCache)
		admin.DELETE("/calendar", calendarHandler.DeleteCalendarCache)
	}

	// 日志输出认证状态
	if cfg.AdminAPIKey != "" {
		log.Info().Msg("🔐 Admin API 认证已启用")
	} else {
		log.Warn().Msg("⚠️  Admin API 未配置认证，管理接口对外开放")
	}

	// Create HTTP server with graceful shutdown support
	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Start server in a goroutine
	go func() {
		log.Info().Str("addr", addr).Msg("🌐 Server listening")
		log.Info().Str("admin", "http://localhost"+addr+"/admin").Msg("📊 Admin dashboard available")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to start server")
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("🛑 Shutting down server...")

	// Give outstanding requests a deadline for completion
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("Server forced to shutdown")
	}

	log.Info().Msg("👋 Server exited")
}
