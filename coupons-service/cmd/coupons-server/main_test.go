package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func loadFrom(t *testing.T, vars map[string]string) (config, error) {
	t.Helper()
	for key := range defaults {
		t.Setenv(strings.ToUpper(key), "")
	}
	t.Setenv("COUPONS_DIR", t.TempDir())
	for name, value := range vars {
		t.Setenv(name, value)
	}
	return loadConfig()
}

func TestLoadConfig(t *testing.T) {
	cfg, err := loadFrom(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8081" || cfg.PollInterval != 2*time.Second || cfg.DiscountPercent != 10 || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	cfg, err = loadFrom(t, map[string]string{"LOG_LEVEL": "debug", "COUPONS_DISCOUNT_PERCENT": "25", "COUPONS_POLL_INTERVAL": "500ms"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.DiscountPercent != 25 || cfg.PollInterval != 500*time.Millisecond {
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
		"bad duration":     {"SHUTDOWN_TIMEOUT": "soon"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := loadFrom(t, vars); err == nil {
				t.Errorf("loadConfig(%v) succeeded", vars)
			}
		})
	}
}
