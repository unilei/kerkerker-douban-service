package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"kerkerker-douban-service/internal/model"
)

// mongoTestURI returns the URI to use for live MongoDB integration tests.
// Defaults to the local kerkerker-mongo container so `go test ./...` works out-of-the-box here.
// Set MONGO_TEST_URI="" to skip these tests entirely.
func mongoTestURI(t *testing.T) string {
	t.Helper()
	if uri := os.Getenv("MONGO_TEST_URI"); uri != "" {
		return uri
	}
	return "mongodb://localhost:27018"
}

func newTestStore(t *testing.T) (MovieStore, string) {
	t.Helper()
	uri := mongoTestURI(t)
	dbName := fmt.Sprintf("kerkerker_douban_test_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store, err := NewMongoMovieStore(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = store.Close(cctx)
	})
	return store, dbName
}

func TestMovieStore_UpsertInsertsAndAllocatesID(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	m := &Movie{
		DoubanID: "1292052",
		Title:    "肖申克的救赎",
		Detail:   &model.SubjectDetail{ID: "1292052", Title: "肖申克的救赎"},
	}
	if err := store.Upsert(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m.InternalID == 0 {
		t.Fatal("expected InternalID to be allocated")
	}
	if m.RefreshStatus != RefreshStatusFresh {
		t.Fatalf("expected fresh, got %s", m.RefreshStatus)
	}
}

func TestMovieStore_UpsertPreservesInternalIDOnUpdate(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	first := &Movie{DoubanID: "1292720", Title: "阿甘正传", Detail: &model.SubjectDetail{ID: "1292720"}}
	if err := store.Upsert(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	originalID := first.InternalID

	second := &Movie{DoubanID: "1292720", Title: "阿甘正传 (1994)", Detail: &model.SubjectDetail{ID: "1292720", Title: "阿甘正传"}}
	if err := store.Upsert(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.InternalID != originalID {
		t.Fatalf("expected InternalID preserved=%d, got %d", originalID, second.InternalID)
	}
	if second.Title != "阿甘正传 (1994)" {
		t.Fatalf("expected title updated, got %s", second.Title)
	}

	// Re-read from store and confirm same InternalID + new title.
	got, err := store.GetByDoubanID(ctx, "1292720")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.InternalID != originalID {
		t.Fatalf("stored InternalID mismatch: %d vs %d", got.InternalID, originalID)
	}
	if got.Title != "阿甘正传 (1994)" {
		t.Fatalf("stored title mismatch: %s", got.Title)
	}
}

func TestMovieStore_GetByInternalID(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	m := &Movie{DoubanID: "1291561", Title: "千与千寻", Detail: &model.SubjectDetail{ID: "1291561"}}
	if err := store.Upsert(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.GetByInternalID(ctx, m.InternalID)
	if err != nil {
		t.Fatalf("get by internal id: %v", err)
	}
	if got.DoubanID != "1291561" {
		t.Fatalf("expected douban id 1291561, got %s", got.DoubanID)
	}
}

func TestMovieStore_GetByDoubanIDNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.GetByDoubanID(ctx, "does-not-exist-99999")
	if err != ErrMovieNotFound {
		t.Fatalf("expected ErrMovieNotFound, got %v", err)
	}
}

func TestMovieStore_ListStale(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// Insert one fresh, one stale.
	stale := &Movie{
		DoubanID:      "stale-1",
		Title:         "旧片",
		RefreshStatus: RefreshStatusStale,
		LastRefreshed: time.Now().Add(-48 * time.Hour),
	}
	fresh := &Movie{
		DoubanID:      "fresh-1",
		Title:         "新片",
		RefreshStatus: RefreshStatusFresh,
		LastRefreshed: time.Now(),
	}
	if err := store.Upsert(ctx, stale); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}
	if err := store.Upsert(ctx, fresh); err != nil {
		t.Fatalf("upsert fresh: %v", err)
	}

	threshold := time.Now().Add(-1 * time.Hour)
	results, err := store.ListStale(ctx, 100, threshold)
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	foundStale := false
	for _, r := range results {
		if r.DoubanID == "stale-1" {
			foundStale = true
		}
		if r.DoubanID == "fresh-1" {
			t.Fatal("fresh movie should not appear in stale list")
		}
	}
	if !foundStale {
		t.Fatal("expected to find stale-1 in results")
	}
}

func TestMovieStore_MarkStale(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	m := &Movie{
		DoubanID:      "aging-1",
		Title:         "老化中",
		RefreshStatus: RefreshStatusFresh,
		LastRefreshed: time.Now().Add(-48 * time.Hour),
	}
	if err := store.Upsert(ctx, m); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	threshold := time.Now().Add(-1 * time.Hour)
	n, err := store.MarkStale(ctx, threshold)
	if err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	if n == 0 {
		t.Fatal("expected at least one document to be marked stale")
	}

	got, err := store.GetByDoubanID(ctx, "aging-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RefreshStatus != RefreshStatusStale {
		t.Fatalf("expected status stale, got %s", got.RefreshStatus)
	}
}
