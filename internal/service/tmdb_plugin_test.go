package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

func TestTMDBCandidateUsesBackdropWhenPosterIsMissing(t *testing.T) {
	service := NewTMDBService([]string{"secret"}, "", "https://image.tmdb.org/t/p/original")
	candidate, ok := service.candidate(tmdbMediaResult{
		ID:           123,
		Title:        "Backdrop Only",
		BackdropPath: "/backdrop.jpg",
	}, "movie", "en-US")
	if !ok || candidate.Preview == nil {
		t.Fatalf("expected a valid candidate, got %#v", candidate)
	}
	if candidate.Preview.PosterURL != "https://image.tmdb.org/t/p/w500/backdrop.jpg" {
		t.Fatalf("expected backdrop fallback poster, got %q", candidate.Preview.PosterURL)
	}
}

func TestInvokePluginCatalogAnnotatesMovieAndSeriesSections(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload string
		switch r.URL.Path {
		case "/3/movie/popular":
			payload = `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":101,"title":"Movie","poster_path":"/movie.jpg"}]}`
		case "/3/tv/popular":
			payload = `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":202,"name":"Series","poster_path":"/series.jpg"}]}`
		default:
			t.Fatalf("unexpected section path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	service := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	for _, tc := range []struct {
		key       string
		wantType  string
		wantTitle string
	}{
		{key: "movies", wantType: "movie", wantTitle: "Movies"},
		{key: "series", wantType: "series", wantTitle: "TV Shows"},
	} {
		request, _ := json.Marshal(tmdbPluginRequest{View: "sections", Key: tc.key, Limit: 20})
		result, fault := service.InvokePlugin(context.Background(), "content.catalog", "catalog", request, PluginContext{RequestID: "section-" + tc.key, Profile: "en-default", Locale: "en-US"})
		if fault != nil {
			t.Fatalf("unexpected section fault for %s: %+v", tc.key, fault)
		}
		page, ok := result.(PluginPage)
		if !ok || len(page.Items) != 1 {
			t.Fatalf("unexpected section result for %s: %#v", tc.key, result)
		}
		if page.Items[0].Type != tc.wantType || page.Items[0].Catalog == nil || page.Items[0].Catalog.Section == nil {
			t.Fatalf("missing section metadata for %s: %#v", tc.key, page.Items[0])
		}
		section := page.Items[0].Catalog.Section
		if section.Key != tc.key || len(section.Titles) != 1 || section.Titles[0].Value != tc.wantTitle {
			t.Fatalf("unexpected section metadata for %s: %#v", tc.key, section)
		}
	}
}

func TestInvokePluginCatalogLatestMixesMovieAndSeriesWithSections(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload string
		switch r.URL.Path {
		case "/3/discover/movie":
			if got := r.URL.Query().Get("primary_release_date.lte"); got == "" || got > time.Now().UTC().Format("2006-01-02") {
				t.Fatalf("latest movies must be capped at today, got %q", got)
			}
			payload = `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":303,"title":"Latest Movie","release_date":"2026-08-01"}]}`
		case "/3/discover/tv":
			if got := r.URL.Query().Get("first_air_date.lte"); got == "" || got > time.Now().UTC().Format("2006-01-02") {
				t.Fatalf("latest series must be capped at today, got %q", got)
			}
			payload = `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":404,"name":"Latest Series","first_air_date":"2026-08-02"}]}`
		default:
			t.Fatalf("unexpected latest path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	service := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, _ := json.Marshal(tmdbPluginRequest{View: "new-releases", Limit: 20})
	result, fault := service.InvokePlugin(context.Background(), "content.catalog", "catalog", request, PluginContext{RequestID: "latest-mixed", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected mixed latest fault: %+v", fault)
	}
	page, ok := result.(PluginPage)
	if !ok || len(page.Items) != 2 {
		t.Fatalf("unexpected mixed latest result: %#v", result)
	}
	sections := map[string]bool{}
	for _, item := range page.Items {
		if item.Catalog == nil || item.Catalog.Section == nil {
			t.Fatalf("latest item is missing section metadata: %#v", item)
		}
		sections[item.Catalog.Section.Key] = true
	}
	if !sections["latest-movies"] || !sections["latest-series"] {
		t.Fatalf("expected latest movie and series sections, got %v", sections)
	}
}

func TestInvokePluginCatalogTop250AggregatesTMDBTopRatedPages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/top_rated" {
			t.Fatalf("unexpected Top250 path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("vote_count.gte"); got != "200" {
			t.Fatalf("unexpected vote count filter: %q", got)
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 || page > 13 {
			t.Fatalf("unexpected Top250 page: %q", r.URL.Query().Get("page"))
		}
		results := make([]tmdbMediaResult, 20)
		for index := range results {
			id := (page-1)*20 + index + 1
			results[index] = tmdbMediaResult{ID: id, Title: fmt.Sprintf("Movie %03d", id)}
		}
		_ = json.NewEncoder(w).Encode(tmdbPaged{Page: page, TotalPages: 13, TotalResults: 260, Results: results})
	}))
	defer upstream.Close()

	service := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, _ := json.Marshal(tmdbPluginRequest{View: "category", Key: "top250", Limit: 20})
	result, fault := service.InvokePlugin(context.Background(), "content.catalog", "catalog", request, PluginContext{RequestID: "top250", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected Top250 fault: %+v", fault)
	}
	page, ok := result.(PluginPage)
	if !ok || len(page.Items) != 250 || page.Total != 250 || page.HasMore {
		t.Fatalf("unexpected Top250 result: items=%d total=%d hasMore=%v", len(page.Items), page.Total, page.HasMore)
	}
	if got := page.Items[0].ExternalRefs[0].ExternalID; got != "1" {
		t.Fatalf("unexpected first Top250 item: %s", got)
	}
	if got := page.Items[249].ExternalRefs[0].ExternalID; got != "250" {
		t.Fatalf("unexpected last Top250 item: %s", got)
	}
}

func TestInvokePluginCalendarUsesEpisodeAirDate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/discover/tv":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"total_results":1,"results":[{"id":501,"name":"Current Show","first_air_date":"1963-04-01","poster_path":"/poster.jpg"}]}`))
		case "/3/tv/501":
			_, _ = w.Write([]byte(`{"last_episode_to_air":{"id":1,"name":"Old Episode","air_date":"1963-04-01","season_number":1,"episode_number":1},"next_episode_to_air":{"id":2,"name":"New Episode","air_date":"2026-08-24","season_number":3,"episode_number":4}}`))
		default:
			t.Fatalf("unexpected calendar path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	tmdb := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, _ := json.Marshal(tmdbPluginRequest{From: "2026-08-20", To: "2026-08-26", Region: "US", Limit: 20})
	result, fault := tmdb.InvokePlugin(context.Background(), "content.calendar", "calendar", request, PluginContext{RequestID: "calendar", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected calendar fault: %+v", fault)
	}
	page, ok := result.(PluginCalendarPage)
	if !ok || len(page.Items) != 1 {
		t.Fatalf("unexpected calendar result: %#v", result)
	}
	item := page.Items[0]
	if item.Calendar.AirDate != "2026-08-24" || item.Calendar.SeasonNumber != 3 || item.Calendar.EpisodeNumber != 4 || item.Calendar.EpisodeName != "New Episode" {
		t.Fatalf("expected episode schedule metadata, got %#v", item.Calendar)
	}
}

func TestInvokePluginCalendarIncludesIntermediateSeasonEpisodes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/discover/tv":
			_, _ = w.Write([]byte(`{"page":1,"total_pages":1,"total_results":1,"results":[{"id":502,"name":"Weekly Show","first_air_date":"1963-04-01","poster_path":"/poster.jpg"}]}`))
		case "/3/tv/502":
			_, _ = w.Write([]byte(`{"seasons":[{"season_number":3,"air_date":"2026-08-01","episode_count":3}]}`))
		case "/3/tv/502/season/3":
			_, _ = w.Write([]byte(`{"episodes":[{"id":31,"name":"Episode One","air_date":"2026-08-21","season_number":3,"episode_number":1},{"id":32,"name":"Episode Two","air_date":"2026-08-22","season_number":3,"episode_number":2},{"id":33,"name":"Outside Window","air_date":"2026-08-30","season_number":3,"episode_number":3}]}`))
		default:
			t.Fatalf("unexpected calendar path: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	tmdb := NewTMDBService([]string{"secret"}, upstream.URL+"/3", "https://image.tmdb.org/t/p/original")
	request, _ := json.Marshal(tmdbPluginRequest{From: "2026-08-20", To: "2026-08-26", Region: "US", Limit: 20})
	result, fault := tmdb.InvokePlugin(context.Background(), "content.calendar", "calendar", request, PluginContext{RequestID: "calendar-intermediate", Profile: "en-default", Locale: "en-US"})
	if fault != nil {
		t.Fatalf("unexpected calendar fault: %+v", fault)
	}
	page, ok := result.(PluginCalendarPage)
	if !ok || len(page.Items) != 2 {
		t.Fatalf("expected two in-window episodes, got %#v", result)
	}
	if page.Items[0].Calendar.EpisodeNumber != 1 || page.Items[1].Calendar.EpisodeNumber != 2 {
		t.Fatalf("unexpected episode order: %#v", page.Items)
	}
	if page.Items[0].Preview == nil || page.Items[1].Preview == nil || page.Items[0].Preview.EpisodeInfo == page.Items[1].Preview.EpisodeInfo {
		t.Fatalf("calendar entries share mutable preview state: %#v", page.Items)
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
