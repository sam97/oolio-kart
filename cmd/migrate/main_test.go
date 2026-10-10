package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
)

func TestLoadConfig(t *testing.T) {
	for _, key := range append(envconfig.Keys(config{}), envconfig.FileVar) {
		t.Setenv(key, "")
	}
	defaults, err := os.ReadFile(filepath.Join("..", "..", envconfig.DefaultsFile))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, envconfig.DefaultsFile), defaults, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL == "" || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	t.Setenv("LOG_FORMAT", "xml")
	if _, err := loadConfig(dir); err == nil {
		t.Error("loadConfig accepted LOG_FORMAT=xml")
	}
}
