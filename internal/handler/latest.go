package handler

import (
	"context"
	"net/http"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const latestCacheKey = "douban:latest:all"

// LatestHandler handles latest content API requests
type LatestHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewLatestHandler creates a new LatestHandler
func NewLatestHandler(douban *service.DoubanService, cache *repository.Cache) *LatestHandler {
	return &LatestHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// GetLatest returns latest content data
// GET /api/v1/latest
func (h *LatestHandler) GetLatest(c *gin.Context) {
	ctx := context.Background()

	// Check cache
	var cachedData []model.CategoryData
	if err := h.cache.Get(ctx, latestCacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().Msg("🆕 开始获取最新内容数据...")

	// Fetch data in parallel
	type fetchResult struct {
		name string
		data []model.Subject
	}

	categories := []struct {
		name string
		typ  string
		tag  string
	}{
		{"院线新片", "", "院线新片"},
		{"最新电影", "", "最新"},
		{"即将上映", "", "即将上映"},
		{"新剧上线", "tv", "最新"},
		{"本周口碑榜", "", "本周口碑榜"},
		{"热门趋势", "", "热门"},
	}

	results := make([]fetchResult, len(categories))
	var wg sync.WaitGroup

	for i, cat := range categories {
		wg.Add(1)
		go func(idx int, c struct {
			name string
			typ  string
			tag  string
		}) {
			defer wg.Done()
			data, err := h.doubanService.SearchSubjects(c.typ, c.tag, 24, 0)
			if err != nil {
				log.Warn().Err(err).Str("tag", c.tag).Msg("Failed to fetch")
				results[idx] = fetchResult{name: c.name, data: []model.Subject{}}
				return
			}
			log.Debug().Str("tag", c.tag).Int("count", len(data.Subjects)).Msg("✓ 抓取成功")
			results[idx] = fetchResult{name: c.name, data: data.Subjects}
		}(i, cat)
	}

	wg.Wait()

	// Build response
	resultData := make([]model.CategoryData, len(results))
	totalItems := 0
	for i, r := range results {
		resultData[i] = model.CategoryData{
			Name: r.name,
			Data: r.data,
		}
		totalItems += len(r.data)
	}

	// Cache result (30 minutes)
	h.cache.Set(ctx, latestCacheKey, resultData)

	log.Info().Msg("✅ 最新内容数据获取成功")

	c.JSON(http.StatusOK, gin.H{
		"code":            200,
		"data":            resultData,
		"source":          "fresh",
		"totalCategories": len(resultData),
		"totalItems":      totalItems,
	})
}

// DeleteLatestCache clears latest content cache
// DELETE /api/v1/latest
func (h *LatestHandler) DeleteLatestCache(c *gin.Context) {
	ctx := context.Background()
	h.cache.Delete(ctx, latestCacheKey)

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "最新内容缓存已清除",
	})
}
