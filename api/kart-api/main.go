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
	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/handlers/kartapi"
	"github.com/sam97/oolio-kart/pkg/helpers/healthcheck"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/services/coupons/validator"
	"github.com/sam97/oolio-kart/pkg/services/orders"
	"github.com/sam97/oolio-kart/pkg/services/products"
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

	// Stores connect lazily: while they are down, requests that need them
	// answer 503 and the server keeps running.
	stores, err := datasources.Open(ctx, datasources.Config{
		DatabaseURL: cfg.DatabaseURL,
		Cache: datasources.CacheConfig{
			MaxEntries:        cfg.CacheMaxEntries,
			CouponTTL:         cfg.CouponCacheTTL,
			CouponNegativeTTL: cfg.CouponNegativeCacheTTL,
			SettingsTTL:       cfg.CouponSettingsCacheTTL,
		},
		Logger: logger,
	})
	if err != nil {
		return err
	}
	defer stores.Close()

	catalogue := products.NewService(stores.Products)
	coupons := validator.NewStore(stores.CouponLookup, stores.CouponSettings, cfg.CouponsQueryTimeout)
	orderService := orders.NewService(stores.Products, stores.Orders, coupons)

	var ready atomic.Bool
	ready.Store(true)

	router := kartapi.NewRouter(kartapi.Options{
		Logger:             logger,
		Products:           catalogue,
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
