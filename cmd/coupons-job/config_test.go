package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
)

// loadFrom loads the config from the committed .env.defaults with only vars
// set among the known variables.
func loadFrom(t *testing.T, vars map[string]string) (config, error) {
	t.Helper()
	for _, key := range append(envconfig.Keys(config{}), envconfig.FileVar) {
		t.Setenv(key, "")
	}
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
	start := time.Date(2026, 10, 10, 9, 0, 30, 0, time.UTC)
	if cfg.Dir != "data/coupons" || cfg.Scanner != "go" || cfg.BucketDir != "data/buckets" || cfg.MemoryLimit != 256<<20 ||
		cfg.LogLevel != slog.LevelInfo || cfg.DatabaseURL == "" || cfg.Schedule.Next(start) != start.Add(time.Minute) {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	cfg, err = loadFrom(t, map[string]string{"LOG_LEVEL": "debug", "COUPONS_SCHEDULE": "*/5 * * * *", "COUPONS_MEMORY_LIMIT": "1GiB"})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 10, 9, 5, 0, 0, time.UTC); cfg.Schedule.Next(start) != want {
		t.Errorf("next run = %v, want %v", cfg.Schedule.Next(start), want)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.MemoryLimit != 1<<30 {
		t.Errorf("unexpected overrides: %+v", cfg)
	}

	for _, name := range []string{"clickhouse", "pebble"} {
		cfg, err = loadFrom(t, map[string]string{"COUPONS_SCANNER": name})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Scanner != name || cfg.ClickHouseURL == "" || cfg.ClickHouseDir == "" || cfg.PebbleDir == "" {
			t.Errorf("unexpected %s config: %+v", name, cfg)
		}
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"bad level":        {"LOG_LEVEL": "loud"},
		"bad format":       {"LOG_FORMAT": "xml"},
		"bad schedule":     {"COUPONS_SCHEDULE": "every so often"},
		"memory too small": {"COUPONS_MEMORY_LIMIT": "16MiB"},
		"bad memory":       {"COUPONS_MEMORY_LIMIT": "plenty"},
		"unknown scanner":  {"COUPONS_SCANNER": "duckdb"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFrom(t, vars); err == nil {
				t.Errorf("loadConfig(%v) succeeded", vars)
			}
		})
	}
}
