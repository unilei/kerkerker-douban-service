package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"

	"github.com/rs/zerolog/log"
	"golang.org/x/sync/singleflight"
)

const defaultMaxImageBytes int64 = 10 * 1024 * 1024
const defaultSyncConcurrency = 8
const imageSyncOperationTimeout = 30 * time.Second

// FetchedImage is the normalized result of downloading a remote image.
type FetchedImage struct {
	Body        []byte
	ContentType string
}

// StoredObject is the object that will be written to R2.
type StoredObject struct {
	Key          string
	Body         []byte
	ContentType  string
	CacheControl string
}

// ImageFetcher downloads a source image.
type ImageFetcher interface {
	FetchImage(ctx context.Context, imageURL string, maxBytes int64) (FetchedImage, error)
}

// ObjectStore writes image objects to durable storage.
type ObjectStore interface {
	PutObject(ctx context.Context, object StoredObject) error
}

// ImageSyncerConfig configures Douban image mirroring.
type ImageSyncerConfig struct {
	Enabled       bool
	PublicBaseURL string
	KeyPrefix     string
	MaxImageBytes int64
}

// ImageSyncer mirrors Douban image URLs into object storage and returns public URLs.
type ImageSyncer struct {
	cfg     ImageSyncerConfig
	fetcher ImageFetcher
	store   ObjectStore
	// mapStore 持久化「原图URL → R2镜像URL」映射，使进程重启后能复用已上传对象。
	// 为 nil 时仅用进程内存缓存（旧行为）。
	mapStore repository.ImageMapStore

	mu    sync.RWMutex
	cache map[string]string
	// flight 按「原图URL」去重 fetch+upload：同一进程内并发请求同一图片时，
	// 只有一个 goroutine 真正下载并上传，其余调用复用其结果，避免 R2 重复上传。
	flight singleflight.Group
}

// NewImageSyncer creates a new image syncer.
func NewImageSyncer(cfg ImageSyncerConfig, fetcher ImageFetcher, store ObjectStore) *ImageSyncer {
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.KeyPrefix = strings.Trim(strings.TrimSpace(cfg.KeyPrefix), "/")
	if cfg.MaxImageBytes <= 0 {
		cfg.MaxImageBytes = defaultMaxImageBytes
	}

	return &ImageSyncer{
		cfg:     cfg,
		fetcher: fetcher,
		store:   store,
		cache:   make(map[string]string),
	}
}

// SetMapStore 注入持久化映射存储。可选调用；传入 nil 表示仅用内存缓存。
func (s *ImageSyncer) SetMapStore(store repository.ImageMapStore) {
	if s == nil {
		return
	}
	s.mapStore = store
}

// Enabled reports whether the syncer is ready to mirror images.
func (s *ImageSyncer) Enabled() bool {
	return s != nil &&
		s.cfg.Enabled &&
		s.cfg.PublicBaseURL != "" &&
		s.fetcher != nil &&
		s.store != nil
}

// PersistentMappingEnabled reports whether successful mirrors survive process restarts.
func (s *ImageSyncer) PersistentMappingEnabled() bool {
	return s != nil && s.mapStore != nil
}

// LookupURL returns a previously mirrored URL without performing network I/O.
// Plugin responses use this fast path so an already warmed R2 mapping never
// delays the request; a background SyncURLs call handles cache misses.
func (s *ImageSyncer) LookupURL(rawURL string) string {
	if s == nil {
		return rawURL
	}
	trimmedURL := strings.TrimSpace(rawURL)
	if trimmedURL == "" || strings.HasPrefix(trimmedURL, s.cfg.PublicBaseURL+"/") {
		return trimmedURL
	}
	if cached, ok := s.cachedURL(trimmedURL); ok {
		return cached
	}
	return rawURL
}

// SyncURL mirrors a Douban/TMDB image URL to R2. It returns the original URL on any failure.
func (s *ImageSyncer) SyncURL(ctx context.Context, rawURL string) string {
	if !s.Enabled() {
		return rawURL
	}

	trimmedURL := strings.TrimSpace(rawURL)
	if trimmedURL == "" {
		return rawURL
	}

	if strings.HasPrefix(trimmedURL, s.cfg.PublicBaseURL+"/") {
		return trimmedURL
	}

	if !isMirrorableImageURL(trimmedURL) {
		return rawURL
	}

	if cached, ok := s.cachedURL(trimmedURL); ok {
		return cached
	}

	// 内存未命中时查持久化映射（命中即复用，无需重新上传）。
	if s.mapStore != nil {
		if mapped, err := s.mapStore.Get(ctx, trimmedURL); err == nil && mapped != "" {
			s.setCachedURL(trimmedURL, mapped)
			return mapped
		}
	}

	// singleflight 去重：同一 URL 的并发上传只执行一次，其余请求复用结果。
	// 首次完成后会写入 cache/mapStore，之后的新请求会走缓存路径，不再进入 flight。
	result, _, _ := s.flight.Do(trimmedURL, func() (any, error) {
		return s.fetchAndUpload(ctx, trimmedURL, rawURL), nil
	})
	return result.(string)
}

// fetchAndUpload 执行单张图片的下载与上传，并在成功后写回内存缓存与持久化映射。
// 失败时返回原始 URL（降级，不阻断主流程）。
func (s *ImageSyncer) fetchAndUpload(ctx context.Context, trimmedURL, rawURL string) string {
	operationCtx, cancel := context.WithTimeout(ctx, imageSyncOperationTimeout)
	defer cancel()

	image, err := s.fetcher.FetchImage(operationCtx, trimmedURL, s.cfg.MaxImageBytes)
	if err != nil {
		log.Warn().Err(err).Str("url", trimmedURL).Msg("Failed to fetch Douban image for R2 sync")
		return rawURL
	}

	if image.ContentType == "" {
		image.ContentType = http.DetectContentType(image.Body)
	}
	image.ContentType = strings.TrimSpace(strings.Split(image.ContentType, ";")[0])
	if !strings.HasPrefix(image.ContentType, "image/") {
		log.Warn().Str("url", trimmedURL).Str("content_type", image.ContentType).Msg("Skipping non-image Douban URL")
		return rawURL
	}

	key := s.objectKey(trimmedURL, image.ContentType)
	publicURL := s.publicURL(key)
	err = s.store.PutObject(operationCtx, StoredObject{
		Key:          key,
		Body:         image.Body,
		ContentType:  image.ContentType,
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		log.Warn().Err(err).Str("url", trimmedURL).Str("key", key).Msg("Failed to upload Douban image to R2")
		return rawURL
	}

	s.setCachedURL(trimmedURL, publicURL)
	if s.mapStore != nil {
		if err := s.mapStore.Put(ctx, trimmedURL, publicURL); err != nil {
			log.Warn().Err(err).Str("url", trimmedURL).Msg("Failed to persist image mapping")
		}
	}
	return publicURL
}

// SyncSubjectImages rewrites Subject image fields.
func (s *ImageSyncer) SyncSubjectImages(ctx context.Context, subjects []model.Subject) []model.Subject {
	urls := make([]string, 0, len(subjects))
	for i := range subjects {
		urls = append(urls, subjects[i].Cover)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range subjects {
		subjects[i].Cover = synced[subjects[i].Cover]
	}
	return subjects
}

// SyncCategoryDataImages rewrites nested Subject image fields in category data.
func (s *ImageSyncer) SyncCategoryDataImages(ctx context.Context, categories []model.CategoryData) []model.CategoryData {
	urls := make([]string, 0)
	for i := range categories {
		for j := range categories[i].Data {
			urls = append(urls, categories[i].Data[j].Cover)
		}
	}
	synced := s.syncURLs(ctx, urls)

	for i := range categories {
		for j := range categories[i].Data {
			categories[i].Data[j].Cover = synced[categories[i].Data[j].Cover]
		}
	}
	return categories
}

// SyncSuggestImages rewrites SuggestItem image fields.
func (s *ImageSyncer) SyncSuggestImages(ctx context.Context, items []model.SuggestItem) []model.SuggestItem {
	urls := make([]string, 0, len(items))
	for i := range items {
		urls = append(urls, items[i].Img)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range items {
		items[i].Img = synced[items[i].Img]
	}
	return items
}

// SyncSearchResultImages rewrites every image field in a search result.
func (s *ImageSyncer) SyncSearchResultImages(ctx context.Context, result model.SearchResult) model.SearchResult {
	urls := make([]string, 0, len(result.Suggest)+len(result.Advanced))
	for i := range result.Suggest {
		urls = append(urls, result.Suggest[i].Img)
	}
	for i := range result.Advanced {
		urls = append(urls, result.Advanced[i].Cover)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range result.Suggest {
		result.Suggest[i].Img = synced[result.Suggest[i].Img]
	}
	for i := range result.Advanced {
		result.Advanced[i].Cover = synced[result.Advanced[i].Cover]
	}
	return result
}

// SyncPhotosImages rewrites Photo image fields.
func (s *ImageSyncer) SyncPhotosImages(ctx context.Context, photos []model.Photo) []model.Photo {
	urls := make([]string, 0, len(photos)*2)
	for i := range photos {
		urls = append(urls, photos[i].Image, photos[i].Thumb)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range photos {
		photos[i].Image = synced[photos[i].Image]
		photos[i].Thumb = synced[photos[i].Thumb]
	}
	return photos
}

// SyncSubjectDetailImages rewrites every Douban image field in subject details.
func (s *ImageSyncer) SyncSubjectDetailImages(ctx context.Context, detail model.SubjectDetail) model.SubjectDetail {
	urls := []string{detail.Cover}
	for i := range detail.Photos {
		urls = append(urls, detail.Photos[i].Image, detail.Photos[i].Thumb)
	}
	for i := range detail.Recommendations {
		urls = append(urls, detail.Recommendations[i].Cover)
	}
	synced := s.syncURLs(ctx, urls)

	detail.Cover = synced[detail.Cover]
	for i := range detail.Photos {
		detail.Photos[i].Image = synced[detail.Photos[i].Image]
		detail.Photos[i].Thumb = synced[detail.Photos[i].Thumb]
	}
	for i := range detail.Recommendations {
		detail.Recommendations[i].Cover = synced[detail.Recommendations[i].Cover]
	}
	return detail
}

// SyncHeroImages rewrites Douban image fields in hero payloads.
func (s *ImageSyncer) SyncHeroImages(ctx context.Context, heroes []model.HeroMovie) []model.HeroMovie {
	urls := make([]string, 0, len(heroes)*3)
	for i := range heroes {
		urls = append(urls, heroes[i].Cover, heroes[i].PosterVertical, heroes[i].PosterHorizontal)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range heroes {
		heroes[i].Cover = synced[heroes[i].Cover]
		heroes[i].PosterVertical = synced[heroes[i].PosterVertical]
		heroes[i].PosterHorizontal = synced[heroes[i].PosterHorizontal]
	}
	return heroes
}

// SyncCalendarImages rewrites calendar day entries' posters/backdrops (TMDB) to R2 URLs.
func (s *ImageSyncer) SyncCalendarImages(ctx context.Context, days []model.CalendarDay) []model.CalendarDay {
	urls := make([]string, 0)
	for i := range days {
		for j := range days[i].Entries {
			urls = append(urls, days[i].Entries[j].Poster, days[i].Entries[j].Backdrop)
		}
	}
	synced := s.syncURLs(ctx, urls)

	for i := range days {
		for j := range days[i].Entries {
			days[i].Entries[j].Poster = synced[days[i].Entries[j].Poster]
			days[i].Entries[j].Backdrop = synced[days[i].Entries[j].Backdrop]
		}
	}
	return days
}

// SyncCalendarEntriesImages rewrites flat calendar entries' posters/backdrops (airing endpoint).
func (s *ImageSyncer) SyncCalendarEntriesImages(ctx context.Context, entries []model.CalendarEntry) []model.CalendarEntry {
	urls := make([]string, 0, len(entries)*2)
	for i := range entries {
		urls = append(urls, entries[i].Poster, entries[i].Backdrop)
	}
	synced := s.syncURLs(ctx, urls)

	for i := range entries {
		entries[i].Poster = synced[entries[i].Poster]
		entries[i].Backdrop = synced[entries[i].Backdrop]
	}
	return entries
}

// SyncURLs mirrors a deduplicated set of Douban/TMDB image URLs. It returns a
// mapping from each original URL to either its R2 URL or the original URL on
// failure, preserving the service's existing graceful-degradation behavior.
func (s *ImageSyncer) SyncURLs(ctx context.Context, urls []string) map[string]string {
	return s.syncURLs(ctx, urls)
}

func (s *ImageSyncer) syncURLs(ctx context.Context, urls []string) map[string]string {
	unique := make(map[string]struct{}, len(urls))
	for _, rawURL := range urls {
		unique[rawURL] = struct{}{}
	}

	results := make(map[string]string, len(unique))
	if len(unique) == 0 {
		return results
	}

	workerCount := defaultSyncConcurrency
	if len(unique) < workerCount {
		workerCount = len(unique)
	}

	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(workerCount)

	for range workerCount {
		go func() {
			defer wg.Done()
			for rawURL := range jobs {
				syncedURL := s.SyncURL(ctx, rawURL)
				mu.Lock()
				results[rawURL] = syncedURL
				mu.Unlock()
			}
		}()
	}

	for rawURL := range unique {
		jobs <- rawURL
	}
	close(jobs)
	wg.Wait()

	return results
}

func (s *ImageSyncer) cachedURL(rawURL string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cached, ok := s.cache[rawURL]
	return cached, ok
}

func (s *ImageSyncer) setCachedURL(rawURL, publicURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[rawURL] = publicURL
}

func (s *ImageSyncer) objectKey(rawURL, contentType string) string {
	sum := sha256.Sum256([]byte(rawURL))
	fileName := hex.EncodeToString(sum[:]) + imageExtension(rawURL, contentType)
	if s.cfg.KeyPrefix == "" {
		return fileName
	}
	return s.cfg.KeyPrefix + "/" + fileName
}

func (s *ImageSyncer) publicURL(key string) string {
	return s.cfg.PublicBaseURL + "/" + strings.TrimLeft(key, "/")
}

// isMirrorableImageURL reports whether the URL points at an image host we mirror:
// 豆瓣图床（doubanio.com / douban.com）与 TMDB（image.tmdb.org，国内被墙，需镜像）。
func isMirrorableImageURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	host := strings.ToLower(parsed.Hostname())
	mirrorable :=
		isHostOrSubdomain(host, "doubanio.com") ||
			isHostOrSubdomain(host, "douban.com") ||
			isHostOrSubdomain(host, "tmdb.org") ||
			isHostOrSubdomain(host, "themoviedb.org")
	if !mirrorable {
		return false
	}

	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif", ".bmp":
		return true
	}

	return strings.Contains(parsed.Path, "/view/photo/")
}

func isHostOrSubdomain(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func imageExtension(rawURL, contentType string) string {
	if parsed, err := url.Parse(rawURL); err == nil {
		ext := strings.ToLower(path.Ext(parsed.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif", ".bmp":
			return ext
		}
	}

	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/bmp":
		return ".bmp"
	}

	if extensions, err := mime.ExtensionsByType(contentType); err == nil && len(extensions) > 0 {
		return extensions[0]
	}

	return ".jpg"
}

// HTTPImageFetcher downloads images with browser-like headers for Douban.
type HTTPImageFetcher struct {
	client *http.Client
}

// NewHTTPImageFetcher creates an HTTP image fetcher with a request timeout.
func NewHTTPImageFetcher(timeout time.Duration) *HTTPImageFetcher {
	return &HTTPImageFetcher{
		client: &http.Client{Timeout: timeout},
	}
}

// FetchImage downloads an image and rejects oversized or non-image responses.
func (f *HTTPImageFetcher) FetchImage(ctx context.Context, imageURL string, maxBytes int64) (FetchedImage, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxImageBytes
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return FetchedImage{}, err
	}
	req.Header.Set("User-Agent", getImageUserAgent())
	req.Header.Set("Referer", "https://movie.douban.com/")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return FetchedImage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return FetchedImage{}, fmt.Errorf("image request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return FetchedImage{}, err
	}
	if int64(len(body)) > maxBytes {
		return FetchedImage{}, fmt.Errorf("image exceeds max size %d bytes", maxBytes)
	}

	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return FetchedImage{}, fmt.Errorf("unexpected content type %q", contentType)
	}

	return FetchedImage{
		Body:        body,
		ContentType: contentType,
	}, nil
}

func getImageUserAgent() string {
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
}
