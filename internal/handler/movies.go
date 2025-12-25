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

const moviesCacheKey = "douban:movies:all"

// MoviesHandler handles movies API requests
type MoviesHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewMoviesHandler creates a new MoviesHandler
func NewMoviesHandler(douban *service.DoubanService, cache *repository.Cache) *MoviesHandler {
	return &MoviesHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// GetMovies returns movie categories data
// GET /api/v1/movies
func (h *MoviesHandler) GetMovies(c *gin.Context) {
	ctx := context.Background()

	// Check cache
	var cachedData []model.CategoryData
	if err := h.cache.Get(ctx, moviesCacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().Msg("🎬 开始获取电影分类数据...")

	categories := []struct {
		name string
		tag  string
	}{
		{"热门电影", "热门"},
		{"豆瓣高分", "豆瓣高分"},
		{"动作片", "动作"},
		{"喜剧片", "喜剧"},
		{"科幻片", "科幻"},
		{"惊悚片", "惊悚"},
		{"爱情片", "爱情"},
		{"动画电影", "动画"},
	}

	results := make([]model.CategoryData, len(categories))
	var wg sync.WaitGroup

	for i, cat := range categories {
		wg.Add(1)
		go func(idx int, name, tag string) {
			defer wg.Done()
			data, err := h.doubanService.SearchSubjects("movie", tag, 24, 0)
			if err != nil {
				log.Warn().Err(err).Str("tag", tag).Msg("Failed to fetch movies")
				results[idx] = model.CategoryData{Name: name, Data: []model.Subject{}}
				return
			}
			log.Debug().Str("tag", tag).Int("count", len(data.Subjects)).Msg("✓ 抓取成功")
			results[idx] = model.CategoryData{Name: name, Data: data.Subjects}
		}(i, cat.name, cat.tag)
	}

	wg.Wait()

	// Cache result (1 hour)
	h.cache.Set(ctx, moviesCacheKey, results)

	totalItems := 0
	for _, r := range results {
		totalItems += len(r.Data)
	}

	log.Info().Msg("✅ 电影分类数据获取成功")

	c.JSON(http.StatusOK, gin.H{
		"code":            200,
		"data":            results,
		"source":          "fresh",
		"totalCategories": len(results),
		"totalItems":      totalItems,
	})
}

// DeleteMoviesCache clears movies cache
// DELETE /api/v1/movies
func (h *MoviesHandler) DeleteMoviesCache(c *gin.Context) {
	ctx := context.Background()
	h.cache.Delete(ctx, moviesCacheKey)

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "电影缓存已清除",
	})
}
