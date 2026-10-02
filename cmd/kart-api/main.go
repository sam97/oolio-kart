// Command kart-api serves the food ordering API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/api"
	"github.com/sam97/oolio-kart/internal/cache"
	"github.com/sam97/oolio-kart/internal/config"
	"github.com/sam97/oolio-kart/internal/couponclient"
	"github.com/sam97/oolio-kart/internal/healthcheck"
	"github.com/sam97/oolio-kart/internal/httpapi"
	"github.com/sam97/oolio-kart/internal/logging"
	"github.com/sam97/oolio-kart/internal/order"
	"github.com/sam97/oolio-kart/internal/product"
)

func main() {
	probe := flag.Bool("healthcheck", false, "check that the running server is ready, then exit")
	flag.Parse()

	run := serve
	if *probe {
		run = checkHealth
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kart-api:", err)
		os.Exit(1)
	}
}

// checkHealth probes the server configured by the environment. It is the
// container health check, since the image has no shell or curl.
func checkHealth() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return healthcheck.Probe(cfg.Addr)
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	memory, err := cache.NewMemory(cfg.CacheMaxEntries)
	if err != nil {
		return fmt.Errorf("create cache: %w", err)
	}
	client, err := couponclient.NewClient(cfg.CouponsURL, cfg.CouponsTimeout)
	if err != nil {
		return err
	}
	coupons := couponclient.NewCached(client, memory, cfg.CouponCacheTTL, cfg.CouponNegativeCacheTTL, logger)

	products := product.NewMemory(product.Demo())
	orders := order.NewService(products, order.NewMemory(), coupons)

	var ready atomic.Bool
	ready.Store(true)

	router := httpapi.NewRouter(httpapi.Options{
		Logger:             logger,
		Products:           products,
		Orders:             orders,
		Coupons:            coupons,
		APIKeys:            cfg.APIKeys,
		CouponLimit:        httpapi.Limit{Requests: cfg.CouponRateLimit, Window: cfg.CouponRateWindow},
		OrderLimit:         httpapi.Limit{Requests: cfg.OrderRateLimit, Window: cfg.OrderRateWindow},
		TrustedProxies:     cfg.TrustedProxies,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		Ready:              ready.Load,
		Spec:               api.OpenAPI,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fail readiness as soon as shutdown starts, so load balancers stop
	// routing here while in-flight requests drain.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down", "timeout", cfg.ShutdownTimeout)
		ready.Store(false)
	}()

	logger.Info("starting", "coupons_url", cfg.CouponsURL)
	server := echo.StartConfig{
		Address:         cfg.Addr,
		HideBanner:      true,
		GracefulTimeout: cfg.ShutdownTimeout,
		BeforeServeFunc: func(server *http.Server) error {
			server.ReadHeaderTimeout = cfg.ReadHeaderTimeout
			server.ReadTimeout = cfg.ReadTimeout
			server.WriteTimeout = cfg.WriteTimeout
			server.IdleTimeout = cfg.IdleTimeout
			return nil
		},
		OnShutdownError: func(err error) {
			logger.Error("graceful shutdown", "err", err)
		},
	}
	if err := server.Start(ctx, router); err != nil {
		return err
	}
	logger.Info("stopped")
	return nil
}
