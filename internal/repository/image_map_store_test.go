package repository

import (
	"context"
	"testing"
	"time"
)

func TestImageMapStore_GetPutRoundTrip(t *testing.T) {
	uri := mongoTestURI(t)
	dbName := "kerkerker_douban_test_imgmap_" + timeNowSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := NewMongoStores(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = store.Close(cctx)
	})

	original := "https://img1.doubanio.com/view/photo/l/public/p123.jpg"
	r2 := "https://r2.example.com/douban-images/abc.jpg"

	// Miss before Put.
	if _, err := store.ImageMap.Get(ctx, original); err != ErrImageMappingNotFound {
		t.Fatalf("expected ErrImageMappingNotFound before put, got %v", err)
	}

	if err := store.ImageMap.Put(ctx, original, r2); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.ImageMap.Get(ctx, original)
	if err != nil {
		t.Fatalf("get after put: %v", err)
	}
	if got != r2 {
		t.Fatalf("expected %s, got %s", r2, got)
	}
}

func TestImageMapStore_PutIsIdempotent(t *testing.T) {
	uri := mongoTestURI(t)
	dbName := "kerkerker_douban_test_imgmap_idem_" + timeNowSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	store, err := NewMongoStores(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = store.Close(cctx)
	})

	original := "https://img2.doubanio.com/view/photo/l/public/p999.jpg"
	first := "https://r2.example.com/douban-images/first.jpg"
	second := "https://r2.example.com/douban-images/second.jpg"

	if err := store.ImageMap.Put(ctx, original, first); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if err := store.ImageMap.Put(ctx, original, second); err != nil {
		t.Fatalf("second put: %v", err)
	}
	got, err := store.ImageMap.Get(ctx, original)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != second {
		t.Fatalf("expected second to overwrite, got %s", got)
	}
}

func timeNowSuffix() string {
	return time.Now().UTC().Format("20060102T150405000000000")
}
