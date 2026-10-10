// Package coupons keeps the set of valid promo codes in sync with a set of
// coupon base files.
//
// A promo code is valid when it appears in at least MinFiles of the files.
// The work is split into swappable stages:
//
//	couponsource.Reader ──chunks──▶ scanner.Scanner ──codes──▶ couponstore.Store
//	  lists and reads files          finds valid codes          keeps the result
//
// Service watches the Reader's files, runs a scan when they change, and
// serves lookups from the stored result.
package coupons

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/couponsource"
	"github.com/sam97/oolio-kart/pkg/datasources/couponstore"
	"github.com/sam97/oolio-kart/pkg/services/coupons/scanner"
)

// DefaultPollInterval is how often the files are checked for changes.
const DefaultPollInterval = 2 * time.Second

type Config struct {
	Reader  couponsource.Reader
	Scanner scanner.Scanner
	Store   couponstore.Store

	// Rules describes the validity rules. A stored result is only reused
	// if it was built under the same rules.
	Rules string

	// PollInterval defaults to DefaultPollInterval. A change is only loaded
	// once the files look the same on two polls in a row, so files that are
	// still being copied in are not read half-written.
	PollInterval time.Duration

	// OnLoad, if set, is called after every load attempt. It must return
	// quickly; a slow OnLoad delays the next reload.
	OnLoad func(LoadResult)

	Logger *slog.Logger
}

type LoadResult struct {
	Files    []string
	Codes    int
	Duration time.Duration
	Err      error

	// Restored is true when the codes came from the store instead of a
	// scan.
	Restored bool

	// Scan describes the scan; it is zero when Restored.
	Scan scanner.Stats
}

// Service keeps the list of valid coupons in sync with the Reader's files.
type Service struct {
	cfg Config

	mu    sync.RWMutex
	codes []string
}

func New(cfg Config) *Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{cfg: cfg}
}

// ValidCoupons returns the valid coupons from the last successful load, in
// sorted order.
func (s *Service) ValidCoupons() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.codes)
}

func (s *Service) IsValid(code string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, found := slices.BinarySearch(s.codes, code)
	return found
}

// Run watches the files and reloads the coupons whenever they change, until
// ctx is cancelled. A failed load keeps the previous coupons and is retried
// on the next change.
//
// On start, if the store holds a result for the current files, it is used
// right away instead of waiting for a second poll and scanning.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	var prev, loaded []couponsource.Info
	everLoaded := false
	// A stored result was built from complete files, so files that match it
	// need not be seen twice before they are trusted.
	if sources, err := s.cfg.Reader.List(ctx); err == nil && s.restore(ctx, sources) {
		prev, loaded, everLoaded = couponsource.Infos(sources), couponsource.Infos(sources), true
	}
	for {
		sources, err := s.cfg.Reader.List(ctx)
		if err != nil {
			s.cfg.Logger.Error("list coupon files", "err", err)
		} else {
			current := couponsource.Infos(sources)
			if couponsource.SameInfos(current, prev) && (!everLoaded || !couponsource.SameInfos(current, loaded)) {
				s.load(ctx, sources)
				loaded, everLoaded = current, true
			}
			prev = current
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) fingerprint(sources []couponsource.Source) couponstore.Fingerprint {
	return couponstore.Fingerprint{Sources: couponsource.Infos(sources), Rules: s.cfg.Rules}
}

// restore loads the stored codes if they were built from sources. It reports
// whether it did; on false the caller should scan.
func (s *Service) restore(ctx context.Context, sources []couponsource.Source) bool {
	start := time.Now()
	codes, found, err := s.cfg.Store.Load(ctx, s.fingerprint(sources))
	if err != nil {
		s.cfg.Logger.Warn("ignore stored coupons", "err", err)
		return false
	}
	if !found {
		return false
	}
	s.setCodes(codes)
	s.cfg.Logger.Info("restored stored coupons", "files", len(sources), "codes", len(codes))
	if s.cfg.OnLoad != nil {
		s.cfg.OnLoad(LoadResult{Files: names(sources), Codes: len(codes), Duration: time.Since(start), Restored: true})
	}
	return true
}

func (s *Service) load(ctx context.Context, sources []couponsource.Source) {
	if s.restore(ctx, sources) {
		return
	}
	start := time.Now()
	stats, err := s.scan(ctx, sources)
	var codes []string
	if err == nil {
		var found bool
		codes, found, err = s.cfg.Store.Load(ctx, s.fingerprint(sources))
		if err == nil && !found {
			err = errors.New("the store did not keep the result")
		}
	}
	result := LoadResult{Files: names(sources), Codes: len(codes), Duration: time.Since(start), Err: err, Scan: stats}

	if err != nil {
		s.cfg.Logger.Error("load coupons", "files", result.Files, "err", err)
	} else {
		s.setCodes(codes)
		s.cfg.Logger.Info("loaded coupons",
			"files", len(sources),
			"codes", len(codes),
			"took", result.Duration,
			"scatter", stats.Scatter,
			"count", stats.Count,
			"buckets", stats.Buckets,
			"oversized", stats.Oversized,
		)
	}
	// Return the scan's memory to the OS rather than holding it until the
	// next scan.
	debug.FreeOSMemory()

	if s.cfg.OnLoad != nil {
		s.cfg.OnLoad(result)
	}
}

// scan runs the Scanner into a new batch, committed only if the scan
// succeeds.
func (s *Service) scan(ctx context.Context, sources []couponsource.Source) (scanner.Stats, error) {
	batch, err := s.cfg.Store.Begin(ctx, s.fingerprint(sources))
	if err != nil {
		return scanner.Stats{}, err
	}
	stats, err := s.cfg.Scanner.Scan(ctx, s.cfg.Reader, sources, batch)
	if err != nil {
		batch.Abort()
		return stats, err
	}
	return stats, batch.Commit()
}

func (s *Service) setCodes(codes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes = codes
}

func names(sources []couponsource.Source) []string {
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Info().Name
	}
	return names
}
