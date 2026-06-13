package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestSetKeepTTLPreservesExistingExpiration(t *testing.T) {
	redisServer := miniredis.RunT(t)
	cache, err := NewCache("redis://"+redisServer.Addr(), time.Hour)
	if err != nil {
		t.Fatalf("create cache: %v", err)
	}
	defer cache.Close()

	ctx := context.Background()
	const key = "douban:hero:movies"
	if err := cache.Set(ctx, key, map[string]string{"cover": "douban"}, 30*time.Minute); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	redisServer.FastForward(10 * time.Minute)
	before, err := cache.TTL(ctx, key)
	if err != nil {
		t.Fatalf("read ttl before rewrite: %v", err)
	}

	if err := cache.SetKeepTTL(ctx, key, map[string]string{"cover": "r2"}); err != nil {
		t.Fatalf("rewrite cache: %v", err)
	}

	after, err := cache.TTL(ctx, key)
	if err != nil {
		t.Fatalf("read ttl after rewrite: %v", err)
	}
	if before != after {
		t.Fatalf("expected TTL to stay %s, got %s", before, after)
	}

	var got map[string]string
	if err := cache.Get(ctx, key, &got); err != nil {
		t.Fatalf("read rewritten value: %v", err)
	}
	if got["cover"] != "r2" {
		t.Fatalf("expected rewritten cache value, got %q", got["cover"])
	}
}
