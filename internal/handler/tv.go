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

const tvCacheKey = "douban:tv:all"

// TVHandler handles TV show API requests
type TVHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewTVHandler creates a new TVHandler
func NewTVHandler(douban *service.DoubanService, cache *repository.Cache) *TVHandler {
	return &TVHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// GetTV returns TV show categories data
// GET /api/v1/tv
func (h *TVHandler) GetTV(c *gin.Context) {
	ctx := context.Background()

	// Check cache
	var cachedData []model.CategoryData
	if err := h.cache.Get(ctx, tvCacheKey, &cachedData); err == nil {
		cachedData = h.doubanService.SyncCategoryDataImages(ctx, cachedData)
		if h.doubanService.ImageSyncEnabled() {
			h.cache.SetKeepTTL(ctx, tvCacheKey, cachedData)
		}
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().Msg("📺 开始获取电视剧分类数据...")

	categories := []struct {
		name string
		tag  string
	}{
		{"热门剧集", "热门"},
		{"国产剧", "国产剧"},
		{"美剧", "美剧"},
		{"日剧", "日剧"},
		{"韩剧", "韩剧"},
		{"英剧", "英剧"},
		{"综艺节目", "综艺"},
		{"日本动画", "日本动画"},
	}

	results := make([]model.CategoryData, len(categories))
	var wg sync.WaitGroup

	for i, cat := range categories {
		wg.Add(1)
		go func(idx int, name, tag string) {
			defer wg.Done()
			data, err := h.doubanService.SearchSubjects("tv", tag, 24, 0)
			if err != nil {
				log.Warn().Err(err).Str("tag", tag).Msg("Failed to fetch TV")
				results[idx] = model.CategoryData{Name: name, Data: []model.Subject{}}
				return
			}
			log.Debug().Str("tag", tag).Int("count", len(data.Subjects)).Msg("✓ 抓取成功")
			results[idx] = model.CategoryData{Name: name, Data: data.Subjects}
		}(i, cat.name, cat.tag)
	}

	wg.Wait()
	results = h.doubanService.SyncCategoryDataImages(ctx, results)

	// Cache result (1 hour)
	h.cache.Set(ctx, tvCacheKey, results)

	totalItems := 0
	for _, r := range results {
		totalItems += len(r.Data)
	}

	log.Info().Msg("✅ 电视剧分类数据获取成功")

	c.JSON(http.StatusOK, gin.H{
		"code":            200,
		"data":            results,
		"source":          "fresh",
		"totalCategories": len(results),
		"totalItems":      totalItems,
	})
}

// DeleteTVCache clears TV cache
// DELETE /api/v1/tv
func (h *TVHandler) DeleteTVCache(c *gin.Context) {
	ctx := context.Background()
	h.cache.Delete(ctx, tvCacheKey)

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "电视剧缓存已清除",
	})
}
