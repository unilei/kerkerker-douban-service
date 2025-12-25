package repository

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Metrics stores API metrics in Redis
type Metrics struct {
	client *redis.Client
}

// APIStats represents statistics for an API endpoint
type APIStats struct {
	Path         string  `json:"path"`
	TotalCalls   int64   `json:"total_calls"`
	SuccessCalls int64   `json:"success_calls"`
	ErrorCalls   int64   `json:"error_calls"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	MaxLatencyMs float64 `json:"max_latency_ms"`
	MinLatencyMs float64 `json:"min_latency_ms"`
	CacheHits    int64   `json:"cache_hits"`
	CacheMisses  int64   `json:"cache_misses"`
}

// DailyStats represents daily API statistics
type DailyStats struct {
	Date       string  `json:"date"`
	TotalCalls int64   `json:"total_calls"`
	AvgLatency float64 `json:"avg_latency"`
}

// OverallStats represents overall system statistics
type OverallStats struct {
	TotalAPICalls int64        `json:"total_api_calls"`
	TodayAPICalls int64        `json:"today_api_calls"`
	AvgLatencyMs  float64      `json:"avg_latency_ms"`
	CacheHitRate  float64      `json:"cache_hit_rate"`
	TopEndpoints  []APIStats   `json:"top_endpoints"`
	DailyTrend    []DailyStats `json:"daily_trend"`
	ErrorRate     float64      `json:"error_rate"`
	Uptime        int64        `json:"uptime_seconds"`
}

// NewMetrics creates a new Metrics instance
func NewMetrics(redisURL string) (*Metrics, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opt)
	return &Metrics{client: client}, nil
}

// RecordAPICall records an API call
func (m *Metrics) RecordAPICall(ctx context.Context, path string, statusCode int, latencyMs float64, cacheHit bool) error {
	now := time.Now()
	today := now.Format("2006-01-02")
	hour := now.Format("2006-01-02-15")

	pipe := m.client.Pipeline()

	// Total calls for path
	pathKey := fmt.Sprintf("metrics:path:%s", path)
	pipe.HIncrBy(ctx, pathKey, "total", 1)
	pipe.HIncrByFloat(ctx, pathKey, "latency_sum", latencyMs)

	// Track min/max latency
	pipe.HSetNX(ctx, pathKey, "min_latency", latencyMs)
	pipe.HSetNX(ctx, pathKey, "max_latency", latencyMs)

	// Success/Error counts
	if statusCode >= 200 && statusCode < 400 {
		pipe.HIncrBy(ctx, pathKey, "success", 1)
	} else {
		pipe.HIncrBy(ctx, pathKey, "error", 1)
	}

	// Cache hit/miss
	if cacheHit {
		pipe.HIncrBy(ctx, pathKey, "cache_hits", 1)
	} else {
		pipe.HIncrBy(ctx, pathKey, "cache_misses", 1)
	}

	// Daily stats
	dailyKey := fmt.Sprintf("metrics:daily:%s", today)
	pipe.HIncrBy(ctx, dailyKey, "total", 1)
	pipe.HIncrByFloat(ctx, dailyKey, "latency_sum", latencyMs)
	pipe.Expire(ctx, dailyKey, 30*24*time.Hour) // Keep 30 days

	// Hourly stats
	hourlyKey := fmt.Sprintf("metrics:hourly:%s", hour)
	pipe.HIncrBy(ctx, hourlyKey, "total", 1)
	pipe.Expire(ctx, hourlyKey, 48*time.Hour) // Keep 48 hours

	// Global stats
	pipe.Incr(ctx, "metrics:global:total")
	pipe.IncrByFloat(ctx, "metrics:global:latency_sum", latencyMs)

	// Track all paths
	pipe.SAdd(ctx, "metrics:paths", path)

	_, err := pipe.Exec(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to record metrics")
	}
	return err
}

// GetAPIStats gets statistics for a specific API path
func (m *Metrics) GetAPIStats(ctx context.Context, path string) (*APIStats, error) {
	pathKey := fmt.Sprintf("metrics:path:%s", path)

	result, err := m.client.HGetAll(ctx, pathKey).Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return &APIStats{Path: path}, nil
	}

	total, _ := strconv.ParseInt(result["total"], 10, 64)
	success, _ := strconv.ParseInt(result["success"], 10, 64)
	errors, _ := strconv.ParseInt(result["error"], 10, 64)
	latencySum, _ := strconv.ParseFloat(result["latency_sum"], 64)
	minLatency, _ := strconv.ParseFloat(result["min_latency"], 64)
	maxLatency, _ := strconv.ParseFloat(result["max_latency"], 64)
	cacheHits, _ := strconv.ParseInt(result["cache_hits"], 10, 64)
	cacheMisses, _ := strconv.ParseInt(result["cache_misses"], 10, 64)

	avgLatency := 0.0
	if total > 0 {
		avgLatency = latencySum / float64(total)
	}

	return &APIStats{
		Path:         path,
		TotalCalls:   total,
		SuccessCalls: success,
		ErrorCalls:   errors,
		AvgLatencyMs: avgLatency,
		MaxLatencyMs: maxLatency,
		MinLatencyMs: minLatency,
		CacheHits:    cacheHits,
		CacheMisses:  cacheMisses,
	}, nil
}

// GetOverallStats gets overall system statistics
func (m *Metrics) GetOverallStats(ctx context.Context) (*OverallStats, error) {
	stats := &OverallStats{}

	// Get total calls
	total, _ := m.client.Get(ctx, "metrics:global:total").Int64()
	latencySum, _ := m.client.Get(ctx, "metrics:global:latency_sum").Float64()
	stats.TotalAPICalls = total

	if total > 0 {
		stats.AvgLatencyMs = latencySum / float64(total)
	}

	// Get today's calls
	today := time.Now().Format("2006-01-02")
	todayKey := fmt.Sprintf("metrics:daily:%s", today)
	todayCalls, _ := m.client.HGet(ctx, todayKey, "total").Int64()
	stats.TodayAPICalls = todayCalls

	// Get all paths and their stats
	paths, _ := m.client.SMembers(ctx, "metrics:paths").Result()
	var allStats []APIStats
	var totalCacheHits, totalCacheMisses, totalErrors int64

	for _, path := range paths {
		pathStats, err := m.GetAPIStats(ctx, path)
		if err == nil && pathStats.TotalCalls > 0 {
			allStats = append(allStats, *pathStats)
			totalCacheHits += pathStats.CacheHits
			totalCacheMisses += pathStats.CacheMisses
			totalErrors += pathStats.ErrorCalls
		}
	}

	// Sort by total calls and get top 10
	sort.Slice(allStats, func(i, j int) bool {
		return allStats[i].TotalCalls > allStats[j].TotalCalls
	})

	if len(allStats) > 10 {
		stats.TopEndpoints = allStats[:10]
	} else {
		stats.TopEndpoints = allStats
	}

	// Calculate cache hit rate
	totalCacheOps := totalCacheHits + totalCacheMisses
	if totalCacheOps > 0 {
		stats.CacheHitRate = float64(totalCacheHits) / float64(totalCacheOps) * 100
	}

	// Calculate error rate
	if total > 0 {
		stats.ErrorRate = float64(totalErrors) / float64(total) * 100
	}

	// Get daily trend (last 7 days)
	stats.DailyTrend = m.getDailyTrend(ctx, 7)

	// Calculate uptime
	uptimeKey := "metrics:server:start_time"
	startTime, err := m.client.Get(ctx, uptimeKey).Int64()
	if err == nil && startTime > 0 {
		stats.Uptime = time.Now().Unix() - startTime
	}

	return stats, nil
}

// getDailyTrend gets daily statistics for the last N days
func (m *Metrics) getDailyTrend(ctx context.Context, days int) []DailyStats {
	var trend []DailyStats

	for i := days - 1; i >= 0; i-- {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		dailyKey := fmt.Sprintf("metrics:daily:%s", date)

		result, err := m.client.HGetAll(ctx, dailyKey).Result()
		if err != nil {
			continue
		}

		total, _ := strconv.ParseInt(result["total"], 10, 64)
		latencySum, _ := strconv.ParseFloat(result["latency_sum"], 64)

		avgLatency := 0.0
		if total > 0 {
			avgLatency = latencySum / float64(total)
		}

		trend = append(trend, DailyStats{
			Date:       date,
			TotalCalls: total,
			AvgLatency: avgLatency,
		})
	}

	return trend
}

// RecordServerStart records server start time
func (m *Metrics) RecordServerStart(ctx context.Context) {
	m.client.Set(ctx, "metrics:server:start_time", time.Now().Unix(), 0)
}

// ResetMetrics resets all metrics
func (m *Metrics) ResetMetrics(ctx context.Context) error {
	// Get all metrics keys
	keys, err := m.client.Keys(ctx, "metrics:*").Result()
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		return m.client.Del(ctx, keys...).Err()
	}

	return nil
}

// Close closes the Redis connection
func (m *Metrics) Close() error {
	return m.client.Close()
}
