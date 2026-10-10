// Command coupons-server keeps the valid coupons in sync with a watched folder
// of coupon base files and answers lookups over HTTP. It is an internal
// service: expose it only to the API, never to the internet.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/pkg/handlers/couponsapi"
	"github.com/sam97/oolio-kart/pkg/helpers/healthcheck"
	"github.com/sam97/oolio-kart/pkg/services/coupons"
)

func main() {
	probe := flag.Bool("healthcheck", false, "check that the running server is ready, then exit")
	flag.Parse()

	run := serve
	if *probe {
		run = checkHealth
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coupons-server:", err)
		os.Exit(1)
	}
}

// checkHealth probes the server configured by the environment. It is the
// container health check, since the image has no shell or curl.
func checkHealth() error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return healthcheck.Probe(cfg.Addr)
}

func newLogger(out io.Writer, cfg config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(out, opts))
	}
	return slog.New(slog.NewJSONHandler(out, opts))
}

func serve() error {
	cfg, err := loadConfig(".")
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
	return server.Start(ctx, couponsapi.NewRouter(service, loaded.Load, cfg.DiscountPercent, logger))
}
