package config

import (
	"fmt"
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
	MongoURI      string // MongoDB 持久层连接串（为空则降级为纯 Redis 模式）
	MongoDBName   string // MongoDB 数据库名
	DoubanProxies []string
	TMDBAPIKeys   []string // 支持多个 API Key 轮询
	TMDBBaseURL   string
	TMDBImageBase string

	// Cloudflare R2 image sync
	R2Images           R2ImageConfig
	RequireR2ImageSync bool

	// 缓存 TTL 配置（差异化）
	CacheTTLHero     time.Duration // Hero Banner 缓存时间
	CacheTTLDetail   time.Duration // 详情页缓存时间
	CacheTTLCategory time.Duration // 分类缓存时间
	CacheTTLSearch   time.Duration // 搜索缓存时间
	CacheTTLDefault  time.Duration // 默认缓存时间

	// Admin API 认证
	AdminAPIKey string // 为空则不启用认证
}

// R2ImageConfig holds Cloudflare R2 settings for Douban image mirroring.
type R2ImageConfig struct {
	Enabled         bool
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	PublicBaseURL   string
	UploadAPIURL    string
	UploadAPIToken  string
	KeyPrefix       string
	MaxImageBytes   int64
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
		Port:               getEnv("PORT", "8080"),
		GinMode:            getEnv("GIN_MODE", "debug"),
		RedisURL:           getEnv("REDIS_URL", "redis://localhost:6379"),
		MongoURI:           getEnv("MONGO_URI", ""),
		MongoDBName:        getEnv("MONGO_DB_NAME", "kerkerker_douban"),
		DoubanProxies:      proxies,
		TMDBAPIKeys:        tmdbKeys,
		TMDBBaseURL:        getEnv("TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		TMDBImageBase:      getEnv("TMDB_IMAGE_BASE", "https://image.tmdb.org/t/p/original"),
		R2Images:           loadR2ImageConfig(),
		RequireR2ImageSync: getBool("REQUIRE_R2_IMAGE_SYNC", false),

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

func loadR2ImageConfig() R2ImageConfig {
	endpoint := getEnvAny("CLOUDFLARE_R2_ENDPOINT", "R2_ENDPOINT")
	if endpoint == "" {
		if accountID := getEnvAny("CLOUDFLARE_R2_ACCOUNT_ID", "R2_ACCOUNT_ID"); accountID != "" {
			endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
		}
	}

	cfg := R2ImageConfig{
		Endpoint:        strings.TrimSpace(endpoint),
		AccessKeyID:     strings.TrimSpace(getEnvAny("CLOUDFLARE_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(getEnvAny("CLOUDFLARE_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY")),
		Bucket:          strings.TrimSpace(getEnvAny("CLOUDFLARE_R2_BUCKET", "R2_BUCKET")),
		PublicBaseURL:   strings.TrimRight(strings.TrimSpace(getEnvAny("CLOUDFLARE_R2_PUBLIC_URL", "R2_PUBLIC_URL")), "/"),
		UploadAPIURL:    strings.TrimRight(strings.TrimSpace(getEnv("CLOUDFLARE_R2_UPLOAD_API_URL", "")), "/"),
		UploadAPIToken:  strings.TrimSpace(getEnv("CLOUDFLARE_R2_UPLOAD_API_TOKEN", "")),
		KeyPrefix:       strings.Trim(strings.TrimSpace(getEnv("CLOUDFLARE_R2_KEY_PREFIX", "douban-images")), "/"),
		MaxImageBytes:   getInt64("CLOUDFLARE_R2_MAX_IMAGE_BYTES", 10*1024*1024),
	}

	hasDirectR2 := cfg.Endpoint != "" &&
		cfg.AccessKeyID != "" &&
		cfg.SecretAccessKey != "" &&
		cfg.Bucket != ""
	hasUploadWorker := cfg.UploadAPIURL != "" && cfg.UploadAPIToken != ""
	cfg.Enabled = cfg.PublicBaseURL != "" && (hasDirectR2 || hasUploadWorker)

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAny(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func getDurationMinutes(key string, defaultMinutes int) time.Duration {
	if value := os.Getenv(key); value != "" {
		if minutes, err := strconv.Atoi(value); err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute
		}
	}
	return time.Duration(defaultMinutes) * time.Minute
}

func getInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

func getBool(key string, defaultValue bool) bool {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
