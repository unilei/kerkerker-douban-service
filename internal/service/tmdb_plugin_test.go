package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvokePluginCatalogUsesServerSideKeyAndProviderDTO(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/popular" {
			t.Fatalf("unexpected TMDB path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-secret" {
			t.Fatalf("unexpected upstream authorization: %q", got)
		}
		if got := r.URL.Query().Get("language"); got != "en-US" {
			t.Fatalf("unexpected locale: %q", got)
		}
		_, _ = w.Write([]byte(`{"page":1,"total_pages":2,"total_results":1,"results":[{"id":123,"title":"Example","overview":"Overview","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":8.2}]}`))
	}))
	defer upstream.Close()

	service := NewTMDBService([]string{"upstream-secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, err := json.Marshal(tmdbPluginRequest{View: "category", Key: "hot_movies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	result, fault := service.InvokePlugin(context.Background(), "content.catalog", "catalog", request, PluginContext{RequestID: "req-1", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected plugin fault: %+v", fault)
	}
	page, ok := result.(PluginPage)
	if !ok || len(page.Items) != 1 {
		t.Fatalf("unexpected catalog result: %#v", result)
	}
	item := page.Items[0]
	if item.ExternalRefs[0].ProviderID != TMDBPluginID || item.ExternalRefs[0].ExternalID != "123" {
		t.Fatalf("unexpected external reference: %+v", item.ExternalRefs)
	}
	if item.Preview == nil || item.Preview.PosterURL != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("unexpected image mapping: %+v", item.Preview)
	}
	if strings.Contains(string(mustJSON(t, result)), "upstream-secret") {
		t.Fatal("TMDB API key leaked in plugin response")
	}
}

func TestInvokePluginDetailFallsBackFromMovieToTV(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/movie/456" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/3/tv/456" {
			t.Fatalf("unexpected detail path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":456,"name":"Example TV","overview":"Description","first_air_date":"2026-01-02","poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","vote_average":7.5,"number_of_episodes":8,"episode_run_time":[45],"genres":[{"name":"Drama"}],"production_countries":[{"iso_3166_1":"US"}],"credits":{"cast":[{"name":"Actor"}],"crew":[{"name":"Director","job":"Director"}]},"images":{"posters":[],"backdrops":[],"logos":[]},"recommendations":{"results":[]}}`))
	}))
	defer upstream.Close()

	service := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, _ := json.Marshal(tmdbPluginRequest{ExternalRef: &PluginExternalRef{ProviderID: TMDBPluginID, ExternalID: "456"}})
	result, fault := service.InvokePlugin(context.Background(), "content.detail", "detail", request, PluginContext{RequestID: "req-2", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected detail fault: %+v", fault)
	}
	detail, ok := result.(*PluginDetailCandidate)
	if !ok || detail.Details.EpisodeCount != "8" || detail.Type != "series" {
		t.Fatalf("unexpected detail result: %#v", result)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
