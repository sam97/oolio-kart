// Package coupons builds the set of valid promo codes from a set of coupon
// base files and publishes it.
//
// A promo code is valid when it appears in at least MinFiles of the files.
// The work is split into swappable stages:
//
//	couponsource.Reader ──chunks──▶ scanner.Scanner ──codes──▶ couponstore.Store
//	  lists and reads files          finds valid codes          publishes them
//
// Job runs one build when the files or the rules have changed since the
// published one, and Run repeats that on a schedule.
package coupons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// Locker keeps two runs, in this process or another, from building at once.
type Locker interface {
	// TryLock takes the lock if it is free. ok is false if another run
	// holds it. unlock must be called once ok is true.
	TryLock(ctx context.Context) (unlock func(), ok bool, err error)
}

// ScannerFor returns the scanner for one run's settings, and the rules string
// its result is fingerprinted with.
type ScannerFor func(settings models.CouponSettings) (scan scanner.Scanner, rules string, err error)

// Schedule says when to run next; cron schedules satisfy it.
type Schedule interface {
	Next(time.Time) time.Time
}

type Config struct {
	Reader     couponsource.Reader
	Store      couponstore.Store
	Settings   couponstore.Settings
	Locker     Locker
	ScannerFor ScannerFor
	Logger     *slog.Logger
}

type Outcome string

const (
	Built     Outcome = "built"
	Unchanged Outcome = "unchanged" // the published codes match the files and rules
	Locked    Outcome = "locked"    // another run is building
	NoFiles   Outcome = "no files"  // nothing published, so lookups stay unavailable
	// FilesChanged means a file changed during the scan, most likely because
	// it was still being copied in. Nothing is published; the next run
	// builds again.
	FilesChanged Outcome = "files changed"
	Failed       Outcome = "failed"
)

type Result struct {
	Outcome  Outcome
	Files    []string
	Codes    int
	Duration time.Duration

	// Scan describes the scan; it is zero unless a scan ran.
	Scan scanner.Stats
}

type Job struct {
	cfg Config
}

func New(cfg Config) *Job {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Job{cfg: cfg}
}

// Run runs a build now and then at each time schedule gives, until ctx is
// cancelled. Runs never overlap. A failed run is logged, and the next one
// tries again; a run cancelled part way publishes nothing.
func (j *Job) Run(ctx context.Context, schedule Schedule) error {
	for {
		j.RunOnce(ctx) // logs its own result

		timer := time.NewTimer(time.Until(schedule.Next(time.Now())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// RunOnce builds and publishes the valid codes, unless the published ones
// were already built from the same files and rules. It logs the result.
func (j *Job) RunOnce(ctx context.Context) (result Result, err error) {
	defer func() { j.log(ctx, result, err) }()
	return j.runOnce(ctx)
}

func (j *Job) runOnce(ctx context.Context) (Result, error) {
	start := time.Now()
	unlock, ok, err := j.cfg.Locker.TryLock(ctx)
	if err != nil {
		return Result{Outcome: Failed}, fmt.Errorf("take the build lock: %w", err)
	}
	if !ok {
		return Result{Outcome: Locked}, nil
	}
	defer unlock()

	// The settings are read for every run, so a change to them is picked up
	// without a restart.
	settings, err := j.cfg.Settings.Settings(ctx)
	if err != nil {
		return Result{Outcome: Failed}, fmt.Errorf("read coupon settings: %w", err)
	}
	scan, rules, err := j.cfg.ScannerFor(settings)
	if err != nil {
		return Result{Outcome: Failed}, fmt.Errorf("coupon settings %+v: %w", settings, err)
	}

	sources, err := j.cfg.Reader.List(ctx)
	if err != nil {
		return Result{Outcome: Failed}, fmt.Errorf("list coupon files: %w", err)
	}
	result := Result{Files: names(sources)}
	if len(sources) == 0 {
		result.Outcome = NoFiles
		return result, nil
	}
	fp := couponstore.Fingerprint{Sources: couponsource.Infos(sources), Rules: rules}
	published, found, err := j.cfg.Store.Published(ctx)
	if err != nil {
		result.Outcome = Failed
		return result, fmt.Errorf("read the published manifest: %w", err)
	}
	if found && published.Equal(fp) {
		result.Outcome = Unchanged
		return result, nil
	}

	result.Outcome, result.Scan, err = j.build(ctx, scan, sources, fp)
	result.Codes = result.Scan.Codes
	result.Duration = time.Since(start)
	// Return the scan's memory to the OS rather than holding it until the
	// next run.
	debug.FreeOSMemory()
	if err != nil {
		result.Outcome = Failed
	}
	return result, err
}

// build scans the sources into a batch and publishes it, unless a file
// changed during the scan.
func (j *Job) build(ctx context.Context, scan scanner.Scanner, sources []couponsource.Source, fp couponstore.Fingerprint) (Outcome, scanner.Stats, error) {
	batch, err := j.cfg.Store.Begin(ctx)
	if err != nil {
		return Failed, scanner.Stats{}, err
	}
	defer batch.Abort() // a no-op once committed

	stats, err := scan.Scan(ctx, j.cfg.Reader, sources, batch)
	if err != nil {
		return Failed, stats, err
	}

	// A file still being copied in when it was listed has a new size or
	// modification time by now, so its codes may be incomplete.
	after, err := j.cfg.Reader.List(ctx)
	if err != nil {
		return Failed, stats, fmt.Errorf("list coupon files: %w", err)
	}
	if !couponsource.SameInfos(couponsource.Infos(after), fp.Sources) {
		return FilesChanged, stats, nil
	}

	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return Failed, stats, err
	}
	err = batch.Commit(ctx, couponstore.Manifest{Fingerprint: fp, Layout: stats.Layout, Stats: statsJSON})
	return Built, stats, err
}

func (j *Job) log(ctx context.Context, result Result, err error) {
	logger := j.cfg.Logger
	switch {
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		logger.Info("coupon build interrupted; nothing was published")
	case err != nil:
		logger.Error("build coupons", "files", result.Files, "err", err)
	case result.Outcome == Built:
		logger.Info("built coupons",
			"files", len(result.Files),
			"codes", result.Codes,
			"took", result.Duration,
			"scatter", result.Scan.Scatter,
			"count", result.Scan.Count,
			"buckets", result.Scan.Buckets,
			"oversized", result.Scan.Oversized,
		)
	case result.Outcome == Unchanged:
		logger.Debug("coupon files unchanged", "files", len(result.Files))
	case result.Outcome == Locked:
		logger.Info("another run is building coupons")
	case result.Outcome == NoFiles:
		logger.Warn("no coupon files to build from")
	case result.Outcome == FilesChanged:
		logger.Warn("coupon files changed during the build; the next run builds again", "files", result.Files)
	}
}

func names(sources []couponsource.Source) []string {
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Info().Name
	}
	return names
}
