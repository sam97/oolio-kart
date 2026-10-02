// Command coupons-server keeps the valid coupons in sync with a watched folder
// of coupon base files and answers lookups over HTTP. It is an internal
// service: expose it only to the API, never to the internet.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/labstack/echo/v5"
	coupons "github.com/sam97/oolio-kart/coupons-service"
	"github.com/spf13/viper"
)

type config struct {
	Addr            string        `mapstructure:"coupons_addr"`
	Dir             string        `mapstructure:"coupons_dir"`
	PollInterval    time.Duration `mapstructure:"coupons_poll_interval"`
	DiscountPercent int           `mapstructure:"coupons_discount_percent"` // TODO: This should be in a data store
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	LogLevel        slog.Level    `mapstructure:"log_level"`
	LogFormat       string        `mapstructure:"log_format"`
}

// defaults lists every key with its default. Each key is read from the
// environment variable of the same name in upper case.
var defaults = map[string]string{
	"coupons_addr":             "127.0.0.1:8081",
	"coupons_dir":              "coupons-service/data",
	"coupons_poll_interval":    "2s",
	"coupons_discount_percent": "10",
	"shutdown_timeout":         "10s",
	"log_level":                "info",
	"log_format":               "json",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coupons-server:", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	reader := viper.New()
	for key, value := range defaults {
		reader.SetDefault(key, value)
	}
	reader.AutomaticEnv()

	var cfg config
	err := reader.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.TextUnmarshallerHookFunc(),
		mapstructure.StringToTimeDurationHookFunc(),
	)))
	if err != nil {
		return config{}, err
	}

	if info, err := os.Stat(cfg.Dir); err != nil || !info.IsDir() {
		return config{}, fmt.Errorf("COUPONS_DIR %q is not a readable directory", cfg.Dir)
	}
	if cfg.DiscountPercent < 0 || cfg.DiscountPercent > 100 {
		return config{}, errors.New("COUPONS_DISCOUNT_PERCENT must be between 0 and 100")
	}
	if cfg.PollInterval <= 0 || cfg.ShutdownTimeout <= 0 {
		return config{}, errors.New("COUPONS_POLL_INTERVAL and SHUTDOWN_TIMEOUT must be positive")
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return config{}, fmt.Errorf("unknown LOG_FORMAT %q, want json or text", cfg.LogFormat)
	}
	return cfg, nil
}

func newLogger(out io.Writer, cfg config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(out, opts))
	}
	return slog.New(slog.NewJSONHandler(out, opts))
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := newLogger(os.Stdout, cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var loaded atomic.Bool
	service := coupons.New(coupons.Config{
		Dir:          cfg.Dir,
		PollInterval: cfg.PollInterval,
		Logger:       logger,
		OnLoad: func(result coupons.LoadResult) {
			if result.Err == nil {
				loaded.Store(true)
			}
		},
	})
	go service.Run(ctx)

	logger.Info("starting", "dir", cfg.Dir)
	server := echo.StartConfig{
		Address:         cfg.Addr,
		GracefulTimeout: cfg.ShutdownTimeout,
		BeforeServeFunc: func(server *http.Server) error {
			server.ReadHeaderTimeout = 5 * time.Second
			server.ReadTimeout = 5 * time.Second
			server.WriteTimeout = 10 * time.Second
			server.IdleTimeout = 60 * time.Second
			return nil
		},
		OnShutdownError: func(err error) {
			logger.Error("graceful shutdown", "err", err)
		},
	}
	return server.Start(ctx, newRouter(service, loaded.Load, cfg.DiscountPercent, logger))
}
