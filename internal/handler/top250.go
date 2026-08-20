package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"kerkerker-douban-service/internal/model"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const (
	top250CacheKey        = "douban:top250:all"
	top250SubjectCount    = 250
	top250ImageSyncWindow = 15 * time.Minute
)

type top250Service interface {
	GetTop250() ([]model.Subject, error)
	ImageSyncEnabled() bool
	SnapshotStore() repository.SnapshotStore
	SyncSubjectImages(context.Context, []model.Subject) []model.Subject
}

type top250Payload struct {
	Subjects  []model.Subject `json:"subjects" bson:"subjects"`
	FetchedAt time.Time       `json:"fetched_at" bson:"fetched_at"`
}

type top250Fallback struct {
	payload top250Payload
	source  string
}

type top250ImageSyncJob struct {
	payload    top250Payload
	generation uint64
}

// Top250Handler handles the complete Douban Top 250 ranking.
type Top250Handler struct {
	doubanService top250Service
	cache         *repository.Cache
	freshnessTTL  time.Duration

	imageSyncMu         sync.Mutex
	imageSyncRunning    bool
	imageSyncGeneration uint64
	imageSyncActive     top250ImageSyncJob
	imageSyncPending    *top250ImageSyncJob
}

// NewTop250Handler creates a new Top250Handler.
func NewTop250Handler(douban *service.DoubanService, cache *repository.Cache, freshnessTTL time.Duration) *Top250Handler {
	if freshnessTTL <= 0 {
		freshnessTTL = time.Hour
	}
	return &Top250Handler{
		doubanService: douban,
		cache:         cache,
		freshnessTTL:  freshnessTTL,
	}
}

// GetTop250 returns all 250 ranked subjects.
// GET /api/v1/250
func (h *Top250Handler) GetTop250(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now().UTC()
	var fallback *top250Fallback

	var cachedData top250Payload
	if err := h.cache.Get(ctx, top250CacheKey, &cachedData); err == nil {
		if err := validateTop250Payload(cachedData); err != nil {
			log.Warn().Err(err).Str("key", top250CacheKey).Msg("Ignoring invalid Top 250 Redis payload")
		} else if top250PayloadIsFresh(cachedData, now, h.freshnessTTL) {
			c.Set("cache_source", "redis-cache")
			h.respond(c, cachedData, "redis-cache")
			return
		} else {
			fallback = newerTop250Fallback(fallback, cachedData, "redis-stale")
		}
	}

	snapshotStore := h.doubanService.SnapshotStore()
	if snapshotStore != nil {
		var snapshotData top250Payload
		if err := snapshotStore.Load(ctx, top250CacheKey, &snapshotData); err == nil {
			if err := validateTop250Payload(snapshotData); err != nil {
				log.Warn().Err(err).Str("key", top250CacheKey).Msg("Ignoring invalid Top 250 Mongo snapshot")
			} else if top250PayloadIsFresh(snapshotData, now, h.freshnessTTL) {
				if err := h.cache.Set(ctx, top250CacheKey, snapshotData, h.freshnessTTL); err != nil {
					log.Warn().Err(err).Str("key", top250CacheKey).Msg("Failed to backfill Redis from Top 250 snapshot")
				}
				c.Set("cache_source", "mongo-snapshot")
				h.respond(c, snapshotData, "mongo-snapshot")
				return
			} else {
				fallback = newerTop250Fallback(fallback, snapshotData, "mongo-stale")
			}
		}
	}

	subjects, err := h.doubanService.GetTop250()
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch Douban Top 250")
		if fallback != nil {
			if cacheErr := h.cache.Set(ctx, top250CacheKey, fallback.payload, h.freshnessTTL); cacheErr != nil {
				log.Warn().Err(cacheErr).Str("key", top250CacheKey).Msg("Failed to cache stale Top 250 fallback")
			}
			c.Set("cache_source", fallback.source)
			h.respond(c, fallback.payload, fallback.source)
			return
		}
		c.JSON(http.StatusBadGateway, model.APIResponse{
			Code:  http.StatusBadGateway,
			Error: "获取豆瓣 Top 250 失败",
		})
		return
	}

	payload := top250Payload{
		Subjects:  subjects,
		FetchedAt: now,
	}
	if err := validateTop250Payload(payload); err != nil {
		log.Error().Err(err).Msg("Douban Top 250 returned an invalid payload")
		if fallback != nil {
			c.Set("cache_source", fallback.source)
			h.respond(c, fallback.payload, fallback.source)
			return
		}
		c.JSON(http.StatusBadGateway, model.APIResponse{
			Code:  http.StatusBadGateway,
			Error: "豆瓣 Top 250 数据不完整",
		})
		return
	}

	if err := h.cache.Set(ctx, top250CacheKey, payload, h.freshnessTTL); err != nil {
		log.Warn().Err(err).Str("key", top250CacheKey).Msg("Failed to cache Top 250 in Redis")
	}
	if snapshotStore != nil {
		if err := snapshotStore.Store(ctx, top250CacheKey, payload); err != nil {
			log.Warn().Err(err).Str("key", top250CacheKey).Msg("Failed to persist Top 250 snapshot")
		}
	}

	h.respond(c, payload, "fresh-data")
}

func (h *Top250Handler) respond(c *gin.Context, payload top250Payload, source string) {
	h.scheduleImageSync(payload)
	c.JSON(http.StatusOK, model.APIResponse{
		Code:   http.StatusOK,
		Data:   payload,
		Source: source,
	})
}

// scheduleImageSync mirrors all Top 250 covers without making the public
// request wait for up to 250 downloads/uploads. The generation guard prevents
// an in-flight job from resurrecting data after an admin cache reset, while the
// pending slot keeps the newest payload that arrives before the worker exits.
func (h *Top250Handler) scheduleImageSync(payload top250Payload) {
	if !h.doubanService.ImageSyncEnabled() || !top250NeedsImageSync(payload.Subjects) {
		return
	}
	// The response encoder reads the original slice after this method returns.
	// Give the background image rewriter its own backing array to avoid races.
	payload.Subjects = append([]model.Subject(nil), payload.Subjects...)

	h.imageSyncMu.Lock()
	job := top250ImageSyncJob{payload: payload, generation: h.imageSyncGeneration}
	if h.imageSyncRunning {
		if top250ImageSyncJobIsNewer(job, h.imageSyncActive) &&
			(h.imageSyncPending == nil || top250ImageSyncJobIsNewer(job, *h.imageSyncPending)) {
			pending := job
			h.imageSyncPending = &pending
		}
		h.imageSyncMu.Unlock()
		return
	}
	h.imageSyncRunning = true
	h.imageSyncActive = job
	h.imageSyncMu.Unlock()

	go h.runImageSync(job)
}

func (h *Top250Handler) runImageSync(job top250ImageSyncJob) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), top250ImageSyncWindow)
		mirrored := job.payload
		mirrored.Subjects = h.doubanService.SyncSubjectImages(ctx, mirrored.Subjects)
		h.persistMirroredTop250(ctx, job.generation, mirrored)
		cancel()

		h.imageSyncMu.Lock()
		if h.imageSyncPending != nil {
			job = *h.imageSyncPending
			h.imageSyncPending = nil
			h.imageSyncActive = job
			h.imageSyncMu.Unlock()
			continue
		}
		h.imageSyncRunning = false
		h.imageSyncActive = top250ImageSyncJob{}
		h.imageSyncMu.Unlock()
		return
	}
}

func (h *Top250Handler) persistMirroredTop250(ctx context.Context, generation uint64, payload top250Payload) {
	h.imageSyncMu.Lock()
	defer h.imageSyncMu.Unlock()
	if generation != h.imageSyncGeneration || ctx.Err() != nil || !h.top250PayloadStillCurrent(ctx, payload.FetchedAt) {
		return
	}

	if err := h.cache.Set(ctx, top250CacheKey, payload, h.freshnessTTL); err != nil {
		log.Warn().Err(err).Str("key", top250CacheKey).Msg("Failed to persist mirrored Top 250 images in Redis")
	}
	if snapshotStore := h.doubanService.SnapshotStore(); snapshotStore != nil {
		if err := snapshotStore.Store(ctx, top250CacheKey, payload); err != nil {
			log.Warn().Err(err).Str("key", top250CacheKey).Msg("Failed to persist mirrored Top 250 images in Mongo")
		}
	}
}

func top250ImageSyncJobIsNewer(candidate, current top250ImageSyncJob) bool {
	if candidate.generation != current.generation {
		return candidate.generation > current.generation
	}
	return candidate.payload.FetchedAt.After(current.payload.FetchedAt)
}

func (h *Top250Handler) top250PayloadStillCurrent(ctx context.Context, fetchedAt time.Time) bool {
	var cached top250Payload
	if err := h.cache.Get(ctx, top250CacheKey, &cached); err == nil && cached.FetchedAt.Equal(fetchedAt) {
		return true
	}
	if snapshotStore := h.doubanService.SnapshotStore(); snapshotStore != nil {
		var snapshot top250Payload
		if err := snapshotStore.Load(ctx, top250CacheKey, &snapshot); err == nil && snapshot.FetchedAt.Equal(fetchedAt) {
			return true
		}
	}
	return false
}

func top250NeedsImageSync(subjects []model.Subject) bool {
	for _, subject := range subjects {
		parsed, err := url.Parse(subject.Cover)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "douban.com" || strings.HasSuffix(host, ".douban.com") || host == "doubanio.com" || strings.HasSuffix(host, ".doubanio.com") {
			return true
		}
	}
	return false
}

func validateTop250Payload(payload top250Payload) error {
	if len(payload.Subjects) != top250SubjectCount {
		return fmt.Errorf("contains %d subjects, expected %d", len(payload.Subjects), top250SubjectCount)
	}
	seen := make(map[string]struct{}, top250SubjectCount)
	for index, subject := range payload.Subjects {
		if strings.TrimSpace(subject.ID) == "" || strings.TrimSpace(subject.Title) == "" || strings.TrimSpace(subject.Rate) == "" || strings.TrimSpace(subject.Cover) == "" || strings.TrimSpace(subject.URL) == "" {
			return fmt.Errorf("rank %d is missing required fields", index+1)
		}
		if _, exists := seen[subject.ID]; exists {
			return fmt.Errorf("contains duplicate subject id %s", subject.ID)
		}
		seen[subject.ID] = struct{}{}
	}
	return nil
}

func top250PayloadIsFresh(payload top250Payload, now time.Time, freshnessTTL time.Duration) bool {
	if payload.FetchedAt.IsZero() {
		return false
	}
	return !payload.FetchedAt.Before(now.Add(-freshnessTTL))
}

func newerTop250Fallback(current *top250Fallback, candidate top250Payload, source string) *top250Fallback {
	if current == nil || candidate.FetchedAt.After(current.payload.FetchedAt) {
		return &top250Fallback{payload: candidate, source: source}
	}
	return current
}

// DeleteTop250Cache removes both the Redis hot cache and Mongo durable
// snapshot, so the next request performs a genuine cold-source refresh.
// DELETE /api/v1/250
func (h *Top250Handler) DeleteTop250Cache(c *gin.Context) {
	h.imageSyncMu.Lock()
	h.imageSyncGeneration++
	h.imageSyncPending = nil
	h.imageSyncMu.Unlock()

	ctx := c.Request.Context()
	if err := h.cache.Delete(ctx, top250CacheKey); err != nil {
		c.JSON(http.StatusInternalServerError, model.APIResponse{
			Code:  http.StatusInternalServerError,
			Error: err.Error(),
		})
		return
	}
	if snapshotStore := h.doubanService.SnapshotStore(); snapshotStore != nil {
		if err := snapshotStore.Delete(ctx, top250CacheKey); err != nil {
			c.JSON(http.StatusInternalServerError, model.APIResponse{
				Code:  http.StatusInternalServerError,
				Error: err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusOK, model.APIResponse{
		Code:    http.StatusOK,
		Message: "豆瓣 Top 250 缓存与快照已清除",
	})
}
