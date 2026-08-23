package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TMDBPluginID      = "kerkerker.tmdb-content"
	TMDBPluginVersion = "1.0.0"
	PluginContractV1  = "1.0.0"
)

type PluginContext struct {
	Runtime         string `json:"runtime,omitempty"`
	RequestID       string `json:"requestId"`
	RunID           string `json:"runId,omitempty"`
	Profile         string `json:"profile"`
	Locale          string `json:"locale"`
	Region          string `json:"region"`
	Deadline        string `json:"deadline"`
	ContractVersion string `json:"contractVersion,omitempty"`
}

type PluginExternalRef struct {
	ProviderID   string `json:"providerId"`
	ExternalID   string `json:"externalId"`
	CanonicalURL string `json:"canonicalUrl,omitempty"`
}

type PluginSource struct {
	ProviderID string `json:"providerId"`
	SourceID   string `json:"sourceId,omitempty"`
	SourceURL  string `json:"sourceUrl,omitempty"`
}

type PluginProvenance struct {
	Source        PluginSource `json:"source"`
	PluginVersion string       `json:"pluginVersion"`
	FetchedAt     string       `json:"fetchedAt"`
}

type PluginPreview struct {
	PosterURL   string   `json:"posterUrl,omitempty"`
	BackdropURL string   `json:"backdropUrl,omitempty"`
	Rating      string   `json:"rating,omitempty"`
	URL         string   `json:"url,omitempty"`
	EpisodeInfo string   `json:"episodeInfo,omitempty"`
	Genres      []string `json:"genres,omitempty"`
}

type PluginCandidate struct {
	Type         string              `json:"type"`
	ExternalRefs []PluginExternalRef `json:"externalRefs"`
	Titles       []PluginLocalized   `json:"titles"`
	Preview      *PluginPreview      `json:"preview,omitempty"`
	Overview     []PluginLocalized   `json:"overview,omitempty"`
	ReleaseDate  string              `json:"releaseDate,omitempty"`
	Region       string              `json:"region,omitempty"`
	Catalog      *PluginCatalog      `json:"catalog,omitempty"`
	Provenance   PluginProvenance    `json:"provenance"`
}

type PluginCatalog struct {
	Section *PluginCatalogSection `json:"section,omitempty"`
}

type PluginCatalogSection struct {
	Key    string            `json:"key"`
	Titles []PluginLocalized `json:"titles"`
}

type PluginLocalized struct {
	Locale string `json:"locale"`
	Value  string `json:"value"`
}

type PluginPage struct {
	Items      []PluginCandidate `json:"items"`
	Total      int               `json:"total,omitempty"`
	NextCursor string            `json:"nextCursor,omitempty"`
	HasMore    bool              `json:"hasMore"`
}

type PluginCalendar struct {
	EventID       string  `json:"eventId"`
	AirDate       string  `json:"airDate"`
	SeasonNumber  int     `json:"seasonNumber"`
	EpisodeNumber int     `json:"episodeNumber"`
	EpisodeName   string  `json:"episodeName,omitempty"`
	PosterURL     string  `json:"posterUrl,omitempty"`
	BackdropURL   string  `json:"backdropUrl,omitempty"`
	Rating        float64 `json:"rating,omitempty"`
}

type PluginCalendarCandidate struct {
	PluginCandidate
	Calendar PluginCalendar `json:"calendar"`
}

type PluginCalendarPage struct {
	Items      []PluginCalendarCandidate `json:"items"`
	Total      int                       `json:"total,omitempty"`
	NextCursor string                    `json:"nextCursor,omitempty"`
	HasMore    bool                      `json:"hasMore"`
}

type PluginPhoto struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumbUrl,omitempty"`
}

type PluginRecommendation struct {
	ExternalRefs []PluginExternalRef `json:"externalRefs"`
	Titles       []PluginLocalized   `json:"titles"`
	PosterURL    string              `json:"posterUrl,omitempty"`
	Rating       string              `json:"rating,omitempty"`
}

type PluginDetails struct {
	Rating          string                 `json:"rating,omitempty"`
	Genres          []string               `json:"genres,omitempty"`
	Directors       []string               `json:"directors,omitempty"`
	Actors          []string               `json:"actors,omitempty"`
	Duration        string                 `json:"duration,omitempty"`
	EpisodeCount    string                 `json:"episodeCount,omitempty"`
	Photos          []PluginPhoto          `json:"photos,omitempty"`
	Recommendations []PluginRecommendation `json:"recommendations,omitempty"`
}

type PluginDetailCandidate struct {
	PluginCandidate
	Details PluginDetails `json:"details"`
}

type PluginImageCandidate struct {
	ContentID  string           `json:"contentId"`
	Purpose    string           `json:"purpose"`
	URL        string           `json:"url"`
	Width      int              `json:"width,omitempty"`
	Height     int              `json:"height,omitempty"`
	Provenance PluginProvenance `json:"provenance"`
}

type tmdbPluginRequest struct {
	View        string             `json:"view"`
	Key         string             `json:"key"`
	Category    string             `json:"category"`
	Cursor      string             `json:"cursor"`
	Limit       int                `json:"limit"`
	Filters     tmdbCatalogFilters `json:"filters"`
	From        string             `json:"from"`
	To          string             `json:"to"`
	Region      string             `json:"region"`
	Query       string             `json:"query"`
	Intent      string             `json:"intent"`
	Content     *tmdbHostContent   `json:"content"`
	ExternalRef *PluginExternalRef `json:"externalRef"`
	Purpose     string             `json:"purpose"`
}

type tmdbCatalogFilters struct {
	ContentType string `json:"contentType"`
	Genre       string `json:"genre"`
	Year        string `json:"year"`
	Region      string `json:"region"`
	Sort        string `json:"sort"`
}

type tmdbHostContent struct {
	ContentID    string              `json:"contentId"`
	ExternalRefs []PluginExternalRef `json:"externalRefs"`
}

type tmdbPaged struct {
	Page         int               `json:"page"`
	Results      []tmdbMediaResult `json:"results"`
	TotalPages   int               `json:"total_pages"`
	TotalResults int               `json:"total_results"`
}

type tmdbMediaResult struct {
	ID            int     `json:"id"`
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	VoteAverage   float64 `json:"vote_average"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	GenreIDs      []int   `json:"genre_ids"`
}

type tmdbDetail struct {
	tmdbMediaResult
	Runtime           int           `json:"runtime"`
	EpisodeRunTime    []int         `json:"episode_run_time"`
	NumberOfEpisodes  int           `json:"number_of_episodes"`
	Genres            []tmdbGenre   `json:"genres"`
	ProductionCountry []tmdbCountry `json:"production_countries"`
	Credits           tmdbCredits   `json:"credits"`
	Images            tmdbImages    `json:"images"`
	Recommendations   tmdbPaged     `json:"recommendations"`
}

type tmdbGenre struct {
	Name string `json:"name"`
}

type tmdbCountry struct {
	ISO string `json:"iso_3166_1"`
}

type tmdbCredits struct {
	Crew []tmdbPerson `json:"crew"`
	Cast []tmdbPerson `json:"cast"`
}

type tmdbPerson struct {
	Name string `json:"name"`
	Job  string `json:"job"`
}

type tmdbImages struct {
	Posters   []tmdbImage `json:"posters"`
	Backdrops []tmdbImage `json:"backdrops"`
	Logos     []tmdbImage `json:"logos"`
}

type tmdbImage struct {
	FilePath string `json:"file_path"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type tmdbEpisode struct {
	ID            int     `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	SeasonNumber  int     `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	VoteAverage   float64 `json:"vote_average"`
	StillPath     string  `json:"still_path"`
}

type tmdbEpisodeEnvelope struct {
	NextEpisodeToAir *tmdbEpisode `json:"next_episode_to_air"`
	LastEpisodeToAir *tmdbEpisode `json:"last_episode_to_air"`
}

type PluginFault struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	Status    int            `json:"-"`
}

func (e *PluginFault) Error() string { return e.Message }

func tmdbFault(err error) *PluginFault {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "API key not configured") {
		return &PluginFault{Code: "CONFIGURATION_ERROR", Message: "TMDB 插件未配置 API 密钥", Status: http.StatusServiceUnavailable}
	}
	var requestErr *TMDBRequestError
	if asTMDBRequestError(err, &requestErr) {
		status := http.StatusBadGateway
		if requestErr.Status == http.StatusNotFound {
			status = http.StatusNotFound
		}
		return &PluginFault{Code: "UPSTREAM_ERROR", Message: fmt.Sprintf("TMDB 请求失败（HTTP %d）", requestErr.Status), Retryable: requestErr.Status >= 500 || requestErr.Status == http.StatusTooManyRequests, Status: status}
	}
	return &PluginFault{Code: "UPSTREAM_ERROR", Message: "TMDB 插件请求失败", Retryable: true, Status: http.StatusBadGateway}
}

func asTMDBRequestError(err error, target **TMDBRequestError) bool {
	for err != nil {
		if value, ok := err.(*TMDBRequestError); ok {
			*target = value
			return true
		}
		switch value := err.(type) {
		case interface{ Unwrap() error }:
			err = value.Unwrap()
		default:
			return false
		}
	}
	return false
}

func pageNumber(cursor string) int {
	page, err := strconv.Atoi(cursor)
	if err != nil || page < 1 {
		return 1
	}
	if page > 500 {
		return 500
	}
	return page
}

func resultLimit(limit int) int {
	if limit < 1 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func tmdbTitle(result tmdbMediaResult) string {
	for _, value := range []string{result.Title, result.Name, result.OriginalTitle, result.OriginalName} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func tmdbDate(result tmdbMediaResult) string {
	if result.ReleaseDate != "" {
		return result.ReleaseDate
	}
	return result.FirstAirDate
}

func contentKind(result tmdbMediaResult, fallback string) string {
	kind := result.MediaType
	if kind != "movie" && kind != "tv" {
		kind = fallback
	}
	return kind
}

func contentType(kind string) string {
	if kind == "tv" {
		return "series"
	}
	return "movie"
}

func canonicalTMDBURL(kind string, id int) string {
	return fmt.Sprintf("https://www.themoviedb.org/%s/%d", kind, id)
}

func (s *TMDBService) imageURL(path, size string) string {
	if path == "" {
		return ""
	}
	base := strings.TrimRight(s.imageBase, "/")
	if base == "" {
		base = "https://image.tmdb.org/t/p/original"
	}
	if size != "" {
		for _, suffix := range []string{"/original", "/w45", "/w92", "/w154", "/w185", "/w300", "/w342", "/w500", "/w780", "/w1280"} {
			if strings.HasSuffix(base, suffix) {
				base = strings.TrimSuffix(base, suffix)
				break
			}
		}
		base += "/" + strings.Trim(size, "/")
	}
	return base + "/" + strings.TrimLeft(path, "/")
}

func (s *TMDBService) provenance(sourceURL string) PluginProvenance {
	return PluginProvenance{
		Source:        PluginSource{ProviderID: TMDBPluginID, SourceID: "themoviedb", SourceURL: sourceURL},
		PluginVersion: TMDBPluginVersion,
		FetchedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func (s *TMDBService) candidate(result tmdbMediaResult, fallbackKind, locale string) (PluginCandidate, bool) {
	kind := contentKind(result, fallbackKind)
	if (kind != "movie" && kind != "tv") || result.ID <= 0 {
		return PluginCandidate{}, false
	}
	title := tmdbTitle(result)
	if title == "" {
		return PluginCandidate{}, false
	}
	canonical := canonicalTMDBURL(kind, result.ID)
	posterPath := result.PosterPath
	if posterPath == "" {
		posterPath = result.BackdropPath
	}
	backdropPath := result.BackdropPath
	if backdropPath == "" {
		backdropPath = result.PosterPath
	}
	preview := &PluginPreview{
		PosterURL:   s.imageURL(posterPath, "w500"),
		BackdropURL: s.imageURL(backdropPath, "w1280"),
		URL:         canonical,
	}
	if result.VoteAverage > 0 {
		preview.Rating = strconv.FormatFloat(result.VoteAverage, 'f', -1, 64)
	}
	candidate := PluginCandidate{
		Type:         contentType(kind),
		ExternalRefs: []PluginExternalRef{{ProviderID: TMDBPluginID, ExternalID: strconv.Itoa(result.ID), CanonicalURL: canonical}},
		Titles:       []PluginLocalized{{Locale: locale, Value: title}},
		Preview:      preview,
		ReleaseDate:  tmdbDate(result),
		Provenance:   s.provenance(canonical),
	}
	if result.Overview != "" {
		candidate.Overview = []PluginLocalized{{Locale: locale, Value: strings.TrimSpace(result.Overview)}}
	}
	return candidate, true
}

func (s *TMDBService) fetchPaged(ctx context.Context, path, locale string, params map[string]string) (tmdbPaged, *PluginFault) {
	data, err := s.FetchJSON(ctx, path, locale, params)
	if err != nil {
		return tmdbPaged{}, tmdbFault(err)
	}
	var page tmdbPaged
	if err := json.Unmarshal(data, &page); err != nil {
		return tmdbPaged{}, &PluginFault{Code: "UPSTREAM_ERROR", Message: "TMDB 返回了无效数据", Retryable: true, Status: http.StatusBadGateway}
	}
	return page, nil
}

func requestedCatalogKind(request tmdbPluginRequest) string {
	if request.Filters.ContentType == "movie" {
		return "movie"
	}
	if request.Filters.ContentType == "series" {
		return "tv"
	}
	if request.Key == "series" || request.Category == "series" {
		return "tv"
	}
	if request.Key == "movies" || request.Category == "movies" {
		return "movie"
	}
	switch firstNonEmptyPlugin(request.Key, request.Category) {
	case "hot_movies", "in_theaters", "documentary", "top250":
		return "movie"
	case "hot_tv", "us_tv", "jp_tv", "kr_tv", "anime", "variety":
		return "tv"
	default:
		return ""
	}
}

func catalogSection(key, locale string) *PluginCatalog {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	title := key
	switch key {
	case "movies":
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			title = "电影"
		} else {
			title = "Movies"
		}
	case "series":
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			title = "电视剧"
		} else {
			title = "TV Shows"
		}
	case "latest-movies":
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			title = "最新电影"
		} else {
			title = "Latest Movies"
		}
	case "latest-series":
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			title = "最新剧集"
		} else {
			title = "Latest TV Shows"
		}
	case "top250":
		if strings.HasPrefix(strings.ToLower(locale), "zh") {
			title = "TMDB 高分榜 250"
		} else {
			title = "TMDB Top Rated 250"
		}
	}
	return &PluginCatalog{Section: &PluginCatalogSection{
		Key:    key,
		Titles: []PluginLocalized{{Locale: locale, Value: title}},
	}}
}

func annotateCatalogSection(candidate PluginCandidate, key, locale string) PluginCandidate {
	candidate.Catalog = catalogSection(key, locale)
	return candidate
}

func (s *TMDBService) catalogForKind(ctx context.Context, request tmdbPluginRequest, pc PluginContext, kind, sectionKey string) (PluginPage, *PluginFault) {
	view := request.View
	if view == "" {
		view = "category"
	}
	page := pageNumber(request.Cursor)
	params := map[string]string{"page": strconv.Itoa(page), "include_adult": "false"}
	path := "/" + kind + "/popular"
	fallbackKind := kind
	key := firstNonEmptyPlugin(request.Key, request.Category)
	if view == "latest" || view == "new-releases" {
		path = "/discover/" + kind
		// TMDB includes announced/future titles in Discover (for example
		// 100 Years in 2099). Latest/release pages must never surface content
		// that has not aired or premiered yet.
		today := time.Now().UTC().Format("2006-01-02")
		if kind == "tv" {
			params["sort_by"] = "first_air_date.desc"
			params["first_air_date.lte"] = today
			if request.Filters.Year != "" {
				params["first_air_date_year"] = request.Filters.Year
			}
		} else {
			params["sort_by"] = "primary_release_date.desc"
			params["primary_release_date.lte"] = today
			if request.Filters.Year != "" {
				params["primary_release_year"] = request.Filters.Year
			}
		}
	} else {
		switch key {
		case "in_theaters":
			path = "/movie/now_playing"
		case "us_tv", "jp_tv", "kr_tv", "anime", "variety", "documentary":
			path = "/discover/" + kind
			params["sort_by"] = "popularity.desc"
			switch key {
			case "us_tv":
				params["with_origin_country"] = "US"
			case "jp_tv":
				params["with_origin_country"] = "JP"
			case "kr_tv":
				params["with_origin_country"] = "KR"
			case "anime":
				params["with_genres"] = "16"
				params["with_origin_country"] = "JP"
			case "variety":
				params["with_genres"] = "10764"
			case "documentary":
				params["with_genres"] = "99"
			}
		}
	}
	if request.Filters.Region != "" {
		params["region"] = request.Filters.Region
	}
	if request.Filters.Genre != "" {
		params["with_genres"] = request.Filters.Genre
	}
	if request.Filters.Sort == "rating" {
		params["sort_by"] = "vote_average.desc"
	}
	if request.Filters.Sort == "recommended" {
		params["sort_by"] = "popularity.desc"
	}
	pageData, fault := s.fetchPaged(ctx, path, pc.Locale, params)
	if fault != nil {
		return PluginPage{}, fault
	}
	items := make([]PluginCandidate, 0, resultLimit(request.Limit))
	for _, result := range pageData.Results {
		candidate, ok := s.candidate(result, fallbackKind, pc.Locale)
		if ok {
			if sectionKey != "" {
				candidate = annotateCatalogSection(candidate, sectionKey, pc.Locale)
			}
			items = append(items, candidate)
		}
		if len(items) >= resultLimit(request.Limit) {
			break
		}
	}
	return PluginPage{Items: items, Total: pageData.TotalResults, NextCursor: nextCursor(page, pageData.TotalPages), HasMore: pageData.TotalPages > page}, nil
}

func mergeCatalogPages(left, right PluginPage, limit int) PluginPage {
	items := make([]PluginCandidate, 0, limit)
	seen := make(map[string]struct{}, len(left.Items)+len(right.Items))
	for _, page := range []PluginPage{left, right} {
		for _, item := range page.Items {
			id := ""
			if len(item.ExternalRefs) > 0 {
				id = item.ExternalRefs[0].ProviderID + ":" + item.ExternalRefs[0].ExternalID
			}
			if id != "" {
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
			}
			items = append(items, item)
			if len(items) >= limit {
				break
			}
		}
		if len(items) >= limit {
			break
		}
	}
	return PluginPage{
		Items:      items,
		Total:      left.Total + right.Total,
		HasMore:    left.HasMore || right.HasMore,
		NextCursor: firstNonEmptyPlugin(left.NextCursor, right.NextCursor),
	}
}

func (s *TMDBService) mixedCatalog(ctx context.Context, request tmdbPluginRequest, pc PluginContext, sectioned bool) (PluginPage, *PluginFault) {
	limit := resultLimit(request.Limit)
	partRequest := request
	partRequest.Limit = (limit + 1) / 2
	leftKey, rightKey := "", ""
	if sectioned {
		leftKey, rightKey = "latest-movies", "latest-series"
	}
	left, leftFault := s.catalogForKind(ctx, partRequest, pc, "movie", leftKey)
	if leftFault != nil {
		return PluginPage{}, leftFault
	}
	right, rightFault := s.catalogForKind(ctx, partRequest, pc, "tv", rightKey)
	if rightFault != nil {
		return PluginPage{}, rightFault
	}
	return mergeCatalogPages(left, right, limit), nil
}

func (s *TMDBService) top250(ctx context.Context, request tmdbPluginRequest, pc PluginContext) (PluginPage, *PluginFault) {
	const totalLimit = 250
	const providerPageSize = 20
	const pageCount = (totalLimit + providerPageSize - 1) / providerPageSize
	items := make([]PluginCandidate, 0, totalLimit)
	seen := make(map[string]struct{}, totalLimit)
	for page := 1; page <= pageCount; page++ {
		pageData, fault := s.fetchPaged(ctx, "/movie/top_rated", pc.Locale, map[string]string{
			"page":           strconv.Itoa(page),
			"include_adult":  "false",
			"vote_count.gte": "200",
		})
		if fault != nil {
			return PluginPage{}, fault
		}
		for _, result := range pageData.Results {
			candidate, ok := s.candidate(result, "movie", pc.Locale)
			if !ok || len(candidate.ExternalRefs) == 0 {
				continue
			}
			id := candidate.ExternalRefs[0].ExternalID
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			items = append(items, annotateCatalogSection(candidate, "top250", pc.Locale))
			if len(items) >= totalLimit {
				break
			}
		}
		if len(items) >= totalLimit || len(pageData.Results) == 0 {
			break
		}
	}
	return PluginPage{Items: items, Total: len(items), HasMore: false}, nil
}

func (s *TMDBService) catalog(ctx context.Context, request tmdbPluginRequest, pc PluginContext) (PluginPage, *PluginFault) {
	view := request.View
	if view == "" {
		view = "category"
	}
	key := firstNonEmptyPlugin(request.Key, request.Category)
	if key == "top250" {
		return s.top250(ctx, request, pc)
	}
	requestedKind := requestedCatalogKind(request)
	if requestedKind == "" && (view == "latest" || view == "new-releases") {
		return s.mixedCatalog(ctx, request, pc, view == "new-releases")
	}
	if requestedKind == "" {
		trend := "day"
		if view == "featured" {
			trend = "week"
		}
		page := pageNumber(request.Cursor)
		pageData, fault := s.fetchPaged(ctx, "/trending/all/"+trend, pc.Locale, map[string]string{
			"page":          strconv.Itoa(page),
			"include_adult": "false",
		})
		if fault != nil {
			return PluginPage{}, fault
		}
		items := make([]PluginCandidate, 0, resultLimit(request.Limit))
		for _, result := range pageData.Results {
			candidate, ok := s.candidate(result, "", pc.Locale)
			if ok {
				items = append(items, candidate)
			}
			if len(items) >= resultLimit(request.Limit) {
				break
			}
		}
		return PluginPage{Items: items, Total: pageData.TotalResults, NextCursor: nextCursor(page, pageData.TotalPages), HasMore: pageData.TotalPages > page}, nil
	}
	sectionKey := ""
	if view == "sections" && (key == "movies" || key == "series") {
		sectionKey = key
	}
	request.View = view
	return s.catalogForKind(ctx, request, pc, requestedKind, sectionKey)
}

func firstNonEmptyPlugin(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nextCursor(page, totalPages int) string {
	if totalPages > page {
		return strconv.Itoa(page + 1)
	}
	return ""
}

func (s *TMDBService) calendarEpisodes(ctx context.Context, id int, locale string) ([]tmdbEpisode, error) {
	data, err := s.FetchJSON(ctx, "/tv/"+strconv.Itoa(id), locale, nil)
	if err != nil {
		return nil, err
	}
	var envelope tmdbEpisodeEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	episodes := make([]tmdbEpisode, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, episode := range []*tmdbEpisode{envelope.LastEpisodeToAir, envelope.NextEpisodeToAir} {
		if episode == nil || episode.ID <= 0 || episode.AirDate == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", episode.ID, episode.AirDate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		episodes = append(episodes, *episode)
	}
	return episodes, nil
}

func (s *TMDBService) calendar(ctx context.Context, request tmdbPluginRequest, pc PluginContext) (PluginCalendarPage, *PluginFault) {
	from, fromErr := time.Parse("2006-01-02", strings.TrimSpace(request.From))
	to, toErr := time.Parse("2006-01-02", strings.TrimSpace(request.To))
	if fromErr != nil || toErr != nil || to.Before(from) || to.Sub(from) > 30*24*time.Hour {
		return PluginCalendarPage{}, &PluginFault{Code: "INVALID_REQUEST", Message: "日历日期范围无效", Status: http.StatusBadRequest}
	}
	page := pageNumber(request.Cursor)
	params := map[string]string{
		"page":          strconv.Itoa(page),
		"air_date.gte":  request.From,
		"air_date.lte":  request.To,
		"sort_by":       "first_air_date.asc",
		"include_adult": "false",
	}
	region := request.Region
	if region == "" {
		region = pc.Region
	}
	if region != "" {
		params["with_origin_country"] = region
	}
	pageData, fault := s.fetchPaged(ctx, "/discover/tv", pc.Locale, params)
	if fault != nil {
		return PluginCalendarPage{}, fault
	}
	// Discover TV returns show-level records. Resolve each show's last/next
	// episode so the calendar contains actual episode dates rather than the
	// show's historical first_air_date (which can be decades outside the range).
	items := make([]PluginCalendarCandidate, 0, resultLimit(request.Limit))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, result := range pageData.Results {
		result := result
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			episodes, err := s.calendarEpisodes(ctx, result.ID, pc.Locale)
			if err != nil {
				return
			}
			candidate, ok := s.candidate(result, "tv", pc.Locale)
			if !ok {
				return
			}
			posterPath := result.PosterPath
			if posterPath == "" {
				posterPath = result.BackdropPath
			}
			backdropPath := result.BackdropPath
			if backdropPath == "" {
				backdropPath = result.PosterPath
			}
			for _, episode := range episodes {
				if episode.AirDate < request.From || episode.AirDate > request.To {
					continue
				}
				candidateCopy := candidate
				if candidateCopy.Preview != nil && episode.Name != "" {
					candidateCopy.Preview.EpisodeInfo = fmt.Sprintf("S%dE%d", episode.SeasonNumber, episode.EpisodeNumber)
				}
				candidateCalendar := PluginCalendar{
					EventID:       fmt.Sprintf("%d:%s:%d:%d", result.ID, episode.AirDate, episode.SeasonNumber, episode.EpisodeNumber),
					AirDate:       episode.AirDate,
					SeasonNumber:  episode.SeasonNumber,
					EpisodeNumber: episode.EpisodeNumber,
					EpisodeName:   episode.Name,
					PosterURL:     s.imageURL(posterPath, "w500"),
					BackdropURL:   s.imageURL(backdropPath, "w1280"),
					Rating:        episode.VoteAverage,
				}
				if candidateCalendar.Rating == 0 {
					candidateCalendar.Rating = result.VoteAverage
				}
				mu.Lock()
				items = append(items, PluginCalendarCandidate{PluginCandidate: candidateCopy, Calendar: candidateCalendar})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Calendar.AirDate != items[right].Calendar.AirDate {
			return items[left].Calendar.AirDate < items[right].Calendar.AirDate
		}
		return items[left].Calendar.EventID < items[right].Calendar.EventID
	})
	total := len(items)
	limit := resultLimit(request.Limit)
	if len(items) > limit {
		items = items[:limit]
	}
	return PluginCalendarPage{Items: items, Total: total, HasMore: false}, nil
}

func (s *TMDBService) lookupDetail(ctx context.Context, id, locale string) (*tmdbDetail, string, *PluginFault) {
	if id == "" || len(id) > 20 {
		return nil, "", &PluginFault{Code: "CONFIGURATION_ERROR", Message: "TMDB 外部 ID 无效", Status: http.StatusBadRequest}
	}
	if _, err := strconv.Atoi(id); err != nil {
		return nil, "", &PluginFault{Code: "CONFIGURATION_ERROR", Message: "TMDB 外部 ID 必须为数字", Status: http.StatusBadRequest}
	}
	for _, kind := range []string{"movie", "tv"} {
		data, err := s.FetchJSON(ctx, "/"+kind+"/"+url.PathEscape(id), locale, map[string]string{"append_to_response": "credits,images,recommendations"})
		if err == nil {
			var detail tmdbDetail
			if json.Unmarshal(data, &detail) != nil {
				return nil, "", &PluginFault{Code: "UPSTREAM_ERROR", Message: "TMDB 返回了无效详情", Retryable: true, Status: http.StatusBadGateway}
			}
			return &detail, kind, nil
		}
		var requestErr *TMDBRequestError
		if asTMDBRequestError(err, &requestErr) && requestErr.Status == http.StatusNotFound {
			continue
		}
		return nil, "", tmdbFault(err)
	}
	return nil, "", nil
}

func detailID(request tmdbPluginRequest) string {
	if request.ExternalRef != nil && request.ExternalRef.ProviderID == TMDBPluginID {
		return request.ExternalRef.ExternalID
	}
	if request.Content != nil {
		for _, ref := range request.Content.ExternalRefs {
			if ref.ProviderID == TMDBPluginID {
				return ref.ExternalID
			}
		}
	}
	return ""
}

func (s *TMDBService) detail(ctx context.Context, request tmdbPluginRequest, pc PluginContext) (*PluginDetailCandidate, *PluginFault) {
	detail, kind, fault := s.lookupDetail(ctx, detailID(request), pc.Locale)
	if fault != nil || detail == nil {
		return nil, fault
	}
	base, ok := s.candidate(detail.tmdbMediaResult, kind, pc.Locale)
	if !ok {
		return nil, &PluginFault{Code: "UPSTREAM_ERROR", Message: "TMDB 详情缺少标题或 ID", Status: http.StatusBadGateway}
	}
	photos := make([]PluginPhoto, 0, 16)
	for index, image := range append(append([]tmdbImage{}, detail.Images.Posters[:min(len(detail.Images.Posters), 8)]...), detail.Images.Backdrops[:min(len(detail.Images.Backdrops), 8)]...) {
		if image.FilePath == "" {
			continue
		}
		photos = append(photos, PluginPhoto{ID: image.FilePath, URL: s.imageURL(image.FilePath, "w500"), ThumbURL: s.imageURL(image.FilePath, "w185")})
		if index >= 15 {
			break
		}
	}
	genres := make([]string, 0, len(detail.Genres))
	for _, genre := range detail.Genres {
		if genre.Name != "" {
			genres = append(genres, genre.Name)
		}
	}
	directors := make([]string, 0)
	for _, person := range detail.Credits.Crew {
		if person.Job == "Director" && person.Name != "" {
			directors = append(directors, person.Name)
		}
	}
	actors := make([]string, 0, min(len(detail.Credits.Cast), 12))
	for _, person := range detail.Credits.Cast {
		if person.Name != "" {
			actors = append(actors, person.Name)
		}
		if len(actors) >= 12 {
			break
		}
	}
	recommendations := make([]PluginRecommendation, 0, min(len(detail.Recommendations.Results), 20))
	for _, result := range detail.Recommendations.Results {
		candidate, ok := s.candidate(result, kind, pc.Locale)
		if !ok {
			continue
		}
		recommendations = append(recommendations, PluginRecommendation{ExternalRefs: candidate.ExternalRefs, Titles: candidate.Titles, PosterURL: candidate.Preview.PosterURL, Rating: candidate.Preview.Rating})
		if len(recommendations) >= 20 {
			break
		}
	}
	duration := ""
	runtime := detail.Runtime
	if kind == "tv" && len(detail.EpisodeRunTime) > 0 {
		runtime = detail.EpisodeRunTime[0]
	}
	if runtime > 0 {
		if strings.HasPrefix(strings.ToLower(pc.Locale), "zh") {
			duration = fmt.Sprintf("%d 分钟", runtime)
		} else {
			duration = fmt.Sprintf("%d minutes", runtime)
		}
	}
	episodeCount := ""
	if kind == "tv" && detail.NumberOfEpisodes > 0 {
		episodeCount = strconv.Itoa(detail.NumberOfEpisodes)
	}
	if detail.BackdropPath != "" {
		base.Preview.BackdropURL = s.imageURL(detail.BackdropPath, "w1280")
	} else if base.Preview.BackdropURL == "" {
		base.Preview.BackdropURL = base.Preview.PosterURL
	}
	return &PluginDetailCandidate{PluginCandidate: base, Details: PluginDetails{Rating: base.Preview.Rating, Genres: genres, Directors: directors, Actors: actors, Duration: duration, EpisodeCount: episodeCount, Photos: photos, Recommendations: recommendations}}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *TMDBService) search(ctx context.Context, request tmdbPluginRequest, pc PluginContext) (PluginPage, *PluginFault) {
	if query := strings.TrimSpace(request.Query); query == "" || len(query) > 200 {
		return PluginPage{}, &PluginFault{Code: "INVALID_REQUEST", Message: "搜索关键词无效", Status: http.StatusBadRequest}
	}
	page := pageNumber(request.Cursor)
	pageData, fault := s.fetchPaged(ctx, "/search/multi", pc.Locale, map[string]string{"query": strings.TrimSpace(request.Query), "page": strconv.Itoa(page), "include_adult": "false"})
	if fault != nil {
		return PluginPage{}, fault
	}
	items := make([]PluginCandidate, 0, resultLimit(request.Limit))
	for _, result := range pageData.Results {
		candidate, ok := s.candidate(result, "", pc.Locale)
		if ok {
			items = append(items, candidate)
		}
		if len(items) >= resultLimit(request.Limit) {
			break
		}
	}
	return PluginPage{Items: items, Total: pageData.TotalResults, NextCursor: nextCursor(page, pageData.TotalPages), HasMore: pageData.TotalPages > page}, nil
}

func (s *TMDBService) images(ctx context.Context, request tmdbPluginRequest, pc PluginContext) ([]PluginImageCandidate, *PluginFault) {
	contentID := ""
	if request.Content != nil {
		contentID = request.Content.ContentID
	}
	if contentID == "" {
		return nil, &PluginFault{Code: "CONFIGURATION_ERROR", Message: "图片请求缺少宿主 contentId", Status: http.StatusBadRequest}
	}
	detail, kind, fault := s.lookupDetail(ctx, detailID(request), pc.Locale)
	if fault != nil || detail == nil {
		return nil, fault
	}
	purpose := request.Purpose
	if purpose == "" {
		purpose = "poster"
	}
	rows := make([]tmdbImage, 0, 21)
	if purpose == "poster" && detail.PosterPath != "" {
		rows = append(rows, tmdbImage{FilePath: detail.PosterPath})
	}
	if purpose == "backdrop" && detail.BackdropPath != "" {
		rows = append(rows, tmdbImage{FilePath: detail.BackdropPath})
	}
	switch purpose {
	case "poster":
		rows = append(rows, detail.Images.Posters...)
	case "backdrop", "still":
		rows = append(rows, detail.Images.Backdrops...)
	case "logo":
		rows = append(rows, detail.Images.Logos...)
	}
	images := make([]PluginImageCandidate, 0, min(len(rows), 20))
	source := canonicalTMDBURL(kind, detail.ID)
	for _, image := range rows {
		if image.FilePath == "" {
			continue
		}
		size := "w1280"
		if purpose == "poster" {
			size = "w500"
		}
		images = append(images, PluginImageCandidate{ContentID: contentID, Purpose: purpose, URL: s.imageURL(image.FilePath, size), Width: image.Width, Height: image.Height, Provenance: s.provenance(source)})
		if len(images) >= 20 {
			break
		}
	}
	return images, nil
}

// InvokePlugin handles the provider-neutral v1 operation allow-list. It is
// intentionally the only place where TMDB paths and API credentials meet.
func (s *TMDBService) InvokePlugin(ctx context.Context, capability, operation string, request json.RawMessage, pc PluginContext) (any, *PluginFault) {
	if capability == "" || operation == "" {
		return nil, &PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件能力和操作不能为空", Status: http.StatusBadRequest}
	}
	if (capability == "content.catalog" && operation == "catalog") || (capability == "content.calendar" && operation == "calendar") || (capability == "content.detail" && operation == "detail") || (capability == "content.search" && operation == "search") || (capability == "asset.image" && operation == "image") {
		// allowed below
	} else {
		return nil, &PluginFault{Code: "UNSUPPORTED_CAPABILITY", Message: "TMDB 插件不支持该能力或操作", Status: http.StatusBadRequest}
	}
	var parsed tmdbPluginRequest
	if err := json.Unmarshal(request, &parsed); err != nil {
		return nil, &PluginFault{Code: "CONFIGURATION_ERROR", Message: "插件请求格式无效", Status: http.StatusBadRequest}
	}
	switch operation {
	case "catalog":
		return s.catalog(ctx, parsed, pc)
	case "calendar":
		return s.calendar(ctx, parsed, pc)
	case "detail":
		return s.detail(ctx, parsed, pc)
	case "search":
		return s.search(ctx, parsed, pc)
	case "image":
		return s.images(ctx, parsed, pc)
	}
	return nil, &PluginFault{Code: "UNSUPPORTED_CAPABILITY", Message: "TMDB 插件不支持该操作", Status: http.StatusBadRequest}
}
