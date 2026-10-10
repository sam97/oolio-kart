package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
)

// loadFrom loads the config from the committed .env.defaults with only
// vars set among the known variables, and COUPONS_DIR pointing at an empty
// directory.
func loadFrom(t *testing.T, vars map[string]string) (config, error) {
	t.Helper()
	for _, key := range append(envconfig.Keys(config{}), envconfig.FileVar) {
		t.Setenv(key, "")
	}
	t.Setenv("COUPONS_DIR", t.TempDir())
	for name, value := range vars {
		t.Setenv(name, value)
	}
	return loadConfig(defaultsDir(t))
}

// defaultsDir copies the committed .env.defaults into a fresh directory, so a
// developer's local .env cannot leak into the tests.
func defaultsDir(t *testing.T) string {
	t.Helper()
	defaults, err := os.ReadFile(filepath.Join("..", "..", envconfig.DefaultsFile))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, envconfig.DefaultsFile), defaults, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadFrom(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8081" || cfg.PollInterval != 2*time.Second || cfg.DiscountPercent != 10 || cfg.LogLevel != slog.LevelInfo ||
		cfg.MemoryLimit != 256<<20 || cfg.BucketDir != "data/buckets" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	cfg, err = loadFrom(t, map[string]string{"LOG_LEVEL": "debug", "COUPONS_DISCOUNT_PERCENT": "25", "COUPONS_POLL_INTERVAL": "500ms", "COUPONS_MEMORY_LIMIT": "1GiB"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.DiscountPercent != 25 || cfg.PollInterval != 500*time.Millisecond || cfg.MemoryLimit != 1<<30 {
		t.Errorf("unexpected overrides: %+v", cfg)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"missing dir":      {"COUPONS_DIR": "does/not/exist"},
		"bad level":        {"LOG_LEVEL": "loud"},
		"bad format":       {"LOG_FORMAT": "xml"},
		"discount too big": {"COUPONS_DISCOUNT_PERCENT": "101"},
		"zero poll":        {"COUPONS_POLL_INTERVAL": "0s"},
		"bad duration":     {"COUPONS_SHUTDOWN_TIMEOUT": "soon"},
		"memory too small": {"COUPONS_MEMORY_LIMIT": "16MiB"},
		"bad memory":       {"COUPONS_MEMORY_LIMIT": "plenty"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFrom(t, vars); err == nil {
				t.Errorf("loadConfig(%v) succeeded", vars)
			}
		})
	}
}
