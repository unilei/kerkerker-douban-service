package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const heroDataCacheKey = "douban:hero:movies"
const defaultRequestTimeout = 30 * time.Second

// HeroHandler handles Hero Banner API requests
type HeroHandler struct {
	doubanService *service.DoubanService
	tmdbService   *service.TMDBService
	cache         *repository.Cache
	cacheTTL      time.Duration
}

// NewHeroHandler creates a new HeroHandler
func NewHeroHandler(douban *service.DoubanService, tmdb *service.TMDBService, cache *repository.Cache, cacheTTL time.Duration) *HeroHandler {
	return &HeroHandler{
		doubanService: douban,
		tmdbService:   tmdb,
		cache:         cache,
		cacheTTL:      cacheTTL,
	}
}

// GetHero returns Hero Banner data
// GET /api/v1/hero
func (h *HeroHandler) GetHero(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), defaultRequestTimeout)
	defer cancel()

	// Check cache
	var cachedData []model.HeroMovie
	if err := h.cache.Get(ctx, heroDataCacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache") // 标记缓存命中供 metrics 追踪
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	proxyInfo := ""
	if h.doubanService.HasProxy() {
		proxyInfo = fmt.Sprintf(" (代理: %d个)", h.doubanService.ProxyCount())
	}
	log.Info().Str("proxy", proxyInfo).Msg("🎬 开始获取 Hero Banner 数据...")

	// Fetch hot movies from Douban
	data, err := h.doubanService.SearchSubjects("", "热门", 20, 0)
	if err != nil || len(data.Subjects) == 0 {
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: "未获取到电影数据",
		})
		return
	}

	// Sort by rating and get top 5
	subjects := data.Subjects
	sort.Slice(subjects, func(i, j int) bool {
		rateI := parseFloat(subjects[i].Rate)
		rateJ := parseFloat(subjects[j].Rate)
		return rateI > rateJ
	})

	const maxHeroCount = 5
	if len(subjects) > maxHeroCount {
		subjects = subjects[:maxHeroCount]
	}

	// 优化1: 使用带索引的结果槽位，保持评分排序顺序
	type heroResult struct {
		index int
		hero  *model.HeroMovie
	}

	resultChan := make(chan heroResult, len(subjects))
	var wg sync.WaitGroup

	// 优化2: 为每个 goroutine 创建子 context，控制单个请求超时
	perMovieTimeout := 10 * time.Second

	for idx, movie := range subjects {
		wg.Add(1)
		go func(index int, m model.Subject) {
			defer wg.Done()

			// 为单个电影的请求创建超时 context
			movieCtx, movieCancel := context.WithTimeout(ctx, perMovieTimeout)
			defer movieCancel()

			// Get movie details for genres
			var genres []string
			var description string
			var releaseYear string

			// 使用 channel 接收详情结果，实现超时控制
			detailDone := make(chan struct{})
			go func() {
				if detail, err := h.doubanService.GetSubjectAbstract(m.ID); err == nil && detail.Subject != nil {
					genres = detail.Subject.Types
					releaseYear = detail.Subject.ReleaseYear
					if detail.Subject.ShortComment != nil {
						description = detail.Subject.ShortComment.Content
					}
				}
				close(detailDone)
			}()

			select {
			case <-detailDone:
				// 详情获取成功
			case <-movieCtx.Done():
				log.Debug().Str("title", m.Title).Msg("⏱️ 获取详情超时")
			}

			// Get TMDB backdrop
			var backdropURL string
			if h.tmdbService.IsConfigured() {
				tmdbDone := make(chan struct{})
				go func() {
					backdropURL, _ = h.tmdbService.SearchMovieBackdrop(m.Title, releaseYear)
					close(tmdbDone)
				}()

				select {
				case <-tmdbDone:
					// TMDB 请求完成
				case <-movieCtx.Done():
					log.Debug().Str("title", m.Title).Msg("⏱️ TMDB 请求超时")
				}
			}

			// Convert cover to high quality
			cover := getHighQualityPoster(m.Cover)

			// 优化3: 降级策略 - 无 backdrop 时使用封面
			posterHorizontal := backdropURL
			if posterHorizontal == "" {
				posterHorizontal = cover // 使用封面作为备选
				log.Debug().Str("title", m.Title).Msg("📸 使用封面作为横幅备选")
			}

			hero := &model.HeroMovie{
				ID:               m.ID,
				Title:            m.Title,
				Rate:             m.Rate,
				Cover:            cover,
				PosterHorizontal: posterHorizontal,
				PosterVertical:   cover,
				URL:              m.URL,
				EpisodeInfo:      m.EpisodeInfo,
				Genres:           genres,
				Description:      description,
			}

			resultChan <- heroResult{index: index, hero: hero}
		}(idx, movie)
	}

	// 等待所有 goroutine 完成
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果并按原始索引排序
	results := make([]*model.HeroMovie, len(subjects))
	for result := range resultChan {
		results[result.index] = result.hero
	}

	// 过滤掉 nil 结果并转换为 slice
	heroMovies := make([]model.HeroMovie, 0, len(subjects))
	for _, hero := range results {
		if hero != nil {
			heroMovies = append(heroMovies, *hero)
		}
	}

	// Cache the result
	if len(heroMovies) > 0 {
		h.cache.Set(ctx, heroDataCacheKey, heroMovies, h.cacheTTL)
	}

	log.Info().Int("count", len(heroMovies)).Msg("✅ Hero Banner 数据获取成功")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:   200,
		Data:   heroMovies,
		Source: "fresh",
	})
}

// DeleteHeroCache clears Hero Banner cache
// DELETE /api/v1/hero
func (h *HeroHandler) DeleteHeroCache(c *gin.Context) {
	ctx := context.Background()
	h.cache.Delete(ctx, heroDataCacheKey)

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "Hero Banner 缓存已清除",
	})
}

// getHighQualityPoster converts Douban small poster to large
func getHighQualityPoster(url string) string {
	if url == "" {
		return url
	}
	return strings.Replace(url, "/view/photo/s_ratio_poster/", "/view/photo/l/", 1)
}

// parseFloat parses a string to float64
func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	result, _ := strconv.ParseFloat(s, 64)
	return result
}
