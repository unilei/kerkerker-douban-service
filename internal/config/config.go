package config

import (
	"os"
	"strings"
)

// Config holds all configuration for the service
type Config struct {
	Port          string
	GinMode       string
	MongoDBURI    string
	MongoDBName   string
	RedisURL      string
	DoubanProxies []string
	TMDBAPIKeys   []string // 支持多个 API Key 轮询
	TMDBBaseURL   string
	TMDBImageBase string
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
		MongoDBURI:    getEnv("MONGODB_URI", "mongodb://localhost:27017"),
		MongoDBName:   getEnv("MONGODB_DATABASE", "douban_api"),
		RedisURL:      getEnv("REDIS_URL", "redis://localhost:6379"),
		DoubanProxies: proxies,
		TMDBAPIKeys:   tmdbKeys,
		TMDBBaseURL:   getEnv("TMDB_BASE_URL", "https://api.themoviedb.org/3"),
		TMDBImageBase: getEnv("TMDB_IMAGE_BASE", "https://image.tmdb.org/t/p/original"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
