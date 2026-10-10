// Command kart-api serves the food ordering API.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/labstack/echo/v5"
	"github.com/sam97/oolio-kart/pkg/datasources/cache"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/orderstore"
	"github.com/sam97/oolio-kart/pkg/datasources/postgres"
	"github.com/sam97/oolio-kart/pkg/datasources/productstore"
	"github.com/sam97/oolio-kart/pkg/handlers/kartapi"
	"github.com/sam97/oolio-kart/pkg/helpers/healthcheck"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/services/coupons/validator"
	"github.com/sam97/oolio-kart/pkg/services/orders"
)

// openAPI is the API's OpenAPI 3.1 document, served at /api/openapi.yaml.
//
//go:embed openapi.yaml
var openAPI []byte

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
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return healthcheck.Probe(cfg.Addr)
}

func serve() error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	memory, err := cache.NewMemory(cfg.CacheMaxEntries)
	if err != nil {
		return fmt.Errorf("create cache: %w", err)
	}
	// The pool connects lazily: while the database is down, orders without
	// coupons still work and coupon checks answer unavailable.
	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := couponstore.NewPostgres(pool)
	coupons := validator.NewStore(
		validator.NewCachedLookup(store, memory, cfg.CouponCacheTTL, cfg.CouponNegativeCacheTTL, logger),
		validator.NewCachedSettings(store, memory, cfg.CouponSettingsCacheTTL, logger),
		cfg.CouponsQueryTimeout,
	)

	products := productstore.NewMemory(productstore.Demo())
	orderService := orders.NewService(products, orderstore.NewMemory(), coupons)

	var ready atomic.Bool
	ready.Store(true)

	router := kartapi.NewRouter(kartapi.Options{
		Logger:             logger,
		Products:           products,
		Orders:             orderService,
		Coupons:            coupons,
		APIKeys:            cfg.APIKeys,
		CouponLimit:        kartapi.Limit{Requests: cfg.CouponRateLimit, Window: cfg.CouponRateWindow},
		OrderLimit:         kartapi.Limit{Requests: cfg.OrderRateLimit, Window: cfg.OrderRateWindow},
		TrustedProxies:     cfg.TrustedProxies,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		Ready:              ready.Load,
		Spec:               openAPI,
	})

	// Fail readiness as soon as shutdown starts, so load balancers stop
	// routing here while in-flight requests drain.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down", "timeout", cfg.ShutdownTimeout)
		ready.Store(false)
	}()

	logger.Info("starting", "addr", cfg.Addr)
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
