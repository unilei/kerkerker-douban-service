package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kerkerker-douban-service/internal/middleware"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
)

func pluginTestRouter(token string, tmdb *service.TMDBService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := NewPluginHandler(tmdb)
	router := gin.New()
	router.GET("/plugin/v1/manifest", handler.Manifest)
	plugin := router.Group("/plugin/v1")
	plugin.Use(middleware.PluginServiceAuth(token))
	plugin.GET("/health", handler.Health)
	plugin.POST("/invoke", handler.Invoke)
	return router
}

func TestPluginManifestContainsPublicContractMetadata(t *testing.T) {
	router := pluginTestRouter("service-token", service.NewTMDBService(nil, "", ""))
	request := httptest.NewRequest(http.MethodGet, "/plugin/v1/manifest", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected manifest 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		ID         string         `json:"id"`
		Compliance map[string]any `json:"compliance"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if body.ID != service.TMDBPluginID || len(body.Compliance) == 0 {
		t.Fatalf("manifest missing contract metadata: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "TMDB_API_KEY") {
		t.Fatal("manifest must not expose upstream secret names or values")
	}
}

func TestPluginAuthFailsClosedAndIsCaseSensitive(t *testing.T) {
	router := pluginTestRouter("service-token", service.NewTMDBService(nil, "", ""))
	cases := []struct {
		name string
		auth string
		code int
	}{
		{name: "missing", code: http.StatusUnauthorized},
		{name: "wrong case", auth: "Bearer Service-Token", code: http.StatusUnauthorized},
		{name: "wrong value", auth: "Bearer other", code: http.StatusUnauthorized},
		{name: "empty config", auth: "Bearer service-token", code: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := router
			if tc.name == "empty config" {
				current = pluginTestRouter("", service.NewTMDBService(nil, "", ""))
			}
			request := httptest.NewRequest(http.MethodGet, "/plugin/v1/health", nil)
			if tc.auth != "" {
				request.Header.Set("Authorization", tc.auth)
			}
			response := httptest.NewRecorder()
			current.ServeHTTP(response, request)
			if response.Code != tc.code {
				t.Fatalf("expected %d, got %d: %s", tc.code, response.Code, response.Body.String())
			}
		})
	}
}

func TestPluginVersionNegotiationRejectsUnsupportedVersion(t *testing.T) {
	router := pluginTestRouter("service-token", service.NewTMDBService(nil, "", ""))
	request := httptest.NewRequest(http.MethodGet, "/plugin/v1/health", nil)
	request.Header.Set("Authorization", "Bearer service-token")
	request.Header.Set("x-kerkerker-contract-versions", "2.0.0")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "CONTRACT_VERSION_UNSUPPORTED") {
		t.Fatalf("unexpected error envelope: %s", response.Body.String())
	}
}

func TestPluginInvokeValidatesContextRequestID(t *testing.T) {
	router := pluginTestRouter("service-token", service.NewTMDBService([]string{"not-used"}, "", ""))
	request := httptest.NewRequest(http.MethodPost, "/plugin/v1/invoke", strings.NewReader(`{"contractVersion":"1.0.0","capability":"content.search","operation":"search","context":{"profile":"en-default","locale":"en-US"},"request":{"query":"test"}}`))
	request.Header.Set("Authorization", "Bearer service-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "requestId") {
		t.Fatalf("expected requestId validation error: %s", response.Body.String())
	}
}
