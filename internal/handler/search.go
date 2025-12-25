package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// SearchHandler handles search API requests
type SearchHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewSearchHandler creates a new SearchHandler
func NewSearchHandler(douban *service.DoubanService, cache *repository.Cache) *SearchHandler {
	return &SearchHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// Search handles search requests
// GET /api/v1/search?q=关键词&type=movie
func (h *SearchHandler) Search(c *gin.Context) {
	ctx := context.Background()

	query := c.Query("q")
	typ := c.Query("type")
	sort := c.DefaultQuery("sort", "U")
	genres := c.Query("genres")
	yearRange := c.Query("year_range")
	start, _ := strconv.Atoi(c.DefaultQuery("start", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	if query == "" {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "缺少搜索关键词参数 q",
		})
		return
	}

	// Build cache key
	cacheKey := fmt.Sprintf("douban:search:%s:%s:%s:%s:%s:%d:%d",
		query, typ, sort, genres, yearRange, start, limit)

	// Check cache
	var cachedData model.SearchResult
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().Str("query", query).Msg("🔍 搜索豆瓣")

	var suggestResult []model.SuggestItem
	var advancedResult []model.Subject

	var wg sync.WaitGroup
	wg.Add(2)

	// Get search suggestions
	go func() {
		defer wg.Done()
		suggestResult, _ = h.doubanService.GetSubjectSuggest(query)

		// Filter by type if specified
		if typ != "" {
			var filtered []model.SuggestItem
			for _, item := range suggestResult {
				if typ == "movie" && item.Type == "movie" {
					filtered = append(filtered, item)
				} else if typ == "tv" && item.Type == "tv" {
					filtered = append(filtered, item)
				}
			}
			suggestResult = filtered
		}
	}()

	// Get advanced search results if type is specified
	go func() {
		defer wg.Done()
		if typ != "" {
			tags := "电影"
			if typ == "tv" {
				tags = "电视剧"
			}
			advancedResult, _ = h.doubanService.AdvancedSearch(tags, sort, genres, yearRange, start, limit)
		}
	}()

	wg.Wait()

	result := model.SearchResult{
		Suggest:  suggestResult,
		Advanced: advancedResult,
	}

	// Cache result
	h.cache.Set(ctx, cacheKey, result)

	log.Info().
		Int("suggest", len(suggestResult)).
		Int("advanced", len(advancedResult)).
		Msg("✅ 搜索完成")

	c.JSON(http.StatusOK, gin.H{
		"code":   200,
		"data":   result,
		"source": "fresh",
		"query":  query,
		"type":   typ,
	})
}

// GetSearchTags returns available search tags
// POST /api/v1/search (body: { type: "movie" | "tv" })
func (h *SearchHandler) GetSearchTags(c *gin.Context) {
	ctx := context.Background()

	var body struct {
		Type string `json:"type"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "无效的请求体",
		})
		return
	}

	if body.Type != "movie" && body.Type != "tv" {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "无效的类型参数，必须是 movie 或 tv",
		})
		return
	}

	cacheKey := "douban:tags:" + body.Type

	// Check cache
	var cachedTags []string
	if err := h.cache.Get(ctx, cacheKey, &cachedTags); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedTags,
			Source: "redis-cache",
		})
		return
	}

	tags, _ := h.doubanService.GetSearchTags(body.Type)

	// Cache result (24 hours - tags rarely change)
	h.cache.Set(ctx, cacheKey, tags)

	c.JSON(http.StatusOK, gin.H{
		"code":   200,
		"data":   tags,
		"source": "fresh",
		"type":   body.Type,
	})
}

// DeleteSearchCache clears all search cache
// DELETE /api/v1/search
func (h *SearchHandler) DeleteSearchCache(c *gin.Context) {
	ctx := context.Background()

	// Delete search results cache
	searchDeleted, _ := h.cache.DeletePattern(ctx, "douban:search:*")
	// Delete tags cache
	tagsDeleted, _ := h.cache.DeletePattern(ctx, "douban:tags:*")

	total := searchDeleted + tagsDeleted

	log.Info().
		Int64("search", searchDeleted).
		Int64("tags", tagsDeleted).
		Msg("🗑️ 搜索缓存已清除")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: fmt.Sprintf("搜索缓存已清除 (%d 条)", total),
	})
}
