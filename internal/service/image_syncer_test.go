package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"kerkerker-douban-service/internal/model"
)

type fakeImageFetcher struct {
	images map[string]FetchedImage
	mu     sync.Mutex
	calls  int
}

func (f *fakeImageFetcher) FetchImage(ctx context.Context, imageURL string, maxBytes int64) (FetchedImage, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	image, ok := f.images[imageURL]
	if !ok {
		return FetchedImage{}, errors.New("missing image")
	}
	return image, nil
}

type fakeObjectStore struct {
	mu   sync.Mutex
	puts []StoredObject
	err  error
}

type fakeImageMapStore struct{}

func (fakeImageMapStore) Get(context.Context, string) (string, error) {
	return "", errors.New("not found")
}

func (fakeImageMapStore) Put(context.Context, string, string) error { return nil }

func (fakeImageMapStore) Close(context.Context) error { return nil }

func (s *fakeObjectStore) PutObject(ctx context.Context, object StoredObject) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts = append(s.puts, object)
	return nil
}

func TestImageSyncerUploadsDoubanImageAndReturnsPublicR2URL(t *testing.T) {
	sourceURL := "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p1234567890.jpg"
	fetcher := &fakeImageFetcher{images: map[string]FetchedImage{
		sourceURL: {
			Body:        []byte("fake-jpeg"),
			ContentType: "image/jpeg",
		},
	}}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com/assets",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	got := syncer.SyncURL(context.Background(), sourceURL)

	if !strings.HasPrefix(got, "https://img.example.com/assets/douban/") {
		t.Fatalf("expected public R2 URL with prefix, got %q", got)
	}
	if !strings.HasSuffix(got, ".jpg") {
		t.Fatalf("expected extension to be preserved, got %q", got)
	}
	if len(store.puts) != 1 {
		t.Fatalf("expected one R2 upload, got %d", len(store.puts))
	}
	if store.puts[0].ContentType != "image/jpeg" {
		t.Fatalf("unexpected content type: %q", store.puts[0].ContentType)
	}
	if string(store.puts[0].Body) != "fake-jpeg" {
		t.Fatalf("unexpected uploaded body: %q", string(store.puts[0].Body))
	}
}

func TestImageSyncerReportsPersistentMappingState(t *testing.T) {
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
	}, &fakeImageFetcher{}, &fakeObjectStore{})

	if syncer.PersistentMappingEnabled() {
		t.Fatal("mapping persistence should be disabled without a map store")
	}
	syncer.SetMapStore(fakeImageMapStore{})
	if !syncer.PersistentMappingEnabled() {
		t.Fatal("mapping persistence should be enabled after a map store is attached")
	}
}

func TestImageSyncerKeepsOriginalURLWhenUploadFails(t *testing.T) {
	sourceURL := "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p0987654321.webp"
	fetcher := &fakeImageFetcher{images: map[string]FetchedImage{
		sourceURL: {
			Body:        []byte("fake-webp"),
			ContentType: "image/webp",
		},
	}}
	store := &fakeObjectStore{err: errors.New("r2 unavailable")}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	got := syncer.SyncURL(context.Background(), sourceURL)

	if got != sourceURL {
		t.Fatalf("expected original URL on upload failure, got %q", got)
	}
}

func TestImageSyncerLeavesUnrelatedImagesUntouched(t *testing.T) {
	sourceURL := "https://cdn.example.com/some-other-host/poster.jpg"
	fetcher := &fakeImageFetcher{}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	got := syncer.SyncURL(context.Background(), sourceURL)

	if got != sourceURL {
		t.Fatalf("expected unrelated URL to stay unchanged, got %q", got)
	}
	if fetcher.calls != 0 {
		t.Fatalf("expected no image fetch for unrelated URL, got %d", fetcher.calls)
	}
	if len(store.puts) != 0 {
		t.Fatalf("expected no R2 upload for unrelated URL, got %d", len(store.puts))
	}
}

func TestImageSyncerUploadsTMDBImage(t *testing.T) {
	sourceURL := "https://image.tmdb.org/t/p/w500/abc123.jpg"
	fetcher := &fakeImageFetcher{images: map[string]FetchedImage{
		sourceURL: {
			Body:        []byte("tmdb-poster"),
			ContentType: "image/jpeg",
		},
	}}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	got := syncer.SyncURL(context.Background(), sourceURL)

	if !strings.HasPrefix(got, "https://img.example.com/douban/") {
		t.Fatalf("expected TMDB URL to be mirrored to R2, got %q", got)
	}
	if len(store.puts) != 1 {
		t.Fatalf("expected one R2 upload for TMDB URL, got %d", len(store.puts))
	}
}

func TestImageSyncerLeavesLookalikeDoubanHostUntouched(t *testing.T) {
	sourceURL := "https://notdoubanio.com/view/photo/l/public/p123.jpg"
	fetcher := &fakeImageFetcher{}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	got := syncer.SyncURL(context.Background(), sourceURL)

	if got != sourceURL {
		t.Fatalf("expected lookalike host URL to stay unchanged, got %q", got)
	}
	if fetcher.calls != 0 {
		t.Fatalf("expected no image fetch for lookalike host, got %d", fetcher.calls)
	}
}

func TestImageSyncerRewritesNestedDoubanImageFields(t *testing.T) {
	coverURL := "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p1.jpg"
	photoURL := "https://img3.doubanio.com/view/photo/l/public/p2.jpg"
	tmdbURL := "https://image.tmdb.org/t/p/original/backdrop.jpg"
	fetcher := &fakeImageFetcher{images: map[string]FetchedImage{
		coverURL: {
			Body:        []byte("cover"),
			ContentType: "image/jpeg",
		},
		photoURL: {
			Body:        []byte("photo"),
			ContentType: "image/jpeg",
		},
		tmdbURL: {
			Body:        []byte("tmdb-backdrop"),
			ContentType: "image/jpeg",
		},
	}}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	detail := model.SubjectDetail{
		Cover: coverURL,
		Photos: []model.Photo{{
			Image: photoURL,
			Thumb: photoURL,
		}},
		Recommendations: []model.Subject{{
			Cover: coverURL,
		}},
	}
	hero := []model.HeroMovie{{
		Cover:            coverURL,
		PosterVertical:   coverURL,
		PosterHorizontal: tmdbURL,
	}}

	detail = syncer.SyncSubjectDetailImages(context.Background(), detail)
	hero = syncer.SyncHeroImages(context.Background(), hero)

	if !strings.HasPrefix(detail.Cover, "https://img.example.com/douban/") {
		t.Fatalf("expected detail cover to be rewritten, got %q", detail.Cover)
	}
	if !strings.HasPrefix(detail.Photos[0].Image, "https://img.example.com/douban/") {
		t.Fatalf("expected detail photo image to be rewritten, got %q", detail.Photos[0].Image)
	}
	if !strings.HasPrefix(hero[0].PosterHorizontal, "https://img.example.com/douban/") {
		t.Fatalf("expected TMDB backdrop to be mirrored, got %q", hero[0].PosterHorizontal)
	}
	if len(store.puts) != 3 {
		t.Fatalf("expected repeated source URLs to be uploaded once each, got %d", len(store.puts))
	}
}

func TestImageSyncerRewritesCalendarImages(t *testing.T) {
	posterURL := "https://image.tmdb.org/t/p/w500/p1.jpg"
	airingPoster := "https://image.tmdb.org/t/p/w342/p2.jpg"
	fetcher := &fakeImageFetcher{images: map[string]FetchedImage{
		posterURL: {
			Body:        []byte("poster"),
			ContentType: "image/jpeg",
		},
		airingPoster: {
			Body:        []byte("airing"),
			ContentType: "image/jpeg",
		},
	}}
	store := &fakeObjectStore{}
	syncer := NewImageSyncer(ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://img.example.com",
		KeyPrefix:     "douban",
		MaxImageBytes: 4096,
	}, fetcher, store)

	days := []model.CalendarDay{{
		Date: "2026-08-16",
		Entries: []model.CalendarEntry{
			{ShowName: "剧A", Poster: posterURL},
			{ShowName: "剧A", Poster: posterURL}, // 同 URL 只上传一次
		},
	}}
	entries := []model.CalendarEntry{{ShowName: "剧B", Poster: airingPoster}}

	days = syncer.SyncCalendarImages(context.Background(), days)
	entries = syncer.SyncCalendarEntriesImages(context.Background(), entries)

	if !strings.HasPrefix(days[0].Entries[0].Poster, "https://img.example.com/douban/") {
		t.Fatalf("expected calendar poster to be mirrored, got %q", days[0].Entries[0].Poster)
	}
	if days[0].Entries[0].Poster != days[0].Entries[1].Poster {
		t.Fatalf("expected identical source URLs to map to the same R2 URL")
	}
	if !strings.HasPrefix(entries[0].Poster, "https://img.example.com/douban/") {
		t.Fatalf("expected airing poster to be mirrored, got %q", entries[0].Poster)
	}
	if len(store.puts) != 2 {
		t.Fatalf("expected 2 unique uploads, got %d", len(store.puts))
	}
}
