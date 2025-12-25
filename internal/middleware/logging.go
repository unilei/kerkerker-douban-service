package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// Logging returns a logging middleware
func Logging() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		logEvent := log.Info()
		if status >= 400 {
			logEvent = log.Warn()
		}
		if status >= 500 {
			logEvent = log.Error()
		}

		logEvent.
			Int("status", status).
			Str("method", c.Request.Method).
			Str("path", path).
			Str("query", query).
			Dur("latency", latency).
			Str("ip", c.ClientIP()).
			Msg("request")
	}
}
