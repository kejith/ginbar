package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRuntimeConfigRequiresDatabaseAndMediaRoot(t *testing.T) {
	_, err := loadRuntimeConfig(mapEnv(map[string]string{"GINBAR_MEDIA_SOURCE_ROOT": "/tmp/media"}))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err=%v", err)
	}
	_, err = loadRuntimeConfig(mapEnv(map[string]string{"DATABASE_URL": "postgres://example"}))
	if err == nil || !strings.Contains(err.Error(), "GINBAR_MEDIA_SOURCE_ROOT") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadRuntimeConfigDefaultsAreBoundedAndComposable(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"DATABASE_URL":             "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT": "/srv/ginbar/media",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.dbMaxConns != 8 || cfg.mediaSourceMax != defaultMediaSourceMaxBytes || cfg.ingest.MaxConcurrent != defaultIngestMaxConcurrent {
		t.Fatalf("db/source/concurrency=%d/%d/%d", cfg.dbMaxConns, cfg.mediaSourceMax, cfg.ingest.MaxConcurrent)
	}
	if cfg.api.IngestRequestTimeout != defaultIngestRequestTimeout || cfg.ingest.StageTimeout != defaultIngestStageTimeout || cfg.ingest.DBTimeout != defaultIngestDBTimeout || cfg.ingest.CleanupTimeout != defaultIngestCleanupTimeout {
		t.Fatalf("timeouts api=%s ingest=%#v", cfg.api.IngestRequestTimeout, cfg.ingest)
	}
	if cfg.urlFetcher.MaxBytes != cfg.mediaSourceMax || cfg.urlFetcher.ConnectTimeout != defaultURLConnectTimeout || cfg.urlFetcher.ResponseHeaderTimeout != defaultURLResponseHeaderTimeout || cfg.urlFetcher.MaxRedirects != defaultURLMaxRedirects {
		t.Fatalf("fetcher=%#v", cfg.urlFetcher)
	}
	if cfg.ingest.StageTimeout+cfg.ingest.DBTimeout >= cfg.api.IngestRequestTimeout {
		t.Fatalf("request deadline does not contain stage+db: request=%s stage=%s db=%s", cfg.api.IngestRequestTimeout, cfg.ingest.StageTimeout, cfg.ingest.DBTimeout)
	}
}

func TestLoadRuntimeConfigOverridesIngestionAndURLFetcherLimits(t *testing.T) {
	cfg, err := loadRuntimeConfig(mapEnv(map[string]string{
		"DATABASE_URL":                       "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT":           "/srv/ginbar/media",
		"DB_MAX_CONNS":                       "3",
		"LISTEN_ADDR":                        "127.0.0.1:9090",
		"GINBAR_MEDIA_SOURCE_MAX_BYTES":      "1048576",
		"GINBAR_INGEST_MAX_CONCURRENT":       "2",
		"GINBAR_INGEST_REQUEST_TIMEOUT":      "45s",
		"GINBAR_INGEST_STAGE_TIMEOUT":        "30s",
		"GINBAR_INGEST_DB_TIMEOUT":           "4s",
		"GINBAR_INGEST_CLEANUP_TIMEOUT":      "3s",
		"GINBAR_URL_CONNECT_TIMEOUT":         "2s",
		"GINBAR_URL_RESPONSE_HEADER_TIMEOUT": "6s",
		"GINBAR_URL_MAX_REDIRECTS":           "2",
		"GINBAR_SESSION_COOKIE_SECURE":       "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.dbMaxConns != 3 || cfg.listenAddr != "127.0.0.1:9090" || cfg.mediaSourceMax != 1048576 || cfg.ingest.MaxConcurrent != 2 {
		t.Fatalf("cfg=%#v", cfg)
	}
	if cfg.api.IngestRequestTimeout != 45*time.Second || cfg.ingest.StageTimeout != 30*time.Second || cfg.ingest.DBTimeout != 4*time.Second || cfg.ingest.CleanupTimeout != 3*time.Second {
		t.Fatalf("timeouts api=%s ingest=%#v", cfg.api.IngestRequestTimeout, cfg.ingest)
	}
	if cfg.urlFetcher.MaxBytes != 1048576 || cfg.urlFetcher.ConnectTimeout != 2*time.Second || cfg.urlFetcher.ResponseHeaderTimeout != 6*time.Second || cfg.urlFetcher.MaxRedirects != 2 || cfg.api.CookieSecure {
		t.Fatalf("fetcher/api=%#v/%#v", cfg.urlFetcher, cfg.api)
	}
}

func TestLoadRuntimeConfigRejectsInvalidOrIncoherentLimits(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":             "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT": "/srv/ginbar/media",
	}
	for _, tt := range []struct {
		key   string
		value string
	}{
		{"DB_MAX_CONNS", "0"},
		{"GINBAR_MEDIA_SOURCE_MAX_BYTES", "0"},
		{"GINBAR_INGEST_MAX_CONCURRENT", "0"},
		{"GINBAR_INGEST_REQUEST_TIMEOUT", "0s"},
		{"GINBAR_INGEST_STAGE_TIMEOUT", "nope"},
		{"GINBAR_INGEST_DB_TIMEOUT", "-1s"},
		{"GINBAR_INGEST_CLEANUP_TIMEOUT", "0s"},
		{"GINBAR_URL_CONNECT_TIMEOUT", "0s"},
		{"GINBAR_URL_RESPONSE_HEADER_TIMEOUT", "0s"},
		{"GINBAR_URL_MAX_REDIRECTS", "-1"},
		{"GINBAR_SESSION_COOKIE_SECURE", "not-bool"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			env := cloneEnv(base)
			env[tt.key] = tt.value
			if _, err := loadRuntimeConfig(mapEnv(env)); err == nil {
				t.Fatalf("%s=%q unexpectedly accepted", tt.key, tt.value)
			}
		})
	}

	for name, overrides := range map[string]map[string]string{
		"stage-db-exceed-request": {
			"GINBAR_INGEST_REQUEST_TIMEOUT": "10s",
			"GINBAR_INGEST_STAGE_TIMEOUT":   "8s",
			"GINBAR_INGEST_DB_TIMEOUT":      "2s",
		},
		"connect-exceeds-stage": {
			"GINBAR_INGEST_STAGE_TIMEOUT": "5s",
			"GINBAR_URL_CONNECT_TIMEOUT":  "6s",
		},
		"header-exceeds-stage": {
			"GINBAR_INGEST_STAGE_TIMEOUT":        "5s",
			"GINBAR_URL_RESPONSE_HEADER_TIMEOUT": "6s",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := cloneEnv(base)
			for key, value := range overrides {
				env[key] = value
			}
			if _, err := loadRuntimeConfig(mapEnv(env)); err == nil {
				t.Fatalf("incoherent configuration unexpectedly accepted: %#v", overrides)
			}
		})
	}
}

func TestIngestWriteTimeoutAllowsCleanupAndResponseSlack(t *testing.T) {
	if got := ingestWriteTimeout(2*time.Minute, 5*time.Second); got != 2*time.Minute+10*time.Second {
		t.Fatalf("write timeout=%s", got)
	}
	if got := ingestWriteTimeout(time.Second, time.Second); got != 10*time.Second {
		t.Fatalf("minimum write timeout=%s", got)
	}
	maxDuration := time.Duration(1<<63 - 1)
	if got := ingestWriteTimeout(maxDuration-time.Second, 2*time.Second); got != maxDuration {
		t.Fatalf("overflow-safe write timeout=%s", got)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func cloneEnv(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func TestLoadRuntimeConfigProxyTrustRequiresExplicitOptIn(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":             "postgres://example",
		"GINBAR_MEDIA_SOURCE_ROOT": "/srv/ginbar/media",
	}
	cfg, err := loadRuntimeConfig(mapEnv(base))
	if err != nil || cfg.api.TrustLoopbackProxy {
		t.Fatalf("default proxy trust config=%#v err=%v", cfg.api, err)
	}
	for _, tt := range []struct {
		value string
		want bool
		valid bool
	}{
		{"true", true, true},
		{"false", false, true},
		{"1", true, true},
		{"not-a-boolean", false, false},
	} {
		env := cloneEnv(base)
		env["GINBAR_TRUST_LOOPBACK_PROXY"] = tt.value
		got, err := loadRuntimeConfig(mapEnv(env))
		if (err == nil) != tt.valid {
			t.Fatalf("env=%q err=%v valid=%v", tt.value, err, tt.valid)
		}
		if tt.valid && got.api.TrustLoopbackProxy != tt.want {
			t.Fatalf("env=%q trusted=%v want=%v", tt.value, got.api.TrustLoopbackProxy, tt.want)
		}
	}
}
