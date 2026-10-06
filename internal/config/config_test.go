package config

import (
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"DB_HOST", "localhost"}, {"DB_PORT", "3306"}, {"DB_USER", "u"},
		{"DB_PASSWORD", "p"}, {"DB_NAME", "db"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	for _, name := range []string{
		"REDIS_ADDR", "SERVER_PORT", "LOG_LEVEL", "CACHE_TTL", "REDIS_OP_TIMEOUT", "REDIS_POOL_SIZE",
		"REQUEST_TIMEOUT", "SHUTDOWN_TIMEOUT", "BATCH_WORKERS", "DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME",
	} {
		t.Setenv(name, "")
	}
}

func TestDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CacheTTL != 60*time.Second || cfg.BatchWorkers != 8 || cfg.DBMaxOpenConns != 25 || cfg.ServerPort != "8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("CACHE_TTL", "2m")
	t.Setenv("BATCH_WORKERS", "16")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CacheTTL != 2*time.Minute || cfg.BatchWorkers != 16 {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct{ name, key, val, want string }{
		{"missing required", "DB_HOST", "", "DB_HOST"},
		{"bad int", "BATCH_WORKERS", "eight", "BATCH_WORKERS"},
		{"bad duration", "CACHE_TTL", "60", "CACHE_TTL"},
		{"idle exceeds open", "DB_MAX_IDLE_CONNS", "100", "DB_MAX_IDLE_CONNS"},
		{"non-positive duration", "REQUEST_TIMEOUT", "0s", "REQUEST_TIMEOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.key, tc.val)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %s, got %v", tc.want, err)
			}
		})
	}
}
