package handler

import "time"

// CacheTTLConfig holds cache TTL configuration for different data types
type CacheTTLConfig struct {
	Hero     time.Duration
	Detail   time.Duration
	Category time.Duration
	Search   time.Duration
	Default  time.Duration
}

// DefaultCacheTTL returns default cache TTL configuration
func DefaultCacheTTL() *CacheTTLConfig {
	return &CacheTTLConfig{
		Hero:     6 * time.Hour,
		Detail:   24 * time.Hour,
		Category: 1 * time.Hour,
		Search:   30 * time.Minute,
		Default:  1 * time.Hour,
	}
}
