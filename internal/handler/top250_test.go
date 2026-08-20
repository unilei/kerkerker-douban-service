package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

type fakeTop250Service struct {
	mu            sync.Mutex
	subjects      []model.Subject
	fetchErr      error
	fetchCalls    int
	syncCalls     int
	imageSync     bool
	snapshotStore repository.SnapshotStore
	syncStarted   chan struct{}
	syncRelease   chan struct{}
	startOnce     sync.Once
}

func (s *fakeTop250Service) GetTop250() ([]model.Subject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchCalls++
	return append([]model.Subject(nil), s.subjects...), s.fetchErr
}

func (s *fakeTop250Service) ImageSyncEnabled() bool {
	return s.imageSync
}

func (s *fakeTop250Service) SnapshotStore() repository.SnapshotStore {
	return s.snapshotStore
}

func (s *fakeTop250Service) SyncSubjectImages(_ context.Context, subjects []model.Subject) []model.Subject {
	s.mu.Lock()
	s.syncCalls++
	s.mu.Unlock()
	if s.syncStarted != nil {
		s.startOnce.Do(func() { close(s.syncStarted) })
	}
	if s.syncRelease != nil {
		<-s.syncRelease
	}
	result := append([]model.Subject(nil), subjects...)
	for index := range result {
		result[index].Cover = "https://r2.example.com/" + result[index].ID + ".jpg"
	}
	return result
}

func (s *fakeTop250Service) callCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchCalls, s.syncCalls
}

type fakeTop250SnapshotStore struct {
	mu          sync.Mutex
	payload     top250Payload
	loadErr     error
	loadCalls   int
	storeCalls  int
	deleteCalls int
	stored      top250Payload
}

func (s *fakeTop250SnapshotStore) Load(_ context.Context, _ string, dest any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	if s.loadErr != nil {
		return s.loadErr
	}
	target, ok := dest.(*top250Payload)
	if !ok {
		return errors.New("unexpected snapshot destination")
	}
	*target = cloneTop250Payload(s.payload)
	return nil
}

func (s *fakeTop250SnapshotStore) Store(_ context.Context, _ string, payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storeCalls++
	value, ok := payload.(top250Payload)
	if !ok {
		return errors.New("unexpected snapshot payload")
	}
	s.stored = cloneTop250Payload(value)
	s.payload = cloneTop250Payload(value)
	s.loadErr = nil
	return nil
}

func (s *fakeTop250SnapshotStore) Delete(_ context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	s.payload = top250Payload{}
	s.stored = top250Payload{}
	s.loadErr = repository.ErrSnapshotNotFound
	return nil
}

func (s *fakeTop250SnapshotStore) state() (top250Payload, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTop250Payload(s.stored), s.storeCalls, s.deleteCalls
}

func TestTop250HandlerRedisHitSkipsLowerLayers(t *testing.T) {
	service := &fakeTop250Service{}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()
	seed := validTop250Payload(time.Now().UTC(), "https://cdn.example.com")
	if err := cache.Set(context.Background(), top250CacheKey, seed); err != nil {
		t.Fatalf("seed Redis: %v", err)
	}

	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || body.Source != "redis-cache" {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	fetchCalls, _ := service.callCounts()
	if fetchCalls != 0 {
		t.Fatal("Redis hit should not fetch the cold source")
	}
}

func TestTop250HandlerInvalidRedisPayloadFallsThroughToColdSource(t *testing.T) {
	service := &fakeTop250Service{subjects: validTop250Subjects("https://cdn.example.com")}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()
	if err := cache.Set(context.Background(), top250CacheKey, top250Payload{
		Subjects:  []model.Subject{{ID: "broken"}},
		FetchedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed invalid Redis payload: %v", err)
	}

	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || body.Source != "fresh-data" || len(body.Data.Subjects) != top250SubjectCount {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	fetchCalls, _ := service.callCounts()
	if fetchCalls != 1 {
		t.Fatalf("expected one cold-source call, got %d", fetchCalls)
	}
}

func TestTop250HandlerFreshMongoHitBackfillsRedis(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{payload: validTop250Payload(time.Now().UTC(), "https://cdn.example.com")}
	service := &fakeTop250Service{snapshotStore: snapshot}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || body.Source != "mongo-snapshot" {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	fetchCalls, _ := service.callCounts()
	if fetchCalls != 0 {
		t.Fatal("fresh Mongo hit should not fetch the cold source")
	}
	var cached top250Payload
	if err := cache.Get(context.Background(), top250CacheKey, &cached); err != nil {
		t.Fatalf("expected Redis backfill: %v", err)
	}
	if len(cached.Subjects) != top250SubjectCount {
		t.Fatalf("unexpected Redis backfill: %d subjects", len(cached.Subjects))
	}
}

func TestTop250HandlerExpiredMongoRefreshesColdSource(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{payload: validTop250Payload(time.Now().Add(-2*time.Hour), "https://old.example.com")}
	service := &fakeTop250Service{
		subjects:      validTop250Subjects("https://new.example.com"),
		snapshotStore: snapshot,
	}
	handler, _, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || body.Source != "fresh-data" {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	fetchCalls, _ := service.callCounts()
	stored, storeCalls, _ := snapshot.state()
	if fetchCalls != 1 || storeCalls != 1 {
		t.Fatalf("expected cold refresh and snapshot update, fetch=%d store=%d", fetchCalls, storeCalls)
	}
	if !strings.HasPrefix(stored.Subjects[0].Cover, "https://new.example.com") {
		t.Fatalf("snapshot was not refreshed: %s", stored.Subjects[0].Cover)
	}
}

func TestTop250HandlerColdFailureFallsBackToExpiredMongo(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{payload: validTop250Payload(time.Now().Add(-2*time.Hour), "https://old.example.com")}
	service := &fakeTop250Service{
		fetchErr:      errors.New("upstream verification page"),
		snapshotStore: snapshot,
	}
	handler, _, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || body.Source != "mongo-stale" {
		t.Fatalf("expected Mongo stale fallback, got %d: %s", response.Code, response.Body.String())
	}
}

func TestTop250HandlerColdSourceFailureWithoutFallbackReturnsBadGateway(t *testing.T) {
	service := &fakeTop250Service{fetchErr: errors.New("upstream verification page")}
	handler, _, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	response := performTop250Request(handler)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", response.Code, response.Body.String())
	}
	fetchCalls, _ := service.callCounts()
	if fetchCalls != 1 {
		t.Fatalf("expected one cold-source call, got %d", fetchCalls)
	}
}

func TestTop250HandlerImageSyncDoesNotBlockPublicResponse(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{loadErr: repository.ErrSnapshotNotFound}
	started := make(chan struct{})
	release := make(chan struct{})
	service := &fakeTop250Service{
		subjects:      validTop250Subjects("https://img3.doubanio.com"),
		imageSync:     true,
		snapshotStore: snapshot,
		syncStarted:   started,
		syncRelease:   release,
	}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	response := performTop250Request(handler)
	if response.Code != http.StatusOK {
		t.Fatalf("expected immediate 200, got %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background image sync did not start")
	}

	var before top250Payload
	if err := cache.Get(context.Background(), top250CacheKey, &before); err != nil {
		t.Fatalf("read raw cache: %v", err)
	}
	if !strings.Contains(before.Subjects[0].Cover, "doubanio.com") {
		t.Fatalf("public path unexpectedly waited for image mirroring: %s", before.Subjects[0].Cover)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var after top250Payload
		_, storeCalls, _ := snapshot.state()
		if err := cache.Get(context.Background(), top250CacheKey, &after); err == nil && strings.Contains(after.Subjects[0].Cover, "r2.example.com") && storeCalls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background image sync did not update Redis")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTop250HandlerDeleteClearsRedisAndMongoSnapshot(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{payload: validTop250Payload(time.Now().UTC(), "https://cdn.example.com")}
	service := &fakeTop250Service{snapshotStore: snapshot}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()
	if err := cache.Set(context.Background(), top250CacheKey, snapshot.payload); err != nil {
		t.Fatalf("seed Redis: %v", err)
	}

	response := performTop250DeleteRequest(handler)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if err := cache.Get(context.Background(), top250CacheKey, &top250Payload{}); !repository.IsCacheMiss(err) {
		t.Fatalf("expected Redis miss after delete, got %v", err)
	}
	_, _, deleteCalls := snapshot.state()
	if deleteCalls != 1 {
		t.Fatalf("expected one snapshot delete, got %d", deleteCalls)
	}
}

func TestTop250HandlerDeletePreventsBackgroundImageResurrection(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{loadErr: repository.ErrSnapshotNotFound}
	started := make(chan struct{})
	release := make(chan struct{})
	service := &fakeTop250Service{
		subjects:      validTop250Subjects("https://img3.doubanio.com"),
		imageSync:     true,
		snapshotStore: snapshot,
		syncStarted:   started,
		syncRelease:   release,
	}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	if response := performTop250Request(handler); response.Code != http.StatusOK {
		t.Fatalf("seed request failed: %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background image sync did not start")
	}
	if response := performTop250DeleteRequest(handler); response.Code != http.StatusOK {
		t.Fatalf("delete failed: %d: %s", response.Code, response.Body.String())
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		handler.imageSyncMu.Lock()
		running := handler.imageSyncRunning
		handler.imageSyncMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background image sync did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cache.Get(context.Background(), top250CacheKey, &top250Payload{}); !repository.IsCacheMiss(err) {
		t.Fatalf("background sync resurrected Redis payload: %v", err)
	}
	_, storeCalls, deleteCalls := snapshot.state()
	if storeCalls != 1 || deleteCalls != 1 {
		t.Fatalf("background sync resurrected snapshot: stores=%d deletes=%d", storeCalls, deleteCalls)
	}
}

func TestTop250HandlerDeleteThenGetContinuesImageSyncForNewGeneration(t *testing.T) {
	snapshot := &fakeTop250SnapshotStore{loadErr: repository.ErrSnapshotNotFound}
	started := make(chan struct{})
	release := make(chan struct{})
	service := &fakeTop250Service{
		subjects:      validTop250Subjects("https://img3.doubanio.com"),
		imageSync:     true,
		snapshotStore: snapshot,
		syncStarted:   started,
		syncRelease:   release,
	}
	handler, cache, cleanup := newTop250TestHandler(t, service)
	defer cleanup()

	if response := performTop250Request(handler); response.Code != http.StatusOK {
		t.Fatalf("seed request failed: %d: %s", response.Code, response.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background image sync did not start")
	}

	if response := performTop250DeleteRequest(handler); response.Code != http.StatusOK {
		t.Fatalf("delete failed: %d: %s", response.Code, response.Body.String())
	}
	response := performTop250Request(handler)
	body := decodeTop250Response(t, response)
	if response.Code != http.StatusOK || !strings.Contains(body.Data.Subjects[0].Cover, "doubanio.com") {
		t.Fatalf("new generation request should return before image sync: %d: %s", response.Code, response.Body.String())
	}

	handler.imageSyncMu.Lock()
	pendingGeneration := uint64(0)
	if handler.imageSyncPending != nil {
		pendingGeneration = handler.imageSyncPending.generation
	}
	generation := handler.imageSyncGeneration
	handler.imageSyncMu.Unlock()
	if pendingGeneration != generation {
		t.Fatalf("new generation image sync was not queued: generation=%d pending=%d", generation, pendingGeneration)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var cached top250Payload
		cacheErr := cache.Get(context.Background(), top250CacheKey, &cached)
		fetchCalls, syncCalls := service.callCounts()
		handler.imageSyncMu.Lock()
		running := handler.imageSyncRunning
		handler.imageSyncMu.Unlock()
		if cacheErr == nil && strings.Contains(cached.Subjects[0].Cover, "r2.example.com") && fetchCalls == 2 && syncCalls == 2 && !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("new generation image sync did not finish: cacheErr=%v fetch=%d sync=%d running=%t", cacheErr, fetchCalls, syncCalls, running)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type top250TestResponse struct {
	Code   int           `json:"code"`
	Data   top250Payload `json:"data"`
	Source string        `json:"source"`
}

func newTop250TestHandler(t *testing.T, service *fakeTop250Service) (*Top250Handler, *repository.Cache, func()) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	cache, err := repository.NewCache("redis://"+redisServer.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	return &Top250Handler{
		doubanService: service,
		cache:         cache,
		freshnessTTL:  time.Hour,
	}, cache, func() { _ = cache.Close() }
}

func performTop250Request(handler *Top250Handler) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/250", handler.GetTop250)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/250", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func performTop250DeleteRequest(handler *Top250Handler) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/api/v1/250", handler.DeleteTop250Cache)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/250", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeTop250Response(t *testing.T, response *httptest.ResponseRecorder) top250TestResponse {
	t.Helper()
	var body top250TestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func validTop250Payload(fetchedAt time.Time, coverBase string) top250Payload {
	return top250Payload{
		Subjects:  validTop250Subjects(coverBase),
		FetchedAt: fetchedAt,
	}
}

func validTop250Subjects(coverBase string) []model.Subject {
	subjects := make([]model.Subject, top250SubjectCount)
	for index := range subjects {
		id := fmt.Sprintf("%d", 1000000+index)
		subjects[index] = model.Subject{
			ID:    id,
			Title: fmt.Sprintf("电影 %d", index+1),
			Rate:  "9.0",
			Cover: fmt.Sprintf("%s/%s.jpg", strings.TrimRight(coverBase, "/"), id),
			URL:   "https://movie.douban.com/subject/" + id + "/",
		}
	}
	return subjects
}

func cloneTop250Payload(payload top250Payload) top250Payload {
	return top250Payload{
		Subjects:  append([]model.Subject(nil), payload.Subjects...),
		FetchedAt: payload.FetchedAt,
	}
}
