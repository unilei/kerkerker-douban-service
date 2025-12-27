package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AdminAuth returns a middleware that validates admin API key
// If apiKey is empty, authentication is disabled
func AdminAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 如果未配置 API Key，则跳过认证
		if apiKey == "" {
			c.Next()
			return
		}

		// 从 Authorization header 获取 token
		// 支持 "Bearer <token>" 和 "ApiKey <token>" 格式
		auth := c.GetHeader("Authorization")
		if auth == "" {
			// 也支持从查询参数获取（方便测试）
			auth = c.Query("api_key")
			if auth == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"code":  401,
					"error": "未授权：缺少 API Key",
				})
				return
			}
		} else {
			// 移除 Bearer 或 ApiKey 前缀
			auth = strings.TrimPrefix(auth, "Bearer ")
			auth = strings.TrimPrefix(auth, "ApiKey ")
		}

		// 验证 API Key
		if auth != apiKey {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":  403,
				"error": "禁止访问：API Key 无效",
			})
			return
		}

		c.Next()
	}
}
