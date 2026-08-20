package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	sort, sortProvided := c.GetQuery("sort")
	if strings.TrimSpace(sort) == "" {
		sort = "recommend"
	}
	page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, pageSizeErr := strconv.Atoi(c.DefaultQuery("pageSize", "30"))
	if pageErr != nil || page < 1 || page > 10_000 || pageSizeErr != nil || pageSize < 1 || pageSize > 100 {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  http.StatusBadRequest,
			Error: "page 必须为 1-10000，pageSize 必须为 1-100",
		})
		return
	}

	hasFilters := typ != "" || year != "" || region != "" || genre != "" || sortProvided

	// Build cache key
	var cacheKey string
	if hasFilters {
		cacheKey = newFilteredCacheKey(typ, year, region, genre, sort, page, pageSize)
	} else {
		cacheKey = "douban:new:all"
	}

	// Check cache
	if hasFilters {
		var cached newFilteredSnapshot
		if err := h.cache.Get(ctx, cacheKey, &cached); err == nil {
			cached.Data = h.doubanService.SyncCategoryDataImages(ctx, cached.Data)
			if h.doubanService.ImageSyncEnabled() {
				h.cache.SetKeepTTL(ctx, cacheKey, cached)
			}
			c.Set("cache_source", "redis-cache")
			h.respondFiltered(c, cached, "redis-cache", typ, year, region, genre, sort)
			return
		}
	} else {
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
	}

	// Mongo 快照兜底：Redis 过期后先从持久层取。
	// 过滤分支的快照包含分页信息（newFilteredSnapshot），无过滤分支只是 []CategoryData。
	snapshotStore := h.doubanService.SnapshotStore()
	if snapshotStore != nil {
		if hasFilters {
			var snap newFilteredSnapshot
			if err := snapshotStore.Load(ctx, cacheKey, &snap); err == nil {
				snap.Data = h.doubanService.SyncCategoryDataImages(ctx, snap.Data)
				if err := h.cache.Set(ctx, cacheKey, snap); err != nil {
					log.Warn().Err(err).Str("key", cacheKey).Msg("Failed to backfill Redis from Mongo snapshot")
				}
				c.Set("cache_source", "mongo-snapshot")
				h.respondFiltered(c, snap, "mongo-snapshot", typ, year, region, genre, sort)
				return
			}
		} else {
			var snapData []model.CategoryData
			if err := snapshotStore.Load(ctx, cacheKey, &snapData); err == nil {
				snapData = h.doubanService.SyncCategoryDataImages(ctx, snapData)
				if err := h.cache.Set(ctx, cacheKey, snapData); err != nil {
					log.Warn().Err(err).Str("key", cacheKey).Msg("Failed to backfill Redis from Mongo snapshot")
				}
				c.Set("cache_source", "mongo-snapshot")
				c.JSON(http.StatusOK, gin.H{
					"code":    200,
					"data":    snapData,
					"source":  "mongo-snapshot",
					"filters": gin.H{"type": typ, "year": year, "region": region, "genre": genre, "sort": sort},
				})
				return
			}
		}
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

		filteredSnap := newFilteredSnapshot{
			Data:       resultData,
			Pagination: newPagination{Page: page, PageSize: pageSize, Total: total, HasMore: hasMore},
		}
		// Redis 与 Mongo 保存相同的分页载荷，避免热缓存命中时丢失
		// pagination，也避免不同页共用一个缓存键。
		h.cache.Set(ctx, cacheKey, filteredSnap)
		if snapshotStore != nil {
			if err := snapshotStore.Store(ctx, cacheKey, filteredSnap); err != nil {
				log.Warn().Err(err).Str("key", cacheKey).Msg("Failed to persist new (filtered) snapshot")
			}
		}

		h.respondFiltered(c, filteredSnap, "fresh-data", typ, year, region, genre, sort)
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
	if snapshotStore != nil {
		if err := snapshotStore.Store(ctx, cacheKey, resultData); err != nil {
			log.Warn().Err(err).Str("key", cacheKey).Msg("Failed to persist new snapshot")
		}
	}

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

func newFilteredCacheKey(typ, year, region, genre, sort string, page, pageSize int) string {
	parts := []string{typ, year, region, genre, sort}
	for index := range parts {
		parts[index] = url.QueryEscape(parts[index])
	}
	return fmt.Sprintf(
		"douban:new:v2:%s:%s:%s:%s:%s:page:%d:size:%d",
		parts[0], parts[1], parts[2], parts[3], parts[4], page, pageSize,
	)
}

func (h *NewHandler) respondFiltered(c *gin.Context, payload newFilteredSnapshot, source, typ, year, region, genre, sort string) {
	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"data":    payload.Data,
		"source":  source,
		"filters": gin.H{"type": typ, "year": year, "region": region, "genre": genre, "sort": sort},
		"pagination": gin.H{
			"page":     payload.Pagination.Page,
			"pageSize": payload.Pagination.PageSize,
			"total":    payload.Pagination.Total,
			"hasMore":  payload.Pagination.HasMore,
		},
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

// newPagination 是筛选分支快照里保存的分页信息，与 fresh-data 响应的 pagination 字段一一对应。
type newPagination struct {
	Page     int  `json:"page" bson:"page"`
	PageSize int  `json:"pageSize" bson:"page_size"`
	Total    int  `json:"total" bson:"total"`
	HasMore  bool `json:"hasMore" bson:"has_more"`
}

// newFilteredSnapshot 是 /new 筛选分支在 Redis 与 Mongo 共用的载荷：
// 分类数据和分页信息必须一起缓存，所有缓存层才能保持同一响应契约。
type newFilteredSnapshot struct {
	Data       []model.CategoryData `json:"data" bson:"data"`
	Pagination newPagination        `json:"pagination" bson:"pagination"`
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
