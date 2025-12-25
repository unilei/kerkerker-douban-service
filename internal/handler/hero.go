package handler

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const heroDataCacheKey = "douban:hero:movies"

// HeroHandler handles Hero Banner API requests
type HeroHandler struct {
	doubanService *service.DoubanService
	tmdbService   *service.TMDBService
	cache         *repository.Cache
}

// NewHeroHandler creates a new HeroHandler
func NewHeroHandler(douban *service.DoubanService, tmdb *service.TMDBService, cache *repository.Cache) *HeroHandler {
	return &HeroHandler{
		doubanService: douban,
		tmdbService:   tmdb,
		cache:         cache,
	}
}

// GetHero returns Hero Banner data
// GET /api/v1/hero
func (h *HeroHandler) GetHero(c *gin.Context) {
	ctx := context.Background()

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
		proxyInfo = " (代理: " + string(rune(h.doubanService.ProxyCount())) + "个)"
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

	if len(subjects) > 5 {
		subjects = subjects[:5]
	}

	// Fetch TMDB backdrops in parallel
	heroMovies := make([]model.HeroMovie, 0, len(subjects))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, movie := range subjects {
		wg.Add(1)
		go func(m model.Subject) {
			defer wg.Done()

			// Get movie details for genres
			var genres []string
			var description string
			var releaseYear string

			if detail, err := h.doubanService.GetSubjectAbstract(m.ID); err == nil && detail.Subject != nil {
				genres = detail.Subject.Types
				releaseYear = detail.Subject.ReleaseYear
				if detail.Subject.ShortComment != nil {
					description = detail.Subject.ShortComment.Content
				}
			}

			// Get TMDB backdrop
			var backdropURL string
			if h.tmdbService.IsConfigured() {
				backdropURL, _ = h.tmdbService.SearchMovieBackdrop(m.Title, releaseYear)
			}

			// Skip if no backdrop
			if backdropURL == "" {
				log.Debug().Str("title", m.Title).Msg("Skipping - no TMDB backdrop")
				return
			}

			// Convert cover to high quality
			cover := getHighQualityPoster(m.Cover)

			hero := model.HeroMovie{
				ID:               m.ID,
				Title:            m.Title,
				Rate:             m.Rate,
				Cover:            cover,
				PosterHorizontal: backdropURL,
				PosterVertical:   cover,
				URL:              m.URL,
				EpisodeInfo:      m.EpisodeInfo,
				Genres:           genres,
				Description:      description,
			}

			mu.Lock()
			heroMovies = append(heroMovies, hero)
			mu.Unlock()
		}(movie)
	}

	wg.Wait()

	// Cache the result
	if len(heroMovies) > 0 {
		h.cache.Set(ctx, heroDataCacheKey, heroMovies)
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
	var result float64
	for i, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + float64(c-'0')
		} else if c == '.' {
			// Handle decimal part
			decimal := 0.1
			for _, d := range s[i+1:] {
				if d >= '0' && d <= '9' {
					result += float64(d-'0') * decimal
					decimal /= 10
				}
			}
			break
		}
	}
	return result
}
