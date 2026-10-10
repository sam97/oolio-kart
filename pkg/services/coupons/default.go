package coupons

import (
	"errors"
	"fmt"
	"log/slog"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner/clickhouse"
)

// Scanners are the names Options.Scanner accepts; the first is the default.
var Scanners = []string{scanner.BucketsName, clickhouse.Name}

// Options configure the default pipeline.
type Options struct {
	// Files opens the coupon base files, given the memory it may use.
	Files couponsource.Opener

	// Scanner picks the scanner by name, one of Scanners. Empty means the
	// first, the bucket scanner.
	Scanner string

	// BucketDir holds the bucket scanner's files between builds.
	BucketDir string

	// ClickHouse configures the clickhouse scanner.
	ClickHouse ClickHouseOptions

	// MemoryLimit caps the build, in bytes; at least MinMemoryLimit. The
	// clickhouse scanner's memory is capped on the server instead.
	MemoryLimit int64

	Store    couponstore.Store
	Settings couponstore.Settings
	Locker   couponstore.Locker
	Logger   *slog.Logger
}

type ClickHouseOptions struct {
	// URL locates the server, e.g. clickhouse://user:password@host:9000/db.
	URL string

	// Dir is the folder of coupon base files as the server sees it, relative
	// to its user_files folder.
	Dir string
}

// NewDefault wires the standard pipeline: alphanumeric codes and the scanner
// opts.Scanner names, reading opts.Files and publishing to opts.Store. Close
// the job when done with it.
func NewDefault(opts Options) (*Job, error) {
	if opts.MemoryLimit < MinMemoryLimit {
		return nil, fmt.Errorf("memory limit %s is below the minimum of %s",
			humanize.IBytes(uint64(max(opts.MemoryLimit, 0))), humanize.IBytes(MinMemoryLimit))
	}
	if opts.Files == nil {
		return nil, errors.New("Files is required")
	}
	if opts.Store == nil || opts.Settings == nil || opts.Locker == nil {
		return nil, errors.New("Store, Settings and Locker are required")
	}
	if opts.Scanner == "" {
		opts.Scanner = Scanners[0]
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	budget := NewBudget(opts.MemoryLimit)
	opts.Logger.Info("coupon memory budget", "budget", budget, "scanner", opts.Scanner)

	scannerFor, closeScanner, err := newScannerFor(opts, budget.Scanner)
	if err != nil {
		return nil, err
	}
	job := New(Config{
		Reader:     opts.Files(budget.Reader),
		Store:      opts.Store,
		Settings:   opts.Settings,
		Locker:     opts.Locker,
		ScannerFor: scannerFor,
		Logger:     opts.Logger,
	})
	job.close = closeScanner
	return job, nil
}

// newScannerFor returns the ScannerFor of the scanner opts names, and what to
// close once the job is done with it.
func newScannerFor(opts Options, budget scanner.Budget) (ScannerFor, func() error, error) {
	noClose := func() error { return nil }
	switch opts.Scanner {
	case scanner.BucketsName:
		if opts.BucketDir == "" {
			return nil, nil, errors.New("BucketDir is required by the go scanner")
		}
		return func(settings models.CouponSettings) (scanner.Scanner, string, error) {
			return NewBucketScanner(settings, opts.BucketDir, budget)
		}, noClose, nil

	case clickhouse.Name:
		if opts.ClickHouse.URL == "" || opts.ClickHouse.Dir == "" {
			return nil, nil, errors.New("ClickHouse URL and Dir are required by the clickhouse scanner")
		}
		chOptions, err := ch.ParseDSN(opts.ClickHouse.URL)
		if err != nil {
			return nil, nil, fmt.Errorf("parse the ClickHouse URL: %w", err)
		}
		// Connects lazily: a server that is down fails the build, not this.
		conn, err := ch.Open(chOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("open ClickHouse: %w", err)
		}
		return func(settings models.CouponSettings) (scanner.Scanner, string, error) {
			scan, err := clickhouse.New(clickhouse.Options{Conn: conn, Dir: opts.ClickHouse.Dir, Settings: settings})
			return scan, Rules(settings, clickhouse.Name), err
		}, conn.Close, nil
	}
	return nil, nil, fmt.Errorf("unknown scanner %q, want one of %v", opts.Scanner, Scanners)
}

// NewBucketScanner returns the bucket scanner for alphanumeric codes under
// settings, and its rules string.
func NewBucketScanner(settings models.CouponSettings, bucketDir string, budget scanner.Budget) (scanner.Scanner, string, error) {
	codes, err := codec.NewAlphanumeric(settings.MinLength, settings.MaxLength)
	if err != nil {
		return nil, "", err
	}
	buckets, err := scanner.NewBuckets(scanner.BucketOptions{
		Codec:    codes,
		MinFiles: settings.MinFiles,
		Dir:      bucketDir,
		Budget:   budget,
	})
	if err != nil {
		return nil, "", err
	}
	return buckets, Rules(settings, scanner.BucketsName), nil
}

// Rules describes what decides the published codes: the settings that make a
// code valid, and the scanner. The discount is not one of them, so changing
// it never needs a build. Every scanner finds the same codes, but switching
// scanners rebuilds anyway, so their builds can be compared.
func Rules(settings models.CouponSettings, scannerName string) string {
	return fmt.Sprintf("alphanumeric:%d-%d;minFiles=%d;scanner=%s",
		settings.MinLength, settings.MaxLength, settings.MinFiles, scannerName)
}
