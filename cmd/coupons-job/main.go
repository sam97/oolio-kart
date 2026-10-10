// Command coupons-job builds the valid coupons from a folder of coupon base
// files and publishes them, where kart-api reads them. It runs a build on
// start, then whenever COUPONS_SCHEDULE fires; a build is skipped while the
// files and rules match the published coupons.
//
// Flags pick one-off tasks instead:
//
//	-once         run one build, then exit (for a Kubernetes CronJob or cron)
//	-healthcheck  exit 0 once coupons have been published
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/sam97/oolio-kart/pkg/datasources"
	"github.com/sam97/oolio-kart/pkg/helpers/healthcheck"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/services/coupons"
)

func main() {
	once := flag.Bool("once", false, "run one build, then exit")
	probe := flag.Bool("healthcheck", false, "exit 0 once coupons have been published")
	flag.Parse()

	run := func() error { return build(*once) }
	if *probe {
		run = checkHealth
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coupons-job:", err)
		os.Exit(1)
	}
}

func build(once bool) error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Let the garbage collector work to the same cap the build is sized
	// for, unless GOMEMLIMIT already sets one.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(int64(cfg.MemoryLimit))
	}

	// No cache: every run reads the current settings.
	stores, err := datasources.Open(ctx, datasources.Config{DatabaseURL: cfg.DatabaseURL, Logger: logger})
	if err != nil {
		return err
	}
	defer stores.Close()
	job, err := coupons.NewDefault(coupons.Options{
		Files:       datasources.CouponFiles(cfg.Dir),
		Scanner:     cfg.Scanner,
		BucketDir:   cfg.BucketDir,
		ClickHouse:  coupons.ClickHouseOptions{URL: cfg.ClickHouseURL, Dir: cfg.ClickHouseDir},
		PebbleDir:   cfg.PebbleDir,
		MemoryLimit: int64(cfg.MemoryLimit),
		Store:       stores.Coupons,
		Settings:    stores.CouponSettings,
		Locker:      stores.BuildLock,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	defer job.Close()

	if once {
		_, err := job.RunOnce(ctx)
		return err
	}
	logger.Info("starting", "dir", cfg.Dir, "scanner", cfg.Scanner, "schedule", cfg.Schedule.String())
	if err := job.Run(ctx, cfg.Schedule); !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("stopped")
	return nil
}

// checkHealth is the container health check, since the image has no shell:
// the job is healthy once coupons are published for kart-api to read.
func checkHealth() error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheck.Timeout)
	defer cancel()

	stores, err := datasources.Open(ctx, datasources.Config{DatabaseURL: cfg.DatabaseURL})
	if err != nil {
		return err
	}
	defer stores.Close()
	_, found, err := stores.Coupons.Published(ctx)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no coupons have been published yet")
	}
	return nil
}
