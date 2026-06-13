package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxWorkerErrorBodyBytes int64 = 4 * 1024

// WorkerObjectStore writes objects through an authenticated Cloudflare Worker.
type WorkerObjectStore struct {
	uploadAPIURL string
	token        string
	client       *http.Client
}

// NewWorkerObjectStore creates an object store backed by an upload Worker.
func NewWorkerObjectStore(uploadAPIURL, token string, timeout time.Duration) *WorkerObjectStore {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &WorkerObjectStore{
		uploadAPIURL: strings.TrimRight(strings.TrimSpace(uploadAPIURL), "/"),
		token:        strings.TrimSpace(token),
		client:       &http.Client{Timeout: timeout},
	}
}

// PutObject uploads an object through the Worker API.
func (s *WorkerObjectStore) PutObject(ctx context.Context, object StoredObject) error {
	if s.uploadAPIURL == "" || s.token == "" {
		return fmt.Errorf("upload worker URL and token are required")
	}
	if object.Key == "" {
		return fmt.Errorf("object key is required")
	}

	targetURL := s.uploadAPIURL + "/" + strings.TrimLeft(object.Key, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, bytes.NewReader(object.Body))
	if err != nil {
		return fmt.Errorf("create upload worker request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", object.ContentType)
	req.Header.Set("Cache-Control", object.CacheControl)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload worker request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxWorkerErrorBodyBytes))
		return fmt.Errorf("upload worker returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}
