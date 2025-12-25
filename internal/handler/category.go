package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// Category tag mapping
var categoryTagMap = map[string]struct {
	Tag  string
	Type string
}{
	"in_theaters": {Tag: "热门", Type: ""},
	"hot_movies":  {Tag: "热门", Type: "movie"},
	"hot_tv":      {Tag: "热门", Type: "tv"},
	"us_tv":       {Tag: "美剧", Type: "tv"},
	"jp_tv":       {Tag: "日剧", Type: "tv"},
	"kr_tv":       {Tag: "韩剧", Type: "tv"},
	"anime":       {Tag: "日本动画", Type: "tv"},
	"documentary": {Tag: "纪录片", Type: "tv"},
	"variety":     {Tag: "综艺", Type: "tv"},
	"chinese_tv":  {Tag: "国产剧", Type: "tv"},
}

// CategoryHandler handles category API requests
type CategoryHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewCategoryHandler creates a new CategoryHandler
func NewCategoryHandler(douban *service.DoubanService, cache *repository.Cache) *CategoryHandler {
	return &CategoryHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// GetCategory returns paginated category data
// GET /api/v1/category?category=hot_movies&page=1&limit=20
func (h *CategoryHandler) GetCategory(c *gin.Context) {
	ctx := context.Background()

	category := c.DefaultQuery("category", "in_theaters")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// Validate parameters
	if page < 1 {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "页码必须大于0",
		})
		return
	}

	if limit < 1 || limit > 50 {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "每页数量必须在1-50之间",
		})
		return
	}

	// Get category config
	config, ok := categoryTagMap[category]
	if !ok {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "无效的分类类型",
		})
		return
	}

	pageStart := (page - 1) * limit
	cacheKey := fmt.Sprintf("douban:category:%s:page%d:limit%d", category, page, limit)

	// Check cache
	var cachedData struct {
		Subjects []model.Subject `json:"subjects"`
		Total    int             `json:"total"`
	}
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code: 200,
			Data: gin.H{
				"subjects": cachedData.Subjects,
				"pagination": model.Pagination{
					Page:    page,
					Limit:   limit,
					Total:   cachedData.Total,
					HasMore: pageStart+len(cachedData.Subjects) < cachedData.Total,
				},
			},
			Source: "redis-cache",
		})
		return
	}

	log.Info().
		Str("category", category).
		Int("page", page).
		Int("limit", limit).
		Msg("🔍 分页获取分类数据")

	// Fetch data
	data, err := h.doubanService.SearchSubjects(config.Type, config.Tag, limit, pageStart)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: err.Error(),
		})
		return
	}

	subjects := data.Subjects
	estimatedTotal := 100
	if len(subjects) < limit {
		estimatedTotal = pageStart + len(subjects)
	}

	log.Info().
		Str("category", category).
		Int("page", page).
		Int("count", len(subjects)).
		Msg("✓ 分页获取成功")

	// Cache result
	h.cache.Set(ctx, cacheKey, struct {
		Subjects []model.Subject `json:"subjects"`
		Total    int             `json:"total"`
	}{
		Subjects: subjects,
		Total:    estimatedTotal,
	})

	c.JSON(http.StatusOK, model.APIResponse{
		Code: 200,
		Data: gin.H{
			"subjects": subjects,
			"pagination": model.Pagination{
				Page:    page,
				Limit:   limit,
				Total:   estimatedTotal,
				HasMore: len(subjects) == limit,
			},
		},
		Source: "fresh-data",
	})
}

// DeleteCategoryCache clears all category cache
// DELETE /api/v1/category
func (h *CategoryHandler) DeleteCategoryCache(c *gin.Context) {
	ctx := context.Background()

	deleted, err := h.cache.DeletePattern(ctx, "douban:category:*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: err.Error(),
		})
		return
	}

	log.Info().Int64("deleted", deleted).Msg("🗑️ 分类分页缓存已清除")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: fmt.Sprintf("分类分页缓存已清除 (%d 条)", deleted),
	})
}
