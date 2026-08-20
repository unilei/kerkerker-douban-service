package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

type newHandlerResponse struct {
	Code       int                  `json:"code"`
	Data       []model.CategoryData `json:"data"`
	Source     string               `json:"source"`
	Error      string               `json:"error"`
	Pagination newPagination        `json:"pagination"`
}

func TestNewHandlerFilteredRedisSeparatesPagesAndKeepsPagination(t *testing.T) {
	handler, cache := newCachedNewHandler(t)
	for page := 1; page <= 2; page++ {
		payload := newFilteredSnapshot{
			Data: []model.CategoryData{{
				Name: "科幻",
				Data: []model.Subject{{ID: string(rune('0' + page)), Title: "影片"}},
			}},
			Pagination: newPagination{Page: page, PageSize: 30, Total: 60, HasMore: page == 1},
		}
		key := newFilteredCacheKey("", "", "", "科幻", "time", page, 30)
		if err := cache.Set(context.Background(), key, payload); err != nil {
			t.Fatalf("seed page %d: %v", page, err)
		}
	}

	first := performNewRequest(handler, "/api/v1/new?genre=%E7%A7%91%E5%B9%BB&sort=time&page=1&pageSize=30")
	second := performNewRequest(handler, "/api/v1/new?genre=%E7%A7%91%E5%B9%BB&sort=time&page=2&pageSize=30")
	firstBody := decodeNewResponse(t, first)
	secondBody := decodeNewResponse(t, second)

	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("unexpected status: page1=%d page2=%d", first.Code, second.Code)
	}
	if firstBody.Source != "redis-cache" || secondBody.Source != "redis-cache" {
		t.Fatalf("expected Redis hits: page1=%s page2=%s", firstBody.Source, secondBody.Source)
	}
	if firstBody.Pagination.Page != 1 || secondBody.Pagination.Page != 2 {
		t.Fatalf("pagination lost: page1=%+v page2=%+v", firstBody.Pagination, secondBody.Pagination)
	}
	if firstBody.Data[0].Data[0].ID == secondBody.Data[0].Data[0].ID {
		t.Fatal("different pages resolved to the same cached payload")
	}
}

func TestNewHandlerExplicitRecommendedSortUsesFilteredContract(t *testing.T) {
	handler, cache := newCachedNewHandler(t)
	payload := newFilteredSnapshot{
		Data: []model.CategoryData{{
			Name: "热门",
			Data: []model.Subject{{ID: "recommended-1", Title: "热门影片"}},
		}},
		Pagination: newPagination{Page: 1, PageSize: 30, Total: 30, HasMore: false},
	}
	key := newFilteredCacheKey("", "", "", "", "recommend", 1, 30)
	if err := cache.Set(context.Background(), key, payload); err != nil {
		t.Fatalf("seed recommended page: %v", err)
	}

	response := performNewRequest(handler, "/api/v1/new?sort=recommend&page=1&pageSize=30")
	body := decodeNewResponse(t, response)
	if response.Code != http.StatusOK || body.Data[0].Data[0].ID != "recommended-1" {
		t.Fatalf("explicit sort did not use filtered cache: %d %s", response.Code, response.Body.String())
	}
	if body.Pagination.PageSize != 30 {
		t.Fatalf("pagination missing from hot cache: %+v", body.Pagination)
	}
}

func TestNewHandlerRejectsInvalidPaginationBeforeUpstreamAccess(t *testing.T) {
	handler, _ := newCachedNewHandler(t)
	for _, path := range []string{
		"/api/v1/new?genre=x&page=0&pageSize=30",
		"/api/v1/new?genre=x&page=1&pageSize=101",
		"/api/v1/new?genre=x&page=oops&pageSize=30",
	} {
		response := performNewRequest(handler, path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", path, response.Code)
		}
	}
}

func newCachedNewHandler(t *testing.T) (*NewHandler, *repository.Cache) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	cache, err := repository.NewCache("redis://"+redisServer.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return &NewHandler{cache: cache}, cache
}

func performNewRequest(handler *NewHandler, path string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/new", handler.GetNew)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeNewResponse(t *testing.T, response *httptest.ResponseRecorder) newHandlerResponse {
	t.Helper()
	var body newHandlerResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}
