// Command coupons-job builds the valid coupons from a folder of coupon base
// files and publishes them to Postgres, where kart-api reads them. It runs a
// build on start, then whenever COUPONS_SCHEDULE fires; a build is skipped
// while the files and rules match the published coupons.
//
// Flags pick one-off tasks instead:
//
//	-once         run one build, then exit (for a Kubernetes CronJob or cron)
//	-migrate      apply the database migrations, then exit
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

	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/datasources/postgres"
	"github.com/sam97/oolio-kart/pkg/helpers/healthcheck"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/services/coupons"
)

// buildLockKey names the Postgres advisory lock that keeps two builds from
// running at once.
const buildLockKey = 0x636f75706f6e73 // "coupons"

func main() {
	once := flag.Bool("once", false, "run one build, then exit")
	migrate := flag.Bool("migrate", false, "apply the database migrations, then exit")
	probe := flag.Bool("healthcheck", false, "exit 0 once coupons have been published")
	flag.Parse()

	run := func() error { return build(*once) }
	switch {
	case *migrate:
		run = applyMigrations
	case *probe:
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

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := couponstore.NewPostgres(pool)
	job, err := coupons.NewDefault(coupons.Options{
		Dir:         cfg.Dir,
		BucketDir:   cfg.BucketDir,
		MemoryLimit: int64(cfg.MemoryLimit),
		Store:       store,
		Settings:    store,
		Locker:      postgres.NewLock(pool, buildLockKey),
		Logger:      logger,
	})
	if err != nil {
		return err
	}

	if once {
		_, err := job.RunOnce(ctx)
		return err
	}
	logger.Info("starting", "dir", cfg.Dir, "schedule", cfg.Schedule.String())
	if err := job.Run(ctx, cfg.Schedule); !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("stopped")
	return nil
}

func applyMigrations() error {
	cfg, err := loadConfig(".")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.LogFormat)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("database migrated")
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

	pool, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, found, err := couponstore.NewPostgres(pool).Published(ctx)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no coupons have been published yet")
	}
	return nil
}
