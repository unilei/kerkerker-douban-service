package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

const calendarTestR2BaseURL = "https://images.example.test"

type calendarTestImageFetcher struct{}

func (calendarTestImageFetcher) FetchImage(context.Context, string, int64) (service.FetchedImage, error) {
	return service.FetchedImage{
		Body:        []byte("test-image"),
		ContentType: "image/jpeg",
	}, nil
}

type calendarTestObjectStore struct{}

func (calendarTestObjectStore) PutObject(context.Context, service.StoredObject) error {
	return nil
}

func TestCalendarHandlerRewritesCachedCalendarImagesWithoutExtendingTTL(t *testing.T) {
	handler, cache, redisServer := newCalendarR2TestHandler(t)
	ctx := context.Background()
	const (
		startDate = "2026-08-20"
		endDate   = "2026-08-21"
		region    = "CN"
	)
	cacheKey := calendarCacheKeyPrefix + startDate + "_" + endDate + "_" + region
	cached := model.CalendarResponse{
		StartDate: startDate,
		EndDate:   endDate,
		Days: []model.CalendarDay{{
			Date: startDate,
			Entries: []model.CalendarEntry{{
				ShowName: "测试剧集",
				Poster:   "https://image.tmdb.org/t/p/w500/calendar-poster.jpg",
				Backdrop: "https://image.tmdb.org/t/p/original/calendar-backdrop.jpg",
			}},
		}},
		Total: 1,
	}
	if err := cache.Set(ctx, cacheKey, cached, 30*time.Minute); err != nil {
		t.Fatalf("seed calendar cache: %v", err)
	}

	redisServer.FastForward(10 * time.Minute)
	ttlBefore := cacheTTL(t, cache, cacheKey)
	response := performCalendarRequest(handler, "/api/v1/calendar?start_date="+startDate+"&end_date="+endDate+"&region="+region)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Code   int                    `json:"code"`
		Data   model.CalendarResponse `json:"data"`
		Source string                 `json:"source"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Source != "redis-cache" {
		t.Fatalf("expected redis-cache source, got %q", body.Source)
	}
	assertCalendarEntryUsesR2(t, body.Data.Days[0].Entries[0])
	if ttlAfter := cacheTTL(t, cache, cacheKey); ttlAfter != ttlBefore {
		t.Fatalf("expected TTL to stay %s, got %s", ttlBefore, ttlAfter)
	}

	var rewritten model.CalendarResponse
	if err := cache.Get(ctx, cacheKey, &rewritten); err != nil {
		t.Fatalf("read rewritten calendar cache: %v", err)
	}
	assertCalendarEntryUsesR2(t, rewritten.Days[0].Entries[0])
}

func TestCalendarHandlerRewritesCachedAiringImagesWithoutExtendingTTL(t *testing.T) {
	handler, cache, redisServer := newCalendarR2TestHandler(t)
	ctx := context.Background()
	const (
		page   = "2"
		region = "US"
	)
	cacheKey := airingCacheKeyPrefix + time.Now().Format("2006-01-02") + "_page" + page + "_" + region
	cached := []model.CalendarEntry{{
		ShowName: "测试热播",
		Poster:   "https://image.tmdb.org/t/p/w342/airing-poster.jpg",
		Backdrop: "https://image.tmdb.org/t/p/original/airing-backdrop.jpg",
	}}
	if err := cache.Set(ctx, cacheKey, cached, 30*time.Minute); err != nil {
		t.Fatalf("seed airing cache: %v", err)
	}

	redisServer.FastForward(10 * time.Minute)
	ttlBefore := cacheTTL(t, cache, cacheKey)
	response := performCalendarRequest(handler, "/api/v1/calendar/airing?page="+page+"&region="+region)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	var body struct {
		Code   int                   `json:"code"`
		Data   []model.CalendarEntry `json:"data"`
		Source string                `json:"source"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Source != "redis-cache" {
		t.Fatalf("expected redis-cache source, got %q", body.Source)
	}
	assertCalendarEntryUsesR2(t, body.Data[0])
	if ttlAfter := cacheTTL(t, cache, cacheKey); ttlAfter != ttlBefore {
		t.Fatalf("expected TTL to stay %s, got %s", ttlBefore, ttlAfter)
	}

	var rewritten []model.CalendarEntry
	if err := cache.Get(ctx, cacheKey, &rewritten); err != nil {
		t.Fatalf("read rewritten airing cache: %v", err)
	}
	assertCalendarEntryUsesR2(t, rewritten[0])
}

func newCalendarR2TestHandler(t *testing.T) (*CalendarHandler, *repository.Cache, *miniredis.Miniredis) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	cache, err := repository.NewCache("redis://"+redisServer.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	imageSyncer := service.NewImageSyncer(service.ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: calendarTestR2BaseURL,
		KeyPrefix:     "douban",
	}, calendarTestImageFetcher{}, calendarTestObjectStore{})
	doubanService := service.NewDoubanService(httpclient.NewClient(nil), imageSyncer)
	tmdbService := service.NewTMDBService(nil, "", "")
	return NewCalendarHandler(tmdbService, doubanService, cache, time.Hour), cache, redisServer
}

func performCalendarRequest(handler *CalendarHandler, path string) *httptest.ResponseRecorder {
	router := gin.New()
	router.GET("/api/v1/calendar", handler.GetCalendar)
	router.GET("/api/v1/calendar/airing", handler.GetAiring)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func cacheTTL(t *testing.T, cache *repository.Cache, key string) time.Duration {
	t.Helper()
	ttl, err := cache.TTL(context.Background(), key)
	if err != nil {
		t.Fatalf("read cache TTL: %v", err)
	}
	return ttl
}

func assertCalendarEntryUsesR2(t *testing.T, entry model.CalendarEntry) {
	t.Helper()
	prefix := calendarTestR2BaseURL + "/douban/"
	if !strings.HasPrefix(entry.Poster, prefix) {
		t.Fatalf("expected R2 poster URL with prefix %q, got %q", prefix, entry.Poster)
	}
	if !strings.HasPrefix(entry.Backdrop, prefix) {
		t.Fatalf("expected R2 backdrop URL with prefix %q, got %q", prefix, entry.Backdrop)
	}
}
