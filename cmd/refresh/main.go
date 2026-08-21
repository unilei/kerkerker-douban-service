// cmd/refresh 是一个独立二进制，用于每日定时刷新 douban-service 持久层中陈旧的影片数据。
//
// 典型调用：
//
//	MONGO_URI=mongodb://... MONGO_DB_NAME=kerkerker_douban DOUBAN_API_PROXY=... \
//	  ./refresh --max-age=24h --limit=500 [--dry-run]
//
// 由 GitHub Actions cron 每日触发（见 .github/workflows/refresh.yml）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"kerkerker-douban-service/internal/config"
	"kerkerker-douban-service/internal/jobreport"
	"kerkerker-douban-service/internal/repository"
	"kerkerker-douban-service/internal/service"
	"kerkerker-douban-service/pkg/httpclient"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	var (
		maxAge = flag.Duration("max-age", 24*time.Hour, "超过该时长未刷新的影片视为陈旧")
		limit  = flag.Int("limit", 500, "单次刷新的最大条目数")
		dryRun = flag.Bool("dry-run", false, "仅列出待刷新条目，不实际抓取")
	)
	flag.Parse()

	reportMode := os.Getenv("KERKERKER_JOB_REPORT")
	metadata := jobreport.Metadata{
		RunID:         envOr("KERKERKER_REFRESH_RUN_ID", "refresh-"+strconv.FormatInt(time.Now().UTC().UnixNano(), 10)),
		PluginID:      envOr("KERKERKER_PLUGIN_ID", "kerkerker.douban-content"),
		PluginVersion: envOr("KERKERKER_PLUGIN_VERSION", "runtime"),
		ProfileID:     envOr("KERKERKER_PLUGIN_PROFILE", "cn-default"),
		ConfigVersion: envOr("KERKERKER_PLUGIN_CONFIG_VERSION", "runtime"),
		Actor:         envOr("KERKERKER_JOB_ACTOR", "system/refresh"),
		Attempt:       1,
	}
	var reporter *jobreport.JSONLinesReporter
	if reportMode == "stdout" {
		reporter = jobreport.NewJSONLinesReporter(os.Stdout, metadata)
	}
	emit := func(kind jobreport.Kind, status jobreport.Status, progress jobreport.Progress, reportError *jobreport.Error) {
		if reporter == nil {
			return
		}
		if err := reporter.Emit(kind, status, progress, reportError); err != nil {
			log.Warn().Err(err).Msg("Failed to emit optional job report")
		}
	}
	fatal := func(code, message string, err error) {
		emit(jobreport.KindFinished, jobreport.StatusFailed, jobreport.Progress{}, &jobreport.Error{Code: code, Message: message})
		event := log.Fatal()
		if err != nil {
			event = event.Err(err)
		}
		event.Msg(message)
	}

	cfg := config.Load()
	if cfg.RequireR2ImageSync && !cfg.R2Images.Enabled {
		fatal("CONFIG_R2", "REQUIRE_R2_IMAGE_SYNC is enabled, but Cloudflare R2 image configuration is incomplete", nil)
	}
	log.Info().
		Str("db", cfg.MongoDBName).
		Dur("max-age", *maxAge).
		Int("limit", *limit).
		Bool("dry-run", *dryRun).
		Str("run_id", metadata.RunID).
		Msg("🛠  kerkerker-douban-service refresh starting")
	emit(jobreport.KindStarted, jobreport.StatusRunning, jobreport.Progress{}, nil)

	if cfg.MongoURI == "" {
		fatal("CONFIG_MONGO", "MONGO_URI is required for refresh", nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	stores, err := repository.NewMongoStores(ctx, cfg.MongoURI, cfg.MongoDBName)
	if err != nil {
		fatal("MONGO_CONNECT", "Failed to connect to MongoDB", err)
	}
	defer stores.Close(context.Background())

	threshold := time.Now().UTC().Add(-*maxAge)

	// 1) 先把超过阈值的 fresh 条目批量标记为 stale，便于按索引扫描。
	marked, err := stores.Movie.MarkStale(ctx, threshold)
	if err != nil {
		fatal("MARK_STALE", "Failed to mark stale movies", err)
	}
	log.Info().Int64("marked", marked).Msg("Marked stale movies")

	// 2) 取出待刷新条目。
	toRefresh, err := stores.Movie.ListStale(ctx, *limit, threshold)
	if err != nil {
		fatal("LIST_STALE", "Failed to list stale movies", err)
	}
	log.Info().Int("count", len(toRefresh)).Msg("Movies to refresh")

	if *dryRun {
		for _, m := range toRefresh {
			log.Info().Int64("internal_id", m.InternalID).Str("douban_id", m.DoubanID).Str("title", m.Title).Msg("would refresh")
		}
		emit(jobreport.KindFinished, jobreport.StatusSucceeded, jobreport.Progress{Total: len(toRefresh), Processed: len(toRefresh), Skipped: len(toRefresh)}, nil)
		return
	}

	// 3) 构造与主服务一致的 DoubanService（共享 FetchDetail 与图片同步逻辑）。
	httpClient := httpclient.NewClient(cfg.DoubanProxies)

	var imageSyncer *service.ImageSyncer
	if cfg.R2Images.Enabled {
		var objectStore service.ObjectStore
		if cfg.R2Images.UploadAPIURL != "" {
			objectStore = service.NewWorkerObjectStore(cfg.R2Images.UploadAPIURL, cfg.R2Images.UploadAPIToken, 30*time.Second)
		} else {
			r2Store, err := service.NewR2ObjectStore(ctx, service.R2ObjectStoreConfig{
				Endpoint:        cfg.R2Images.Endpoint,
				AccessKeyID:     cfg.R2Images.AccessKeyID,
				SecretAccessKey: cfg.R2Images.SecretAccessKey,
				Bucket:          cfg.R2Images.Bucket,
			})
			if err != nil {
				fatal("R2_INIT", "Failed to initialize R2 store", err)
			}
			objectStore = r2Store
		}
		imageSyncer = service.NewImageSyncer(service.ImageSyncerConfig{
			Enabled:       true,
			PublicBaseURL: cfg.R2Images.PublicBaseURL,
			KeyPrefix:     cfg.R2Images.KeyPrefix,
			MaxImageBytes: cfg.R2Images.MaxImageBytes,
		}, service.NewHTTPImageFetcher(15*time.Second), objectStore)
		imageSyncer.SetMapStore(stores.ImageMap)
	}

	doubanService := service.NewDoubanService(httpClient, imageSyncer)

	// 4) 逐条刷新。豆瓣对连续请求有限流，串行处理并加间隔，避免触发 403。
	var success, failed int
	for i, m := range toRefresh {
		select {
		case <-ctx.Done():
			log.Warn().Msg("Refresh deadline reached, stopping early")
			goto done
		default:
		}

		detail, ok := doubanService.FetchDetail(ctx, m.DoubanID)
		if !ok {
			log.Warn().Str("douban_id", m.DoubanID).Str("title", m.Title).Msg("Refresh failed: not found")
			if err := stores.Movie.Upsert(ctx, &repository.Movie{
				DoubanID:      m.DoubanID,
				Title:         m.Title,
				Detail:        m.Detail,
				RefreshStatus: repository.RefreshStatusError,
				RefreshError:  "fetch detail returned no data",
				LastRefreshed: time.Now().UTC(),
			}); err != nil {
				log.Warn().Err(err).Str("douban_id", m.DoubanID).Msg("Failed to record refresh error")
			}
			failed++
		} else {
			detail = doubanService.SyncSubjectDetailImages(ctx, detail)
			detail.InternalID = m.InternalID
			if err := stores.Movie.Upsert(ctx, &repository.Movie{
				DoubanID:      m.DoubanID,
				Title:         detail.Title,
				Rate:          detail.Rate,
				Cover:         detail.Cover,
				URL:           detail.URL,
				Detail:        &detail,
				RefreshStatus: repository.RefreshStatusFresh,
				LastRefreshed: time.Now().UTC(),
			}); err != nil {
				log.Warn().Err(err).Str("douban_id", m.DoubanID).Msg("Failed to upsert refreshed movie")
				failed++
			} else {
				success++
			}
		}

		if (i+1)%50 == 0 {
			log.Info().Int("done", i+1).Int("total", len(toRefresh)).Msg("Refresh progress")
			emit(jobreport.KindProgress, jobreport.StatusRunning, jobreport.Progress{
				Total: len(toRefresh), Processed: i + 1, Created: success, Failed: failed,
			}, nil)
		}
		// 轻微限速，降低被豆瓣封禁概率。
		time.Sleep(800 * time.Millisecond)
	}

done:
	processed := success + failed
	finalStatus := jobreport.StatusSucceeded
	if failed > 0 || processed < len(toRefresh) {
		finalStatus = jobreport.StatusPartial
	}
	emit(jobreport.KindFinished, finalStatus, jobreport.Progress{Total: len(toRefresh), Processed: processed, Created: success, Failed: failed}, nil)
	log.Info().
		Int("success", success).
		Int("failed", failed).
		Int("total", len(toRefresh)).
		Msg(fmt.Sprintf("✅ Refresh complete: %d success, %d failed of %d total", success, failed, len(toRefresh)))
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
