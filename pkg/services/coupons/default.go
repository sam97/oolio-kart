package coupons

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dustin/go-humanize"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/models"
	"github.com/sam97/oolio-kart/pkg/services/coupons/codec"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// Settings configure the default pipeline.
type Settings struct {
	// Dir is the watched folder of coupon base files. The stored result is
	// kept there too.
	Dir string

	// BucketDir holds the bucket files between scans.
	BucketDir string

	// MemoryLimit caps the build, in bytes; at least MinMemoryLimit.
	MemoryLimit int64

	// MinFiles is how many files a code must appear in; it defaults to 2.
	MinFiles int

	PollInterval time.Duration
	OnLoad       func(LoadResult)
	Logger       *slog.Logger
}

// NewDefault wires the standard pipeline: files in a folder, gzip or plain
// text, alphanumeric codes, the bucket scanner and a JSON result file.
func NewDefault(settings Settings) (*Service, error) {
	if settings.MemoryLimit < MinMemoryLimit {
		return nil, fmt.Errorf("memory limit %s is below the minimum of %s",
			humanize.IBytes(uint64(max(settings.MemoryLimit, 0))), humanize.IBytes(MinMemoryLimit))
	}
	if settings.Dir == "" || settings.BucketDir == "" {
		return nil, errors.New("Dir and BucketDir are required")
	}
	if settings.MinFiles == 0 {
		settings.MinFiles = 2
	}
	if settings.Logger == nil {
		settings.Logger = slog.Default()
	}

	codes, err := codec.NewAlphanumeric(models.CouponMinLength, models.CouponMaxLength)
	if err != nil {
		return nil, err
	}
	rules := fmt.Sprintf("alphanumeric:%d-%d;minFiles=%d", models.CouponMinLength, models.CouponMaxLength, settings.MinFiles)

	budget := NewBudget(settings.MemoryLimit)
	buckets, err := scanner.NewBuckets(scanner.BucketOptions{
		Codec:    codes,
		MinFiles: settings.MinFiles,
		Dir:      settings.BucketDir,
		Rules:    rules,
		Budget:   budget.Scanner,
	})
	if err != nil {
		return nil, err
	}
	settings.Logger.Info("coupon memory budget", "budget", budget)

	return New(Config{
		Reader:       couponsource.NewFS(settings.Dir, budget.Reader, couponsource.NewGzip()),
		Scanner:      buckets,
		Store:        couponstore.NewJSONFile(settings.Dir),
		Rules:        rules,
		PollInterval: settings.PollInterval,
		OnLoad:       settings.OnLoad,
		Logger:       settings.Logger,
	}), nil
}
