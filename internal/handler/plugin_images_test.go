package handler

import (
	"context"
	"testing"

	"kerkerker-douban-service/internal/service"
)

type pluginImageTestFetcher struct{}

func (pluginImageTestFetcher) FetchImage(context.Context, string, int64) (service.FetchedImage, error) {
	return service.FetchedImage{Body: []byte{1, 2, 3}, ContentType: "image/jpeg"}, nil
}

type pluginImageTestStore struct{}

func (pluginImageTestStore) PutObject(context.Context, service.StoredObject) error { return nil }

func TestPreparePluginImagesRewritesCachedURLs(t *testing.T) {
	syncer := service.NewImageSyncer(service.ImageSyncerConfig{
		Enabled:       true,
		PublicBaseURL: "https://images.example.com",
	}, pluginImageTestFetcher{}, pluginImageTestStore{})
	source := "https://image.tmdb.org/t/p/w500/example.jpg"
	want := syncer.SyncURL(context.Background(), source)
	if want == source {
		t.Fatal("expected test image to be mirrored")
	}

	page := service.PluginPage{Items: []service.PluginCandidate{{
		Preview: &service.PluginPreview{PosterURL: source},
	}}}
	rewritten, ok := preparePluginImages(page, syncer).(service.PluginPage)
	if !ok || rewritten.Items[0].Preview == nil {
		t.Fatalf("expected rewritten plugin page, got %#v", rewritten)
	}
	if got := rewritten.Items[0].Preview.PosterURL; got != want {
		t.Fatalf("expected cached R2 URL %q, got %q", want, got)
	}
}

func TestCollectPluginImageRefsIncludesCalendarDetailAndAssetImages(t *testing.T) {
	detail := &service.PluginDetailCandidate{
		PluginCandidate: service.PluginCandidate{Preview: &service.PluginPreview{PosterURL: "poster"}},
		Details: service.PluginDetails{
			Photos:          []service.PluginPhoto{{URL: "photo", ThumbURL: "thumb"}},
			Recommendations: []service.PluginRecommendation{{PosterURL: "recommendation"}},
		},
	}
	refs := collectPluginImageRefs(detail)
	if len(refs) != 4 {
		t.Fatalf("expected 4 detail image refs, got %d", len(refs))
	}

	calendar := service.PluginCalendarPage{Items: []service.PluginCalendarCandidate{{
		PluginCandidate: service.PluginCandidate{Preview: &service.PluginPreview{BackdropURL: "preview-backdrop"}},
		Calendar:        service.PluginCalendar{PosterURL: "calendar-poster", BackdropURL: "calendar-backdrop"},
	}}}
	if got := len(collectPluginImageRefs(calendar)); got != 3 {
		t.Fatalf("expected 3 calendar image refs, got %d", got)
	}

	assets := []service.PluginImageCandidate{{URL: "asset"}}
	if got := len(collectPluginImageRefs(assets)); got != 1 {
		t.Fatalf("expected 1 asset image ref, got %d", got)
	}
}
