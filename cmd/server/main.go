package main

import (
	"context"
	"os"
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

	// Initialize handlers
	heroHandler := handler.NewHeroHandler(doubanService, tmdbService, cache)
	categoryHandler := handler.NewCategoryHandler(doubanService, cache)
	detailHandler := handler.NewDetailHandler(doubanService, cache)
	latestHandler := handler.NewLatestHandler(doubanService, cache)
	moviesHandler := handler.NewMoviesHandler(doubanService, cache)
	tvHandler := handler.NewTVHandler(doubanService, cache)
	newHandler := handler.NewNewHandler(doubanService, cache)
	searchHandler := handler.NewSearchHandler(doubanService, cache)
	adminHandler := handler.NewAdminHandler(doubanService, tmdbService, metrics)

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

	// API routes
	api := r.Group("/api/v1")
	{
		// Status and Analytics (for admin dashboard)
		api.GET("/status", adminHandler.GetStatus)
		api.GET("/analytics", adminHandler.GetAnalytics)
		api.GET("/analytics/endpoint", adminHandler.GetEndpointStats)
		api.DELETE("/analytics", adminHandler.ResetAnalytics)

		// Hero Banner
		api.GET("/hero", heroHandler.GetHero)
		api.DELETE("/hero", heroHandler.DeleteHeroCache)

		// Category
		api.GET("/category", categoryHandler.GetCategory)
		api.DELETE("/category", categoryHandler.DeleteCategoryCache)

		// Detail
		api.GET("/detail/:id", detailHandler.GetDetail)
		api.DELETE("/detail/:id", detailHandler.DeleteDetailCache)
		api.DELETE("/detail", detailHandler.DeleteAllDetailCache)

		// Latest
		api.GET("/latest", latestHandler.GetLatest)
		api.DELETE("/latest", latestHandler.DeleteLatestCache)

		// Movies
		api.GET("/movies", moviesHandler.GetMovies)
		api.DELETE("/movies", moviesHandler.DeleteMoviesCache)

		// TV
		api.GET("/tv", tvHandler.GetTV)
		api.DELETE("/tv", tvHandler.DeleteTVCache)

		// New
		api.GET("/new", newHandler.GetNew)
		api.DELETE("/new", newHandler.DeleteNewCache)

		// Search
		api.GET("/search", searchHandler.Search)
		api.POST("/search", searchHandler.GetSearchTags)
		api.DELETE("/search", searchHandler.DeleteSearchCache)
	}

	// Start server
	addr := ":" + cfg.Port
	log.Info().Str("addr", addr).Msg("🌐 Server listening")
	log.Info().Str("admin", "http://localhost"+addr+"/admin").Msg("📊 Admin dashboard available")

	if err := r.Run(addr); err != nil {
		log.Fatal().Err(err).Msg("Failed to start server")
	}
}
