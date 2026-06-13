package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// NewHandler handles new content API requests
type NewHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewNewHandler creates a new NewHandler
func NewNewHandler(douban *service.DoubanService, cache *repository.Cache) *NewHandler {
	return &NewHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// RegionTagMap maps region filter to search tags
var regionTagMap = map[string]string{
	"大陆": "国产",
	"香港": "港剧",
	"台湾": "台剧",
	"美国": "美剧",
	"韩国": "韩剧",
	"日本": "日剧",
	"英国": "英剧",
}

// GetNew returns new content with optional filters
// GET /api/v1/new?type=movie&year=2024&region=美国&genre=动作&sort=recommend
func (h *NewHandler) GetNew(c *gin.Context) {
	ctx := context.Background()

	typ := c.Query("type")
	year := c.Query("year")
	region := c.Query("region")
	genre := c.Query("genre")
	sort := c.DefaultQuery("sort", "recommend")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "30"))

	hasFilters := typ != "" || year != "" || region != "" || genre != ""

	// Build cache key
	var cacheKey string
	if hasFilters {
		cacheKey = fmt.Sprintf("douban:new:%s:%s:%s:%s:%s", typ, year, region, genre, sort)
	} else {
		cacheKey = "douban:new:all"
	}

	// Check cache
	var cachedData []model.CategoryData
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		cachedData = h.doubanService.SyncCategoryDataImages(ctx, cachedData)
		if h.doubanService.ImageSyncEnabled() {
			h.cache.SetKeepTTL(ctx, cacheKey, cachedData)
		}
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"data":    cachedData,
			"source":  "redis-cache",
			"filters": gin.H{"type": typ, "year": year, "region": region, "genre": genre, "sort": sort},
		})
		return
	}

	log.Info().
		Str("type", typ).
		Str("year", year).
		Str("region", region).
		Str("genre", genre).
		Msg("🚀 开始抓取豆瓣数据...")

	var resultData []model.CategoryData

	if hasFilters {
		// With filters - use tag search
		subjects, total, hasMore := h.fetchWithTagSearch(typ, year, region, genre, sort, page, pageSize)
		subjects = h.doubanService.SyncSubjectImages(ctx, subjects)

		resultData = []model.CategoryData{{
			Name: buildCategoryName(typ, year, region, genre),
			Data: subjects,
		}}

		c.JSON(http.StatusOK, gin.H{
			"code":    200,
			"data":    resultData,
			"source":  "fresh-data",
			"filters": gin.H{"type": typ, "year": year, "region": region, "genre": genre, "sort": sort},
			"pagination": gin.H{
				"page":     page,
				"pageSize": pageSize,
				"total":    total,
				"hasMore":  hasMore,
			},
		})
		return
	}

	// No filters - return default categories
	categories := []struct {
		name string
		typ  string
		tag  string
	}{
		{"豆瓣热映", "", "热门"},
		{"热门电视", "tv", "热门"},
		{"国产剧", "tv", "国产剧"},
		{"综艺", "tv", "综艺"},
		{"美剧", "tv", "美剧"},
		{"日剧", "tv", "日剧"},
		{"韩剧", "tv", "韩剧"},
		{"日本动画", "tv", "日本动画"},
		{"纪录片", "tv", "纪录片"},
	}

	results := make([]model.CategoryData, len(categories))
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
				results[idx] = model.CategoryData{Name: c.name, Data: []model.Subject{}}
				return
			}
			results[idx] = model.CategoryData{Name: c.name, Data: data.Subjects}
		}(i, cat)
	}

	wg.Wait()
	resultData = results
	resultData = h.doubanService.SyncCategoryDataImages(ctx, resultData)

	// Cache result
	h.cache.Set(ctx, cacheKey, resultData)

	totalItems := 0
	for _, r := range resultData {
		totalItems += len(r.Data)
	}

	log.Info().Msg("✅ 豆瓣数据抓取成功")

	c.JSON(http.StatusOK, gin.H{
		"code":            200,
		"data":            resultData,
		"source":          "fresh-data",
		"filters":         gin.H{"type": typ, "year": year, "region": region, "genre": genre, "sort": sort},
		"totalCategories": len(resultData),
		"totalItems":      totalItems,
	})
}

// fetchWithTagSearch fetches data with tag search
func (h *NewHandler) fetchWithTagSearch(typ, year, region, genre, sort string, page, pageSize int) ([]model.Subject, int, bool) {
	tag := "热门"
	searchType := "movie"
	if typ == "tv" {
		searchType = "tv"
	}

	// Determine best tag based on filters
	if genre != "" {
		tag = genre
	} else if region != "" {
		if searchType == "tv" {
			if mapped, ok := regionTagMap[region]; ok {
				tag = mapped
			} else {
				tag = region
			}
		} else {
			tag = region
		}
	} else if year != "" {
		tag = year
	} else if sort == "rank" {
		tag = "高分"
	} else if sort == "time" {
		tag = "最新"
	}

	start := (page - 1) * pageSize

	data, err := h.doubanService.SearchSubjects(searchType, tag, pageSize, start)
	if err != nil {
		log.Warn().Err(err).Str("tag", tag).Msg("Tag search failed")
		return []model.Subject{}, 0, false
	}

	subjects := data.Subjects
	total := len(subjects)
	if len(subjects) >= pageSize {
		total = page*pageSize + pageSize
	} else {
		total = (page-1)*pageSize + len(subjects)
	}

	return subjects, total, len(subjects) >= pageSize
}

// buildCategoryName builds category name from filters
func buildCategoryName(typ, year, region, genre string) string {
	var parts []string

	if year != "" {
		parts = append(parts, year)
	}
	if region != "" {
		parts = append(parts, region)
	}
	if genre != "" {
		parts = append(parts, genre)
	}
	if typ == "movie" {
		parts = append(parts, "电影")
	} else if typ == "tv" {
		parts = append(parts, "电视剧")
	}

	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	return "热门"
}

// DeleteNewCache clears new content cache
// DELETE /api/v1/new
func (h *NewHandler) DeleteNewCache(c *gin.Context) {
	ctx := context.Background()
	h.cache.Delete(ctx, "douban:new:all")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "新上线缓存已清除（筛选缓存将自动过期）",
	})
}
