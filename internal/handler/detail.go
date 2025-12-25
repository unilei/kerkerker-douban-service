package handler

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
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

	// Check cache
	var cachedData model.SubjectDetail
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, gin.H{
			"id":              cachedData.ID,
			"title":           cachedData.Title,
			"rate":            cachedData.Rate,
			"url":             cachedData.URL,
			"cover":           cachedData.Cover,
			"types":           cachedData.Types,
			"release_year":    cachedData.ReleaseYear,
			"directors":       cachedData.Directors,
			"actors":          cachedData.Actors,
			"duration":        cachedData.Duration,
			"region":          cachedData.Region,
			"episodes_count":  cachedData.EpisodesCount,
			"short_comment":   cachedData.ShortComment,
			"photos":          cachedData.Photos,
			"comments":        cachedData.Comments,
			"recommendations": cachedData.Recommendations,
			"source":          "redis-cache",
		})
		return
	}

	// Get abstract
	detail, err := h.doubanService.GetSubjectAbstract(id)
	if err != nil || detail.Subject == nil {
		c.JSON(http.StatusNotFound, model.APIResponse{
			Code:  404,
			Error: "未找到该影片信息",
		})
		return
	}

	// Extract search query from title
	title := detail.Subject.Title
	searchQuery := cleanTitleForSearch(title)

	// Fetch additional data in parallel
	var cover string
	var photos []model.Photo
	var comments []model.Comment
	var recommendations []model.Subject

	var wg sync.WaitGroup
	wg.Add(4)

	// Get cover from suggest
	go func() {
		defer wg.Done()
		if searchQuery != "" {
			if suggestions, err := h.doubanService.GetSubjectSuggest(searchQuery); err == nil {
				for _, s := range suggestions {
					if s.ID == id {
						cover = s.Img
						break
					}
				}
			}
		}
	}()

	// Get photos
	go func() {
		defer wg.Done()
		photos, _ = h.doubanService.GetPhotos(id, 6, "S")
	}()

	// Get comments
	go func() {
		defer wg.Done()
		comments, _ = h.doubanService.GetComments(id, 5)
	}()

	// Get recommendations
	go func() {
		defer wg.Done()
		recommendations, _ = h.doubanService.GetRecommendations(id)
		if len(recommendations) > 6 {
			recommendations = recommendations[:6]
		}
	}()

	wg.Wait()

	// Build response
	var shortComment *model.Comment
	if detail.Subject.ShortComment != nil {
		shortComment = &model.Comment{
			Content: detail.Subject.ShortComment.Content,
			Author: model.CommentAuthor{
				Name: detail.Subject.ShortComment.Author,
			},
		}
	}

	detailData := model.SubjectDetail{
		ID:              detail.Subject.ID,
		Title:           detail.Subject.Title,
		Rate:            detail.Subject.Rate,
		URL:             detail.Subject.URL,
		Cover:           cover,
		Types:           detail.Subject.Types,
		ReleaseYear:     detail.Subject.ReleaseYear,
		Directors:       detail.Subject.Directors,
		Actors:          detail.Subject.Actors,
		Duration:        detail.Subject.Duration,
		Region:          detail.Subject.Region,
		EpisodesCount:   detail.Subject.EpisodesCount,
		ShortComment:    shortComment,
		Photos:          photos,
		Comments:        comments,
		Recommendations: recommendations,
	}

	// Cache result
	h.cache.Set(ctx, cacheKey, detailData)

	c.JSON(http.StatusOK, gin.H{
		"id":              detailData.ID,
		"title":           detailData.Title,
		"rate":            detailData.Rate,
		"url":             detailData.URL,
		"cover":           detailData.Cover,
		"types":           detailData.Types,
		"release_year":    detailData.ReleaseYear,
		"directors":       detailData.Directors,
		"actors":          detailData.Actors,
		"duration":        detailData.Duration,
		"region":          detailData.Region,
		"episodes_count":  detailData.EpisodesCount,
		"short_comment":   detailData.ShortComment,
		"photos":          detailData.Photos,
		"comments":        detailData.Comments,
		"recommendations": detailData.Recommendations,
		"source":          "fresh",
	})
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

// cleanTitleForSearch extracts a clean title for searching
func cleanTitleForSearch(title string) string {
	// Remove Unicode control characters
	re := regexp.MustCompile(`[\x{200B}-\x{200F}\x{2028}-\x{202F}\x{FEFF}]`)
	cleaned := re.ReplaceAllString(title, "")

	// Remove year in parentheses
	reYear := regexp.MustCompile(`\s*[\(（]\d{4}[\)）]\s*`)
	cleaned = reYear.ReplaceAllString(cleaned, "")

	// Get first part
	parts := strings.Fields(cleaned)
	if len(parts) > 0 {
		return strings.TrimSpace(parts[0])
	}

	return strings.TrimSpace(cleaned)
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
