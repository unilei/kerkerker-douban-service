package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kerkerker-douban-service/internal/service"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/gin-gonic/gin"
)

type statusImageMapStore struct{}

func (statusImageMapStore) Get(context.Context, string) (string, error) { return "", nil }
func (statusImageMapStore) Put(context.Context, string, string) error   { return nil }
func (statusImageMapStore) Close(context.Context) error                 { return nil }

type statusImageFetcher struct{}

func (statusImageFetcher) FetchImage(context.Context, string, int64) (service.FetchedImage, error) {
	return service.FetchedImage{}, nil
}

type statusObjectStore struct{}

func (statusObjectStore) PutObject(context.Context, service.StoredObject) error { return nil }

func TestStatusReportsR2SyncAndPersistentMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	imageSyncer := service.NewImageSyncer(service.ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://images.example.test",
	}, statusImageFetcher{}, statusObjectStore{})
	imageSyncer.SetMapStore(statusImageMapStore{})
	doubanService := service.NewDoubanService(httpclient.NewClient(nil), imageSyncer)
	tmdbService := service.NewTMDBService(nil, "", "")
	handler := NewAdminHandler(doubanService, tmdbService, nil)

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	handler.GetStatus(ctx)

	var body struct {
		R2Enabled          bool `json:"r2_image_sync_enabled"`
		MappingsPersistent bool `json:"r2_image_mapping_persistent"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if response.Code != http.StatusOK || !body.R2Enabled || !body.MappingsPersistent {
		t.Fatalf("unexpected status response %d: %s", response.Code, response.Body.String())
	}
}
