package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkerObjectStorePutObjectUsesAuthenticatedUploadAPI(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotAuthorization string
	var gotContentType string
	var gotCacheControl string
	var gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotCacheControl = r.Header.Get("Cache-Control")
		gotBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	store := NewWorkerObjectStore(server.URL+"/objects/", "upload-secret", 5*time.Second)
	err := store.PutObject(context.Background(), StoredObject{
		Key:          "douban/poster.jpg",
		Body:         []byte("fake-jpeg"),
		ContentType:  "image/jpeg",
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		t.Fatalf("put worker object: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT request, got %q", gotMethod)
	}
	if gotPath != "/objects/douban/poster.jpg" {
		t.Fatalf("unexpected request path: %q", gotPath)
	}
	if gotAuthorization != "Bearer upload-secret" {
		t.Fatalf("unexpected authorization header")
	}
	if gotContentType != "image/jpeg" {
		t.Fatalf("unexpected content type: %q", gotContentType)
	}
	if gotCacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected cache control: %q", gotCacheControl)
	}
	if gotBody != "fake-jpeg" {
		t.Fatalf("unexpected body: %q", gotBody)
	}
}

func TestWorkerObjectStoreReturnsErrorForRejectedUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	store := NewWorkerObjectStore(server.URL+"/objects", "bad-secret", 5*time.Second)
	err := store.PutObject(context.Background(), StoredObject{
		Key:         "douban/poster.jpg",
		Body:        []byte("fake-jpeg"),
		ContentType: "image/jpeg",
	})
	if err == nil {
		t.Fatalf("expected rejected upload to return an error")
	}
}
