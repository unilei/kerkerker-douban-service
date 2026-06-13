package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestR2ObjectStorePutObjectUsesS3CompatibleRequest(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody string
	var gotContentType string
	var gotCacheControl string
	var gotAuthorization string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}

		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody = string(body)
		gotContentType = r.Header.Get("Content-Type")
		gotCacheControl = r.Header.Get("Cache-Control")
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewR2ObjectStore(context.Background(), R2ObjectStoreConfig{
		Endpoint:        server.URL,
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
		Bucket:          "douban-images",
	})
	if err != nil {
		t.Fatalf("create R2 object store: %v", err)
	}

	err = store.PutObject(context.Background(), StoredObject{
		Key:          "douban/poster.jpg",
		Body:         []byte("fake-jpeg"),
		ContentType:  "image/jpeg",
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		t.Fatalf("put R2 object: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT request, got %q", gotMethod)
	}
	if gotPath != "/douban-images/douban/poster.jpg" {
		t.Fatalf("unexpected request path: %q", gotPath)
	}
	if gotBody != "fake-jpeg" {
		t.Fatalf("unexpected request body: %q", gotBody)
	}
	if gotContentType != "image/jpeg" {
		t.Fatalf("unexpected content type: %q", gotContentType)
	}
	if gotCacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected cache control: %q", gotCacheControl)
	}
	if gotAuthorization == "" {
		t.Fatalf("expected signed S3-compatible request")
	}
}
