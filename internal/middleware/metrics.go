package middleware

import (
	"context"
	"strings"
	"time"

	"kerkerker-douban-service/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// Metrics returns a middleware that records API metrics
func Metrics(metrics *repository.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only track API endpoints
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}

		start := time.Now()

		c.Next()

		// Record metrics after request completes
		latency := float64(time.Since(start).Milliseconds())
		status := c.Writer.Status()

		// Check if response was from cache (look for source field in response)
		cacheHit := false
		if source := c.GetString("cache_source"); source == "redis-cache" {
			cacheHit = true
		}

		// Record the metrics
		ctx := context.Background()
		path := normalizePath(c.Request.URL.Path)

		if err := metrics.RecordAPICall(ctx, path, status, latency, cacheHit); err != nil {
			log.Warn().Err(err).Msg("Failed to record metrics")
		}
	}
}

// normalizePath normalizes API paths for grouping
func normalizePath(path string) string {
	// Normalize paths with IDs like /api/v1/detail/12345 -> /api/v1/detail/:id
	parts := strings.Split(path, "/")
	for i, part := range parts {
		// Check if part looks like an ID (numeric)
		if isNumeric(part) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// isNumeric checks if a string is purely numeric
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
