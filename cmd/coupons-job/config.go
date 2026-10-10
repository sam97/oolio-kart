package main

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/dustin/go-humanize"
	"github.com/robfig/cron/v3"

	envconfig "github.com/sam97/oolio-kart/pkg/helpers/config"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/services/coupons"
)

type config struct {
	Dir         string             `mapstructure:"coupons_dir"`
	BucketDir   string             `mapstructure:"coupons_bucket_dir"`
	MemoryLimit envconfig.ByteSize `mapstructure:"coupons_memory_limit"`
	Schedule    schedule           `mapstructure:"coupons_schedule"`
	DatabaseURL string             `mapstructure:"database_url"`
	LogLevel    slog.Level         `mapstructure:"log_level"`
	LogFormat   logging.Format     `mapstructure:"log_format"`
}

// schedule is a cron expression, such as "*/5 * * * *", or a descriptor,
// such as "@every 1m" or "@hourly".
type schedule struct {
	cron.Schedule
	text string
}

func (s *schedule) UnmarshalText(text []byte) error {
	parsed, err := cron.ParseStandard(string(text))
	if err != nil {
		return fmt.Errorf("COUPONS_SCHEDULE %q: %w", text, err)
	}
	*s = schedule{Schedule: parsed, text: string(text)}
	return nil
}

func (s schedule) String() string { return s.text }

// loadConfig reads the configuration from the env files in dir and the
// environment; see package config for the layering.
func loadConfig(dir string) (config, error) {
	var cfg config
	if err := envconfig.Load(&cfg, dir); err != nil {
		return config{}, err
	}
	var problems []error
	if cfg.Dir == "" || cfg.BucketDir == "" {
		problems = append(problems, errors.New("COUPONS_DIR and COUPONS_BUCKET_DIR must be set"))
	}
	if cfg.MemoryLimit < coupons.MinMemoryLimit {
		problems = append(problems, fmt.Errorf("COUPONS_MEMORY_LIMIT must be at least %s", humanize.IBytes(coupons.MinMemoryLimit)))
	}
	if cfg.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL must be set"))
	}
	return cfg, errors.Join(problems...)
}
