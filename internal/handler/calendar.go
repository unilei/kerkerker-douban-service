package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const (
	calendarCacheKeyPrefix = "douban:calendar:"
	airingCacheKeyPrefix   = "douban:airing:"
	defaultCalendarDays    = 7
	maxCalendarDays        = 30
)

// CalendarHandler handles Calendar API requests
type CalendarHandler struct {
	tmdbService   *service.TMDBService
	doubanService *service.DoubanService
	cache         *repository.Cache
	cacheTTL      time.Duration
}

// NewCalendarHandler creates a new CalendarHandler
func NewCalendarHandler(tmdb *service.TMDBService, douban *service.DoubanService, cache *repository.Cache, cacheTTL time.Duration) *CalendarHandler {
	return &CalendarHandler{
		tmdbService:   tmdb,
		doubanService: douban,
		cache:         cache,
		cacheTTL:      cacheTTL,
	}
}

// GetCalendar returns calendar data for a date range
// GET /api/v1/calendar?start_date=2026-01-09&end_date=2026-01-16&region=CN
func (h *CalendarHandler) GetCalendar(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Parse parameters
	startDateStr := c.DefaultQuery("start_date", time.Now().Format("2006-01-02"))
	endDateStr := c.DefaultQuery("end_date", "")
	region := c.DefaultQuery("region", "CN")

	// Parse start date
	startDate, err := time.Parse("2006-01-02", startDateStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "Invalid start_date format, expected YYYY-MM-DD",
		})
		return
	}

	// Parse or calculate end date
	var endDate time.Time
	if endDateStr == "" {
		endDate = startDate.AddDate(0, 0, defaultCalendarDays)
	} else {
		endDate, err = time.Parse("2006-01-02", endDateStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.APIResponse{
				Code:  400,
				Error: "Invalid end_date format, expected YYYY-MM-DD",
			})
			return
		}
	}

	// Validate date range
	daysDiff := int(endDate.Sub(startDate).Hours() / 24)
	if daysDiff < 0 {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: "end_date must be after start_date",
		})
		return
	}
	if daysDiff > maxCalendarDays {
		c.JSON(http.StatusBadRequest, model.APIResponse{
			Code:  400,
			Error: fmt.Sprintf("Date range cannot exceed %d days", maxCalendarDays),
		})
		return
	}

	// Generate cache key
	cacheKey := fmt.Sprintf("%s%s_%s_%s", calendarCacheKeyPrefix, startDateStr, endDate.Format("2006-01-02"), region)

	// Check cache
	var cachedData model.CalendarResponse
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache")
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().
		Str("start", startDateStr).
		Str("end", endDate.Format("2006-01-02")).
		Str("region", region).
		Msg("📅 开始获取日历数据...")

	// Check TMDB configuration
	if !h.tmdbService.IsConfigured() {
		c.JSON(http.StatusServiceUnavailable, model.APIResponse{
			Code:  503,
			Error: "TMDB service not configured",
		})
		return
	}

	// Fetch TV shows using discover API
	tvResponse, err := h.tmdbService.DiscoverTV(startDateStr, endDate.Format("2006-01-02"), region, 1)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch TV shows")
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: "Failed to fetch TV shows from TMDB",
		})
		return
	}

	// Build calendar entries concurrently
	calendarEntries := h.buildCalendarEntries(ctx, tvResponse.Results, startDate, endDate)

	// Group entries by date
	calendarDays := h.groupEntriesByDate(calendarEntries, startDate, endDate)

	// Count total entries
	totalEntries := 0
	for _, day := range calendarDays {
		totalEntries += len(day.Entries)
	}

	response := model.CalendarResponse{
		StartDate: startDateStr,
		EndDate:   endDate.Format("2006-01-02"),
		Days:      calendarDays,
		Total:     totalEntries,
	}

	// Cache the result
	h.cache.Set(ctx, cacheKey, response, h.cacheTTL)

	log.Info().
		Int("shows", len(tvResponse.Results)).
		Int("entries", totalEntries).
		Msg("✅ 日历数据获取成功")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:   200,
		Data:   response,
		Source: "fresh",
	})
}

// GetAiring returns today's airing shows
// GET /api/v1/calendar/airing?page=1&region=CN
func (h *CalendarHandler) GetAiring(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	page := 1
	if p := c.Query("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
		if page < 1 {
			page = 1
		}
	}
	region := c.DefaultQuery("region", "CN")

	// Generate cache key
	cacheKey := fmt.Sprintf("%s%s_page%d_%s", airingCacheKeyPrefix, time.Now().Format("2006-01-02"), page, region)

	// Check cache
	var cachedData []model.CalendarEntry
	if err := h.cache.Get(ctx, cacheKey, &cachedData); err == nil {
		c.Set("cache_source", "redis-cache")
		c.JSON(http.StatusOK, model.APIResponse{
			Code:   200,
			Data:   cachedData,
			Source: "redis-cache",
		})
		return
	}

	log.Info().
		Int("page", page).
		Str("region", region).
		Msg("📺 获取今日热播...")

	// Check TMDB configuration
	if !h.tmdbService.IsConfigured() {
		c.JSON(http.StatusServiceUnavailable, model.APIResponse{
			Code:  503,
			Error: "TMDB service not configured",
		})
		return
	}

	// Fetch airing today shows
	tvResponse, err := h.tmdbService.GetAiringToday(page, region)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch airing today")
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  500,
			Error: "Failed to fetch airing shows from TMDB",
		})
		return
	}

	// Convert to calendar entries
	entries := make([]model.CalendarEntry, 0, len(tvResponse.Results))
	today := time.Now().Format("2006-01-02")

	for _, show := range tvResponse.Results {
		entry := model.CalendarEntry{
			ShowID:      show.ID,
			ShowName:    show.Name,
			AirDate:     today,
			Poster:      h.tmdbService.GetImageURL(show.PosterPath),
			Backdrop:    h.tmdbService.GetImageURL(show.BackdropPath),
			Overview:    show.Overview,
			VoteAverage: show.VoteAverage,
		}

		// Use original name if different (for non-Chinese shows)
		if show.OriginalName != show.Name {
			entry.ShowNameCN = show.Name
			entry.ShowName = show.OriginalName
		}

		entries = append(entries, entry)
	}

	// Cache the result
	h.cache.Set(ctx, cacheKey, entries, h.cacheTTL)

	log.Info().Int("count", len(entries)).Msg("✅ 今日热播获取成功")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:   200,
		Data:   entries,
		Source: "fresh",
	})
}

// DeleteCalendarCache clears calendar cache
// DELETE /api/v1/calendar
func (h *CalendarHandler) DeleteCalendarCache(c *gin.Context) {
	ctx := context.Background()

	// Delete calendar and airing caches
	h.cache.DeletePattern(ctx, calendarCacheKeyPrefix+"*")
	h.cache.DeletePattern(ctx, airingCacheKeyPrefix+"*")

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    200,
		Message: "日历缓存已清除",
	})
}

// buildCalendarEntries builds calendar entries from TV shows
func (h *CalendarHandler) buildCalendarEntries(ctx context.Context, shows []model.TMDBTVShow, startDate, endDate time.Time) []model.CalendarEntry {
	var entries []model.CalendarEntry
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Limit concurrent requests
	sem := make(chan struct{}, 5)

	for _, show := range shows {
		wg.Add(1)
		go func(s model.TMDBTVShow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Get TV details to find current season
			details, err := h.tmdbService.GetTVDetails(s.ID)
			if err != nil {
				log.Debug().Err(err).Str("show", s.Name).Msg("Failed to get TV details")
				return
			}

			// Find the latest season (in production)
			if len(details.Seasons) == 0 {
				return
			}

			// Get latest season number (usually the last one, excluding season 0 which is specials)
			latestSeasonNum := 0
			for _, season := range details.Seasons {
				if season.SeasonNumber > latestSeasonNum {
					latestSeasonNum = season.SeasonNumber
				}
			}

			if latestSeasonNum == 0 {
				return
			}

			// Get season details for episode air dates
			season, err := h.tmdbService.GetSeasonDetails(s.ID, latestSeasonNum)
			if err != nil {
				log.Debug().Err(err).Str("show", s.Name).Msg("Failed to get season details")
				return
			}

			// Filter episodes within date range
			for _, ep := range season.Episodes {
				if ep.AirDate == "" {
					continue
				}

				epDate, err := time.Parse("2006-01-02", ep.AirDate)
				if err != nil {
					continue
				}

				// Check if episode is within date range
				if epDate.Before(startDate) || epDate.After(endDate) {
					continue
				}

				entry := model.CalendarEntry{
					ShowID:        s.ID,
					ShowName:      details.Name,
					SeasonNumber:  ep.SeasonNumber,
					EpisodeNumber: ep.EpisodeNumber,
					EpisodeName:   ep.Name,
					AirDate:       ep.AirDate,
					Poster:        h.tmdbService.GetImageURL(details.PosterPath),
					Backdrop:      h.tmdbService.GetImageURL(details.BackdropPath),
					Overview:      ep.Overview,
					VoteAverage:   details.VoteAverage,
				}

				// Use original name if different
				if details.OriginalName != details.Name {
					entry.ShowNameCN = details.Name
					entry.ShowName = details.OriginalName
				}

				mu.Lock()
				entries = append(entries, entry)
				mu.Unlock()
			}
		}(show)
	}

	wg.Wait()
	return entries
}

// groupEntriesByDate groups calendar entries by date
func (h *CalendarHandler) groupEntriesByDate(entries []model.CalendarEntry, startDate, endDate time.Time) []model.CalendarDay {
	// Create a map for quick lookup
	dateMap := make(map[string][]model.CalendarEntry)

	for _, entry := range entries {
		dateMap[entry.AirDate] = append(dateMap[entry.AirDate], entry)
	}

	// Build calendar days for each date in range
	var days []model.CalendarDay
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		dayEntries := dateMap[dateStr]

		// Sort entries by vote average (descending)
		sort.Slice(dayEntries, func(i, j int) bool {
			return dayEntries[i].VoteAverage > dayEntries[j].VoteAverage
		})

		days = append(days, model.CalendarDay{
			Date:    dateStr,
			Entries: dayEntries,
		})
	}

	return days
}
