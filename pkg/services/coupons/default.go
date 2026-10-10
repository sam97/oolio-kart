package coupons

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// Options configure the default pipeline.
type Options struct {
	// Files opens the coupon base files, given the memory it may use.
	Files couponsource.Opener

	// BucketDir holds the bucket files between builds.
	BucketDir string

	// MemoryLimit caps the build, in bytes; at least MinMemoryLimit.
	MemoryLimit int64

	Store    couponstore.Store
	Settings couponstore.Settings
	Locker   couponstore.Locker
	Logger   *slog.Logger
}

// NewDefault wires the standard pipeline: alphanumeric codes and the bucket
// scanner, reading opts.Files and publishing to opts.Store.
func NewDefault(opts Options) (*Job, error) {
	if opts.MemoryLimit < MinMemoryLimit {
		return nil, fmt.Errorf("memory limit %s is below the minimum of %s",
			humanize.IBytes(uint64(max(opts.MemoryLimit, 0))), humanize.IBytes(MinMemoryLimit))
	}
	if opts.Files == nil || opts.BucketDir == "" {
		return nil, errors.New("Files and BucketDir are required")
	}
	if opts.Store == nil || opts.Settings == nil || opts.Locker == nil {
		return nil, errors.New("Store, Settings and Locker are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	budget := NewBudget(opts.MemoryLimit)
	opts.Logger.Info("coupon memory budget", "budget", budget)

	return New(Config{
		Reader:   opts.Files(budget.Reader),
		Store:    opts.Store,
		Settings: opts.Settings,
		Locker:   opts.Locker,
		ScannerFor: func(settings models.CouponSettings) (scanner.Scanner, string, error) {
			return NewBucketScanner(settings, opts.BucketDir, budget.Scanner)
		},
		Logger: opts.Logger,
	}), nil
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
	return buckets, Rules(settings), nil
}

// Rules describes the settings that decide which codes are valid. The
// discount is not one of them, so changing it never needs a build.
func Rules(settings models.CouponSettings) string {
	return fmt.Sprintf("alphanumeric:%d-%d;minFiles=%d", settings.MinLength, settings.MaxLength, settings.MinFiles)
}
