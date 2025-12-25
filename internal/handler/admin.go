package handler

import (
	"context"
	"net/http"

	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminHandler handles admin-related endpoints
type AdminHandler struct {
	doubanService *service.DoubanService
	tmdbService   *service.TMDBService
	metrics       *repository.Metrics
}

// NewAdminHandler creates a new AdminHandler
func NewAdminHandler(douban *service.DoubanService, tmdb *service.TMDBService, metrics *repository.Metrics) *AdminHandler {
	return &AdminHandler{
		doubanService: douban,
		tmdbService:   tmdb,
		metrics:       metrics,
	}
}

// GetStatus returns service status
// GET /api/v1/status
func (h *AdminHandler) GetStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":        "ok",
		"proxy_enabled": h.doubanService.HasProxy(),
		"proxy_count":   h.doubanService.ProxyCount(),
		"tmdb_enabled":  h.tmdbService.IsConfigured(),
	})
}

// GetAnalytics returns API analytics
// GET /api/v1/analytics
func (h *AdminHandler) GetAnalytics(c *gin.Context) {
	ctx := context.Background()

	stats, err := h.metrics.GetOverallStats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": stats,
	})
}

// GetEndpointStats returns stats for a specific endpoint
// GET /api/v1/analytics/endpoint?path=/api/v1/hero
func (h *AdminHandler) GetEndpointStats(c *gin.Context) {
	ctx := context.Background()
	path := c.Query("path")

	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":  400,
			"error": "path parameter required",
		})
		return
	}

	stats, err := h.metrics.GetAPIStats(ctx, path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": stats,
	})
}

// ResetAnalytics resets all analytics data
// DELETE /api/v1/analytics
func (h *AdminHandler) ResetAnalytics(c *gin.Context) {
	ctx := context.Background()

	if err := h.metrics.ResetMetrics(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":  500,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "所有统计数据已重置",
	})
}
