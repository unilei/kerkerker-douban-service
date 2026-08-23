package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// PluginServiceAuth protects remote plugin calls with a dedicated service
// token. An unset token fails closed instead of exposing a provider facade.
func PluginServiceAuth(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("x-kerkerker-contract-version", "1.0.0")
		c.Header("cache-control", "no-store")
		if strings.TrimSpace(token) == "" {
			abortPluginError(c, http.StatusServiceUnavailable, "CONFIGURATION_ERROR", "插件服务认证未配置")
			return
		}
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		provided := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if !strings.HasPrefix(auth, "Bearer ") || provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			abortPluginError(c, http.StatusUnauthorized, "UNAUTHORIZED", "插件服务认证失败")
			return
		}
		c.Next()
	}
}

func abortPluginError(c *gin.Context, status int, code, message string) {
	body := gin.H{"code": code, "message": message}
	if requestID := strings.TrimSpace(c.GetHeader("x-kerkerker-request-id")); requestID != "" && len(requestID) <= 240 {
		body["requestId"] = requestID
	}
	c.AbortWithStatusJSON(status, gin.H{"error": body})
}
