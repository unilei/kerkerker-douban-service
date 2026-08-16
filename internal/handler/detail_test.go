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
	"kerkerker-douban-service/internal/service"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

// fakeMovieStore 是用于 detail handler 测试的内存版 MovieStore。
type fakeMovieStore struct {
	byDouban map[string]*repository.Movie
	puts     []*repository.Movie
	getErr   error // 强制 GetByDoubanID 返回该错误（模拟降级场景）
}

func (f *fakeMovieStore) GetByDoubanID(ctx context.Context, doubanID string) (*repository.Movie, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if m, ok := f.byDouban[doubanID]; ok {
		return m, nil
	}
	return nil, repository.ErrMovieNotFound
}
func (f *fakeMovieStore) GetByInternalID(ctx context.Context, internalID int64) (*repository.Movie, error) {
	return nil, repository.ErrMovieNotFound
}
func (f *fakeMovieStore) Upsert(ctx context.Context, m *repository.Movie) error {
	f.puts = append(f.puts, m)
	return nil
}
func (f *fakeMovieStore) ListStale(ctx context.Context, limit int, refreshedBefore time.Time) ([]*repository.Movie, error) {
	return nil, nil
}
func (f *fakeMovieStore) MarkStale(ctx context.Context, refreshedBefore time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeMovieStore) Ping(ctx context.Context) error { return nil }
func (f *fakeMovieStore) Close(ctx context.Context) error { return nil }

func newDetailHandlerWithStore(t *testing.T, store repository.MovieStore) (*DetailHandler, *repository.Cache, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	cache, err := repository.NewCache("redis://"+mr.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	// 一个不会真正请求豆瓣的 DoubanService（HTTP client 默认无代理；测试不应触发回源路径）。
	svc := service.NewDoubanService(httpclient.NewClient(nil))
	svc.SetMovieStore(store)
	return NewDetailHandler(svc, cache), cache, func() { _ = cache.Close() }
}

// 当 Mongo 命中时，detail handler 不应回源豆瓣，且应回填 Redis。
func TestDetailHandler_MongoHitSkipsDouban(t *testing.T) {
	store := &fakeMovieStore{
		byDouban: map[string]*repository.Movie{
			"123": {
				InternalID: 42,
				DoubanID:   "123",
				Title:      "测试片",
				Detail: &model.SubjectDetail{
					ID:    "123",
					Title: "测试片",
					Rate:  "9.0",
					Cover: "https://img.doubanio.com/cover.jpg",
				},
			},
		},
	}
	handler, cache, cleanup := newDetailHandlerWithStore(t, store)
	defer cleanup()

	w := performDetailRequest(handler, "123")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["source"] != "mongo-store" {
		t.Fatalf("expected source mongo-store, got %v", body["source"])
	}
	if body["title"] != "测试片" {
		t.Fatalf("expected title 测试片, got %v", body["title"])
	}

	// Redis 应被回填：直接通过 cache 命中相同 key。
	var cached model.SubjectDetail
	if err := cache.Get(context.Background(), "douban:detail:123", &cached); err != nil {
		t.Fatalf("expected Redis backfill, got %v", err)
	}
	if cached.Title != "测试片" {
		t.Fatalf("cached title mismatch: %s", cached.Title)
	}

	// 不应有 Upsert（命中路径不落库）。
	if len(store.puts) != 0 {
		t.Fatalf("expected no upsert on mongo-hit path, got %d", len(store.puts))
	}
}

// 当 Mongo 报非 NotFound 错误时应优雅降级；此处验证不会 panic 且返回 404（因为未配置豆瓣回源数据）。
func TestDetailHandler_MongoErrorDegradesGracefully(t *testing.T) {
	store := &fakeMovieStore{
		getErr: context.DeadlineExceeded, // 模拟 Mongo 故障
	}
	handler, _, cleanup := newDetailHandlerWithStore(t, store)
	defer cleanup()

	w := performDetailRequest(handler, "999")
	// 未配置豆瓣回源 → fetchDetailFromDouban 返回 false → 404。关键是不 500/不 panic。
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected graceful 404 on mongo error + no douban, got %d: %s", w.Code, w.Body.String())
	}
}

func performDetailRequest(h *DetailHandler, id string) *httptest.ResponseRecorder {
	r := gin.New()
	r.GET("/api/v1/detail/:id", h.GetDetail)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/detail/"+id, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
