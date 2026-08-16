package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// DetailHandler handles detail API requests
type DetailHandler struct {
	doubanService *service.DoubanService
	cache         *repository.Cache
}

// NewDetailHandler creates a new DetailHandler
func NewDetailHandler(douban *service.DoubanService, cache *repository.Cache) *DetailHandler {
	return &DetailHandler{
		doubanService: douban,
		cache:         cache,
	}
}

// GetDetail returns movie/TV show details
// GET /api/v1/detail/:id
func (h *DetailHandler) GetDetail(c *gin.Context) {
	ctx := context.Background()
	id := c.Param("id")

	if id == "" {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "缺少豆瓣ID",
		})
		return
	}

	cacheKey := "douban:detail:" + id

	// 1) Redis 热缓存
	var cachedData model.SubjectDetail
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		cachedData = h.doubanService.SyncSubjectDetailImages(ctx, cachedData)
		if h.doubanService.ImageSyncEnabled() {
			h.cache.SetKeepTTL(ctx, cacheKey, cachedData)
		}
		c.Set("cache_source", "redis-cache")
		c.JSON(http.StatusOK, buildDetailResponse(cachedData, "redis-cache"))
		return
	}

	// 2) MongoDB 持久层（若配置）。命中则填充 Redis 后返回，无需回源豆瓣。
	store := h.doubanService.MovieStore()
	if store != nil {
		movie, err := store.GetByDoubanID(ctx, id)
		if err == nil && movie != nil && movie.Detail != nil {
			detailData := h.doubanService.SyncSubjectDetailImages(ctx, *movie.Detail)
			detailData.InternalID = movie.InternalID
			// 回填 Redis，下次直接命中热缓存。
			if err := h.cache.Set(ctx, cacheKey, detailData); err != nil {
				log.Warn().Err(err).Str("id", id).Msg("Failed to backfill Redis from Mongo")
			}
			c.Set("cache_source", "mongo-store")
			c.JSON(http.StatusOK, buildDetailResponse(detailData, "mongo-store"))
			return
		}
		if err != nil && err != repository.ErrMovieNotFound {
			log.Warn().Err(err).Str("id", id).Msg("Mongo lookup failed, falling back to Douban")
		}
	}

	// 3) 回源豆瓣
	detailData, ok := h.fetchDetailFromDouban(ctx, id)
	if !ok {
		c.JSON(http.StatusNotFound, model.APIResponse{
			Code:  404,
			Error: "未找到该影片信息",
		})
		return
	}

	detailData = h.doubanService.SyncSubjectDetailImages(ctx, detailData)

	// 写入 MongoDB 持久层（失败不阻断响应）；Upsert 会分配 internal_id。
	// 必须先于 Redis 写入，否则缓存里 internal_id 恒为 0。
	if store != nil {
		movie := &repository.Movie{
			DoubanID:      id,
			Title:         detailData.Title,
			Rate:          detailData.Rate,
			Cover:         detailData.Cover,
			URL:           detailData.URL,
			Detail:        &detailData,
			RefreshStatus: repository.RefreshStatusFresh,
		}
		if err := store.Upsert(ctx, movie); err != nil {
			log.Warn().Err(err).Str("id", id).Msg("Failed to persist movie to Mongo")
		} else {
			detailData.InternalID = movie.InternalID
		}
	}

	// 写入 Redis 热缓存（此时 internal_id 已填充）
	h.cache.Set(ctx, cacheKey, detailData)

	c.JSON(http.StatusOK, buildDetailResponse(detailData, "fresh"))
}

// fetchDetailFromDouban 委托给 service 层的共享实现，避免与刷新任务重复抓取逻辑。
func (h *DetailHandler) fetchDetailFromDouban(ctx context.Context, id string) (model.SubjectDetail, bool) {
	return h.doubanService.FetchDetail(ctx, id)
}

// GetMovieByInternalID returns movie details by our internal numeric ID.
// GET /api/v1/movies/:internal_id
//
// 内部 ID 是我们自己分配的，只有落库过的影片才有；未命中直接返回 404，
// 不回源豆瓣（豆瓣只认它自己的 ID）。
func (h *DetailHandler) GetMovieByInternalID(c *gin.Context) {
	ctx := context.Background()
	rawID := strings.TrimSpace(c.Param("internal_id"))
	if rawID == "" {
		c.JSON(http.StatusBadRequest, model.APIResponse{Code: 400, Error: "缺少 internal_id"})
		return
	}

	internalID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || internalID <= 0 {
		c.JSON(http.StatusBadRequest, model.APIResponse{Code: 400, Error: "internal_id 必须为正整数"})
		return
	}

	store := h.doubanService.MovieStore()
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, model.APIResponse{
			Code:  503,
			Error: "持久层未启用，无法按 internal_id 查询",
		})
		return
	}

	movie, err := store.GetByInternalID(ctx, internalID)
	if err != nil {
		c.JSON(http.StatusNotFound, model.APIResponse{
			Code:  404,
			Error: "未找到该影片（internal_id=" + rawID + "）",
		})
		return
	}

	detailData := model.SubjectDetail{}
	if movie.Detail != nil {
		detailData = *movie.Detail
	} else {
		detailData.ID = movie.DoubanID
		detailData.Title = movie.Title
		detailData.Rate = movie.Rate
		detailData.Cover = movie.Cover
		detailData.URL = movie.URL
	}
	detailData.InternalID = movie.InternalID
	detailData = h.doubanService.SyncSubjectDetailImages(ctx, detailData)

	// 回填 Redis 热缓存，下次按豆瓣 ID 查也能命中。
	if movie.DoubanID != "" {
		_ = h.cache.Set(ctx, "douban:detail:"+movie.DoubanID, detailData)
	}

	c.Set("cache_source", "mongo-store")
	c.JSON(http.StatusOK, buildDetailResponse(detailData, "mongo-store"))
}

// DeleteDetailCache clears detail cache
// DELETE /api/v1/detail/:id
func (h *DetailHandler) DeleteDetailCache(c *gin.Context) {
	ctx := context.Background()
	id := c.Param("id")

	if id == "" {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "缺少豆瓣ID",
		})
		return
	}

	cacheKey := "douban:detail:" + id
	h.cache.Delete(ctx, cacheKey)

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "影片 " + id + " 的缓存已清除",
	})
}

// DeleteAllDetailCache clears all detail cache
// DELETE /api/v1/detail
func (h *DetailHandler) DeleteAllDetailCache(c *gin.Context) {
	ctx := context.Background()

	deleted, err := h.cache.DeletePattern(ctx, "douban:detail:*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: fmt.Sprintf("影片详情缓存已清除 (%d 条)", deleted),
	})
}

// buildDetailResponse creates a standardized response for detail data
func buildDetailResponse(data model.SubjectDetail, source string) gin.H {
	return gin.H{
		"id":              data.ID,
		"internal_id":     data.InternalID,
		"title":           data.Title,
		"rate":            data.Rate,
		"url":             data.URL,
		"cover":           data.Cover,
		"types":           data.Types,
		"release_year":    data.ReleaseYear,
		"directors":       data.Directors,
		"actors":          data.Actors,
		"duration":        data.Duration,
		"region":          data.Region,
		"episodes_count":  data.EpisodesCount,
		"short_comment":   data.ShortComment,
		"photos":          data.Photos,
		"comments":        data.Comments,
		"recommendations": data.Recommendations,
		"source":          source,
	}
}
