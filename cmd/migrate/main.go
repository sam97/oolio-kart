// Command migrate brings the database schema up to date, then exits. Run it
// before starting kart-api and coupons-job; running it again is a no-op.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sam97/oolio-kart/pkg/datasources"
	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
)

type config struct {
	DatabaseURL string         `mapstructure:"database_url"`
	LogLevel    slog.Level     `mapstructure:"log_level"`
	LogFormat   logging.Format `mapstructure:"log_format"`
}

// loadConfig reads the configuration from the env files in dir and the
// environment; see package config for the layering.
func loadConfig(dir string) (config, error) {
	var cfg config
	if err := envconfig.Load(&cfg, dir); err != nil {
		return config{}, err
	}
	if cfg.DatabaseURL == "" {
		return config{}, errors.New("DATABASE_URL must be set")
	}
	return cfg, nil
}

func main() {
	if err := migrate(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func migrate() error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stores, err := datasources.Open(ctx, datasources.Config{DatabaseURL: cfg.DatabaseURL, Logger: logger})
	if err != nil {
		return err
	}
	defer stores.Close()
	if err := stores.Migrate(ctx); err != nil {
		return err
	}
	logger.Info("database migrated")
	return nil
}
