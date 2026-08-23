package handler

import (
	"context"
	"strings"
	"time"

	"kerkerker-douban-service/internal/service"

	"github.com/rs/zerolog/log"
)

const pluginImageSyncWindow = 15 * time.Minute

type pluginImageRef struct {
	url *string
}

// preparePluginImages rewrites image URLs that are already known in the local
// R2 cache and schedules the remaining source URLs for background mirroring.
// A catalog response must remain fast, especially Top 250 (250 records), so
// the first request is allowed to fall back to the source URL while the
// durable mapping is warmed for subsequent requests.
func preparePluginImages(result any, syncer *service.ImageSyncer) any {
	if syncer == nil || !syncer.Enabled() {
		return result
	}

	// PluginPage and PluginCalendarPage are returned by value. Work on a copy
	// and return that copy, otherwise pointers collected from the type-switch
	// would only mutate a temporary interface value.
	rewritten := result
	switch value := result.(type) {
	case service.PluginPage:
		page := value
		rewritten = page
		refs := collectPluginImageRefs(&page)
		return preparePluginImageRefs(rewritten, refs, syncer)
	case service.PluginCalendarPage:
		page := value
		rewritten = page
		refs := collectPluginImageRefs(&page)
		return preparePluginImageRefs(rewritten, refs, syncer)
	case []service.PluginImageCandidate:
		images := value
		rewritten = images
	}
	refs := collectPluginImageRefs(rewritten)
	return preparePluginImageRefs(rewritten, refs, syncer)
}

func preparePluginImageRefs(result any, refs []pluginImageRef, syncer *service.ImageSyncer) any {
	if len(refs) == 0 {
		return result
	}
	unique := make(map[string]struct{}, len(refs))
	pending := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.url == nil {
			continue
		}
		rawURL := strings.TrimSpace(*ref.url)
		if rawURL == "" {
			continue
		}
		cachedURL := syncer.LookupURL(rawURL)
		if cachedURL != rawURL {
			*ref.url = cachedURL
			continue
		}
		if _, exists := unique[rawURL]; exists {
			continue
		}
		unique[rawURL] = struct{}{}
		pending = append(pending, rawURL)
	}
	if len(pending) == 0 {
		return result
	}

	go func(urls []string) {
		ctx, cancel := context.WithTimeout(context.Background(), pluginImageSyncWindow)
		defer cancel()
		syncer.SyncURLs(ctx, urls)
		log.Debug().Int("images", len(urls)).Msg("TMDB plugin image R2 sync completed")
	}(pending)
	return result
}

func collectPluginImageRefs(result any) []pluginImageRef {
	refs := make([]pluginImageRef, 0, 32)
	appendURL := func(url *string) {
		if url != nil && strings.TrimSpace(*url) != "" {
			refs = append(refs, pluginImageRef{url: url})
		}
	}
	appendCandidate := func(candidate *service.PluginCandidate) {
		if candidate == nil {
			return
		}
		if candidate.Preview != nil {
			appendURL(&candidate.Preview.PosterURL)
			appendURL(&candidate.Preview.BackdropURL)
		}
	}

	switch value := result.(type) {
	case *service.PluginPage:
		if value != nil {
			for index := range value.Items {
				appendCandidate(&value.Items[index])
			}
		}
	case *service.PluginCalendarPage:
		if value != nil {
			for index := range value.Items {
				item := &value.Items[index]
				appendCandidate(&item.PluginCandidate)
				appendURL(&item.Calendar.PosterURL)
				appendURL(&item.Calendar.BackdropURL)
			}
		}
	case service.PluginPage:
		for index := range value.Items {
			appendCandidate(&value.Items[index])
		}
	case service.PluginCalendarPage:
		for index := range value.Items {
			item := &value.Items[index]
			appendCandidate(&item.PluginCandidate)
			appendURL(&item.Calendar.PosterURL)
			appendURL(&item.Calendar.BackdropURL)
		}
	case *service.PluginDetailCandidate:
		if value == nil {
			break
		}
		appendCandidate(&value.PluginCandidate)
		for index := range value.Details.Photos {
			appendURL(&value.Details.Photos[index].URL)
			appendURL(&value.Details.Photos[index].ThumbURL)
		}
		for index := range value.Details.Recommendations {
			appendURL(&value.Details.Recommendations[index].PosterURL)
		}
	case []service.PluginImageCandidate:
		for index := range value {
			appendURL(&value[index].URL)
		}
	}
	return refs
}
