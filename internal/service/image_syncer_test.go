package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kerkerker-douban-service/internal/model"
)

type fakeImageFetcher struct {
	images map[string]FetchedImage
	calls  int
}

func (f *fakeImageFetcher) FetchImage(ctx context.Context, imageURL string, maxBytes int64) (FetchedImage, error) {
	f.calls++
	image, ok := f.images[imageURL]
	if !ok {
		return FetchedImage{}, errors.New("missing image")
	}
	return image, nil
}

type fakeObjectStore struct {
	puts []StoredObject
	err  error
}

func (s *fakeObjectStore) PutObject(ctx context.Context, object StoredObject) error {
	if s.err != nil {
		return s.err
	}
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

func TestImageSyncerLeavesNonDoubanImagesUntouched(t *testing.T) {
	sourceURL := "https://image.tmdb.org/t/p/original/poster.jpg"
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
		t.Fatalf("expected non-Douban URL to stay unchanged, got %q", got)
	}
	if fetcher.calls != 0 {
		t.Fatalf("expected no image fetch for non-Douban URL, got %d", fetcher.calls)
	}
	if len(store.puts) != 0 {
		t.Fatalf("expected no R2 upload for non-Douban URL, got %d", len(store.puts))
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
	if hero[0].PosterHorizontal != tmdbURL {
		t.Fatalf("expected TMDB backdrop to stay unchanged, got %q", hero[0].PosterHorizontal)
	}
	if len(store.puts) != 2 {
		t.Fatalf("expected repeated source URLs to be uploaded once each, got %d", len(store.puts))
	}
}
