package config

import "testing"

func TestLoadR2ImageConfigFromEnv(t *testing.T) {
	t.Setenv("CLOUDFLARE_R2_ENDPOINT", "https://account.r2.cloudflarestorage.com")
	t.Setenv("CLOUDFLARE_R2_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("CLOUDFLARE_R2_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("CLOUDFLARE_R2_BUCKET", "douban-images")
	t.Setenv("CLOUDFLARE_R2_PUBLIC_URL", "https://img.example.com/")
	t.Setenv("CLOUDFLARE_R2_KEY_PREFIX", "posters/")
	t.Setenv("CLOUDFLARE_R2_MAX_IMAGE_BYTES", "2048")

	cfg := Load()

	if !cfg.R2Images.Enabled {
		t.Fatalf("expected R2 image sync to be enabled")
	}
	if cfg.R2Images.Endpoint != "https://account.r2.cloudflarestorage.com" {
		t.Fatalf("unexpected endpoint: %q", cfg.R2Images.Endpoint)
	}
	if cfg.R2Images.AccessKeyID != "test-access-key" {
		t.Fatalf("unexpected access key id: %q", cfg.R2Images.AccessKeyID)
	}
	if cfg.R2Images.SecretAccessKey != "test-secret-key" {
		t.Fatalf("unexpected secret access key")
	}
	if cfg.R2Images.Bucket != "douban-images" {
		t.Fatalf("unexpected bucket: %q", cfg.R2Images.Bucket)
	}
	if cfg.R2Images.PublicBaseURL != "https://img.example.com" {
		t.Fatalf("expected public URL to be trimmed, got %q", cfg.R2Images.PublicBaseURL)
	}
	if cfg.R2Images.KeyPrefix != "posters" {
		t.Fatalf("expected key prefix to be trimmed, got %q", cfg.R2Images.KeyPrefix)
	}
	if cfg.R2Images.MaxImageBytes != 2048 {
		t.Fatalf("unexpected max image bytes: %d", cfg.R2Images.MaxImageBytes)
	}
}

func TestLoadR2ImageConfigDisabledWhenRequiredValuesMissing(t *testing.T) {
	t.Setenv("CLOUDFLARE_R2_ENDPOINT", "https://account.r2.cloudflarestorage.com")
	t.Setenv("CLOUDFLARE_R2_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("CLOUDFLARE_R2_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("CLOUDFLARE_R2_BUCKET", "")
	t.Setenv("CLOUDFLARE_R2_PUBLIC_URL", "https://img.example.com")

	cfg := Load()

	if cfg.R2Images.Enabled {
		t.Fatalf("expected R2 image sync to stay disabled when bucket is missing")
	}
}

func TestLoadR2ImageConfigEnabledWithUploadWorker(t *testing.T) {
	t.Setenv("CLOUDFLARE_R2_ENDPOINT", "")
	t.Setenv("CLOUDFLARE_R2_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_R2_ACCESS_KEY_ID", "")
	t.Setenv("CLOUDFLARE_R2_SECRET_ACCESS_KEY", "")
	t.Setenv("CLOUDFLARE_R2_BUCKET", "")
	t.Setenv("CLOUDFLARE_R2_PUBLIC_URL", "https://pub-example.r2.dev/")
	t.Setenv("CLOUDFLARE_R2_UPLOAD_API_URL", "https://upload.example.workers.dev/objects/")
	t.Setenv("CLOUDFLARE_R2_UPLOAD_API_TOKEN", "upload-secret")

	cfg := Load()

	if !cfg.R2Images.Enabled {
		t.Fatalf("expected R2 image sync to be enabled with upload worker")
	}
	if cfg.R2Images.UploadAPIURL != "https://upload.example.workers.dev/objects" {
		t.Fatalf("unexpected upload API URL: %q", cfg.R2Images.UploadAPIURL)
	}
	if cfg.R2Images.UploadAPIToken != "upload-secret" {
		t.Fatalf("unexpected upload API token")
	}
}

func TestLoadRequiresR2ImageSyncWhenConfigured(t *testing.T) {
	t.Setenv("REQUIRE_R2_IMAGE_SYNC", "true")

	cfg := Load()
	if !cfg.RequireR2ImageSync {
		t.Fatal("expected R2 image sync to be required")
	}
}

func TestLoadDoesNotRequireR2ImageSyncByDefault(t *testing.T) {
	t.Setenv("REQUIRE_R2_IMAGE_SYNC", "")

	cfg := Load()
	if cfg.RequireR2ImageSync {
		t.Fatal("expected R2 image sync requirement to be opt-in")
	}
}
