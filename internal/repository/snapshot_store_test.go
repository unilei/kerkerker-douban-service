package repository

import (
	"context"
	"testing"
	"time"

	"kerkerker-douban-service/internal/model"
)

func TestSnapshotStore_RoundTrip_CategoryData(t *testing.T) {
	uri := mongoTestURI(t)
	dbName := "kerkerker_douban_test_snap_" + timeNowSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stores, err := NewMongoStores(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = stores.Close(cctx)
	})

	key := "douban:movies:all"
	payload := []model.CategoryData{
		{Name: "热门电影", Data: []model.Subject{{ID: "1", Title: "片A", Cover: "https://img.doubanio.com/a.jpg"}}},
		{Name: "豆瓣高分", Data: []model.Subject{{ID: "2", Title: "片B", Rate: "9.5"}}},
	}

	// Miss before Store.
	var miss []model.CategoryData
	if err := stores.Snapshot.Load(ctx, key, &miss); err != ErrSnapshotNotFound {
		t.Fatalf("expected ErrSnapshotNotFound before store, got %v", err)
	}

	if err := stores.Snapshot.Store(ctx, key, payload); err != nil {
		t.Fatalf("store: %v", err)
	}

	var got []model.CategoryData
	if err := stores.Snapshot.Load(ctx, key, &got); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].Name != "热门电影" || len(got[0].Data) != 1 || got[0].Data[0].Title != "片A" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got[1].Data[0].Rate != "9.5" {
		t.Fatalf("nested field mismatch: %+v", got[1])
	}
}

func TestSnapshotStore_Overwrite(t *testing.T) {
	uri := mongoTestURI(t)
	dbName := "kerkerker_douban_test_snap_ov_" + timeNowSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stores, err := NewMongoStores(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = stores.Close(cctx)
	})

	key := "douban:hero:movies"
	first := []model.HeroMovie{{ID: "1", Title: "old"}}
	second := []model.HeroMovie{{ID: "2", Title: "new"}}

	if err := stores.Snapshot.Store(ctx, key, first); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := stores.Snapshot.Store(ctx, key, second); err != nil {
		t.Fatalf("second store: %v", err)
	}

	var got []model.HeroMovie
	if err := stores.Snapshot.Load(ctx, key, &got); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Title != "new" {
		t.Fatalf("expected overwrite to new, got %+v", got)
	}
}

func TestSnapshotStore_SearchResult(t *testing.T) {
	uri := mongoTestURI(t)
	dbName := "kerkerker_douban_test_snap_sr_" + timeNowSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stores, err := NewMongoStores(ctx, uri, dbName)
	if err != nil {
		t.Skipf("mongo unavailable, skipping: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_ = stores.Close(cctx)
	})

	key := "douban:search:庆余年:tv:::"
	payload := model.SearchResult{
		Suggest:  []model.SuggestItem{{ID: "1", Title: "庆余年", Img: "https://img.doubanio.com/q.jpg"}},
		Advanced: []model.Subject{{ID: "1", Title: "庆余年 第一季"}},
	}
	if err := stores.Snapshot.Store(ctx, key, payload); err != nil {
		t.Fatalf("store: %v", err)
	}

	var got model.SearchResult
	if err := stores.Snapshot.Load(ctx, key, &got); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Suggest) != 1 || got.Suggest[0].Title != "庆余年" || len(got.Advanced) != 1 {
		t.Fatalf("SearchResult round-trip mismatch: %+v", got)
	}
}
