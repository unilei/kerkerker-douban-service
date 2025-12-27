package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the service
type Config struct {
	Port          string
	GinMode       string
	RedisURL      string
	DoubanProxies []string
	TMDBAPIKeys   []string // 支持多个 API Key 轮询
	TMDBBaseURL   string
	TMDBImageBase string

	// 缓存 TTL 配置（差异化）
	CacheTTLHero     time.Duration // Hero Banner 缓存时间
	CacheTTLDetail   time.Duration // 详情页缓存时间
	CacheTTLCategory time.Duration // 分类缓存时间
	CacheTTLSearch   time.Duration // 搜索缓存时间
	CacheTTLDefault  time.Duration // 默认缓存时间

	// Admin API 认证
	AdminAPIKey string // 为空则不启用认证
}

// Load reads configuration from environment variables
func Load() *Config {
	proxies := []string{}
	if proxyEnv := os.Getenv("DOUBAN_API_PROXY"); proxyEnv != "" {
		for _, p := range strings.Split(proxyEnv, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				proxies = append(proxies, trimmed)
			}
		}
	}

	// 支持多个 TMDB API Key，用逗号分隔
	tmdbKeys := []string{}
	if keyEnv := os.Getenv("TMDB_API_KEY"); keyEnv != "" {
		for _, k := range strings.Split(keyEnv, ",") {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				tmdbKeys = append(tmdbKeys, trimmed)
			}
		}
	}

	return &Config{
		Port:          getEnv("PORT", "8080"),
		GinMode:       getEnv("GIN_MODE", "debug"),
		RedisURL:      getEnv("REDIS_URL", "redis://localhost:6379"),
		DoubanProxies: proxies,
		TMDBAPIKeys:   tmdbKeys,
		TMDBBaseURL:   getEnv("TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		TMDBImageBase: getEnv("TMDB_IMAGE_BASE", "https://image.tmdb.org/t/p/original"),

		// 缓存 TTL（可通过环境变量覆盖，单位：分钟）
		CacheTTLHero:     getDurationMinutes("CACHE_TTL_HERO", 360),    // 6 小时
		CacheTTLDetail:   getDurationMinutes("CACHE_TTL_DETAIL", 1440), // 24 小时
		CacheTTLCategory: getDurationMinutes("CACHE_TTL_CATEGORY", 60), // 1 小时
		CacheTTLSearch:   getDurationMinutes("CACHE_TTL_SEARCH", 30),   // 30 分钟
		CacheTTLDefault:  getDurationMinutes("CACHE_TTL_DEFAULT", 60),  // 1 小时

		// Admin API 密钥
		AdminAPIKey: getEnv("ADMIN_API_KEY", ""),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getDurationMinutes(key string, defaultMinutes int) time.Duration {
	if value := os.Getenv(key); value != "" {
		if minutes, err := strconv.Atoi(value); err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return time.Duration(defaultMinutes) * time.Minute
}
