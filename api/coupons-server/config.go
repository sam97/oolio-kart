package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
)

type config struct {
	Addr            string        `mapstructure:"coupons_addr"`
	Dir             string        `mapstructure:"coupons_dir"`
	PollInterval    time.Duration `mapstructure:"coupons_poll_interval"`
	DiscountPercent int           `mapstructure:"coupons_discount_percent"` // TODO: This should be in a data store
	ShutdownTimeout time.Duration `mapstructure:"coupons_shutdown_timeout"`
	LogLevel        slog.Level    `mapstructure:"log_level"`
	LogFormat       string        `mapstructure:"log_format"`
}

// loadConfig reads the configuration from the env files in dir and the
// environment; see package config for the layering.
func loadConfig(dir string) (config, error) {
	var cfg config
	if err := envconfig.Load(&cfg, dir); err != nil {
		return config{}, err
	}

	if info, err := os.Stat(cfg.Dir); err != nil || !info.IsDir() {
		return config{}, fmt.Errorf("COUPONS_DIR %q is not a readable directory", cfg.Dir)
	}
	if cfg.DiscountPercent < 0 || cfg.DiscountPercent > 100 {
		return config{}, errors.New("COUPONS_DISCOUNT_PERCENT must be between 0 and 100")
	}
	if cfg.PollInterval <= 0 || cfg.ShutdownTimeout <= 0 {
		return config{}, errors.New("COUPONS_POLL_INTERVAL and COUPONS_SHUTDOWN_TIMEOUT must be positive")
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return config{}, fmt.Errorf("unknown LOG_FORMAT %q, want json or text", cfg.LogFormat)
	}
	return cfg, nil
}
